package core

import (
	"strings"
	"testing"
)

// okRoom accepts any room; tests that care about unknown rooms supply their own.
func okRoom(string) bool { return true }

func chat(text string) map[string]any { return map[string]any{"text": text} }

// TestDecide is the exhaustive table over the decider. Every accept path, every
// rejection, every invariant. The decider is pure, so this table is the whole
// domain — there is nothing behind it to also test.
func TestDecide(t *testing.T) {
	state := State{
		RoomExists: okRoom,
		// evt_q is an addressed event (a question from agent:claude-2 to
		// human:bcm); evt_chat is ambient. Reply-routing reads these.
		EventAuthor: func(ref string) (Actor, bool) {
			switch ref {
			case "evt_q":
				return "agent:claude-2", true
			case "evt_chat":
				return "human:bcm", true
			}
			return "", false
		},
		EventRecipient: func(ref string) (Actor, bool) {
			switch ref {
			case "evt_q":
				return "human:bcm", true
			case "evt_chat":
				return "", true
			}
			return "", false
		},
	}

	tests := []struct {
		name     string
		cmd      Command
		wantErr  string // empty means accept
		wantLane Lane
	}{
		{
			name:     "chat is accepted and ambient",
			cmd:      Command{Room: "core", Author: "human:bcm", Kind: KindChat, Body: chat("morning"), Idem: "i1"},
			wantLane: Ambient,
		},
		{
			name: "finding with severity is accepted and ambient",
			cmd: Command{Room: "core", Author: "agent:claude-1", Kind: KindFinding, Idem: "i2",
				Body: map[string]any{"text": "nil deref", "severity": "p2"}},
			wantLane: Ambient,
		},
		{
			name: "question naming a recipient is accepted and addressed",
			cmd: Command{Room: "core", Author: "agent:claude-2", Kind: KindQuestion, Idem: "i3",
				Body: chat("safe to reorder?"), Recipient: "human:bcm"},
			wantLane: Addressed,
		},
		{
			name: "a reply refs an addressed event and routes to its counterpart",
			cmd: Command{Room: "core", Author: "human:bcm", Kind: KindChat, Idem: "i4",
				Body: chat("yes"), Refs: []string{"evt_q"}},
			wantLane: Addressed,
		},
		{
			name: "a reply whose ref is ambient stays ambient",
			cmd: Command{Room: "core", Author: "agent:claude-1", Kind: KindChat, Idem: "i4b",
				Body: chat("agreed"), Refs: []string{"evt_chat"}},
			wantLane: Ambient,
		},
		{
			name:     "til is accepted and ambient",
			cmd:      Command{Room: "core", Author: "agent:codex-3", Kind: KindTIL, Body: chat("chunk before embed"), Idem: "i5"},
			wantLane: Ambient,
		},
		{
			name: "handoff naming a recipient is accepted and addressed",
			cmd: Command{Room: "core", Author: "human:bcm", Kind: KindHandoff, Idem: "i6",
				Body: chat("retry path is yours"), Recipient: "agent:codex-3"},
			wantLane: Addressed,
		},
		{
			name: "redact referencing one event is accepted",
			cmd: Command{Room: "core", Author: "human:bcm", Kind: KindRedact, Idem: "i8",
				Body: chat("leaked key"), Refs: []string{"evt_chat"}},
			wantLane: Ambient,
		},

		// --- rejections: envelope ---
		{
			name:    "room is required",
			cmd:     Command{Author: "human:bcm", Kind: KindChat, Body: chat("x"), Idem: "i9"},
			wantErr: "room.required",
		},
		{
			name:    "author is required",
			cmd:     Command{Room: "core", Kind: KindChat, Body: chat("x"), Idem: "i10"},
			wantErr: "author.required",
		},
		{
			name:    "idempotency key is required",
			cmd:     Command{Room: "core", Author: "human:bcm", Kind: KindChat, Body: chat("x")},
			wantErr: "idem.required",
		},
		{
			name:    "unknown kind is rejected",
			cmd:     Command{Room: "core", Author: "human:bcm", Kind: "gossip", Body: chat("x"), Idem: "i11"},
			wantErr: "kind.unknown",
		},

		// --- rejections: schema ---
		{
			name:    "chat requires text",
			cmd:     Command{Room: "core", Author: "human:bcm", Kind: KindChat, Body: map[string]any{}, Idem: "i12"},
			wantErr: "body.text.required",
		},
		{
			name: "finding requires text",
			cmd: Command{Room: "core", Author: "human:bcm", Kind: KindFinding, Idem: "i13",
				Body: map[string]any{"severity": "p1"}},
			wantErr: "body.text.required",
		},
		{
			name: "finding requires a valid severity",
			cmd: Command{Room: "core", Author: "human:bcm", Kind: KindFinding, Idem: "i14",
				Body: map[string]any{"text": "x", "severity": "critical"}},
			wantErr: "body.severity.invalid",
		},
		{
			name: "finding rejects missing severity",
			cmd: Command{Room: "core", Author: "human:bcm", Kind: KindFinding, Idem: "i15",
				Body: map[string]any{"text": "x"}},
			wantErr: "body.severity.invalid",
		},
		{
			name: "redact must reference exactly one event",
			cmd: Command{Room: "core", Author: "human:bcm", Kind: KindRedact, Idem: "i17",
				Body: chat("x"), Refs: []string{"a", "b"}},
			wantErr: "refs.exactly_one",
		},

		// --- rejections: attention lanes ---
		{
			name: "addressed kind must name a recipient",
			cmd: Command{Room: "core", Author: "agent:claude-2", Kind: KindQuestion, Idem: "i18",
				Body: chat("anyone?")},
			wantErr: "recipient.required",
		},
		{
			name: "handoff must name a recipient",
			cmd: Command{Room: "core", Author: "human:bcm", Kind: KindHandoff, Idem: "i19",
				Body: chat("someone take this")},
			wantErr: "recipient.required",
		},
		{
			name: "ambient kind cannot name a recipient",
			cmd: Command{Room: "core", Author: "agent:claude-1", Kind: KindFinding, Idem: "i20",
				Body: map[string]any{"text": "x", "severity": "p0"}, Recipient: "human:bcm"},
			wantErr: "recipient.forbidden",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events, rej := Decide(state, tt.cmd)

			if tt.wantErr != "" {
				if rej == nil {
					t.Fatalf("expected rejection %q, got %d events", tt.wantErr, len(events))
				}
				if rej.Invariant != tt.wantErr {
					t.Fatalf("expected invariant %q, got %q (%s)", tt.wantErr, rej.Invariant, rej.Detail)
				}
				if events != nil {
					t.Fatalf("a rejected command must produce no events, got %d", len(events))
				}
				if rej.Detail == "" {
					t.Error("rejection must carry detail so an agent can self-correct")
				}
				return
			}

			if rej != nil {
				t.Fatalf("expected acceptance, got rejection %q: %s", rej.Invariant, rej.Detail)
			}
			if len(events) != 1 {
				t.Fatalf("expected exactly 1 event, got %d", len(events))
			}
			if events[0].Lane != tt.wantLane {
				t.Errorf("lane: want %v, got %v", tt.wantLane, events[0].Lane)
			}
			if events[0].Kind != tt.cmd.Kind {
				t.Errorf("kind: want %v, got %v", tt.cmd.Kind, events[0].Kind)
			}
			if events[0].Author != tt.cmd.Author {
				t.Errorf("author: want %v, got %v", tt.cmd.Author, events[0].Author)
			}
		})
	}
}

