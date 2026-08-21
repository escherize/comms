package core

import "testing"

func TestEveryKindIsKnown(t *testing.T) {
	for _, k := range AllKinds {
		if !knownKind(k) {
			t.Errorf("kind %q is declared but knownKind rejects it", k)
		}
	}
}

// The lane is a property of the deliberate address (ADR-0016 rule 1); no kind
// carries one. Every system kind posted without a recipient must land ambient.
func TestEveryKindHasADeliberateLane(t *testing.T) {
	state := State{RoomExists: okRoom}
	for _, k := range AllKinds {
		cmd := Command{Room: "core", Author: "human:bcm", Kind: k, Idem: "l-" + string(k),
			Body: map[string]any{"text": "x"}}
		if k == KindRedact {
			cmd.ReplyTo = "evt_1"
		}
		events, rej := Decide(state, cmd)
		if rej != nil {
			t.Fatalf("kind %q: %v", k, rej)
		}
		if events[0].Lane != Ambient {
			t.Errorf("kind %q with no address must land ambient", k)
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
		HasCapability: func(Actor, string) bool { return true },
	}

	for _, k := range AllKinds {
		t.Run(string(k), func(t *testing.T) {
			cmd := Command{Room: "core", Author: "human:bcm", Kind: k, Idem: "i-" + string(k),
				Body: map[string]any{"text": "x"}}

			if k == KindRedact {
				cmd.ReplyTo = "evt_1"
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
