package cli

import (
	"strings"
	"testing"
)

// A random key made every re-run a new event, so the fix an agent reaches for
// by reflex — run it again — was the thing that turned one finding into two.
func TestReRunningTheSameCommandIsAReplay(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)
	claimed = stdinClaim{}

	args := []string{"post", "--as", seat,
		"--text", "the auth suite flakes on a cold cache"}

	var first capture
	if code := Run(first.env(t, srv.URL, ""), args); code != ExitOK {
		t.Fatalf("first post failed: %s", first.out.String())
	}
	var second capture
	if code := Run(second.env(t, srv.URL, ""), args); code != ExitOK {
		t.Fatalf("the re-run must not fail: %s", second.out.String())
	}

	a, b := lines(t, &first)[0], lines(t, &second)[0]
	if a["seq"] != b["seq"] {
		t.Errorf("the re-run got a new seq: %v then %v", a["seq"], b["seq"])
	}
	if b["outcome"] != "replayed" {
		t.Errorf("the re-run must report replayed, got %v", b["outcome"])
	}

	recs, err := st.Since("core", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	for _, r := range recs {
		if strings.Contains(r.Text(), "flakes on a cold cache") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("one command run twice must land once, got %d", n)
	}
}

// A different command is a different event. Otherwise the dedup would swallow
// real work, which is worse than a duplicate: the second one is true.
func TestADifferentCommandIsANewEvent(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)
	claimed = stdinClaim{}

	base := []string{"post", "--as", seat, "--text", "the first"}
	Run(new(capture).env(t, srv.URL, ""), base)

	for _, changed := range [][]string{
		{"post", "--as", seat, "--text", "the second"},
		{"post", "--as", seat, "--text", "the first", "--about", "run-2"},
		{"post", "--as", seat, "--text", "the first", "--reply-to", "42"},
	} {
		var c capture
		if code := Run(c.env(t, srv.URL, ""), changed); code != ExitOK {
			t.Fatalf("%v failed: %s", changed, c.out.String())
		}
		if got := lines(t, &c)[0]["outcome"]; got != "accepted" {
			t.Errorf("%v should be a new event, got %v", changed, got)
		}
		claimed = stdinClaim{}
	}

	recs, _ := st.Since("core", 0, 100)
	var n int
	for _, r := range recs {
		if strings.HasPrefix(r.Text(), "the ") {
			n++
		}
	}
	if n != 4 {
		t.Errorf("four distinct commands must land four times, got %d", n)
	}
}

// The key is stable across runs of the identical command and different across
// processes, so a person typing the same thing twice an hour apart gets two
// events — the second one is true and must not vanish.
func TestTheRunKeySeparatesAttempts(t *testing.T) {
	isolateKeys(t)
	cmd := map[string]any{
		"room": "core", "author": "agent:c1", "kind": "til",
		"body": map[string]any{"text": "one lesson"},
	}

	var c capture
	withRun := func(run string) *Env {
		e := c.env(t, "http://x", "")
		e.LookupEnv = func(k string) (string, bool) {
			if k == "COMMS_RUN" {
				return run, true
			}
			return "", false
		}
		return e
	}
	e1, e2, e3 := withRun("attempt-1"), withRun("attempt-1"), withRun("attempt-2")

	if contentIdem(e1, cmd) != contentIdem(e2, cmd) {
		t.Error("the same command in the same attempt must produce the same key")
	}
	if contentIdem(e1, cmd) == contentIdem(e3, cmd) {
		t.Error("a new attempt must produce a new key, or a true second post vanishes")
	}
}

// Map iteration is randomized in Go. A key built by ranging a body would differ
// between two runs of the same command, which is the bug this file removes,
// reintroduced one level down.
func TestTheKeyIsStableAcrossMapOrdering(t *testing.T) {
	isolateKeys(t)
	var c capture
	e := c.env(t, "http://x", "")
	e.LookupEnv = func(k string) (string, bool) {
		if k == "COMMS_RUN" {
			return "fixed", true
		}
		return "", false
	}

	body := map[string]any{"text": "x", "severity": "p2", "about": "auth.py", "step": 3.0}
	first := contentIdem(e, map[string]any{
		"room": "core", "author": "agent:c1", "kind": "finding", "body": body,
		"refs": []string{"LIN-1", "LIN-2"},
	})
	for i := 0; i < 50; i++ {
		again := contentIdem(e, map[string]any{
			"refs": []string{"LIN-1", "LIN-2"}, "body": body,
			"kind": "finding", "author": "agent:c1", "room": "core",
		})
		if again != first {
			t.Fatalf("the key changed between runs of the same command: %s vs %s", first, again)
		}
	}
}

// --idem is the escape hatch for a caller with a natural key, and it wins.
func TestAnExplicitKeyWins(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)
	claimed = stdinClaim{}

	// Two different texts under one natural key: the second is a conflict, not
	// a silent replacement, because the key says they are the same post and the
	// content says they are not.
	Run(new(capture).env(t, srv.URL, ""), []string{"post", "--as", seat,
		"--idem", "LIN-214-note", "--text", "the first version"})

	var c capture
	code := Run(c.env(t, srv.URL, ""), []string{"post", "--as", seat,
		"--idem", "LIN-214-note", "--text", "an edited version"})
	if code == ExitOK {
		t.Fatal("the same key with different content must not silently replace")
	}
	last := lines(t, &c)
	term := last[len(last)-1]
	if term["invariant"] != "idem.conflict" {
		t.Fatalf("want idem.conflict, got %v", term["invariant"])
	}
	// It reads as a re-run with an edited flag, because that is what it usually is.
	detail, _ := term["detail"].(string)
	if !strings.Contains(detail, "re-run with an edited flag") {
		t.Errorf("the conflict must explain what actually happened: %q", detail)
	}
	if !strings.Contains(detail, "--idem") {
		t.Errorf("it must name the flag that caused it: %q", detail)
	}
	_ = st
}

// One session, one seat makes the seat the session — so an agent seat scopes
// replays to itself, which survives what a pid cannot: shelling out once per
// command, and the session resuming under a new process.
func TestAnAgentSeatScopesTheRunToItself(t *testing.T) {
	agent := &Env{Seat: "agent:bcm/claude-s7",
		LookupEnv: func(string) (string, bool) { return "", false }}
	if got := runKey(agent); got != "seat-agent:bcm/claude-s7" {
		t.Errorf("an agent seat with no run should scope to the seat, got %q", got)
	}

	human := &Env{Seat: "human:bcm",
		LookupEnv: func(string) (string, bool) { return "", false }}
	if got := runKey(human); got != processRun {
		t.Errorf("a human seat keeps process scope: typing it again means it again, got %q", got)
	}

	overridden := &Env{Seat: "agent:bcm/claude-s7",
		LookupEnv: func(k string) (string, bool) {
			if k == "COMMS_RUN" {
				return "LIN-214-attempt-2", true
			}
			return "", false
		}}
	if got := runKey(overridden); got != "LIN-214-attempt-2" {
		t.Errorf("an explicit run always wins, got %q", got)
	}
}
