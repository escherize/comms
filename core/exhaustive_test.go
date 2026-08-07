package core

import "testing"

func TestEveryKindIsKnown(t *testing.T) {
	for _, k := range AllKinds {
		if !knownKind(k) {
			t.Errorf("kind %q is declared but knownKind rejects it", k)
		}
	}
}

// Every kind must land in exactly one lane, deliberately. The default arm of
// LaneOf makes silence look like a decision, so assert each kind's lane is the
// one someone chose.
func TestEveryKindHasADeliberateLane(t *testing.T) {
	// Written out rather than derived from LaneOf, so this asserts the intent
	// against the implementation instead of the implementation against itself.
	addressed := map[Kind]bool{
		KindQuestion: true, KindAnswer: true, KindHandoff: true,
		KindDigest: true, KindDecline: true,
	}
	for _, k := range AllKinds {
		want := Ambient
		if addressed[k] {
			want = Addressed
		}
		if got := LaneOf(k); got != want {
			t.Errorf("kind %q: lane %v, expected %v — a new kind must be classified, not defaulted", k, got, want)
		}
	}
}

// Every kind must be reachable through Decide with some body. A kind that
// cannot be accepted at all is a kind nobody can post.
func TestEveryKindIsPostable(t *testing.T) {
	// Every capability granted: this test asks whether a kind can be posted at
	// all, not who may post it. Authorization has its own tests.
	state := State{
		RoomExists: okRoom,
		EventKind: func(ref string) (Kind, bool) {
			if ref == "evt_h" {
				return KindHandoff, true
			}
			return KindQuestion, true
		},
		HasCapability: func(Actor, string) bool { return true },
	}

	for _, k := range AllKinds {
		t.Run(string(k), func(t *testing.T) {
			cmd := Command{Room: "core", Author: "human:bcm", Kind: k, Idem: "i-" + string(k),
				Body: map[string]any{"text": "x"}}

			switch k {
			case KindFinding:
				cmd.Body["severity"] = "p2"
			case KindPRLink:
				cmd.Body["url"] = "https://example.com/pr/1"
			case KindRedact:
				cmd.Refs = []string{"evt_1"}
			case KindAnswer:
				cmd.Refs = []string{"evt_q"}
			case KindDecline:
				cmd.Refs = []string{"evt_h"}
			}
			if LaneOf(k) == Addressed {
				cmd.Recipient = "someone"
			}

			events, rej := Decide(state, cmd)
			if rej != nil {
				t.Fatalf("kind %q cannot be posted at all: %s (%s)", k, rej.Invariant, rej.Detail)
			}
			if len(events) != 1 || events[0].Kind != k {
				t.Fatalf("kind %q did not round-trip through Decide", k)
			}
		})
	}
}

// knownKind must accept exactly the kinds Kinds() describes — no more. A kind
// the server accepts and nothing documents is a kind an agent discovers by
// accident; a kind documented and refused is worse.
func TestKnownKindAcceptsExactlyTheDocumentedSet(t *testing.T) {
	documented := map[Kind]bool{}
	for _, k := range Kinds() {
		documented[k.Kind] = true
		if !knownKind(k.Kind) {
			t.Errorf("Kinds() describes %q and knownKind rejects it", k.Kind)
		}
		if k.Means == "" || k.Requires == "" {
			t.Errorf("kind %q is described with an empty field", k.Kind)
		}
	}
	// Every constant declared in this package must be in the documented set.
	for _, k := range []Kind{
		KindChat, KindFinding, KindQuestion, KindAnswer, KindTIL, KindHandoff,
		KindStatus, KindPRLink, KindDigest, KindRedact, KindDecline,
	} {
		if !documented[k] {
			t.Errorf("kind %q exists and Kinds() does not describe it, so no document "+
				"and no command can tell an agent it is there", k)
		}
	}
}
