package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/escherize/comms/core"
)

// The loop three agents ran on 2026-08-07, when all three went around the
// client to read the log. Every step here is one an agent took; every assertion
// is a workaround it should not have needed.
func TestTheCrewLoopNeedsNoWorkaround(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)
	seedActor(t, st, "agent:scout")

	// The lead hands out a slice. It is long, because a real assignment is.
	assignment := "You own the truth-of-done slice: tickets 01-06, 08, 17, 19-27. " +
		"Scout owns structure, so overlap is expected on 08 and 17 — say so rather " +
		"than dropping it. Verify each checked criterion against code, not prose."
	lead, err := st.Append(core.Event{Room: "core", Author: "agent:scout",
		Kind: core.KindHandoff, Recipient: core.Actor(seat),
		Body: map[string]any{"text": assignment},
		Lane: core.LaneOf(core.KindHandoff)}, "h1", time.Now())
	if err != nil {
		t.Fatal(err)
	}

	// 1. The assignment arrives whole. The auditor nearly asked for a re-send
	//    because a clipped render read as a garbled message.
	var in capture
	if code := Run(in.env(t, srv.URL, ""), []string{"inbox", "--as", seat}); code != ExitOK {
		t.Fatalf("inbox failed: %d %s", code, in.out.String())
	}
	if !strings.Contains(in.out.String(), "overlap is expected on 08 and 17") {
		t.Fatal("an addressed handoff must render in full; reconstructing it is the workaround")
	}

	// 2. Reading it moved the cursor, and it must still be re-readable.
	var again capture
	if code := Run(again.env(t, srv.URL, ""), []string{"inbox", "--as", seat,
		"--from", itoa(lead)}); code != ExitOK {
		t.Fatalf("replay failed: %d %s", code, again.out.String())
	}
	if !strings.Contains(again.out.String(), "truth-of-done slice") {
		t.Error("--from must replay an event already read")
	}

	// 3. A replay must not move the cursor: re-reading is not reading.
	before := Cursor(seat, "core", LaneAddressed)
	Run(new(capture).env(t, srv.URL, ""), []string{"inbox", "--as", seat, "--from", itoa(lead)})
	if after := Cursor(seat, "core", LaneAddressed); after != before {
		t.Errorf("a replay moved the cursor from %d to %d", before, after)
	}

	// 4. The crew posts findings ambient. The lead re-reads the last hour to
	//    consolidate — the step that sent it to curl /stream by hand.
	for _, f := range []struct{ about, text string }{
		{"19", "TestDocsMatchTheVerbSet diffs no flags"},
		{"19", "the bare binary starts a server instead of listing verbs"},
		{"02", "slash-commands are built; the note says they are not"},
	} {
		var c capture
		if code := Run(c.env(t, srv.URL, ""), []string{"post", "finding", "--as", seat,
			"--severity", "p1", "--about", f.about, "--text", f.text}); code != ExitOK {
			t.Fatalf("post finding failed: %d %s", code, c.out.String())
		}
	}

	var replay capture
	if code := Run(replay.env(t, srv.URL, ""), []string{"read", "--as", seat,
		"--since", "1h", "--full"}); code != ExitOK {
		t.Fatalf("--since replay failed: %d %s", code, replay.out.String())
	}
	for _, want := range []string{"diffs no flags", "listing verbs", "slash-commands"} {
		if !strings.Contains(replay.out.String(), want) {
			t.Errorf("the replay lost %q; this is the step that sent the lead to curl", want)
		}
	}

	// 5. "everything about ticket 19" is a filter, not a hope about prose.
	var found capture
	if code := Run(found.env(t, srv.URL, ""), []string{"search", "19", "--as", seat,
		"--kind", "finding"}); code != ExitOK {
		t.Fatalf("search failed: %d %s", code, found.out.String())
	}
	if n := strings.Count(found.out.String(), `"type":"event"`); n != 2 {
		t.Errorf("want the 2 findings about ticket 19, got %d: %s", n, found.out.String())
	}
}

// count:0 has two meanings and an agent has to tell them apart: the scout could
// not tell "I am current" from "the lead has not started".
func TestAQuietReadSaysWhichKindOfQuiet(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)

	var empty capture
	Run(empty.env(t, srv.URL, ""), []string{"read", "--as", seat})
	if last := lines(t, &empty)[0]; last["state"] != "empty" {
		t.Errorf("a room nobody has posted in should read empty, got %v", last["state"])
	}

	Run(new(capture).env(t, srv.URL, ""), []string{"post", "til", "--as", seat, "--text", "hi"})
	Run(new(capture).env(t, srv.URL, ""), []string{"read", "--as", seat})

	var current capture
	Run(current.env(t, srv.URL, ""), []string{"read", "--as", seat})
	last := lines(t, &current)[0]
	if last["state"] != "caught-up" {
		t.Errorf("a drained room should read caught-up, got %v", last["state"])
	}
	if last["head"] == nil {
		t.Error("caught-up should say what it is caught up to")
	}
}

