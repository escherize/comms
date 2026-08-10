package cli

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/escherize/comms/core"
	"github.com/escherize/comms/store"
)

func lines(t *testing.T, c *capture) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(c.out.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("stdout must be JSONL, got %q", l)
		}
		out = append(out, m)
	}
	return out
}

// seedRoom writes directly to the store: these tests exercise reading, and the
// posting path has its own tests. Going through HTTP would need a second
// enrolled seat to sign as.
func seedRoom(t *testing.T, srv *httptest.Server, st *store.Store) {
	t.Helper()
	enrol(t, srv, st)
	seedEvent(t, st, core.KindTIL, "", "ambient one", "s1")
	seedEvent(t, st, core.KindTIL, "", "ambient two", "s2")
	seedEvent(t, st, core.KindQuestion, seat, "for you", "s3")
	seedEvent(t, st, core.KindTIL, "", "ambient three", "s4")
}

func seedEvent(t *testing.T, st *store.Store, kind core.Kind, to, text, idem string) {
	t.Helper()
	ev := core.Event{
		Room: "core", Author: "agent:c9", Kind: kind,
		Body: map[string]any{"text": text}, Lane: core.LaneOf(kind),
		Recipient: core.Actor(to),
	}
	if _, err := st.Append(ev, idem, time.Now()); err != nil {
		t.Fatalf("seed %s: %v", idem, err)
	}
}

// Two lanes, two cursors. One shared integer would make inbox swallow every
// ambient event beneath the addressed high-water mark — silently.
func TestDrainingInboxDoesNotHideAmbientEvents(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	seedRoom(t, srv, st)

	var in capture
	if code := Run(in.env(t, srv.URL, ""), []string{"inbox", "--as", seat}); code != ExitOK {
		t.Fatalf("inbox failed: %d %s", code, in.out.String())
	}
	inLines := lines(t, &in)
	if got := inLines[len(inLines)-1]["count"]; got != float64(1) {
		t.Fatalf("inbox should see the one addressed event, got %v", got)
	}

	// The addressed event is seq 4 of 4; a shared cursor would now hide the
	// three ambient events beneath it.
	var rd capture
	if code := Run(rd.env(t, srv.URL, ""), []string{"read", "--as", seat}); code != ExitOK {
		t.Fatalf("read failed: %d %s", code, rd.out.String())
	}
	rdLines := lines(t, &rd)
	count, _ := rdLines[len(rdLines)-1]["count"].(float64)
	if count < 4 {
		t.Errorf("read must still see every event after inbox drained; got %v", count)
	}
}

// A quiet room returns in one round trip, on the sentinel, never on a timer.
func TestReadOnAQuietRoomReturnsImmediately(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)

	start := time.Now()
	var c capture
	code := Run(c.env(t, srv.URL, ""), []string{"read", "--as", seat})
	elapsed := time.Since(start)

	if code != ExitOK {
		t.Fatalf("read failed: %d %s", code, c.out.String())
	}
	if elapsed > 5*time.Second {
		t.Errorf("a quiet room must return in one round trip, took %s", elapsed)
	}
	last := lines(t, &c)
	if last[len(last)-1]["count"] != float64(0) {
		t.Errorf("want count 0, got %v", last[len(last)-1]["count"])
	}
}

// The cursor advances, so a second read is empty.
func TestCursorAdvancesBetweenReads(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	seedRoom(t, srv, st)

	var first capture
	Run(first.env(t, srv.URL, ""), []string{"read", "--as", seat})
	f := lines(t, &first)
	if c, _ := f[len(f)-1]["count"].(float64); c == 0 {
		t.Fatal("the first read should see the seeded events")
	}

	var second capture
	Run(second.env(t, srv.URL, ""), []string{"read", "--as", seat})
	s := lines(t, &second)
	if c, _ := s[len(s)-1]["count"].(float64); c != 0 {
		t.Errorf("the second read should be empty, got %v", c)
	}
}

// A filtered read never advances a cursor past events it did not print.
func TestFilteredReadImpliesPeek(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	seedRoom(t, srv, st)

	var filtered capture
	Run(filtered.env(t, srv.URL, ""), []string{"read", "--as", seat, "--kind", "question"})
	fl := lines(t, &filtered)
	term := fl[len(fl)-1]
	if term["peek"] != true {
		t.Error("a filtered read must report that it did not advance the cursor")
	}
	if Cursor(seat, "core", LaneAll) != 0 {
		t.Error("a filtered read must leave the cursor untouched")
	}

	// The unfiltered read still sees everything.
	var full capture
	Run(full.env(t, srv.URL, ""), []string{"read", "--as", seat})
	fu := lines(t, &full)
	if c, _ := fu[len(fu)-1]["count"].(float64); c < 4 {
		t.Errorf("nothing may be lost to the filtered read, got %v", c)
	}
}

