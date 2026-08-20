package shell

import (
	"net/http"
	"strings"
	"testing"
	"time"

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
		if _, _, ok := p.charge("agent:a", "core"); !ok {
			t.Fatal("charge refused early")
		}
		p.release("agent:a", "core")
	}
	remaining, _, ok := p.charge("agent:a", "core")
	if !ok || remaining != PostingBudget-1 {
		t.Fatalf("three refunded charges must leave the budget whole, got remaining=%d ok=%v", remaining, ok)
	}
}

// html.EscapeString does not neutralize a javascript: scheme, so a legacy
// pr.link's hand-built href was a stored-XSS sink. A dangerous scheme must
// render as inert text, never a live anchor.
func TestSafeHrefRejectsJavascriptScheme(t *testing.T) {
	if safeHref("javascript:alert(1)") || safeHref("data:text/html,x") {
		t.Error("javascript:/data: must not be a safe href")
	}
	if !safeHref("https://example.com") || !safeHref("mailto:a@b.c") {
		t.Error("http/https/mailto must be safe hrefs")
	}
}

// linkifyEscape is the fold of pr.link into plain text: a url in a body becomes
// an anchor, but only http/https ever does — a javascript: or data: url in
// prose must stay inert escaped text, and markup in the body must stay escaped.
func TestLinkifyEscape(t *testing.T) {
	got := linkifyEscape("PR up: https://github.com/x/y/pull/1, review please")
	want := `PR up: <a href="https://github.com/x/y/pull/1">https://github.com/x/y/pull/1</a>, review please`
	if got != want {
		t.Errorf("linkify = %q, want %q", got, want)
	}
	for _, inert := range []string{
		"try javascript:alert(1) now",
		"data:text/html,<script>x</script>",
	} {
		if strings.Contains(linkifyEscape(inert), "<a ") {
			t.Errorf("dangerous scheme became an anchor: %q", inert)
		}
	}
	if out := linkifyEscape(`<b>hi</b> http://h.co/a?q="x"`); strings.Contains(out, "<b>") ||
		!strings.Contains(out, `href="http://h.co/a?q=`) {
		t.Errorf("markup must stay escaped while the url anchors: %q", out)
	}
}
