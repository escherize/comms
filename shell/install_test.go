package shell

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/escherize/comms/core"
	"github.com/escherize/comms/store"
)

// The hub serves its own executable at /comms. This is the no-pipe install
// path the agent onboarding prompt depends on: agents refuse curl|sh, so the
// prompt's step 1 is a plain file download of this route.
func TestTheHubServesItsOwnBinary(t *testing.T) {
	srv, _ := newServer(t)
	resp, err := http.Get(srv.URL + "/comms")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /comms: %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("content-type %q, want application/octet-stream", ct)
	}
	if resp.Header.Get("X-Comms-Platform") == "" {
		t.Error("the platform header is what lets a cross-OS caller check before exec")
	}
	if resp.ContentLength < 1<<20 {
		t.Errorf("suspiciously small binary: %d bytes", resp.ContentLength)
	}
}

// The mention layer is client-side JS; this pins that both halves ship in the
// page — the render pass and the composer menu.
func TestThePageCarriesTheMentionLayer(t *testing.T) {
	for _, want := range []string{"function mentionize", "mention-menu"} {
		if !strings.Contains(liveScript+composeScript, want) {
			t.Errorf("the room page must carry %q", want)
		}
	}
}

// A human who @names a seat in ambient chat expects that seat's watch to
// ring. The recipient filter passes mentions — bounded, so a prefix of
// another seat's name does not ring it — while the event's empty recipient
// field still says the protocol never addressed it.
func TestRecipientFilterPassesMentions(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bool
	}{
		{"@agent:bcm/claude-1 count to 100", true},
		{"@claude-1 ping", true},
		{"@bcm/claude-1 ping", true},
		{"@claude-10 is someone else", false},
		{"no mention at all", false},
	} {
		rec := store.Record{Body: map[string]any{"text": tc.text}}
		if got := mentions(rec, "agent:bcm/claude-1"); got != tc.want {
			t.Errorf("mentions(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}

// A refused command must not spend the ambient budget: the charge is
// provisional until an event is kept. Before the refund, PostingBudget
// rejections starved a seat that was correcting itself.
func TestRejectedPostRefundsTheBudget(t *testing.T) {
	p := newPosting(time.Now)
	for i := 0; i < 3; i++ {
		if _, _, ok := p.charge("agent:a", "core", core.KindChat); !ok {
			t.Fatal("charge refused early")
		}
		p.release("agent:a", "core", core.KindChat)
	}
	remaining, _, ok := p.charge("agent:a", "core", core.KindChat)
	if !ok || remaining != PostingBudget-1 {
		t.Fatalf("three refunded charges must leave the budget whole, got remaining=%d ok=%v", remaining, ok)
	}
	// An exempt kind must refund nothing it never charged.
	p.release("agent:a", "core", core.Kind("task.done"))
	if remaining2, _, _ := p.charge("agent:a", "core", core.KindChat); remaining2 != PostingBudget-2 {
		t.Fatalf("an exempt release stole a paid stamp: remaining=%d", remaining2)
	}
}
