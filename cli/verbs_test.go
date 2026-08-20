package cli

import (
	"testing"

	"github.com/escherize/comms/core"
)

// knownKindNames is the client-side gate on `comms post <kind>`. It must name
// exactly the kinds core describes, or a post is refused before it reaches the
// server (or a bogus kind slips through). The two once drifted: the hand list
// omitted presence and decline, so `comms post presence` — join's documented
// check-in fallback — was refused client-side. Tie them so they cannot drift.
func TestKnownKindNamesMatchesCoreKinds(t *testing.T) {
	got := knownKindNames()

	want := make([]string, 0, len(core.Kinds()))
	for _, k := range core.Kinds() {
		want = append(want, string(k.Kind))
	}

	if len(got) != len(want) {
		t.Fatalf("knownKindNames has %d kinds, core.Kinds() has %d: %v vs %v",
			len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("knownKindNames[%d]=%q, core.Kinds()[%d]=%q", i, got[i], i, want[i])
		}
	}
}

// The concrete regression: presence must be accepted client-side.
func TestKnownKindAcceptsPresence(t *testing.T) {
	if !knownKind(core.KindPresence) {
		t.Error("knownKind(presence) = false; the client refuses a kind the server accepts")
	}
}
