package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/escherize/comms/core"
	"github.com/escherize/comms/store"
)

// Searching the raw sentence matches on its stopwords, which every other
// question also contains. Distinctive terms are what actually identify it.
func TestDistinctiveTermsDropStopwords(t *testing.T) {
	got := distinctiveTerms("is migration 0031 safe to reorder ahead of 0029?")
	joined := strings.Join(got, " ")
	for _, stop := range []string{"is", "to", "of", "safe"} {
		for _, g := range got {
			if g == stop {
				t.Errorf("%q is a stopword and should not be searched (got %v)", stop, got)
			}
		}
	}
	for _, want := range []string{"migration", "0031", "reorder"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the distinctive term %q was dropped, got %v", want, got)
		}
	}
}

// A natural question against near-identical stored text must attach the stored
// event, which is the whole point of searching before asking.
func TestAskAttachesPriorContext(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)

	seedActor(t, st, "human:bcm")

	// The room already knows.
	ev := core.Event{Room: "core", Author: "agent:c9", Kind: core.Kind("til"),
		Body: map[string]any{"text": "migration 0031 reorder is safe; 0029 is idempotent"},
		Lane: core.Ambient}
	prior, err := st.Append(ev, "prior", time.Now())
	if err != nil {
		t.Fatal(err)
	}

	var c capture
	code := Run(c.env(t, srv.URL, ""), []string{"ask", "--as", seat, "--to", "human:bcm",
		"--text", "is migration 0031 safe to reorder ahead of 0029?"})
	if code != ExitOK {
		t.Fatalf("ask failed: %d %s", code, c.out.String())
	}

	out := c.out.String()
	if !strings.Contains(out, `"type":"attached"`) {
		t.Fatal("ask must print what it attached")
	}
	if !strings.Contains(out, itoa(prior)) {
		t.Errorf("ask should have attached the prior event %d; got %s", prior, out)
	}
}

// It attaches, it never gates. A search returning nothing must not stop the
// question — a client that refused would impose policy the core does not have.
func TestAskPostsEvenWithNoPriorContext(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)
	seedActor(t, st, "human:bcm")

	var c capture
	code := Run(c.env(t, srv.URL, ""), []string{"ask", "--as", seat, "--to", "human:bcm",
		"--text", "does anyone know about zeppelins"})
	if code != ExitOK {
		t.Fatalf("ask must post regardless of what search found: %d %s", code, c.out.String())
	}
	l := lines(t, &c)
	if l[len(l)-1]["outcome"] != "accepted" {
		t.Errorf("the question should be posted, got %v", l[len(l)-1])
	}
}

// A reply sends no recipient; the core derives one from the ref's counterpart
// (ADR-0016 rule 2) — replying is a post that --refs the question.
func TestReplyNeedsNoRecipient(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)

	q := core.Event{Room: "core", Author: "agent:asker", Kind: core.Kind("question"),
		Body: map[string]any{"text": "safe to reorder?"}, Recipient: core.Actor(seat),
		Lane: core.Addressed}
	qseq, err := st.Append(q, "q1", time.Now())
	if err != nil {
		t.Fatal(err)
	}

	var c capture
	code := Run(c.env(t, srv.URL, ""), []string{"post", "--as", seat,
		"--refs", itoa(qseq), "--text", "yes, 0029 is idempotent"})
	if code != ExitOK {
		t.Fatalf("reply failed: %d %s", code, c.out.String())
	}

	recs, _ := st.Since("core", 0, 100)
	var found bool
	for _, r := range recs {
		if len(r.Refs) == 1 && r.Refs[0] == itoa(qseq) {
			found = true
			if string(r.Recipient) != "agent:asker" {
				t.Errorf("the reply should reach whoever asked, got %q", r.Recipient)
			}
			if r.Lane != core.Addressed {
				t.Error("a routed reply must land addressed")
			}
		}
	}
	if !found {
		t.Fatal("the reply was not stored")
	}
}

// attach uploads once and prints a hash post accepts, so a rejected post does
// not mean re-running a three-minute test to reproduce consumed stdin.
func TestAttachThenPostByHash(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)

	var up capture
	env := up.env(t, srv.URL, "# suite\n\n| pkg | status |\n|---|---|\n| auth | fail |\n")
	if code := Run(env, []string{"attach", "-"}); code != ExitOK {
		t.Fatalf("attach failed: %d %s", code, up.out.String())
	}
	hash, _ := lines(t, &up)[0]["hash"].(string)
	if hash == "" {
		t.Fatal("attach must print a hash")
	}

	// The pipe is consumed, but the hash survives a rejected post.
	var bad capture
	if code := Run(bad.env(t, srv.URL, ""), []string{"post", "--as", seat,
		"--attach-hash", hash}); code != ExitRejected {
		t.Fatalf("expected a text rejection, got %d", code)
	}

	var good capture
	if code := Run(good.env(t, srv.URL, ""), []string{"post", "--as", seat,
		"--text", "suite red", "--attach-hash", hash,
		"--attach-title", "suite.md"}); code != ExitOK {
		t.Fatalf("the corrected post must succeed: %d %s", code, good.out.String())
	}

	recs, _ := st.Since("core", 0, 100)
	var attached bool
	for _, r := range recs {
		for _, a := range r.Attach {
			if a.Hash == hash && a.Title == "suite.md" {
				attached = true
			}
		}
	}
	if !attached {
		t.Error("the artifact should be attached with its title")
	}
}