// TestUnknownRoomIsRejected proves the decider reads the decision projection
// rather than trusting the command.
func TestUnknownRoomIsRejected(t *testing.T) {
	state := State{RoomExists: func(r string) bool { return r == "core" }}

	_, rej := Decide(state, Command{Room: "nope", Author: "human:bcm", Kind: KindChat,
		Body: chat("x"), Idem: "i1"})
	if rej == nil || rej.Invariant != "room.unknown" {
		t.Fatalf("expected room.unknown, got %v", rej)
	}

	_, rej = Decide(state, Command{Room: "core", Author: "human:bcm", Kind: KindChat,
		Body: chat("x"), Idem: "i2"})
	if rej != nil {
		t.Fatalf("known room must be accepted, got %v", rej)
	}
}

// A non-member is refused before room existence is checked, and the refusal
// leaks nothing: it names only the author's own rooms, never the room they
// were refused from and never whether it exists. This ordering is the whole
// no-leak property — a seat scoped away from a room must not be able to probe
// for it.
func TestNonMemberIsRefusedWithoutLeakingRoomExistence(t *testing.T) {
	// sarah is a member of comms and ops only. Every room "exists" in this
	// state, so if the refusal were room.unknown-shaped it would still be a
	// leak — the point is she is told nothing about the target at all.
	state := State{
		RoomExists:  func(string) bool { return true },
		IsMember:    func(a Actor, room string) bool { return room == "comms" || room == "ops" },
		MemberRooms: func(Actor) []string { return []string{"comms", "ops"} },
	}

	// A member posts fine.
	if _, rej := Decide(state, Command{Room: "comms", Author: "human:sarah",
		Kind: KindChat, Body: chat("hi"), Idem: "i1"}); rej != nil {
		t.Fatalf("a member must be able to post, got %v", rej)
	}

	// A non-member room is refused room.not_a_member — not room.unknown — and
	// the detail names sarah's own rooms, never "secret".
	_, rej := Decide(state, Command{Room: "secret", Author: "human:sarah",
		Kind: KindChat, Body: chat("probe"), Idem: "i2"})
	if rej == nil || rej.Invariant != "room.not_a_member" {
		t.Fatalf("a non-member must get room.not_a_member, got %v", rej)
	}
	if strings.Contains(rej.Detail, "secret") {
		t.Errorf("the refusal must not name the room it refused: %q", rej.Detail)
	}
	if !strings.Contains(rej.Detail, "comms") || !strings.Contains(rej.Detail, "ops") {
		t.Errorf("the refusal must name the author's own rooms: %q", rej.Detail)
	}

	// The membership check runs BEFORE RoomExists: a non-member naming a room
	// that does not exist still gets room.not_a_member, so existence never
	// leaks through room.unknown.
	strict := State{
		RoomExists:  func(r string) bool { return r == "comms" || r == "ops" },
		IsMember:    func(a Actor, room string) bool { return room == "comms" },
		MemberRooms: func(Actor) []string { return []string{"comms"} },
	}
	_, rej = Decide(strict, Command{Room: "ghost", Author: "human:sarah",
		Kind: KindChat, Body: chat("probe"), Idem: "i3"})
	if rej == nil || rej.Invariant != "room.not_a_member" {
		t.Fatalf("a non-member naming a nonexistent room must get room.not_a_member, not room.unknown, got %v", rej)
	}

	// A '*'-scoped seat is a member of everything and falls through to the
	// existence check — where room.unknown is not a leak, since it sees all
	// rooms anyway.
	super := State{
		RoomExists:  func(r string) bool { return r == "comms" },
		IsMember:    func(Actor, string) bool { return true },
		MemberRooms: func(Actor) []string { return []string{"*"} },
	}
	_, rej = Decide(super, Command{Room: "ghost", Author: "human:owner",
		Kind: KindChat, Body: chat("x"), Idem: "i4"})
	if rej == nil || rej.Invariant != "room.unknown" {
		t.Fatalf("an all-rooms seat must reach room.unknown for a typo, got %v", rej)
	}
}

