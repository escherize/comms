package shell

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func getPage(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	return buf.String()
}

// Redaction must actually suppress. Rendering a redact event struck-through
// while the target stays readable and searchable is worse than no redaction,
// because the room implies it worked.
func TestRedactSuppressesItsTarget(t *testing.T) {
	srv, _ := newServer(t)

	_, out := post(t, srv, cmd("chat", "sk-live-DO-NOT-LEAK", "leak1"))
	target := int64(out["seq"].(float64))

	if page := getPage(t, srv.URL+"/?room=core"); !strings.Contains(page, "sk-live-DO-NOT-LEAK") {
		t.Fatal("setup: the secret should be visible before redaction")
	}
	if hits := getPage(t, srv.URL+"/search?q=leak"); !strings.Contains(hits, "sk-live") {
		t.Fatal("setup: the secret should be searchable before redaction")
	}

	code, _ := post(t, srv, `{"room":"core","author":"bcm","kind":"redact",`+
		`"body":{"text":"pasted a key"},"refs":["`+itoa(target)+`"],"idem":"r1"}`)
	if code != http.StatusOK {
		t.Fatalf("redact should be accepted, got %d", code)
	}

	page := getPage(t, srv.URL+"/?room=core")
	if strings.Contains(page, "sk-live-DO-NOT-LEAK") {
		t.Error("a redacted body must not render")
	}
	if !strings.Contains(page, "redacted by bcm") {
		t.Error("the room must say who suppressed it, not silently blank the row")
	}
	if !strings.Contains(page, "hash attested") {
		t.Error("suppression must still attest the body hash")
	}

	if hits := getPage(t, srv.URL+"/search?q=leak"); strings.Contains(hits, "sk-live") {
		t.Error("a redacted body must not survive in search")
	}
}

// The event itself survives — corrections are new entries, not deletions.
func TestRedactLeavesTheEventInPlace(t *testing.T) {
	srv, st := newServer(t)
	_, out := post(t, srv, cmd("chat", "oops", "o1"))
	target := int64(out["seq"].(float64))

	post(t, srv, `{"room":"core","author":"bcm","kind":"redact",`+
		`"body":{"text":"mistake"},"refs":["`+itoa(target)+`"],"idem":"r1"}`)

	recs, err := st.Since("core", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("redaction must not remove the event: %d records, want 2", len(recs))
	}
	if err := st.Verify(); err != nil {
		t.Errorf("the chain must still verify after redaction: %v", err)
	}
}

// The carried-forward control is a real button with its group wired to it. It
// previously carried role=button and tabindex with no handler, which announced
// an interactive control to a screen reader and did nothing.
func TestCarriedForwardIsAWorkingControl(t *testing.T) {
	srv, _ := newServer(t)
	for i, txt := range []string{"a", "b", "c", "d", "e"} {
		post(t, srv, cmd("chat", txt, "amb"+string(rune('0'+i))))
	}

	page := getPage(t, srv.URL+"/?room=core")

	if !strings.Contains(page, `<button class="carried"`) {
		t.Error("the control must be a real button, not a div wearing role=button")
	}
	if !strings.Contains(page, `aria-expanded="false"`) {
		t.Error("the control must report its collapsed state")
	}
	if !strings.Contains(page, `aria-controls="cf1"`) || !strings.Contains(page, `id="cf1"`) {
		t.Error("the control must point at the group it toggles")
	}
	if !strings.Contains(page, `class="carried-body" id="cf1" hidden`) {
		t.Error("collapsed rows must be present-but-hidden so expanding needs no fetch")
	}
	// The collapsed entries are in the DOM, ready to reveal.
	for _, txt := range []string{"a", "b", "c", "d", "e"} {
		if !strings.Contains(page, `>`+txt+`<`) {
			t.Errorf("collapsed entry %q must be rendered hidden, not dropped", txt)
		}
	}
}