// Titles pair by position; a mismatch is refused rather than zipped, because a
// wrong title on a report is worse than no title.
func TestMismatchedAttachTitlesAreRefused(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)

	var c capture
	code := Run(c.env(t, srv.URL, ""), []string{"post", "--as", seat,
		"--text", "x", "--attach-hash", strings.Repeat("a", 64),
		"--attach-title", "one", "--attach-title", "two"})
	if code != ExitUsage {
		t.Fatalf("a title/attachment mismatch must be refused, got %d", code)
	}
	if l := lines(t, &c); l[len(l)-1]["invariant"] != "attachment.title_count" {
		t.Errorf("want attachment.title_count, got %v", l[len(l)-1]["invariant"])
	}
}

// --text - and --attach - cannot both claim stdin: the second would read
// nothing and be told nothing.
func TestStdinCannotBeClaimedTwice(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)
	claimed = stdinClaim{}

	var c capture
	code := Run(c.env(t, srv.URL, "some text\n"), []string{"post", "--as", seat,
		"--text", "-", "--attach", "-"})
	if code != ExitUsage {
		t.Fatalf("contested stdin must be refused, got %d: %s", code, c.out.String())
	}
	if l := lines(t, &c); l[len(l)-1]["invariant"] != "stdin.contested" {
		t.Errorf("want stdin.contested, got %v", l[len(l)-1]["invariant"])
	}
	claimed = stdinClaim{}
}

// Long text gets a nudge on stderr and posts anyway: policy lives in the core.
func TestLongTextIsNudgedNotRefused(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)
	claimed = stdinClaim{}

	long := "line one\nline two\nline three\nline four\nline five"
	var c capture
	if code := Run(c.env(t, srv.URL, ""), []string{"post", "--as", seat,
		"--text", long}); code != ExitOK {
		t.Fatalf("long text must still post, got %d: %s", code, c.out.String())
	}
	// The nudge must reach a piped agent, which never sees stderr: --quiet
	// defaults on when stdout is not a terminal.
	if !strings.Contains(c.out.String(), `"type":"advice"`) {
		t.Error("the nudge must be a JSONL line on stdout, where an agent reads")
	}
	if !strings.Contains(c.out.String(), "--attach") {
		t.Error("the nudge should point at --attach")
	}
	if !strings.Contains(c.err.String(), "--attach") {
		t.Error("a human on a terminal should see the nudge too")
	}
	if l := lines(t, &c); l[len(l)-1]["outcome"] != "accepted" {
		t.Error("advice must not displace the terminal result line")
	}

	if !isLongForm("```go\nfunc main(){}\n```") {
		t.Error("a fenced block should trip the nudge")
	}
	if isLongForm("one short line") {
		t.Error("a short entry should not be nudged")
	}
	claimed = stdinClaim{}
}

// --text-file is the reason shell quoting stopped being an argument for MCP.
func TestTextFromAFile(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)
	claimed = stdinClaim{}

	path := t.TempDir() + "/entry.md"
	if err := writeFile(path, "a finding with \"quotes\", $vars and `backticks`"); err != nil {
		t.Fatal(err)
	}

	var c capture
	if code := Run(c.env(t, srv.URL, ""), []string{"post", "--as", seat,
		"--text-file", path}); code != ExitOK {
		t.Fatalf("--text-file failed: %d %s", code, c.out.String())
	}
	recs, _ := st.Since("core", 0, 10)
	var found bool
	for _, r := range recs {
		if strings.Contains(r.Text(), "backticks") {
			found = true
		}
	}
	if !found {
		t.Error("the file's text should have been posted verbatim")
	}
	claimed = stdinClaim{}
}

// --help must survive a piped stdout: --quiet defaults on there, and that is
// every agent shelling out to learn a verb's flags.
func TestHelpReachesAPipedCaller(t *testing.T) {
	isolateKeys(t)
	for _, verb := range Verbs {
		if verb == "search" || verb == "room" {
			continue // designed, not built
		}
		var c capture
		env := c.env(t, "http://127.0.0.1:1", "")
		env.Out.Quiet = true
		if code := Run(env, []string{verb, "--help"}); code != ExitOK {
			t.Errorf("%s --help exited %d", verb, code)
		}
		// Plain text, not a JSON envelope: help's piped consumer is a model
		// whose harness truncates escaped blobs (two study agents filed it).
		if strings.Contains(c.out.String(), `"type":"help"`) {
			t.Errorf("%s --help still wraps help in a JSON envelope", verb)
		}
		if strings.TrimSpace(c.out.String()) == "" {
			t.Errorf("%s --help printed nothing a piped caller can read", verb)
		}
	}
}

