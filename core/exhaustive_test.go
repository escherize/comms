package core

import "testing"

// AllKinds is the enumeration every switch in this package must handle. Go has
// no exhaustive matching, so a new Kind added without updating knownKind,
// LaneOf, or checkBody would silently fall through a default and ship. This
// list plus the tests below are the substitute: adding a Kind and forgetting a
// switch fails here instead of in production.
//
// This is the one guarantee a language with ADTs and exhaustive match (the
// Lisette direction in ADR-0009) would give us at compile time rather than in
// a test.
var AllKinds = []Kind{
	KindChat, KindFinding, KindQuestion, KindAnswer, KindTIL,
	KindHandoff, KindStatus, KindPRLink, KindDigest, KindRedact,
}

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
	addressed := map[Kind]bool{
		KindQuestion: true, KindAnswer: true, KindHandoff: true, KindDigest: true,
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
		RoomExists:    okRoom,
		EventKind:     func(string) (Kind, bool) { return KindQuestion, true },
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
