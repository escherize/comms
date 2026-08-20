package cli

import (
	"testing"
	"time"

	"github.com/escherize/comms/core"
)

// Print-only watch advances the cursor like its help promises. It re-delivered
// the same addressed events every interval forever: the print branch skipped
// the high-water mark and the save was gated on a handler being configured
// (study 7, seq 10041).
func TestPrintOnlyWatchAdvancesTheCursor(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)

	if _, err := st.Append(core.Event{Room: "core", Author: "human:sarah",
		Kind: core.KindChat, Recipient: core.Actor(seat),
		Body: map[string]any{"text": "take LIN-9"},
		Lane: core.Addressed}, "w1", time.Now()); err != nil {
		t.Fatal(err)
	}

	var c capture
	if code := Run(c.env(t, srv.URL, ""), []string{"watch", "--as", seat,
		"--once", "--every", "2s"}); code != ExitOK {
		t.Fatalf("watch --once failed: %d %s", code, c.out.String())
	}
	if cur := Cursor(seat, "core", LaneAddressed); cur == 0 {
		t.Error("print-only watch must advance the cursor; it re-delivers forever otherwise")
	}
}