// The two flags that made shell quoting stop being an argument for MCP have to
// be discoverable from the tool itself, not only from the skill file.
func TestPostHelpDocumentsTextAndAttachFlags(t *testing.T) {
	isolateKeys(t)
	var c capture
	env := c.env(t, "http://127.0.0.1:1", "")
	env.Out.Quiet = true
	Run(env, []string{"post", "--help"})
	for _, want := range []string{"--text-file", "--text -", "--attach", "--attach-hash"} {
		if !strings.Contains(c.out.String(), want) {
			t.Errorf("post --help does not mention %q", want)
		}
	}
}

// seedActor makes a seat addressable the way the hub does: by having it post.
func seedActor(t *testing.T, st *store.Store, actor string) {
	t.Helper()
	_, err := st.Append(core.Event{Room: "core", Author: core.Actor(actor),
		Kind: core.KindChat, Body: map[string]any{"text": "here"},
		Lane: core.Ambient}, "seed-"+actor, time.Now())
	if err != nil {
		t.Fatal(err)
	}
}

// Naming a room switches to it. Every chat tool works this way, and an agent
// that orients into one room and then posts into another has written a
// wrong-room event into a log that cannot take it back.
func TestSelectingARoomSticksAndFlagStillWins(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)
	if err := st.EnsureRoom("bash"); err != nil {
		t.Fatal(err)
	}

	var sel capture
	if code := Run(sel.env(t, srv.URL, ""), []string{"room", "bash", "--as", seat}); code != ExitOK {
		t.Fatalf("room bash failed: %d %s", code, sel.out.String())
	}
	if got := SelectedRoom(seat); got != "bash" {
		t.Fatalf("the selection did not stick: %q", got)
	}

	var p capture
	if code := Run(p.env(t, srv.URL, ""), []string{"post", "--as", seat,
		"--text", "landed in the selected room"}); code != ExitOK {
		t.Fatalf("post failed: %d %s", code, p.out.String())
	}
	if !roomHas(t, st, "bash", "landed in the selected room") {
		t.Error("a post with no --room must land in the selected room")
	}

	var o capture
	if code := Run(o.env(t, srv.URL, ""), []string{"post", "--as", seat,
		"--room", "core", "--text", "one-off into core"}); code != ExitOK {
		t.Fatalf("override failed: %d %s", code, o.out.String())
	}
	if !roomHas(t, st, "core", "one-off into core") {
		t.Error("--room must override the selection, so a one-off needs no switch back")
	}
	if roomHas(t, st, "bash", "one-off into core") {
		t.Error("the override leaked into the selected room")
	}
}

// Selecting a room that does not exist must fail loudly, not silently persist a
// selection that sends every later post into a room nobody reads.
func TestSelectingAnUnknownRoomIsRefused(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)

	var c capture
	if code := Run(c.env(t, srv.URL, ""), []string{"room", "nope", "--as", seat}); code == ExitOK {
		t.Fatal("an unknown room must not be selectable")
	}
	if got := SelectedRoom(seat); got == "nope" {
		t.Error("a refused selection must not persist")
	}
}

// whoami is the orientation call: seat, key status, where posts will land, and
// how far each lane has been read.
func TestWhoamiReportsRoomKeyStatusAndCursors(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)
	if err := st.EnsureRoom("bash"); err != nil {
		t.Fatal(err)
	}
	Run(new(capture).env(t, srv.URL, ""), []string{"room", "bash", "--as", seat})

	var c capture
	if code := Run(c.env(t, srv.URL, ""), []string{"whoami", "--as", seat}); code != ExitOK {
		t.Fatalf("whoami failed: %d %s", code, c.out.String())
	}
	last := lines(t, &c)[0]
	if last["room"] != "bash" {
		t.Errorf("whoami must report where posts will land, got %v", last["room"])
	}
	if last["key_status"] != "active" {
		t.Errorf("whoami must report key status, got %v", last["key_status"])
	}
	cursors, ok := last["cursors"].(map[string]any)
	if !ok {
		t.Fatalf("whoami must report each cursor, got %v", last["cursors"])
	}
	for _, lane := range []string{"read", "inbox"} {
		if _, ok := cursors[lane]; !ok {
			t.Errorf("no cursor reported for the %s lane", lane)
		}
	}
}

// The roster is what recipient.unknown is checked against, so `room` with no
// argument has to show it.
func TestRoomWithNoArgumentListsRoomsAndSeats(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)
	seedActor(t, st, "human:sarah")

	var c capture
	if code := Run(c.env(t, srv.URL, ""), []string{"room", "--as", seat}); code != ExitOK {
		t.Fatalf("room failed: %d %s", code, c.out.String())
	}
	out := c.out.String()
	if !strings.Contains(out, `"type":"rooms"`) || !strings.Contains(out, `"type":"actors"`) {
		t.Fatalf("room must list both rooms and seats: %s", out)
	}
	if !strings.Contains(out, "human:sarah") {
		t.Error("the roster must name the seats, since a misspelt --to is refused")
	}
}

func roomHas(t *testing.T, st *store.Store, room, text string) bool {
	t.Helper()
	recs, err := st.Since(room, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if strings.Contains(r.Text(), text) {
			return true
		}
	}
	return false
}