// A seat with no rooms at all is told exactly that, still without confirming
// the target room exists.
func TestMemberlessSeatRefusalIsExistenceIndependent(t *testing.T) {
	state := State{
		RoomExists:  func(string) bool { return true },
		IsMember:    func(Actor, string) bool { return false },
		MemberRooms: func(Actor) []string { return nil },
	}
	_, rej := Decide(state, Command{Room: "anything", Author: "human:nobody",
		Kind: KindChat, Body: chat("x"), Idem: "i1"})
	if rej == nil || rej.Invariant != "room.not_a_member" {
		t.Fatalf("want room.not_a_member, got %v", rej)
	}
	if strings.Contains(rej.Detail, "anything") {
		t.Errorf("must not name the target room: %q", rej.Detail)
	}
}

// TestLaneIsAPropertyOfKind is the attention invariant: nothing an author writes
// inside an event changes its lane. Severity is an author-set claim, so a p0
// finding stays ambient — escalation is priced elsewhere, never assumed here.
func TestLaneIsAPropertyOfKind(t *testing.T) {
	state := State{RoomExists: okRoom}

	for _, sev := range []string{"p0", "p1", "p2", "p3"} {
		events, rej := Decide(state, Command{Room: "core", Author: "agent:claude-1",
			Kind: KindFinding, Idem: "i-" + sev,
			Body: map[string]any{"text": "x", "severity": sev}})
		if rej != nil {
			t.Fatalf("severity %s: unexpected rejection %v", sev, rej)
		}
		if events[0].Lane != Ambient {
			t.Errorf("severity %s must stay ambient; escalation is priced, not claimed", sev)
		}
	}
}

