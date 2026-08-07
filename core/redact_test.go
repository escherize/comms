package core

import "testing"

// Redaction is permanent and instant, so an unauthorized one is unrecoverable.
// Without this rule any actor could erase any other actor's event at wire speed
// — verified against the running server before the rule existed.
func TestOnlyTheAuthorMayRedact(t *testing.T) {
	state := State{
		RoomExists: okRoom,
		EventAuthor: func(ref string) (Actor, bool) {
			if ref == "10000" {
				return "bcm", true
			}
			return "", false
		},
	}

	_, rej := Decide(state, Command{Room: "core", Author: "mallory", Kind: KindRedact,
		Body: chat("nuking"), Refs: []string{"10000"}, Idem: "m1"})
	if rej == nil || rej.Invariant != "redact.not_author" {
		t.Fatalf("another actor must not redact: got %v", rej)
	}
	if !contains(rej.Detail, "bcm") {
		t.Error("the refusal should name who may do it")
	}

	events, rej := Decide(state, Command{Room: "core", Author: "bcm", Kind: KindRedact,
		Body: chat("my paste"), Refs: []string{"10000"}, Idem: "b1"})
	if rej != nil {
		t.Fatalf("the author must be able to redact their own event: %v", rej)
	}
	if len(events) != 1 {
		t.Fatal("expected one event")
	}
}

// A redact naming nothing previously returned applied:true and did nothing,
// telling an agent the secret was gone while it stayed readable.
func TestRedactMustNameARealEvent(t *testing.T) {
	state := State{
		RoomExists:  okRoom,
		EventAuthor: func(string) (Actor, bool) { return "", false },
	}
	_, rej := Decide(state, Command{Room: "core", Author: "bcm", Kind: KindRedact,
		Body: chat("x"), Refs: []string{"999999"}, Idem: "g1"})
	if rej == nil || rej.Invariant != "refs.unknown" {
		t.Fatalf("a redact naming nothing must be refused, got %v", rej)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
