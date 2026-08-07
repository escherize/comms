package shell

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// getJSON asks a lane for JSON and decodes it.
func getJSON(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
	return resp.StatusCode, out
}

// The brief is the one call an agent makes before it decides anything, so it
// has to answer all three questions at once.
func TestRoomBriefAnswersWhatIsInFlight(t *testing.T) {
	srv, st := newServer(t)
	seedActor(t, st, "human:bcm")

	post(t, srv, cmd("chat", "morning", "b1"))
	post(t, srv, `{"room":"core","author":"agent:c2","kind":"status",`+
		`"body":{"text":"migrating projections","step":3,"of":7},"idem":"b2"}`)
	code, out := post(t, srv, `{"room":"core","author":"agent:c2","kind":"question",`+
		`"body":{"text":"safe to reorder?"},"recipient":"human:bcm","idem":"b3"}`)
	if code != http.StatusOK {
		t.Fatalf("setup question rejected: %v", out)
	}
	qseq := int64(out["seq"].(float64))
	post(t, srv, `{"room":"core","author":"agent:c9","kind":"question",`+
		`"body":{"text":"who owns the runner image?"},"recipient":"human:bcm","idem":"b4"}`)
	post(t, srv, `{"room":"core","author":"human:bcm","kind":"answer",`+
		`"body":{"text":"yes"},"refs":["`+itoa(qseq)+`"],"idem":"b5"}`)

	status, body := getJSON(t, srv.URL+"/rooms/core")
	if status != http.StatusOK {
		t.Fatalf("brief returned %d: %v", status, body)
	}
	brief := body["brief"].(map[string]any)

	if brief["head"].(float64) == 0 || brief["events"].(float64) == 0 {
		t.Error("the brief must report head and event count")
	}

	working := brief["working"].([]any)
	if len(working) != 1 {
		t.Fatalf("one actor is working, got %d", len(working))
	}
	w := working[0].(map[string]any)
	if w["author"] != "agent:c2" || w["step"].(float64) != 3 || w["of"].(float64) != 7 {
		t.Errorf("working came back wrong: %v", w)
	}
	if _, ok := w["stalled"]; !ok {
		t.Error("the brief must say who is stalled, not make the reader compute it")
	}

	if brief["ambient"].(map[string]any)["chat"].(float64) < 1 {
		t.Error("ambient counts by kind are missing")
	}

	questions := brief["questions"].([]any)
	if len(questions) != 2 {
		t.Fatalf("two questions were asked, got %d", len(questions))
	}
	var answered, unanswered int
	for _, q := range questions {
		m := q.(map[string]any)
		if m["answered"] == true {
			answered++
			if m["answer_seq"].(float64) == 0 {
				t.Error("an answered question must name the answering seq")
			}
			continue
		}
		unanswered++
		if _, ok := m["waiting_ms"]; !ok {
			t.Error("an unanswered question needs its age; without it nobody knows whether to ask again")
		}
	}
	if answered != 1 || unanswered != 1 {
		t.Errorf("want one answered and one open, got %d and %d", answered, unanswered)
	}
}

// The fold is a projection maintained in the append transaction, not a scan
// over every event's refs at read time.
func TestQuestionFoldIsAProjectionNotAScan(t *testing.T) {
	body, err := readFile("../store/brief.go")
	if err != nil {
		t.Fatal(err)
	}
	var code string
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "//") {
			code += line + "\n"
		}
	}
	if strings.Contains(code, "json_each") {
		t.Error("the brief must not scan refs with json_each; the fold is maintained on append")
	}
	if !strings.Contains(code, "FROM question") {
		t.Error("the brief should read the question projection")
	}
}

// Exactly one definition of stalled: a room brief and a rendered ledger that
// disagree about who is stalled is worse than neither saying.
func TestStalledHasOneDefinition(t *testing.T) {
	for _, f := range []string{"../store/artifact.go", "../store/brief.go", "render.go", "shell.go"} {
		body, err := readFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(body, "15 * time.Minute"); n > 0 && f != "../store/artifact.go" {
			t.Errorf("%s defines its own stall window; there is one, store.StallWindow", f)
		}
	}
}

// A typo'd recipient is accepted forever and waited on forever, so it must be
// rejected at the boundary and the roster must name the spelling that works.
func TestAddressingAnUnknownSeatIsRejectedOverTheWire(t *testing.T) {
	srv, st := newServer(t)
	seedActor(t, st, "human:sarah")

	code, out := post(t, srv, `{"room":"core","author":"agent:c2","kind":"question",`+
		`"body":{"text":"?"},"recipient":"human:sarrah","idem":"u1"}`)
	if code == http.StatusOK {
		t.Fatal("a question to a seat nobody enrolled as must not be accepted")
	}
	if out["invariant"] != "recipient.unknown" {
		t.Errorf("want recipient.unknown, got %v", out)
	}

	status, roster := getJSON(t, srv.URL+"/actors")
	if status != http.StatusOK {
		t.Fatalf("the roster must be readable so the agent can find the right spelling: %d", status)
	}
	var found bool
	for _, a := range roster["actors"].([]any) {
		m := a.(map[string]any)
		if m["actor"] == "human:sarah" {
			found = true
			if m["key_status"] == "" {
				t.Error("the roster must carry key status")
			}
		}
	}
	if !found {
		t.Errorf("human:sarah is not on the roster: %v", roster["actors"])
	}
}

// One roster, two clients: `--to sarah` and the browser's `/ask @sarah` must
// name the same seat, so the expansion happens once at the boundary rather than
// twice in two languages.
func TestShorthandRecipientResolvesTheSameForBothClients(t *testing.T) {
	srv, st := newServer(t)
	seedActor(t, st, "human:sarah")

	code, out := post(t, srv, `{"room":"core","author":"agent:c2","kind":"question",`+
		`"body":{"text":"?"},"recipient":"sarah","idem":"s1"}`)
	if code != http.StatusOK {
		t.Fatalf("a shorthand recipient should resolve, got %d %v", code, out)
	}
	recs, err := st.Since("core", 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	var got string
	for _, r := range recs {
		if r.Kind == "question" {
			got = string(r.Recipient)
		}
	}
	if got != "human:sarah" {
		t.Errorf("the stored recipient must be canonical, got %q", got)
	}
}

// Two seats sharing a local part is exactly when guessing is worst: the wrong
// one waits for an answer and the right one never hears.
func TestAmbiguousShorthandIsRefusedNotGuessed(t *testing.T) {
	srv, st := newServer(t)
	seedActor(t, st, "human:sam")
	seedActor(t, st, "agent:sam")

	code, out := post(t, srv, `{"room":"core","author":"agent:c2","kind":"question",`+
		`"body":{"text":"?"},"recipient":"sam","idem":"s2"}`)
	if code == http.StatusOK {
		t.Fatal("an ambiguous shorthand must not be resolved by guessing")
	}
	if out["invariant"] != "recipient.ambiguous" {
		t.Errorf("want recipient.ambiguous, got %v", out)
	}
	if !strings.Contains(out["detail"].(string), "human:sam") ||
		!strings.Contains(out["detail"].(string), "agent:sam") {
		t.Errorf("the refusal must name both candidates: %v", out["detail"])
	}
}