// TestDecideIsDeterministic guards the purity the whole design leans on.
func TestDecideIsDeterministic(t *testing.T) {
	state := State{RoomExists: okRoom}
	cmd := Command{Room: "core", Author: "human:bcm", Kind: KindChat, Body: chat("x"), Idem: "i1"}

	first, rej1 := Decide(state, cmd)
	second, rej2 := Decide(state, cmd)

	if rej1 != nil || rej2 != nil {
		t.Fatalf("unexpected rejections: %v %v", rej1, rej2)
	}
	if len(first) != len(second) {
		t.Fatalf("event count differed across identical calls: %d vs %d", len(first), len(second))
	}
	a, b := first[0], second[0]
	if a.Room != b.Room || a.Author != b.Author || a.Kind != b.Kind ||
		a.Lane != b.Lane || a.Recipient != b.Recipient {
		t.Error("Decide must be deterministic over identical inputs")
	}
}

func TestActorIsAgent(t *testing.T) {
	cases := map[Actor]bool{
		"agent:claude-1": true,
		"agent:codex-3":  true,
		"human:bcm":      false,
		"human:sarah":    false,
		"agent":          false,
		"":               false,
	}
	for actor, want := range cases {
		if got := actor.IsAgent(); got != want {
			t.Errorf("%q.IsAgent() = %v, want %v", actor, got, want)
		}
	}
}

func TestLaneOf(t *testing.T) {
	addressed := []Kind{KindQuestion, KindHandoff}
	ambient := []Kind{KindChat, KindFinding, KindTIL, KindStatus, KindRedact}

	for _, k := range addressed {
		if LaneOf(k) != Addressed {
			t.Errorf("%s must be addressed", k)
		}
	}
	for _, k := range ambient {
		if LaneOf(k) != Ambient {
			t.Errorf("%s must be ambient", k)
		}
	}
}

// A reply's recipient is derived from the ref's counterpart (ADR-0016 rule 2).
// The rule lives in the core so every client gets it, rather than one client
// reimplementing what the browser does not share.
func TestReplyRecipientIsDerivedFromTheRef(t *testing.T) {
	// evt_q: agent:claude-2 asked human:bcm.
	state := State{
		RoomExists: okRoom,
		EventAuthor: func(ref string) (Actor, bool) {
			if ref == "evt_q" {
				return "agent:claude-2", true
			}
			return "", false
		},
		EventRecipient: func(ref string) (Actor, bool) {
			if ref == "evt_q" {
				return "human:bcm", true
			}
			return "", false
		},
	}

	// The addressee replies: back to whoever asked.
	events, rej := Decide(state, Command{Room: "core", Author: "human:bcm", Kind: KindChat,
		Body: chat("yes, safe"), Refs: []string{"evt_q"}, Idem: "a1"})
	if rej != nil {
		t.Fatalf("a reply with no recipient must derive one: %v", rej)
	}
	if events[0].Recipient != "agent:claude-2" {
		t.Errorf("recipient should be the ref's author, got %q", events[0].Recipient)
	}
	if events[0].Lane != Addressed {
		t.Error("a routed reply must land addressed")
	}

	// The asker follows up: stays with the person they asked.
	events, rej = Decide(state, Command{Room: "core", Author: "agent:claude-2", Kind: KindChat,
		Body: chat("also — the backfill?"), Refs: []string{"evt_q"}, Idem: "a2"})
	if rej != nil {
		t.Fatal(rej)
	}
	if events[0].Recipient != "human:bcm" {
		t.Errorf("a follow-up should route to the original recipient, got %q", events[0].Recipient)
	}

	// A third party citing the exchange interrupts nobody.
	events, rej = Decide(state, Command{Room: "core", Author: "agent:codex-3", Kind: KindChat,
		Body: chat("relevant to my slice too"), Refs: []string{"evt_q"}, Idem: "a3"})
	if rej != nil {
		t.Fatal(rej)
	}
	if events[0].Recipient != "" || events[0].Lane != Ambient {
		t.Errorf("a third-party ref must stay ambient, got recipient=%q", events[0].Recipient)
	}
}