// A clipped preview must be marked as clipped in the data. An ellipsis alone
// reads as authorial style, and that cost a real agent its trust in a message
// that had arrived intact.
func TestATruncatedPreviewSaysSo(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)

	long := strings.Repeat("this is a long finding body. ", 12)
	if _, err := st.Append(core.Event{Room: "core", Author: "agent:scout",
		Kind: core.KindFinding, Body: map[string]any{"text": long, "severity": "p2"},
		Lane: core.LaneOf(core.KindFinding)}, "long1", time.Now()); err != nil {
		t.Fatal(err)
	}

	var c capture
	Run(c.env(t, srv.URL, ""), []string{"read", "--as", seat})
	var marked bool
	for _, l := range lines(t, &c) {
		if l["type"] == "event" && l["truncated"] == true {
			marked = true
			if l["next"] == nil {
				t.Error("a clipped event must say how to read it whole")
			}
			if l["full_chars"] == nil {
				t.Error("a clipped event should say how much was clipped")
			}
		}
	}
	if !marked {
		t.Error("a clipped preview must carry truncated:true, not only an ellipsis")
	}
}

// Findings and status land ambient, so waiting on a crew is the ambient case —
// the one thing --wait did not support.
func TestReadCanWaitOnTheAmbientLane(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)
	Run(new(capture).env(t, srv.URL, ""), []string{"read", "--as", seat})

	// Posted through the client: a store append does not fan out to subscribers,
	// because fan-out lives on the command path where the seq is assigned.
	go func() {
		time.Sleep(250 * time.Millisecond)
		Run(new(capture).env(t, srv.URL, ""), []string{"post", "finding", "--as", seat,
			"--severity", "p3", "--text", "late finding"})
	}()

	var c capture
	start := time.Now()
	if code := Run(c.env(t, srv.URL, ""), []string{"read", "--as", seat,
		"--wait", "10s", "--until-kind", "finding"}); code != ExitOK {
		t.Fatalf("read --wait failed: %d %s", code, c.out.String())
	}
	if !strings.Contains(c.out.String(), "late finding") {
		t.Error("read --wait must return the ambient event it was waiting for")
	}
	if time.Since(start) > 5*time.Second {
		t.Error("read --wait should return when the event arrives, not at the deadline")
	}
}

// search is in cli.Verbs, so docs/CLI.md documents it. It must therefore exist.
func TestSearchIsBuiltNotDocumentedOnly(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)
	Run(new(capture).env(t, srv.URL, ""), []string{"post", "til", "--as", seat,
		"--text", "FTS5 reads a hyphen as NOT; quote every token"})

	var c capture
	if code := Run(c.env(t, srv.URL, ""), []string{"search", "hyphen", "--as", seat}); code != ExitOK {
		t.Fatalf("search must be built: %d %s", code, c.out.String())
	}
	last := lines(t, &c)
	term := last[len(last)-1]
	if term["hits"].(float64) < 1 {
		t.Errorf("search found nothing it should have: %v", term)
	}

	var empty capture
	if code := Run(empty.env(t, srv.URL, ""), []string{"search", "--as", seat}); code != ExitUsage {
		t.Errorf("an empty query is a rejection, not an empty result; got %d", code)
	}
}

// --quiet defaults on when stdout is not a terminal. Ticket 19 shipped it and
// left it untested, and it is precisely the default that later swallowed the
// whole of --help and the long-entry nudge for every agent that shells out.
func TestQuietDefaultsOnWhenStdoutIsPiped(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	real := os.Stdout
	os.Stdout = w
	piped := Std()
	os.Stdout = real
	if !piped.Quiet {
		t.Error("stdout on a pipe must default to --quiet: a harness merging the two " +
			"streams has to receive JSON and nothing else")
	}

	// A redirect to a file is the other shape of the same thing. /dev/null is
	// deliberately not the test: it is a character device, so the rule
	// correctly leaves --quiet off there.
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	os.Stdout = f
	redirected := Std()
	os.Stdout = real
	if !redirected.Quiet {
		t.Error("stdout redirected to a file must default to --quiet")
	}
}

