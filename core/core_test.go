package core

import "testing"

// okRoom accepts any room; tests that care about unknown rooms supply their own.
func okRoom(string) bool { return true }

func chat(text string) map[string]any { return map[string]any{"text": text} }

// TestDecide is the exhaustive table over the decider. Every accept path, every
// rejection, every invariant. The decider is pure, so this table is the whole
// domain — there is nothing behind it to also test.
func TestDecide(t *testing.T) {
	state := State{
		RoomExists: okRoom,
		EventKind: func(ref string) (Kind, bool) {
			switch ref {
			case "evt_q":
				return KindQuestion, true
			case "evt_chat":
				return KindChat, true
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
			cmd:      Command{Room: "core", Author: "bcm", Kind: KindChat, Body: chat("morning"), Idem: "i1"},
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
				Body: chat("safe to reorder?"), Recipient: "bcm"},
			wantLane: Addressed,
		},
		{
			name: "answer referencing a question is accepted",
			cmd: Command{Room: "core", Author: "bcm", Kind: KindAnswer, Idem: "i4",
				Body: chat("yes"), Recipient: "agent:claude-2", Refs: []string{"evt_q"}},
			wantLane: Addressed,
		},
		{
			name:     "til is accepted and ambient",
			cmd:      Command{Room: "core", Author: "agent:codex-3", Kind: KindTIL, Body: chat("chunk before embed"), Idem: "i5"},
			wantLane: Ambient,
		},
		{
			name: "handoff naming a recipient is accepted and addressed",
			cmd: Command{Room: "core", Author: "bcm", Kind: KindHandoff, Idem: "i6",
				Body: chat("retry path is yours"), Recipient: "agent:codex-3"},
			wantLane: Addressed,
		},
		{
			name: "pr.link with url is accepted",
			cmd: Command{Room: "core", Author: "agent:claude-1", Kind: KindPRLink, Idem: "i7",
				Body: map[string]any{"url": "https://github.com/x/y/pull/1"}},
			wantLane: Ambient,
		},
		{
			name: "redact referencing one event is accepted",
			cmd: Command{Room: "core", Author: "bcm", Kind: KindRedact, Idem: "i8",
				Body: chat("leaked key"), Refs: []string{"evt_chat"}},
			wantLane: Ambient,
		},

		// --- rejections: envelope ---
		{
			name:    "room is required",
			cmd:     Command{Author: "bcm", Kind: KindChat, Body: chat("x"), Idem: "i9"},
			wantErr: "room.required",
		},
		{
			name:    "author is required",
			cmd:     Command{Room: "core", Kind: KindChat, Body: chat("x"), Idem: "i10"},
			wantErr: "author.required",
		},
		{
			name:    "idempotency key is required",
			cmd:     Command{Room: "core", Author: "bcm", Kind: KindChat, Body: chat("x")},
			wantErr: "idem.required",
		},
		{
			name:    "unknown kind is rejected",
			cmd:     Command{Room: "core", Author: "bcm", Kind: "gossip", Body: chat("x"), Idem: "i11"},
			wantErr: "kind.unknown",
		},

		// --- rejections: schema ---
		{
			name:    "chat requires text",
			cmd:     Command{Room: "core", Author: "bcm", Kind: KindChat, Body: map[string]any{}, Idem: "i12"},
			wantErr: "body.text.required",
		},
		{
			name: "finding requires text",
			cmd: Command{Room: "core", Author: "bcm", Kind: KindFinding, Idem: "i13",
				Body: map[string]any{"severity": "p1"}},
			wantErr: "body.text.required",
		},
		{
			name: "finding requires a valid severity",
			cmd: Command{Room: "core", Author: "bcm", Kind: KindFinding, Idem: "i14",
				Body: map[string]any{"text": "x", "severity": "critical"}},
			wantErr: "body.severity.invalid",
		},
		{
			name: "finding rejects missing severity",
			cmd: Command{Room: "core", Author: "bcm", Kind: KindFinding, Idem: "i15",
				Body: map[string]any{"text": "x"}},
			wantErr: "body.severity.invalid",
		},
		{
			name: "pr.link requires url",
			cmd: Command{Room: "core", Author: "bcm", Kind: KindPRLink, Idem: "i16",
				Body: map[string]any{}},
			wantErr: "body.url.required",
		},
		{
			name: "redact must reference exactly one event",
			cmd: Command{Room: "core", Author: "bcm", Kind: KindRedact, Idem: "i17",
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
			cmd: Command{Room: "core", Author: "bcm", Kind: KindHandoff, Idem: "i19",
				Body: chat("someone take this")},
			wantErr: "recipient.required",
		},
		{
			name: "ambient kind cannot name a recipient",
			cmd: Command{Room: "core", Author: "agent:claude-1", Kind: KindFinding, Idem: "i20",
				Body: map[string]any{"text": "x", "severity": "p0"}, Recipient: "bcm"},
			wantErr: "recipient.forbidden",
		},

		// --- rejections: answer must answer something ---
		{
			name: "answer without refs is rejected",
			cmd: Command{Room: "core", Author: "bcm", Kind: KindAnswer, Idem: "i21",
				Body: chat("yes"), Recipient: "agent:claude-2"},
			wantErr: "refs.question_required",
		},
		{
			name: "answer referencing a non-question is rejected",
			cmd: Command{Room: "core", Author: "bcm", Kind: KindAnswer, Idem: "i22",
				Body: chat("yes"), Recipient: "agent:claude-2", Refs: []string{"evt_chat"}},
			wantErr: "refs.question_required",
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

	_, rej := Decide(state, Command{Room: "nope", Author: "bcm", Kind: KindChat,
		Body: chat("x"), Idem: "i1"})
	if rej == nil || rej.Invariant != "room.unknown" {
		t.Fatalf("expected room.unknown, got %v", rej)
	}

	_, rej = Decide(state, Command{Room: "core", Author: "bcm", Kind: KindChat,
		Body: chat("x"), Idem: "i2"})
	if rej != nil {
		t.Fatalf("known room must be accepted, got %v", rej)
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
	cmd := Command{Room: "core", Author: "bcm", Kind: KindChat, Body: chat("x"), Idem: "i1"}

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
		"bcm":            false,
		"sarah":          false,
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
	addressed := []Kind{KindQuestion, KindAnswer, KindHandoff, KindDigest}
	ambient := []Kind{KindChat, KindFinding, KindTIL, KindStatus, KindPRLink, KindRedact}

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