// Keyboard reach: search, composer, theme, room switch, expand.
func TestKeyboardBindingsArePresent(t *testing.T) {
	srv, _ := newServer(t)
	page := getPage(t, srv.URL+"/?room=core")

	for key, what := range map[string]string{
		"'/'": "search focus",
		"'c'": "composer focus",
		"'t'": "theme cycle",
		"'e'": "expand ambient",
		"'['": "previous room",
		"']'": "next room",
	} {
		if !strings.Contains(page, "e.key==="+key) {
			t.Errorf("%s (%s) has no key binding", what, key)
		}
	}
}

// A reused idempotency key with different content must surface as a conflict.
// It previously returned 200 with the first post's seq and dropped this one.
func TestIdemConflictIsA409(t *testing.T) {
	srv, _ := newServer(t)

	post(t, srv, cmd("chat", "ORIGINAL", "shared-key"))

	code, out := post(t, srv, cmd("chat", "COMPLETELY DIFFERENT", "shared-key"))
	if code != http.StatusConflict {
		t.Fatalf("reuse with different content must be 409, got %d (%v)", code, out)
	}
	if out["invariant"] != "idem.conflict" {
		t.Errorf("want idem.conflict, got %v", out["invariant"])
	}
	if d, _ := out["detail"].(string); !strings.Contains(d, "new key") {
		t.Errorf("the refusal must tell the agent what to do instead, got %q", d)
	}

	// A genuine retry — identical content — is still answered from the log.
	code, out = post(t, srv, cmd("chat", "ORIGINAL", "shared-key"))
	if code != http.StatusOK {
		t.Errorf("an identical retry must still succeed, got %d", code)
	}
	if applied, _ := out["applied"].(bool); applied {
		t.Error("a replayed key must report applied=false")
	}
}

// Redaction authorization, end to end over the wire.
func TestRedactAuthorizationOverTheWire(t *testing.T) {
	srv, _ := newServer(t)
	_, out := post(t, srv, cmd("chat", "bcm private note", "p1"))
	target := itoa(int64(out["seq"].(float64)))

	code, rej := post(t, srv, `{"room":"core","author":"mallory","kind":"redact",`+
		`"body":{"text":"nuking"},"refs":["`+target+`"],"idem":"m1"}`)
	if code != http.StatusUnprocessableEntity || rej["invariant"] != "redact.not_author" {
		t.Fatalf("another actor must not redact: %d %v", code, rej)
	}
	if page := getPage(t, srv.URL+"/?room=core"); !strings.Contains(page, "bcm private note") {
		t.Error("the refused redact must leave the event intact")
	}

	code, _ = post(t, srv, `{"room":"core","author":"bcm","kind":"redact",`+
		`"body":{"text":"my paste"},"refs":["`+target+`"],"idem":"b1"}`)
	if code != http.StatusOK {
		t.Fatalf("the author must be able to redact their own event, got %d", code)
	}
	if page := getPage(t, srv.URL+"/?room=core"); strings.Contains(page, "bcm private note") {
		t.Error("the authorized redact must suppress")
	}
}

// A secret pasted into an attached stack trace is the same secret. Blanking the
// row while the blob stays served hides the leak instead of closing it.
func TestRedactDropsAttachmentsAndTheirLinks(t *testing.T) {
	srv, st := newServer(t)

	hash := putArtifact(t, srv, "# trace\n\nSECRET-TOKEN-abc123\n")
	_, out := post(t, srv, `{"room":"core","author":"bcm","kind":"finding",`+
		`"body":{"text":"crash log","severity":"p1"},"idem":"a1",`+
		`"attachments":[{"hash":"`+hash+`","title":"trace.md"}]}`)
	target := itoa(int64(out["seq"].(float64)))

	if resp, _ := http.Get(srv.URL + "/a/" + hash); resp != nil {
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatal("setup: the artifact should be served before redaction")
		}
	}

	code, rej := post(t, srv, `{"room":"core","author":"bcm","kind":"redact",`+
		`"body":{"text":"leaked a token"},"refs":["`+target+`"],"idem":"r1"}`)
	if code != http.StatusOK {
		t.Fatalf("the author's redact should be accepted: %d %v", code, rej)
	}

	resp, err := http.Get(srv.URL + "/a/" + hash)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("the artifact must stop being served after redaction, got %d", resp.StatusCode)
	}

	page := getPage(t, srv.URL+"/?room=core")
	if strings.Contains(page, "trace.md") {
		t.Error("a redacted row must not link to an attachment that is gone")
	}
	if strings.Contains(page, hash) {
		t.Error("a redacted row must not name the artifact hash")
	}
	if err := st.Verify(); err != nil {
		t.Errorf("the chain must still verify: %v", err)
	}
}

