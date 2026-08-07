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
	if rej == nil || rej.Invariant != "refs.target_unknown" {
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

// The target must live in the room the redact is posted to, so the record of
// the suppression sits where the suppressed thing was.
func TestRedactTargetMustBeInTheSameRoom(t *testing.T) {
	state := State{
		RoomExists:  okRoom,
		EventAuthor: func(string) (Actor, bool) { return "bcm", true },
		EventRoom:   func(string) (string, bool) { return "other", true },
	}
	_, rej := Decide(state, Command{Room: "core", Author: "bcm", Kind: KindRedact,
		Body: chat("x"), Refs: []string{"10000"}, Idem: "x1"})
	if rej == nil || rej.Invariant != "refs.target_unknown" {
		t.Fatalf("a cross-room target must be refused, got %v", rej)
	}
	if !contains(rej.Detail, "other") {
		t.Error("the refusal should name the room the event is actually in")
	}
}

// Re-redacting is refused rather than absorbed: accepting it silently would
// report success for an act that changed nothing.
func TestRedactRefusesAnAlreadyRedactedEvent(t *testing.T) {
	state := State{
		RoomExists:  okRoom,
		EventAuthor: func(string) (Actor, bool) { return "bcm", true },
		EventRoom:   func(string) (string, bool) { return "core", true },
		IsRedacted:  func(string) bool { return true },
	}
	_, rej := Decide(state, Command{Room: "core", Author: "bcm", Kind: KindRedact,
		Body: chat("again"), Refs: []string{"10000"}, Idem: "x2"})
	if rej == nil || rej.Invariant != "redact.already_redacted" {
		t.Fatalf("a second redact must be refused, got %v", rej)
	}
}

// A seq the log has not assigned yet cannot be pre-redacted: it does not
// resolve, so it lands on the same refusal as any other unknown target. This
// matters because seq jumps by 10,000 on every restart, leaving a wide band of
// plausible-looking future values.
func TestFutureSeqCannotBePreRedacted(t *testing.T) {
	assigned := map[string]bool{"10000": true}
	state := State{
		RoomExists: okRoom,
		EventAuthor: func(ref string) (Actor, bool) {
			if assigned[ref] {
				return "bcm", true
			}
			return "", false
		},
		EventRoom: func(string) (string, bool) { return "core", true },
	}
	// 20000 is inside the next post-restart band, and is not yet assigned.
	_, rej := Decide(state, Command{Room: "core", Author: "bcm", Kind: KindRedact,
		Body: chat("pre-emptive"), Refs: []string{"20000"}, Idem: "x3"})
	if rej == nil || rej.Invariant != "refs.target_unknown" {
		t.Fatalf("an unassigned seq must not be pre-redactable, got %v", rej)
	}
}