// A redact's ref names its target, not a conversation: suppressing your own
// addressed event must not route the redact to the person you had addressed.
func TestRedactDoesNotReplyRoute(t *testing.T) {
	state := State{
		RoomExists:     okRoom,
		EventAuthor:    func(string) (Actor, bool) { return "agent:claude-2", true },
		EventRecipient: func(string) (Actor, bool) { return "human:bcm", true },
	}
	events, rej := Decide(state, Command{Room: "core", Author: "agent:claude-2", Kind: KindRedact,
		Body: chat("pasted a token"), Refs: []string{"77"}, Idem: "r1"})
	if rej != nil {
		t.Fatal(rej)
	}
	if events[0].Recipient != "" || events[0].Lane != Ambient {
		t.Errorf("a redact must stay ambient, got recipient=%q", events[0].Recipient)
	}
}

// A recipient nobody enrolled as is a typo the append-only log keeps forever,
// and its author waits for an answer nobody was asked for.
func TestAddressingAnUnenrolledSeatIsRejected(t *testing.T) {
	roster := map[Actor]bool{"human:sarah": true, "agent:c1": true}
	s := State{RoomExists: okRoom, ActorEnrolled: func(a Actor) bool { return roster[a] }}

	_, rej := Decide(s, Command{
		Room: "core", Author: "agent:c1", Kind: KindQuestion,
		Recipient: "human:sarrah", Body: map[string]any{"text": "is it safe?"}, Idem: "i1",
	})
	if rej == nil {
		t.Fatal("a question to an unenrolled seat must be rejected, not addressed to nobody")
	}
	if rej.Invariant != "recipient.unknown" {
		t.Errorf("want recipient.unknown, got %s", rej.Invariant)
	}

	if _, rej := Decide(s, Command{
		Room: "core", Author: "agent:c1", Kind: KindQuestion,
		Recipient: "human:sarah", Body: map[string]any{"text": "is it safe?"}, Idem: "i2",
	}); rej != nil {
		t.Errorf("the enrolled spelling must be accepted, got %v", rej)
	}
}

// The rule is the core's, so it cannot be true in one client and false in the
// other: a decider with no roster is a decider that has not been wired up.
func TestRecipientCheckIsSkippedOnlyWhenNoRosterIsWired(t *testing.T) {
	s := State{RoomExists: okRoom}
	if _, rej := Decide(s, Command{
		Room: "core", Author: "agent:c1", Kind: KindQuestion,
		Recipient: "human:anyone", Body: map[string]any{"text": "?"}, Idem: "i3",
	}); rej != nil {
		t.Errorf("with no roster wired the check cannot run; got %v", rej)
	}
}

// A reply's ref must live in the reply's room to route. A cross-room ref reads
// as nonexistent — deriving from it would address a seat that never spoke
// here, and a distinct refusal would be a cross-room existence oracle.
func TestReplyRoutingIsRoomScoped(t *testing.T) {
	s := State{
		RoomExists:     okRoom,
		EventAuthor:    func(ref string) (Actor, bool) { return "human:q", true },
		EventRecipient: func(ref string) (Actor, bool) { return "agent:a", true },
		EventRoom:      func(ref string) (string, bool) { return "other-room", true },
	}
	events, rej := Decide(s, Command{
		Room: "core", Author: "agent:a", Kind: KindChat,
		Refs: []string{"42"}, Body: map[string]any{"text": "yes"}, Idem: "x1",
	})
	if rej != nil {
		t.Fatal(rej)
	}
	if events[0].Recipient != "" || events[0].Lane != Ambient {
		t.Errorf("a cross-room ref must not route; got recipient=%q", events[0].Recipient)
	}
}