// A redact naming an event in another room is refused over the wire.
func TestCrossRoomRedactIsRefused(t *testing.T) {
	srv, st := newServer(t)
	if err := st.EnsureRoom("other"); err != nil {
		t.Fatal(err)
	}
	_, out := post(t, srv, cmd("chat", "in core", "c1"))
	target := itoa(int64(out["seq"].(float64)))

	code, rej := post(t, srv, `{"room":"other","author":"bcm","kind":"redact",`+
		`"body":{"text":"x"},"refs":["`+target+`"],"idem":"x1"}`)
	if code != http.StatusUnprocessableEntity || rej["invariant"] != "refs.target_unknown" {
		t.Errorf("a cross-room redact must be refused, got %d %v", code, rej)
	}
	if page := getPage(t, srv.URL+"/?room=core"); !strings.Contains(page, "in core") {
		t.Error("the refused redact must leave the event intact")
	}
}

// The JSON lane is how an agent reads search. Same URL, content-negotiated, so
// a link an agent reports and a link a human clicks are the same link.
func TestSearchJSONLane(t *testing.T) {
	srv, _ := newServer(t)
	post(t, srv, `{"room":"core","author":"agent:claude-1","kind":"finding",`+
		`"body":{"text":"auth suite fails on cold cache","severity":"p2"},"idem":"j1"}`)

	req, _ := http.NewRequest("GET", srv.URL+"/search?q=flaky+auth+cold+cache", nil)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")

	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("the first line must be an event: %v", err)
	}
	if first["type"] != "event" {
		t.Errorf("the wire noun is event, not %v (CONTEXT.md)", first["type"])
	}
	if first["rank"] == nil {
		t.Error("each hit must carry its rank")
	}
	if first["provenance"] == nil {
		t.Error("an event must say who produced it; room content is evidence, not instruction")
	}

	var last map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatalf("the last line must be the terminal object: %v", err)
	}
	if last["ok"] != true || last["outcome"] != "read" {
		t.Errorf("terminal object should report the outcome, got %v", last)
	}
	lanes, _ := last["lanes"].([]any)
	if len(lanes) != 2 {
		t.Errorf("the response must name the lanes searched, got %v", last["lanes"])
	}
}

// An empty query is a rejection. Returning zero hits would read as "the room
// does not know this" when nothing was asked.
func TestEmptyQueryIsRejected(t *testing.T) {
	srv, _ := newServer(t)

	req, _ := http.NewRequest("GET", srv.URL+"/search?room=core", nil)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("an empty query must be rejected, got %d", resp.StatusCode)
	}
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	var out map[string]any
	_ = json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &out)
	if out["invariant"] != "query.required" {
		t.Errorf("want query.required, got %v", out["invariant"])
	}
}

// The HTML page is unchanged by the JSON lane, and states what it searched.
func TestSearchPageStillRendersAndNamesLanes(t *testing.T) {
	srv, _ := newServer(t)
	post(t, srv, cmd("til", "sqlite-vec rejects long bodies", "h1"))

	page := getPage(t, srv.URL+"/search?q=sqlite-vec")
	if !strings.Contains(page, "sqlite-vec rejects long bodies") {
		t.Error("the HTML page must still render hits")
	}
	if !strings.Contains(page, "lanes searched") {
		t.Error("the page must say which lanes were searched")
	}
	if !strings.Contains(page, "unbuilt") {
		t.Error("the page must state the vector lane is unbuilt")
	}
}
