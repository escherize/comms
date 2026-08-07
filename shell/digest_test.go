package shell

import (
	"crypto/ed25519"
	"strings"
	"testing"
	"time"

	"github.com/bcm/agent_comms/core"
	"github.com/bcm/agent_comms/store"
)

// grantDigest gives the bot the capability the core checks. Without it the bot
// is refused exactly as an agent would be, which is the point of ticket 26.
func digestBot(t *testing.T, st *store.Store) DigestBot {
	t.Helper()
	if err := st.Grant("agent:digest", core.CapDigest, "human:bcm", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.RegisterKey("agent:digest", pubFor(t), time.Now()); err != nil {
		t.Fatal(err)
	}
	return DigestBot{Actor: "agent:digest", Room: "core", To: "human:bcm"}
}

// A quiet window produces nothing. A digest that says "nothing happened" teaches
// people to ignore digests, and the next one that matters arrives already
// discounted.
func TestAQuietWindowProducesNoDigest(t *testing.T) {
	srv, st, sv := newServerFull(t, time.Millisecond)
	_ = srv
	bot := digestBot(t, st)

	posted, err := sv.digestStep(bot, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if posted {
		t.Error("an empty room produced a digest")
	}

	// Chat alone is not news either: nothing shipped, nothing learned, nothing
	// stuck, nothing asked.
	post(t, srv, cmd("chat", "morning", "d0"))
	if posted, err := sv.digestStep(bot, time.Now()); err != nil || posted {
		t.Errorf("chatter alone should not be worth interrupting anyone: posted=%v err=%v", posted, err)
	}
}

// The digest summarizes only the window since the last one. A summary of
// everything is the room; a summary of what changed is news.
func TestADigestCoversOnlyTheWindowSinceTheLastOne(t *testing.T) {
	srv, st, sv := newServerFull(t, time.Millisecond)
	seedActor(t, st, "human:bcm")
	bot := digestBot(t, st)

	post(t, srv, `{"room":"core","author":"agent:c1","kind":"til",`+
		`"body":{"text":"the first lesson, before any digest"},"idem":"w1"}`)
	if posted, err := sv.digestStep(bot, time.Now()); err != nil || !posted {
		t.Fatalf("a window with a lesson in it should produce a digest: %v %v", posted, err)
	}

	post(t, srv, `{"room":"core","author":"agent:c1","kind":"til",`+
		`"body":{"text":"the second lesson, after the first digest"},"idem":"w2"}`)
	if posted, err := sv.digestStep(bot, time.Now()); err != nil || !posted {
		t.Fatalf("the second window should produce its own digest: %v %v", posted, err)
	}

	recs, _ := st.Since("core", 0, 100)
	var digests []store.Record
	for _, r := range recs {
		if r.Kind == core.KindDigest {
			digests = append(digests, r)
		}
	}
	if len(digests) != 2 {
		t.Fatalf("want two digests, got %d", len(digests))
	}
	if strings.Contains(digests[1].Text(), "the first lesson") {
		t.Error("the second digest repeated the first window; a digest is news, not the room")
	}
	if !strings.Contains(digests[1].Text(), "the second lesson") {
		t.Error("the second digest missed its own window")
	}
}

// A digest is addressed, so it renders inline rather than collapsing into the
// carried-forward line with the ambient traffic it is summarizing.
func TestADigestIsAddressedAndRendersInline(t *testing.T) {
	srv, st, sv := newServerFull(t, time.Millisecond)
	seedActor(t, st, "human:bcm")
	bot := digestBot(t, st)

	for i := 0; i < 6; i++ {
		post(t, srv, cmd("til", "lesson "+itoa(int64(i)), "i"+itoa(int64(i))))
	}
	if posted, err := sv.digestStep(bot, time.Now()); err != nil || !posted {
		t.Fatalf("setup: %v %v", posted, err)
	}

	recs, _ := st.Since("core", 0, 100)
	var d store.Record
	for _, r := range recs {
		if r.Kind == core.KindDigest {
			d = r
		}
	}
	if d.Lane != core.Addressed {
		t.Error("a digest must be addressed; an ambient one is a second copy of the room")
	}
	if string(d.Recipient) != "human:bcm" {
		t.Errorf("the digest must name who it is for, got %q", d.Recipient)
	}

	// The ambient run collapses; the digest must not be inside it, or the
	// summary is hidden behind the very traffic it summarizes.
	page := getPage(t, srv.URL+"/?room=core")
	at := strings.Index(page, "entries.")
	if at == -1 {
		t.Fatal("the digest is not on the page at all")
	}
	// Walk back to the row that contains it and check the class the renderer
	// puts on addressed rows.
	rowStart := strings.LastIndex(page[:at], `<div class="row`)
	if rowStart == -1 {
		t.Fatal("the digest is not in a row")
	}
	if !strings.Contains(page[rowStart:at], "addressed") {
		t.Error("the digest rendered as an ambient row; it must be inline and addressed")
	}
	// And every collapsed body ends before it.
	for _, open := range allIndexes(page, `class="carried-body"`) {
		closeAt := strings.Index(page[open:], "</div>")
		if closeAt != -1 && open < at && at < open+closeAt {
			t.Error("the digest is inside a collapsed run")
		}
	}
}

// The bot travels the ordinary command path, so the capability check applies to
// it exactly as it would to an agent that tried this.
func TestTheBotIsRefusedWithoutTheCapability(t *testing.T) {
	srv, st, sv := newServerFull(t, time.Millisecond)
	_ = srv
	seedActor(t, st, "human:bcm")
	post(t, srv, cmd("til", "something worth summarizing", "n1"))

	// A bot with a key and no grant.
	if err := st.RegisterKey("agent:ungranted", pubFor(t), time.Now()); err != nil {
		t.Fatal(err)
	}
	bot := DigestBot{Actor: "agent:ungranted", Room: "core", To: "human:bcm"}

	posted, err := sv.digestStep(bot, time.Now())
	if posted {
		t.Fatal("a bot without the capability posted a digest")
	}
	if err == nil || !strings.Contains(err.Error(), "digest.not_authorized") {
		t.Errorf("want digest.not_authorized, got %v", err)
	}
}

// A redacted body is not re-stated in the summary. Otherwise the suppression is
// undone by the surface that exists to help people catch up.
func TestADigestDoesNotRepeatARedactedBody(t *testing.T) {
	srv, st, sv := newServerFull(t, time.Millisecond)
	seedActor(t, st, "human:bcm")
	bot := digestBot(t, st)

	_, out := post(t, srv, `{"room":"core","author":"human:bcm","kind":"til",`+
		`"body":{"text":"the token is PLACEHOLDER-NOT-REAL"},"idem":"r1"}`)
	target := itoa(int64(out["seq"].(float64)))
	post(t, srv, `{"room":"core","author":"human:bcm","kind":"redact",`+
		`"body":{"text":"pasted a credential"},"refs":["`+target+`"],"idem":"r2"}`)
	post(t, srv, `{"room":"core","author":"agent:c1","kind":"til",`+
		`"body":{"text":"an ordinary lesson"},"idem":"r3"}`)

	if posted, err := sv.digestStep(bot, time.Now()); err != nil || !posted {
		t.Fatalf("setup: %v %v", posted, err)
	}
	recs, _ := st.Since("core", 0, 100)
	for _, r := range recs {
		if r.Kind == core.KindDigest && strings.Contains(r.Text(), "PLACEHOLDER-NOT-REAL") {
			t.Error("the digest repeated a redacted body; the summary undid the suppression")
		}
	}
}

// Stuck is not a kind. It is the progress projection noticing somebody stopped,
// which is the one thing in a digest nobody would otherwise see — an agent that
// goes quiet posts nothing to notice.
func TestADigestNoticesWhoWentQuiet(t *testing.T) {
	srv, st, sv := newServerFull(t, time.Millisecond)
	seedActor(t, st, "human:bcm")
	bot := digestBot(t, st)

	post(t, srv, `{"room":"core","author":"agent:c9","kind":"status",`+
		`"body":{"text":"migrating projections","step":3,"of":7},"idem":"s1"}`)
	post(t, srv, cmd("til", "something to make the window non-empty", "s2"))

	// An hour later, that agent has said nothing since.
	later := time.Date(2026, 8, 6, 15, 30, 0, 0, time.UTC)
	if posted, err := sv.digestStep(bot, later); err != nil || !posted {
		t.Fatalf("setup: %v %v", posted, err)
	}

	recs, _ := st.Since("core", 0, 100)
	var text string
	for _, r := range recs {
		if r.Kind == core.KindDigest {
			text = r.Text()
		}
	}
	if !strings.Contains(text, "Quiet:") || !strings.Contains(text, "c9") {
		t.Errorf("the digest must name who went quiet, got: %s", text)
	}
	if !strings.Contains(text, "3/7") {
		t.Errorf("it must say where they stopped, got: %s", text)
	}
}

// pubFor is a throwaway public key. The bot needs a registered key because it
// is an ordinary actor; nothing in these tests verifies a signature.
func pubFor(t *testing.T) ed25519.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

func allIndexes(s, sub string) []int {
	var out []int
	for i := 0; ; {
		n := strings.Index(s[i:], sub)
		if n == -1 {
			return out
		}
		out = append(out, i+n)
		i += n + len(sub)
	}
}
