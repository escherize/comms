package shell

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/bcm/agent_comms/core"
	"net/http"
	"strings"
	"testing"
	"time"
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

// An unattended agent can fill the log at wire speed. The limit is per key,
// because what is being bounded is one seat's loop.
func TestAPerKeyRateLimitReturns429WithARetryAfter(t *testing.T) {
	srv, _ := newServer(t)

	var throttled map[string]any
	var code int
	for i := 0; i < PostBurst+5; i++ {
		code, throttled = post(t, srv, cmd("chat", "flood", itoa(int64(i))))
		if code == http.StatusTooManyRequests {
			break
		}
	}
	if code != http.StatusTooManyRequests {
		t.Fatalf("a seat must not be able to post without bound; got %d after %d posts",
			code, PostBurst+5)
	}
	if throttled["invariant"] != "rate.exceeded" {
		t.Errorf("want rate.exceeded, got %v", throttled["invariant"])
	}
	ms, ok := throttled["retry_after_ms"].(float64)
	if !ok || ms <= 0 {
		t.Errorf(`"slow down" without a number is an invitation to guess: %v`, throttled)
	}
	if throttled["exit"].(float64) != 6 {
		t.Errorf("the throttle must map to the client's exit 6, got %v", throttled["exit"])
	}

	// A different seat is unaffected: the bound is per key.
	if c, _ := post(t, srv, `{"room":"core","author":"agent:other","kind":"chat",`+
		`"body":{"text":"still fine"},"idem":"other1"}`); c != http.StatusOK {
		t.Errorf("one seat's flood must not throttle another, got %d", c)
	}
}

// ADR-0008: budgets never touch task.* or offer.*. Work coordination is not
// chatter, and a limit that could stall a claim turns a busy room into a stuck
// one. The rule is a prefix so a new task.* kind is exempt by construction.
func TestWorkCoordinationIsExemptFromTheBudget(t *testing.T) {
	for _, k := range []string{"task.claim", "task.release", "offer.propose", "offer.settle"} {
		if !exemptFromBudget(core.Kind(k)) {
			t.Errorf("%s must be exempt from the posting budget", k)
		}
	}
	for _, k := range []string{"chat", "finding", "til", "question", "digest"} {
		if exemptFromBudget(core.Kind(k)) {
			t.Errorf("%s is chatter and must be budgeted", k)
		}
	}
}

// docs/CLI.md promises at most two self-corrections. The client cannot keep
// that promise — a self-correction is a fresh process with a fresh idem, so
// there is no lineage in the client to count — but the server sees every
// rejection this seat has had.
func TestTheThirdIdenticalRejectionSaysAskAPerson(t *testing.T) {
	srv, _ := newServer(t)

	bad := func(n int) string {
		return `{"room":"core","author":"agent:c1","kind":"finding",` +
			`"body":{"text":"no severity"},"idem":"sc` + itoa(int64(n)) + `"}`
	}

	for i := 1; i <= 2; i++ {
		code, out := post(t, srv, bad(i))
		if code != http.StatusUnprocessableEntity {
			t.Fatalf("attempt %d should be an ordinary rejection, got %d %v", i, code, out)
		}
		if out["next"] != nil && strings.Contains(fmt.Sprint(out["next"]), "ask a person") {
			t.Errorf("attempt %d gave up too early", i)
		}
	}

	code, out := post(t, srv, bad(3))
	if code != http.StatusConflict {
		t.Fatalf("the third identical rejection must escalate, got %d", code)
	}
	if out["exit"].(float64) != 4 {
		t.Errorf("escalation must be exit 4, not exit 3: %v", out["exit"])
	}
	if !strings.Contains(fmt.Sprint(out["next"]), "agent_comms ask") {
		t.Errorf("the escalation must name the command that asks a human: %v", out["next"])
	}
	if out["invariant"] != "body.severity.invalid" {
		t.Errorf("the escalation must still name what failed, got %v", out["invariant"])
	}
}

// A different invariant is a different problem, and progress resets the count:
// an agent that fixes one thing and hits the next is working, not looping.
func TestASuccessOrADifferentInvariantResetsTheCount(t *testing.T) {
	srv, st := newServer(t)
	seedActor(t, st, "human:bcm")

	post(t, srv, `{"room":"core","author":"agent:c1","kind":"finding",`+
		`"body":{"text":"x"},"idem":"r1"}`)
	post(t, srv, `{"room":"core","author":"agent:c1","kind":"finding",`+
		`"body":{"text":"x"},"idem":"r2"}`)

	// A different invariant: the run starts over rather than inheriting a
	// count from an unrelated mistake.
	if code, _ := post(t, srv, `{"room":"core","author":"agent:c1","kind":"question",`+
		`"body":{"text":"x"},"idem":"r3"}`); code != http.StatusUnprocessableEntity {
		t.Errorf("a different invariant must be an ordinary rejection, got %d", code)
	}

	// A success clears the run entirely.
	if code, _ := post(t, srv, cmd("chat", "got somewhere", "r4")); code != http.StatusOK {
		t.Fatal("setup: the accepted post should succeed")
	}
	for i := 5; i <= 6; i++ {
		if code, _ := post(t, srv, `{"room":"core","author":"agent:c1","kind":"finding",`+
			`"body":{"text":"x"},"idem":"r`+itoa(int64(i))+`"}`); code != http.StatusUnprocessableEntity {
			t.Errorf("after a success the budget restarts; attempt %d got %d", i, code)
		}
	}
}

// The room page must show the tail. Rendering the oldest N means that past N
// events the page freezes on ancient history forever, while the stream keeps
// appending to a head whose body nobody can see.
func TestTheRoomPageShowsTheNewestEventsNotTheOldest(t *testing.T) {
	srv, st := newServer(t)
	for i := 0; i < roomPageRows+20; i++ {
		if _, err := st.Append(core.Event{Room: "core", Author: "agent:c1",
			Kind: core.KindChat, Body: map[string]any{"text": "entry " + itoa(int64(i))},
			Lane: core.LaneOf(core.KindChat)}, "p"+itoa(int64(i)), time.Now()); err != nil {
			t.Fatal(err)
		}
	}

	resp, err := http.Get(srv.URL + "/?room=core")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	page := buf.String()

	newest := "entry " + itoa(int64(roomPageRows+19))
	if !strings.Contains(page, newest) {
		t.Errorf("the page does not contain the most recent entry %q", newest)
	}
	if strings.Contains(page, "entry 0<") || strings.Contains(page, ">entry 0") {
		t.Error("the page still starts at the oldest event")
	}
}

// Latest returns the tail, oldest-first, so a renderer walks it in order.
func TestLatestReturnsTheTailInOrder(t *testing.T) {
	_, st := newServer(t)
	for i := 0; i < 10; i++ {
		if _, err := st.Append(core.Event{Room: "core", Author: "agent:c1",
			Kind: core.KindChat, Body: map[string]any{"text": itoa(int64(i))},
			Lane: core.LaneOf(core.KindChat)}, "l"+itoa(int64(i)), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	recs, err := st.Latest("core", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 {
		t.Fatalf("want 3, got %d", len(recs))
	}
	if recs[0].Text() != "7" || recs[2].Text() != "9" {
		t.Errorf("want the last three ascending, got %q..%q", recs[0].Text(), recs[2].Text())
	}
	if recs[0].Seq > recs[2].Seq {
		t.Error("Latest must return oldest-first so a renderer can walk it")
	}
}
