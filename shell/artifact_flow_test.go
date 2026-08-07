package shell

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const report = "# Suite results\n\n| pkg | status |\n|---|---|\n| auth | fail |\n\n" +
	"- [x] repro confirmed\n- [ ] fix written\n\n" +
	"The failure is a `nil` deref inside sqlite-vec chunking.\n"

func putArtifact(t *testing.T, srv *httptest.Server, body string) string {
	t.Helper()
	resp, err := http.Post(srv.URL+"/artifacts", "text/markdown", strings.NewReader(body))
	if err != nil {
		t.Fatalf("put artifact: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put artifact: status %d", resp.StatusCode)
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	h, _ := out["hash"].(string)
	if h == "" {
		t.Fatal("no hash returned")
	}
	return h
}

func TestArtifactRoundTrip(t *testing.T) {
	srv, _ := newServer(t)
	hash := putArtifact(t, srv, report)

	// Identical content stores once.
	if again := putArtifact(t, srv, report); again != hash {
		t.Errorf("identical content must dedupe to one hash: %s vs %s", hash, again)
	}

	// Attach it to an event.
	cmd := `{"room":"core","author":"agent:claude-1","kind":"finding",` +
		`"body":{"text":"suite failing","severity":"p1"},"idem":"a1",` +
		`"attachments":[{"hash":"` + hash + `","title":"suite-results.md"}]}`
	code, out := post(t, srv, cmd)
	if code != http.StatusOK {
		t.Fatalf("attach: want 200, got %d (%v)", code, out)
	}

	// The row shows the title, not the content.
	resp, err := http.Get(srv.URL + "/?room=core")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	page := buf.String()
	if !strings.Contains(page, "suite-results.md") {
		t.Error("row must show the artifact title")
	}
	if strings.Contains(page, "nil` deref") {
		t.Error("row must not inline artifact content")
	}
	if !strings.Contains(page, "/a/"+hash) {
		t.Error("row must link to the artifact")
	}

	// Fetching renders sanitized HTML.
	art, err := http.Get(srv.URL + "/a/" + hash)
	if err != nil {
		t.Fatal(err)
	}
	defer art.Body.Close()
	ab := new(bytes.Buffer)
	ab.ReadFrom(art.Body)
	rendered := ab.String()
	for _, want := range []string{"<table", "Suite results", "sqlite-vec"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("artifact page missing %q", want)
		}
	}
	if csp := art.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("artifact page must ship a strict CSP, got %q", csp)
	}
}

// Storing HTML is refused outright — that is the whole point of ADR-0011.
func TestArtifactRefusesHTML(t *testing.T) {
	srv, _ := newServer(t)
	resp, err := http.Post(srv.URL+"/artifacts", "text/html",
		strings.NewReader("<script>alert(1)</script>"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("want 415 for text/html, got %d", resp.StatusCode)
	}
}

// An event may not point at content nobody stored.
func TestAttachmentMustExist(t *testing.T) {
	srv, _ := newServer(t)
	ghost := strings.Repeat("a", 64)
	code, out := post(t, srv,
		`{"room":"core","author":"human:bcm","kind":"chat","body":{"text":"x"},"idem":"g1",`+
			`"attachments":[{"hash":"`+ghost+`","title":"ghost.md"}]}`)
	if code != http.StatusUnprocessableEntity || out["invariant"] != "attachment.unknown" {
		t.Errorf("want 422 attachment.unknown, got %d %v", code, out)
	}
}

// A malformed hash fails at the parse boundary, before the decider sees it.
func TestMalformedAttachmentHashFailsParse(t *testing.T) {
	srv, _ := newServer(t)
	code, out := post(t, srv,
		`{"room":"core","author":"human:bcm","kind":"chat","body":{"text":"x"},"idem":"m1",`+
			`"attachments":[{"hash":"NOT-A-HASH","title":"x.md"}]}`)
	if code != http.StatusBadRequest || out["invariant"] != "parse.failed" {
		t.Errorf("want 400 parse.failed, got %d %v", code, out)
	}
}

// Artifact text is indexed with its event, so report contents are searchable.
func TestArtifactContentIsSearchable(t *testing.T) {
	srv, _ := newServer(t)
	hash := putArtifact(t, srv, report)
	post(t, srv, `{"room":"core","author":"agent:claude-1","kind":"finding",`+
		`"body":{"text":"suite failing","severity":"p1"},"idem":"s1",`+
		`"attachments":[{"hash":"`+hash+`","title":"suite-results.md"}]}`)

	resp, err := http.Get(srv.URL + "/search?q=chunking")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	if !strings.Contains(buf.String(), "suite failing") {
		t.Error("searching artifact contents must find the event carrying it")
	}
}

// Redaction takes attachments with the body: a secret in a report must not
// outlive the redaction that erased the message.
func TestPurgeDropsAttachments(t *testing.T) {
	srv, st := newServer(t)
	hash := putArtifact(t, srv, "credentials: sk-live-DO-NOT-LEAK\n")
	_, out := post(t, srv, `{"room":"core","author":"human:bcm","kind":"chat",`+
		`"body":{"text":"oops"},"idem":"p1",`+
		`"attachments":[{"hash":"`+hash+`","title":"env.md"}]}`)
	seq := int64(out["seq"].(float64))

	if _, ok := st.GetArtifact(hash); !ok {
		t.Fatal("artifact should exist before purge")
	}
	if err := st.Purge(seq); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.GetArtifact(hash); ok {
		t.Error("purge must drop the attachment blob with the body")
	}
	if err := st.Verify(); err != nil {
		t.Errorf("chain must still verify after purging attachments: %v", err)
	}

	resp, err := http.Get(srv.URL + "/a/" + hash)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("purged artifact must 404, got %d", resp.StatusCode)
	}
}

// Progress is a projection: the room shows current state, folded from status.
func TestProgressProjectionRendersInRoom(t *testing.T) {
	srv, _ := newServer(t)
	for i, s := range []string{"1", "2", "3"} {
		post(t, srv, `{"room":"core","author":"agent:claude-1","kind":"status",`+
			`"body":{"text":"migrating","step":`+s+`,"of":7},"idem":"st`+s+`"}`)
		_ = i
	}
	post(t, srv, `{"room":"core","author":"agent:codex-3","kind":"status",`+
		`"body":{"text":"running suite"},"idem":"st9"}`)

	resp, err := http.Get(srv.URL + "/?room=core")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	page := buf.String()

	if !strings.Contains(page, "claude-1 step 3/7") {
		t.Error("progress line must show the latest step, not every status")
	}
	if strings.Count(page, "claude-1 step 3/7") != 1 {
		t.Error("progress belongs to the room and must render exactly once")
	}
	if strings.Contains(page, "step 1/7") {
		t.Error("progress is a fold, not a replay: superseded steps must not appear in the line")
	}
	if !strings.Contains(page, "codex-3") {
		t.Error("every working actor must appear in the progress line")
	}
}