// One compact line per event by default; bodies only under --full.
func TestCompactByDefaultFullOnRequest(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	seedRoom(t, srv, st)

	var c capture
	Run(c.env(t, srv.URL, ""), []string{"read", "--as", seat, "--peek"})
	if strings.Contains(c.out.String(), `"provenance"`) {
		t.Error("the compact line must not carry the whole event")
	}
	if !strings.Contains(c.out.String(), `"preview"`) {
		t.Error("the compact line must preview the text")
	}

	var f capture
	Run(f.env(t, srv.URL, ""), []string{"read", "--as", seat, "--peek", "--full"})
	if !strings.Contains(f.out.String(), `"provenance"`) {
		t.Error("--full must print the whole event")
	}
}

// A first read of a large room stays inside a byte budget, so an agent's
// context is not spent on history.
func TestFirstReadOfALargeRoomIsBounded(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)
	for i := range 200 {
		seedEvent(t, st, core.KindTIL, "",
			"a reasonably wordy lesson about something that happened",
			"bulk"+itoa(int64(i)))
	}

	var c capture
	if code := Run(c.env(t, srv.URL, ""), []string{"read", "--as", seat}); code != ExitOK {
		t.Fatalf("read failed: %s", c.out.String())
	}
	const budget = 120 * 1024
	if c.out.Len() > budget {
		t.Errorf("a 200-event read printed %d bytes, over the %d budget", c.out.Len(), budget)
	}
}

// --wait is capped, so a stuck agent surfaces.
func TestWaitIsCapped(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)

	var c capture
	code := Run(c.env(t, srv.URL, ""), []string{"inbox", "--as", seat, "--wait", "2h"})
	if code != ExitUsage {
		t.Fatalf("an over-long wait must be refused, got %d", code)
	}
	l := lines(t, &c)
	if l[len(l)-1]["invariant"] != "wait.too_long" {
		t.Errorf("want wait.too_long, got %v", l[len(l)-1]["invariant"])
	}
}

// Waiting out the clock is exit 0 with a handoff suggestion, not a failure.
func TestWaitingOutTheClockIsSuccessWithAHandoff(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)

	var c capture
	start := time.Now()
	code := Run(c.env(t, srv.URL, ""),
		[]string{"inbox", "--as", seat, "--wait", "2s", "--until-kind", "answer", "--refs", "20014"})
	if code != ExitOK {
		t.Fatalf("waiting out the clock must be exit 0, got %d: %s", code, c.out.String())
	}
	if time.Since(start) < time.Second {
		t.Error("it should actually have waited")
	}
	l := lines(t, &c)
	term := l[len(l)-1]
	if term["outcome"] != "waited" {
		t.Errorf("want outcome waited, got %v", term["outcome"])
	}
	next, _ := term["next"].(string)
	if !strings.Contains(next, "handoff") || !strings.Contains(next, "20014") {
		t.Errorf("the suggestion must name a handoff and the unanswered seq, got %q", next)
	}
	if Cursor(seat, "core", LaneAddressed) != 0 {
		t.Error("a wait that found nothing must not advance the cursor")
	}
}

// Two sessions under one seat must not corrupt each other's cursor file.
func TestConcurrentCursorWritesDoNotCorrupt(t *testing.T) {
	isolateKeys(t)

	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_ = SaveCursor(seat, "core", LaneAll, int64(1000+n))
			_ = SaveCursor(seat, "core", LaneAddressed, int64(2000+n))
		}(i)
	}
	wg.Wait()

	// The file must still parse, and both lanes must be present and sane.
	all := Cursor(seat, "core", LaneAll)
	addressed := Cursor(seat, "core", LaneAddressed)
	if all < 1000 || all > 1019 {
		t.Errorf("the read cursor is corrupt: %d", all)
	}
	if addressed < 2000 || addressed > 2019 {
		t.Errorf("the inbox cursor is corrupt: %d", addressed)
	}

	// And no temp files were left behind.
	entries, _ := os.ReadDir(stateDir())
	for _, ent := range entries {
		if strings.HasPrefix(ent.Name(), ".cursors-") {
			t.Errorf("a temp cursor file was left behind: %s", ent.Name())
		}
	}
}

// A cursor never moves backwards, whatever order writes land in.
func TestCursorNeverGoesBackwards(t *testing.T) {
	isolateKeys(t)
	if err := SaveCursor(seat, "core", LaneAll, 500); err != nil {
		t.Fatal(err)
	}
	if err := SaveCursor(seat, "core", LaneAll, 100); err != nil {
		t.Fatal(err)
	}
	if got := Cursor(seat, "core", LaneAll); got != 500 {
		t.Errorf("a late low write must not rewind the cursor, got %d", got)
	}
}
