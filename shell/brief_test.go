package shell

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"github.com/escherize/comms/core"
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
	if !strings.Contains(fmt.Sprint(out["next"]), "comms ask") {
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

// Escalation is priced because fifteen agents share a room with five people.
// A budget nobody can exhaust is not a price.
func TestEscalationSpendsABudgetAndThenRefuses(t *testing.T) {
	srv, st := newServer(t)
	seedActor(t, st, "human:sarah")
	code, out := post(t, srv, `{"room":"core","author":"agent:c1","kind":"finding",`+
		`"body":{"text":"the migration will time out","severity":"p2"},"idem":"e1"}`)
	if code != http.StatusOK {
		t.Fatalf("setup finding: %v", out)
	}
	target := itoa(int64(out["seq"].(float64)))

	esc := func(idem string) (int, map[string]any) {
		t.Helper()
		return postTo(t, srv, "/escalate", `{"room":"core","author":"agent:c1","refs":"`+target+
			`","to":"human:sarah","text":"this blocks Thursday","idem":"`+idem+`"}`)
	}

	for i := 1; i <= EscalationBudget; i++ {
		code, out := esc("esc" + itoa(int64(i)))
		if code != http.StatusOK {
			t.Fatalf("escalation %d should be affordable: %v", i, out)
		}
		if want := float64(EscalationBudget - i); out["remaining"] != want {
			t.Errorf("after %d spends want %v remaining, got %v", i, want, out["remaining"])
		}
	}

	code, out = esc("esc-over")
	if code != http.StatusTooManyRequests {
		t.Fatalf("the budget must run out, got %d", code)
	}
	if out["invariant"] != "escalation.exhausted" {
		t.Errorf("want escalation.exhausted, got %v", out["invariant"])
	}
	if out["exit"].(float64) != 6 {
		t.Errorf("an exhausted budget refills, so it is exit 6 not 4; got %v", out["exit"])
	}
	if ms, ok := out["retry_after_ms"].(float64); !ok || ms <= 0 {
		t.Error(`"no" without "until when" is an invitation to ask again immediately`)
	}
	if !strings.Contains(fmt.Sprint(out["next"]), "already in the room") {
		t.Errorf("the refusal must say the finding is still recorded: %v", out["next"])
	}

	// Nothing was posted by the refused attempt.
	recs, err := st.Since("core", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var questions int
	for _, r := range recs {
		if r.Kind == core.KindQuestion {
			questions++
		}
	}
	if questions != EscalationBudget {
		t.Errorf("want %d escalations in the log, got %d", EscalationBudget, questions)
	}
}

// What lands is an ordinary addressed question referencing the entry. Escalating
// states no new fact, so it must not invent a kind.
func TestAnEscalationIsAnOrdinaryAddressedQuestion(t *testing.T) {
	srv, st := newServer(t)
	seedActor(t, st, "human:sarah")
	_, out := post(t, srv, `{"room":"core","author":"agent:c1","kind":"finding",`+
		`"body":{"text":"cold cache flake","severity":"p2"},"idem":"e9"}`)
	target := itoa(int64(out["seq"].(float64)))

	code, _ := postTo(t, srv, "/escalate", `{"room":"core","author":"agent:c1","refs":"`+target+
		`","to":"sarah","text":"blocks the release","idem":"esc-x"}`)
	if code != http.StatusOK {
		t.Fatalf("escalation failed: %d", code)
	}

	recs, _ := st.Since("core", 0, 50)
	var found bool
	for _, r := range recs {
		if r.Kind != core.KindQuestion {
			continue
		}
		found = true
		if r.Lane != core.Addressed {
			t.Error("an escalation must be addressed; that is the whole point")
		}
		if string(r.Recipient) != "human:sarah" {
			t.Errorf("the shorthand recipient must resolve here too, got %q", r.Recipient)
		}
		if len(r.Refs) != 1 || r.Refs[0] != target {
			t.Errorf("the escalation must reference the entry, got %v", r.Refs)
		}
	}
	if !found {
		t.Error("nothing was appended")
	}
}

// A row with no ceiling destroys what a ledger is for: scanning by folio. A
// fourteen-line trace was 850px of a 981px viewport. The overflow is folded,
// not dropped, behind the same control the collapsed ambient run uses.
func TestALongBodyIsFoldedNotUnbounded(t *testing.T) {
	srv, st := newServer(t)
	long := "panic under -race:\n" + strings.Repeat("goroutine 1 [running]:\n", 40)
	if _, err := st.Append(core.Event{Room: "core", Author: "agent:c1",
		Kind: core.KindFinding, Body: map[string]any{"text": long, "severity": "p2"},
		Lane: core.LaneOf(core.KindFinding)}, "long", time.Now()); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(srv.URL + "/?room=core")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	page := buf.String()

	if !strings.Contains(page, "more line(s)") {
		t.Error("a body past the ceiling must offer to unfold the rest")
	}
	if !strings.Contains(page, `class="more-body"`) {
		t.Error("the overflow must be rendered, not dropped: folding is not truncation")
	}
	if n := strings.Count(page, "goroutine 1 [running]:"); n != 40 {
		t.Errorf("every line must still be on the page, got %d of 40", n)
	}

	// The hidden attribute works by a UA display:none rule, so any author
	// display rule on the same element outranks it and silently unhides
	// everything. This caught exactly that, once.
	if !strings.Contains(page, ".more-body:not([hidden])") {
		t.Error("the folded body's display rule must be scoped to :not([hidden])")
	}

	// .carried is a two-column grid whose first track is the folio width. The
	// fold control lives inside the entry column and needs none of that; at
	// equal specificity the later rule wins, so the override must come after.
	base := strings.Index(page, ".carried {")
	override := strings.Index(page, ".carried.more {")
	if base == -1 || override == -1 {
		t.Fatal("the fold control must reuse the carried-forward look")
	}
	if override < base {
		t.Error("the .carried.more override must follow .carried, or the grid wins " +
			"and the fold label wraps into the folio column")
	}
}

// A body inside the ceiling is left alone: folding a three-line finding would
// be ceremony, not structure.
func TestAShortBodyIsNotFolded(t *testing.T) {
	srv, st := newServer(t)
	if _, err := st.Append(core.Event{Room: "core", Author: "agent:c1",
		Kind: core.KindTIL, Body: map[string]any{"text": "one\ntwo\nthree"},
		Lane: core.LaneOf(core.KindTIL)}, "short", time.Now()); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(srv.URL + "/?room=core")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	if strings.Contains(buf.String(), "more line(s)") {
		t.Error("a short body must not be folded")
	}
}

// Kind-specific rendering: a finding shows its severity and what it is about, a
// handoff names who is taking over, a status shows its position.
func TestEachKindRendersItsOwnFields(t *testing.T) {
	srv, st := newServer(t)
	seedActor(t, st, "human:bcm")

	post(t, srv, `{"room":"core","author":"agent:c1","kind":"finding","body":{`+
		`"text":"TokenCache.warm() runs after the first assertion","severity":"p1",`+
		`"about":"auth.py:88"},"idem":"k1"}`)
	post(t, srv, `{"room":"core","author":"agent:c1","kind":"handoff","body":{`+
		`"text":"the retry path is yours"},"recipient":"human:bcm","idem":"k2"}`)
	post(t, srv, `{"room":"core","author":"agent:c1","kind":"status","body":{`+
		`"text":"migrating","step":3,"of":7},"idem":"k3"}`)

	resp, err := http.Get(srv.URL + "/?room=core")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	page := buf.String()

	for what, want := range map[string]string{
		"a finding's severity":    `class="sev sev-p1"`,
		"what a finding is about": `class="about">auth.py:88`,
		"a handoff's recipient":   `class="to">human:bcm`,
		"a status's position":     `class="step">3/7`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the row does not render %s (looking for %q)", what, want)
		}
	}
}

// A posting budget is not a rate limit, and the difference is the remedy.
// Thirty findings an hour is not too fast to accept — it is too many to read,
// and waiting does not help.
func TestThePostingBudgetAsksForASummaryNotAWait(t *testing.T) {
	// Five seconds a post: an agent working at a human pace. It never trips the
	// rate limiter, which is the point — too fast and too much are different
	// conditions with different answers, and this is the second one.
	srv, _ := newServerEvery(t, 5*time.Second)

	var code int
	var out map[string]any
	for i := 0; i < PostingBudget+5; i++ {
		code, out = post(t, srv, cmd("til", "entry "+itoa(int64(i)), "b"+itoa(int64(i))))
		if code == http.StatusTooManyRequests {
			break
		}
	}
	if code != http.StatusTooManyRequests {
		t.Fatalf("a seat must not be able to fill a room without bound; got %d", code)
	}
	if out["invariant"] != "budget.exhausted" {
		t.Fatalf("want budget.exhausted, got %v", out["invariant"])
	}
	next := fmt.Sprint(out["next"])
	if !strings.Contains(next, "summarizing") && !strings.Contains(next, "Combine") {
		t.Errorf("the remedy for too much is a summary, not a wait: %v", next)
	}
	if out["kept"] != false {
		t.Error("the reply must say what happened to the entry")
	}

	// Not lost, and not silently swallowed: the refusal is explicit and the
	// agent still holds what it was going to say.
	if !strings.Contains(fmt.Sprint(out["detail"]), "nothing was lost") {
		t.Errorf("the refusal must say nothing was lost: %v", out["detail"])
	}
}

// The budget is per room. A busy hour in a bug bash must not silence the same
// seat in core.
func TestThePostingBudgetIsPerRoom(t *testing.T) {
	srv, st := newServerEvery(t, 5*time.Second)
	if err := st.EnsureRoom("bash"); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < PostingBudget+2; i++ {
		post(t, srv, cmd("til", "core entry "+itoa(int64(i)), "pr"+itoa(int64(i))))
	}
	if code, _ := post(t, srv, cmd("til", "one more in core", "pr-over")); code != http.StatusTooManyRequests {
		t.Fatalf("setup: core's budget should be spent, got %d", code)
	}

	code, out := post(t, srv, `{"room":"bash","author":"agent:c1","kind":"til",`+
		`"body":{"text":"a fresh room"},"idem":"other-room"}`)
	if code != http.StatusOK {
		t.Errorf("a spent budget in one room must not silence the seat in another: %d %v", code, out)
	}
}

// Addressed kinds are not charged here. They are priced by the escalation
// budget and by naming a person, which is a cost the author already feels.
func TestAddressedKindsAreNotChargedToThePostingBudget(t *testing.T) {
	srv, st := newServerEvery(t, 5*time.Second)
	seedActor(t, st, "human:bcm")

	for i := 0; i < PostingBudget+2; i++ {
		post(t, srv, cmd("til", "entry "+itoa(int64(i)), "ad"+itoa(int64(i))))
	}
	code, out := post(t, srv, `{"room":"core","author":"agent:c1","kind":"question",`+
		`"body":{"text":"is this still true?"},"recipient":"human:bcm","idem":"ad-q"}`)
	if code != http.StatusOK {
		t.Errorf("an addressed kind must not be refused by the ambient budget: %d %v", code, out)
	}
}

// Work coordination is exempt by construction, so a burst of task.* is never
// delayed however full the room is. The rule is a prefix, so a task kind added
// later is exempt without anyone remembering to add it.
func TestABurstOfWorkCoordinationIsNeverDelayed(t *testing.T) {
	p := newPosting(func() time.Time { return time.Unix(0, 0) })
	for i := 0; i < PostingBudget*10; i++ {
		if _, _, ok := p.charge("agent:c1", "core", core.Kind("task.done")); !ok {
			t.Fatalf("task.done was delayed on attempt %d; claims must never queue", i)
		}
	}
	for i := 0; i < PostingBudget*10; i++ {
		if _, _, ok := p.charge("agent:c1", "core", core.Kind("offer.settle")); !ok {
			t.Fatalf("offer.settle was delayed on attempt %d", i)
		}
	}
	// And the same ledger still bounds ambient chatter.
	var refused bool
	for i := 0; i < PostingBudget+1; i++ {
		if _, _, ok := p.charge("agent:c1", "core", core.KindTIL); !ok {
			refused = true
		}
	}
	if !refused {
		t.Error("the exemption must not disable the budget for everything else")
	}
}

// A search page that does not update is a snapshot presented as an answer.
// During a bug bash the answer changes while you are reading it.
func TestTheSearchPageIsLive(t *testing.T) {
	srv, _ := newServer(t)
	post(t, srv, cmd("til", "FTS5 reads a hyphen as NOT", "s1"))

	resp, err := http.Get(srv.URL + "/search?q=hyphen")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	page := buf.String()

	if !strings.Contains(page, "hyphen") {
		t.Fatal("the query should have matched the seeded entry")
	}
	// One script, two pages: bespoke JS on one of them is a second place for
	// the resume, dedupe and scroll rules to drift apart.
	if !strings.Contains(page, "new EventSource('/stream?room=") {
		t.Error("the search page must subscribe to the same stream the room does")
	}
	if !strings.Contains(page, `data-q="hyphen"`) {
		t.Error("the page must carry its query so the stream can filter on it")
	}
	if !strings.Contains(page, "data-head=") {
		t.Error("the page must carry a resume point, or a reconnect re-appends every hit")
	}
	if strings.Count(page, "new EventSource") != 1 {
		t.Error("the search page must not open a second, bespoke subscription")
	}
}

// The stream's query filter uses the same index the rendered results came from,
// so live rows and rendered rows cannot disagree about what the search meant.
func TestTheStreamFiltersByQuery(t *testing.T) {
	srv, st := newServer(t)
	post(t, srv, cmd("til", "the cold cache flake is run order", "q1"))
	post(t, srv, cmd("til", "unrelated note about deploys", "q2"))

	fs := frames(t, srv.URL+"/stream?room=core&q=flake", "", 500*time.Millisecond)
	if n := countOf(fs, "event"); n != 1 {
		t.Errorf("q= must filter server-side; want 1 event, got %d", n)
	}

	// And it agrees with what Search itself would return.
	hits, err := st.Search("flake", "core", "", "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("setup: Search should find one, got %d", len(hits))
	}
	if !st.MatchesQuery(hits[0].Seq, "flake") {
		t.Error("MatchesQuery and Search disagree about the same event")
	}
	if st.MatchesQuery(hits[0].Seq, "deploys") {
		t.Error("MatchesQuery matched an event that does not contain the term")
	}
	if st.MatchesQuery(hits[0].Seq, "") {
		t.Error("an empty query must match nothing, not everything")
	}
}

// The browser reads the HTML lane, not the JSON one, and the filters were
// applied to only one of them. A query filter that holds for a program reading
// JSON and not for the person reading the page is not a filter.
func TestTheHTMLStreamAppliesTheSameFiltersAsTheJSONOne(t *testing.T) {
	srv, _ := newServer(t)
	post(t, srv, cmd("til", "the cold cache flake is run order", "h1"))
	post(t, srv, cmd("til", "unrelated note about deploys", "h2"))

	// No Accept header: this is the lane the page uses.
	req, _ := http.NewRequest("GET", srv.URL+"/stream?room=core&q=flake", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	body := buf.String()

	if !strings.Contains(body, "cold cache flake") {
		t.Error("the matching event must be sent")
	}
	if strings.Contains(body, "unrelated note about deploys") {
		t.Error("a non-matching event reached the page; the filter is on one lane only")
	}
	// And it arrives in the shape the page was served with. A room row dropped
	// into a search page lands in the wrong columns.
	if !strings.Contains(body, "srow") {
		t.Error("a live search hit must render as a search row, not a room row")
	}
}

// Without a query it is still the room, and still room-shaped.
func TestTheHTMLStreamStillSendsRoomRowsWithoutAQuery(t *testing.T) {
	srv, _ := newServer(t)
	post(t, srv, cmd("til", "an ordinary entry", "h3"))

	req, _ := http.NewRequest("GET", srv.URL+"/stream?room=core", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	body := buf.String()

	if !strings.Contains(body, "an ordinary entry") {
		t.Fatal("the room stream must still send its events")
	}
	if strings.Contains(body, "srow") {
		t.Error("a room row must not be search-shaped")
	}
}

// A replayed escalation costs nothing. Escalating the same entry with the same
// words twice is one act, and the second one interrupts nobody — so charging it
// would spend a budget on an interrupt that did not happen.
func TestAReplayedEscalationIsFree(t *testing.T) {
	srv, st := newServer(t)
	seedActor(t, st, "human:sarah")
	_, out := post(t, srv, `{"room":"core","author":"agent:c1","kind":"finding",`+
		`"body":{"text":"blocks the migration","severity":"p1"},"idem":"rf"}`)
	target := itoa(int64(out["seq"].(float64)))

	body := `{"room":"core","author":"agent:c1","refs":"` + target +
		`","to":"human:sarah","text":"this blocks Thursday","idem":"same-key"}`

	code, first := postTo(t, srv, "/escalate", body)
	if code != http.StatusOK || first["applied"] != true {
		t.Fatalf("setup: %d %v", code, first)
	}
	afterFirst := first["remaining"].(float64)

	code, second := postTo(t, srv, "/escalate", body)
	if code != http.StatusOK {
		t.Fatalf("a replay must not fail, got %d", code)
	}
	if second["applied"] != false {
		t.Error("the second identical escalation must be a replay")
	}
	if second["remaining"].(float64) != afterFirst {
		t.Errorf("a replay spent budget: %v then %v", afterFirst, second["remaining"])
	}

	// And exactly one interruption is in the log.
	recs, _ := st.Since("core", 0, 100)
	var questions int
	for _, r := range recs {
		if r.Kind == core.KindQuestion {
			questions++
		}
	}
	if questions != 1 {
		t.Errorf("one escalation, run twice, must interrupt once; got %d", questions)
	}
}

// A brief that reports counts tells a second arrival how much it does not know.
// It needs to know what the room already contains, before it posts.
func TestTheBriefCarriesContentNotOnlyCounts(t *testing.T) {
	srv, st := newServer(t)
	seedActor(t, st, "human:bcm")

	post(t, srv, `{"room":"core","author":"agent:c1","kind":"finding","body":{`+
		`"text":"the auth suite flakes on a cold cache","severity":"p1",`+
		`"about":"auth.py"},"idem":"bc1"}`)
	post(t, srv, `{"room":"core","author":"agent:c1","kind":"til",`+
		`"body":{"text":"FTS5 reads a hyphen as NOT"},"idem":"bc2"}`)
	post(t, srv, `{"room":"core","author":"agent:c1","kind":"handoff",`+
		`"body":{"text":"the retry path is yours"},"recipient":"human:bcm","idem":"bc3"}`)

	_, body := getJSON(t, srv.URL+"/rooms/core")
	brief := body["brief"].(map[string]any)

	recent, _ := brief["recent"].([]any)
	if len(recent) < 2 {
		t.Fatalf("the brief must carry what the room knows, got %d entries", len(recent))
	}
	var sawFinding bool
	for _, r := range recent {
		m := r.(map[string]any)
		if m["kind"] == "finding" {
			sawFinding = true
			if m["about"] != "auth.py" {
				t.Errorf("a recent finding must carry what it is about, got %v", m["about"])
			}
			if m["severity"] != "p1" {
				t.Errorf("and its severity, got %v", m["severity"])
			}
			if !strings.Contains(fmt.Sprint(m["text"]), "cold cache") {
				t.Errorf("and enough text to recognise it, got %v", m["text"])
			}
		}
	}
	if !sawFinding {
		t.Error("the brief must surface recent findings, not merely count them")
	}

	addressed, _ := brief["addressed"].([]any)
	if len(addressed) != 1 {
		t.Fatalf("the brief must carry recent addressed events, got %d", len(addressed))
	}
	if addressed[0].(map[string]any)["recipient"] != "human:bcm" {
		t.Error("an addressed entry must say who it is for")
	}
}

// A handoff is typed, signed, permanent, addressed — and was completely
// advisory. The sender could not tell "not read yet" from "read and ignored".
func TestTheBriefShowsWhetherAnAddressedEventWasDrained(t *testing.T) {
	srv, st := newServer(t)
	seedActor(t, st, "human:bcm")
	_, out := post(t, srv, `{"room":"core","author":"agent:c1","kind":"handoff",`+
		`"body":{"text":"the retry path is yours"},"recipient":"human:bcm","idem":"dv1"}`)
	handoff := int64(out["seq"].(float64))

	_, body := getJSON(t, srv.URL+"/rooms/core")
	delivery := body["brief"].(map[string]any)["delivery"].([]any)
	if len(delivery) != 1 {
		t.Fatalf("want one seat with something addressed to it, got %d", len(delivery))
	}
	d := delivery[0].(map[string]any)
	if d["actor"] != "human:bcm" {
		t.Errorf("want human:bcm, got %v", d["actor"])
	}
	if d["pending"].(float64) != 1 {
		t.Errorf("the handoff is undrained, so pending must be 1, got %v", d["pending"])
	}

	// The recipient drains it.
	code, _ := postTo(t, srv, "/delivered",
		fmt.Sprintf(`{"actor":"human:bcm","room":"core","addressed_through":%d}`, handoff))
	if code != http.StatusOK {
		t.Fatalf("recording delivery failed: %d", code)
	}

	_, body = getJSON(t, srv.URL+"/rooms/core")
	d = body["brief"].(map[string]any)["delivery"].([]any)[0].(map[string]any)
	if d["pending"].(float64) != 0 {
		t.Errorf("after draining, pending must be 0, got %v", d["pending"])
	}
	if int64(d["addressed_through"].(float64)) != handoff {
		t.Errorf("the watermark must name what was drained, got %v", d["addressed_through"])
	}
}

// Ambient read state stays private. An agent should not be judged on cursor
// position and a human should not feel watched; what is published is only
// whether transferred responsibility was picked up.
func TestOnlyAddressedDeliveryIsPublished(t *testing.T) {
	srv, st := newServer(t)
	seedActor(t, st, "human:bcm")
	for i := 0; i < 5; i++ {
		post(t, srv, cmd("til", "ambient "+itoa(int64(i)), "amb"+itoa(int64(i))))
	}

	_, body := getJSON(t, srv.URL+"/rooms/core")
	delivery, _ := body["brief"].(map[string]any)["delivery"].([]any)
	if len(delivery) != 0 {
		t.Errorf("a room with only ambient traffic must publish no read state, got %v", delivery)
	}
}

// A decline is a fact in the log, addressed back to whoever handed the work
// over. Without it, "I got this and I am not doing it" has no shape and
// divergence is indistinguishable from silence.
func TestADeclineGoesBackToWhoeverHandedItOver(t *testing.T) {
	srv, st := newServer(t)
	seedActor(t, st, "agent:c1")
	seedActor(t, st, "agent:c2")
	code, out := post(t, srv, `{"room":"core","author":"agent:c2","kind":"handoff",`+
		`"body":{"text":"the retry path is yours"},"recipient":"agent:c1","idem":"h1"}`)
	if code != http.StatusOK {
		t.Fatalf("setup handoff: %v", out)
	}
	handoff := itoa(int64(out["seq"].(float64)))

	code, out = post(t, srv, `{"room":"core","author":"agent:c1","kind":"decline",`+
		`"body":{"text":"already three deep in the auth suite"},"refs":["`+handoff+`"],"idem":"dc1"}`)
	if code != http.StatusOK {
		t.Fatalf("a decline must be accepted: %v", out)
	}

	recs, _ := st.Since("core", 0, 50)
	var found bool
	for _, r := range recs {
		if r.Kind != core.KindDecline {
			continue
		}
		found = true
		if string(r.Recipient) != "agent:c2" {
			t.Errorf("a decline goes back to whoever handed it over, got %q", r.Recipient)
		}
		if r.Lane != core.Addressed {
			t.Error("a decline is addressed; the sender needs to see it")
		}
	}
	if !found {
		t.Fatal("the decline was not stored")
	}

	// It refuses a handoff, not anything else.
	code, out = post(t, srv, `{"room":"core","author":"agent:c1","kind":"decline",`+
		`"body":{"text":"no"},"refs":["`+itoa(int64(9999))+`"],"idem":"dc2"}`)
	if code == http.StatusOK {
		t.Error("a decline naming nothing must be refused")
	}
	if out["invariant"] != "refs.unknown" {
		t.Errorf("want refs.unknown, got %v", out["invariant"])
	}
}

// A refusal the user cannot read is a refusal that did not happen. Somebody
// typed "hi", pressed enter, clicked post, and reported nothing happened —
// because the page cannot sign over plain HTTP to anything but localhost and
// said so only in a title attribute.
func TestTheComposerShowsWhyItRefused(t *testing.T) {
	srv, _ := newServer(t)
	page := getPage(t, srv.URL+"/?room=core")

	if !strings.Contains(page, `id="composer-error"`) {
		t.Error("the composer needs somewhere to say why a post did not go")
	}
	if !strings.Contains(page, "bar.hidden = false") {
		t.Error("the failure path must reveal that element, not only set a title")
	}
	// And the specific case that bit a real user: Web Crypto is unavailable
	// over plain HTTP off localhost, and the message has to say what to do.
	i := strings.Index(page, "if(!crypto.subtle)")
	if i == -1 {
		t.Fatal("the page must check for Web Crypto before trying to sign")
	}
	msg := page[i : i+700]
	for _, want := range []string{"HTTPS", "localhost", "comms CLI"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the unsigned-origin message must mention %q so a reader can act on it", want)
		}
	}
}

// A bare letter is a hotkey; the same letter with a modifier belongs to the
// browser. cmd-C focused the composer and swallowed the copy, which a reader
// blames on their own hands rather than on the page.
func TestHotkeysIgnoreModifiersAndSelections(t *testing.T) {
	srv, _ := newServer(t)
	page := getPage(t, srv.URL+"/?room=core")

	i := strings.Index(page, "document.addEventListener('keydown'")
	if i == -1 {
		t.Fatal("no keydown handler")
	}
	handler := page[i : i+900]

	if !strings.Contains(handler, "e.metaKey") || !strings.Contains(handler, "e.ctrlKey") {
		t.Error("hotkeys must bail on a modifier, or they steal cmd-C, cmd-T and cmd-/")
	}
	if !strings.Contains(handler, "isCollapsed") {
		t.Error("a selection means the reader is reading; hotkeys must not fire over one")
	}
	// The guards have to come before the first key test, or they guard nothing.
	guard := strings.Index(handler, "e.metaKey")
	first := strings.Index(handler, "if(e.key===")
	if guard > first {
		t.Error("the modifier guard runs after the first hotkey, so it protects nothing")
	}
}

// A live token names its seat when asked, without being spent: the composer
// sets its actor from the pasted token instead of making the person match
// two fields by hand.
func TestALiveTokenNamesItsSeatWithoutBeingSpent(t *testing.T) {
	srv, st := newServer(t)

	code, out := postTo(t, srv, "/invite", `{"actor":"human:sarah"}`)
	if code != http.StatusOK {
		t.Fatalf("mint: %d %v", code, out)
	}
	token, _ := out["token"].(string)

	code, out = postTo(t, srv, "/invites/whose", `{"token":"`+token+`"}`)
	if code != http.StatusOK {
		t.Fatalf("lookup of a live token must succeed: %d %v", code, out)
	}
	if out["actor"] != "human:sarah" {
		t.Errorf("want human:sarah, got %v", out["actor"])
	}

	// The lookup spent nothing: the token still enrols.
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 6, 14, 1, 0, 0, time.UTC)
	if err := st.RedeemInvite(token, "human:sarah", pub, at); err != nil {
		t.Errorf("the token must survive its own lookup: %v", err)
	}

	// And a spent token answers nothing.
	if code, _ := postTo(t, srv, "/invites/whose", `{"token":"`+token+`"}`); code != http.StatusNotFound {
		t.Errorf("a spent token must not name a seat: %d", code)
	}

	if code, _ := postTo(t, srv, "/invites/whose", `{"token":"deadbeefdeadbeefdeadbeefdeadbeef"}`); code != http.StatusNotFound {
		t.Errorf("an unknown token must 404: %d", code)
	}
}

// Minting from the running hub removes the whole class of "wrong database":
// the process that will redeem the token is the one that created it.
func TestTheHubMintsIntoItsOwnDatabase(t *testing.T) {
	srv, st := newServer(t)

	code, out := postTo(t, srv, "/invite", `{"actor":"human:sarah"}`)
	if code != http.StatusOK {
		t.Fatalf("loopback must be allowed to mint: %d %v", code, out)
	}
	token, _ := out["token"].(string)
	if token == "" {
		t.Fatal("no token returned")
	}

	// It is redeemable by this hub, which is the whole claim.
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	// Redeem against the server's clock, not the wall: newServer pins a fixed
	// base, so time.Now() here would read the token as half a day old.
	at := time.Date(2026, 8, 6, 14, 1, 0, 0, time.UTC)
	if err := st.RedeemInvite(token, "human:sarah", pub, at); err != nil {
		t.Errorf("the hub minted a token it cannot redeem: %v", err)
	}
}

// An un-namespaced seat is refused here as everywhere else, rather than minting
// a token that can never be spent.
func TestTheHubRefusesToMintForABareName(t *testing.T) {
	srv, _ := newServer(t)
	code, out := postTo(t, srv, "/invite", `{"actor":"sarah"}`)
	if code == http.StatusOK {
		t.Fatal("a bare name must not get a token; enrolment would refuse it later")
	}
	if out["invariant"] != "invite.refused" {
		t.Errorf("want invite.refused, got %v", out["invariant"])
	}
}

// Being able to reach the port is not being the operator.
func TestMintingIsLoopbackOrCapability(t *testing.T) {
	if isLoopback("203.0.113.4:5000") {
		t.Error("a public address must not count as loopback")
	}
	for _, addr := range []string{"127.0.0.1:5000", "[::1]:5000"} {
		if !isLoopback(addr) {
			t.Errorf("%s is loopback", addr)
		}
	}
}