// A retry command is meant to be run. An unquoted one runs as something else:
// `--text probe: no severity --severity p2` posts the word "probe:" and leaves
// the rest as flags, so the correction silently posts a different event.
func TestRetryCommandsArePasteSafe(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)
	claimed = stdinClaim{}

	var c capture
	Run(c.env(t, srv.URL, ""), []string{"post", "finding", "--as", seat,
		"--text", "probe: a finding with no severity, and an apostrophe's worth of trouble"})

	l := lines(t, &c)
	retry, _ := l[len(l)-1]["retry"].(string)
	if retry == "" {
		t.Fatal("a correctable rejection should offer the corrected command")
	}
	// The whole entry must survive as one argument.
	if !strings.Contains(retry, "'probe: a finding with no severity, and an apostrophe'\"'\"'s worth of trouble'") {
		t.Errorf("the retry is not paste-safe: %s", retry)
	}

	// Dropping --to must drop its value too, or the corrected command fails a
	// second time in a new way.
	var amb capture
	Run(amb.env(t, srv.URL, ""), []string{"post", "til", "--as", seat,
		"--to", "human:bcm", "--text", "ambient with a recipient"})
	l = lines(t, &amb)
	retry, _ = l[len(l)-1]["retry"].(string)
	if strings.Contains(retry, "human:bcm") {
		t.Errorf("the recipient survived as a bare positional: %s", retry)
	}
}

// `next: "stop"` on a usage error that detail explains how to fix is one reply
// contradicting itself.
func TestFixableUsageErrorsSayHowToFixThem(t *testing.T) {
	for _, invariant := range []string{
		"attachment.title_count", "stdin.contested", "content.unreadable",
		"query.required", "replay.contested", "wait.too_long", "flags.invalid",
		"attach.outside_tree", "rate.exceeded",
	} {
		next := verdictFor(invariant, ExitUsage)
		if next == "" {
			t.Errorf("%s has no verdict at all", invariant)
			continue
		}
		if strings.HasPrefix(strings.ToLower(next), "stop") {
			t.Errorf("%s is fixable and its verdict says %q", invariant, next)
		}
	}
	// And the ones that genuinely are terminal must still say stop.
	for _, invariant := range []string{"key.revoked", "key.compromised", "signature.invalid"} {
		if !strings.HasPrefix(strings.ToLower(verdictFor(invariant, ExitRefused)), "stop") {
			t.Errorf("%s must tell an agent to stop", invariant)
		}
	}
}

// Slicing bytes splits a multi-byte rune, and the lone continuation byte
// renders as a replacement character — indistinguishable from the ellipsis that
// belongs there, so the corruption hides exactly where someone would look.
func TestTruncationDoesNotSplitARune(t *testing.T) {
	long := strings.Repeat("θ", 200)
	got, clipped := truncateText(long, 120)
	if !clipped {
		t.Fatal("200 runes should clip at 120")
	}
	if !utf8.ValidString(got) {
		t.Error("truncation produced invalid UTF-8")
	}
	if strings.ContainsRune(got, utf8.RuneError) {
		t.Error("truncation produced a replacement character")
	}
	if short, clipped := truncateText("θθθ", 120); clipped || short != "θθθ" {
		t.Error("a short multi-byte string must be returned whole")
	}
}

// A wake-up that a crashed handler eats is a wake-up nobody knows was lost.
// The cursor advances only on success, which makes delivery at-least-once.
func TestAFailedHandlerReDeliversTheEvent(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)
	seedActor(t, st, "human:bcm")

	if _, err := st.Append(core.Event{Room: "core", Author: "human:bcm",
		Kind: core.KindHandoff, Recipient: core.Actor(seat),
		Body: map[string]any{"text": "take the auth suite"},
		Lane: core.LaneOf(core.KindHandoff)}, "w1", time.Now()); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	log := filepath.Join(dir, "seen")
	failing := filepath.Join(dir, "fail.sh")
	working := filepath.Join(dir, "ok.sh")
	if err := os.WriteFile(failing, []byte("#!/bin/sh\ncat >> "+log+"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(working, []byte("#!/bin/sh\ncat >> "+log+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	var first capture
	if code := Run(first.env(t, srv.URL, ""), []string{"watch", "--as", seat,
		"--every", "2s", "--once", "--", failing}); code != ExitOK {
		t.Fatalf("watch should not fail because a handler did: %d", code)
	}

	var second capture
	if code := Run(second.env(t, srv.URL, ""), []string{"watch", "--as", seat,
		"--every", "2s", "--once", "--", working}); code != ExitOK {
		t.Fatalf("second watch failed: %s", second.out.String())
	}
	if !strings.Contains(second.out.String(), `"type":"woke"`) {
		t.Error("the event was consumed by the handler that failed to handle it")
	}

	// Twice: once for the crash, once for the retry.
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(raw), "take the auth suite"); n != 2 {
		t.Errorf("want the event delivered twice (crash, then retry), got %d", n)
	}
}

// The room is untrusted input, so an event's text must never reach a shell.
func TestTheHandlerNeverSeesTheEventInArgv(t *testing.T) {
	src, err := os.ReadFile("watch.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if strings.Contains(s, `exec.Command("sh"`) || strings.Contains(s, `exec.Command("bash"`) ||
		strings.Contains(s, `"-c"`) {
		t.Error("watch runs the handler through a shell; a handoff reading `; rm -rf ~` " +
			"is a handoff, and a shell would make it a command")
	}
	if !strings.Contains(s, "cmd.Stdin") {
		t.Error("the event must reach the handler on stdin")
	}
}
