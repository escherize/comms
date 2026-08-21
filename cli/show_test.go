package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/escherize/comms/core"
)

// show fetches one event whole and never moves the cursor (ADR-0019).
func TestShowPrintsOneEventAndLeavesTheCursor(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)

	long := strings.Repeat("a long body line\n", 8) + "the tail line"
	seq, err := st.Append(core.Event{Room: "core", Author: "agent:someone",
		Kind: core.KindChat, Body: map[string]any{"text": long}}, "s1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(core.Event{Room: "core", Author: "agent:someone",
		Kind: core.KindChat, Body: map[string]any{"text": "the next event"}}, "s2", time.Now()); err != nil {
		t.Fatal(err)
	}

	var c capture
	if code := Run(c.env(t, srv.URL, ""), []string{"show", itoa(seq), "--as", seat}); code != ExitOK {
		t.Fatalf("show failed: %d %s", code, c.out.String())
	}
	out := c.out.String()
	if !strings.Contains(out, "the tail line") {
		t.Error("show must print the whole body, not a preview")
	}
	if strings.Contains(out, "the next event") {
		t.Error("show must print exactly one event, not the stream after it")
	}
	if Cursor(seat, "core", LaneAll) != 0 {
		t.Error("showing is not reading; the cursor must not move")
	}

	// A seq that is not there says so and names the room searched.
	var miss capture
	if code := Run(miss.env(t, srv.URL, ""), []string{"show", "999999", "--as", seat}); code != ExitRejected {
		t.Fatalf("an unknown seq must exit 3, got %d: %s", code, miss.out.String())
	}
	if !strings.Contains(miss.out.String(), "seq.unknown") {
		t.Errorf("want seq.unknown, got %s", miss.out.String())
	}
}
