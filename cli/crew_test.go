package cli

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bcm/agent_comms/core"
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
	if term["lanes"] == nil && term["vector"] == nil {
		t.Error("the reply must say which lanes were searched; a lexical-only result " +
			"over an absent semantic lane is a true result read as a false conclusion")
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
