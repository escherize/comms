package store

import (
	"crypto/ed25519"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/bcm/agent_comms/core"
)

func keyStore(t *testing.T) (*Store, ed25519.PrivateKey, ed25519.PublicKey) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "keys.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return s, priv, pub
}

var kt0 = time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)

func TestVerifyAcceptsAGoodSignature(t *testing.T) {
	s, priv, pub := keyStore(t)
	if err := s.RegisterKey("human:bcm", pub, kt0); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"room":"core","author":"human:bcm","kind":"chat"}`)
	if err := s.VerifySignature("human:bcm", raw, ed25519.Sign(priv, raw), kt0); err != nil {
		t.Errorf("a valid signature must verify: %v", err)
	}
}

// The signature covers the whole posted payload, so tampering with any field —
// swapping the room, the kind, the author — must fail. This is why the shell
// verifies the bytes it received rather than a re-serialized object.
func TestVerifyRejectsTamperedPayload(t *testing.T) {
	s, priv, pub := keyStore(t)
	s.RegisterKey("human:bcm", pub, kt0)

	raw := []byte(`{"room":"core","author":"human:bcm","kind":"chat","body":{"text":"hi"}}`)
	sig := ed25519.Sign(priv, raw)

	tampered := [][]byte{
		[]byte(`{"room":"secret","author":"human:bcm","kind":"chat","body":{"text":"hi"}}`),
		[]byte(`{"room":"core","author":"mallory","kind":"chat","body":{"text":"hi"}}`),
		[]byte(`{"room":"core","author":"human:bcm","kind":"redact","body":{"text":"hi"}}`),
		[]byte(`{"room":"core","author":"human:bcm","kind":"chat","body":{"text":"bye"}}`),
	}
	for _, bad := range tampered {
		if err := s.VerifySignature("human:bcm", bad, sig, kt0); err == nil {
			t.Errorf("tampered payload must not verify: %s", bad)
		}
	}
}

func TestVerifyRejectsWrongKeyAndUnknownActor(t *testing.T) {
	s, _, pub := keyStore(t)
	s.RegisterKey("human:bcm", pub, kt0)

	_, otherPriv, _ := ed25519.GenerateKey(nil)
	raw := []byte(`{"x":1}`)
	if err := s.VerifySignature("human:bcm", raw, ed25519.Sign(otherPriv, raw), kt0); err == nil {
		t.Error("a signature from another key must not verify")
	}
	if err := s.VerifySignature("nobody", raw, ed25519.Sign(otherPriv, raw), kt0); err == nil {
		t.Error("an unregistered actor must not verify")
	}
}

// Revocation rejects new commands; history stays valid as of its server_ts, so
// offboarding does not erase the record.
func TestRevocationRejectsNewButKeepsHistory(t *testing.T) {
	s, priv, pub := keyStore(t)
	s.RegisterKey("human:sarah", pub, kt0)
	raw := []byte(`{"a":1}`)
	sig := ed25519.Sign(priv, raw)

	revokeAt := kt0.Add(time.Hour)
	if err := s.RevokeKey("human:sarah", revokeAt); err != nil {
		t.Fatal(err)
	}

	if err := s.VerifySignature("human:sarah", raw, sig, revokeAt.Add(time.Minute)); err == nil {
		t.Error("a revoked key must not accept new commands")
	}
	if err := s.VerifySignature("human:sarah", raw, sig, revokeAt.Add(-time.Minute)); err != nil {
		t.Errorf("commands from before revocation stay valid: %v", err)
	}

	k, ok := s.KeyFor("human:sarah")
	if !ok || k.RevokedAt.IsZero() {
		t.Error("revocation must be recorded, not deleted")
	}
}

func TestRotationIsRegisterThenRevoke(t *testing.T) {
	s, oldPriv, oldPub := keyStore(t)
	s.RegisterKey("human:bcm", oldPub, kt0)

	newPub, newPriv, _ := ed25519.GenerateKey(nil)
	if err := s.RegisterKey("human:bcm", newPub, kt0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	raw := []byte(`{"a":1}`)
	if err := s.VerifySignature("human:bcm", raw, ed25519.Sign(newPriv, raw), kt0.Add(2*time.Hour)); err != nil {
		t.Errorf("the new key must verify after rotation: %v", err)
	}
	if err := s.VerifySignature("human:bcm", raw, ed25519.Sign(oldPriv, raw), kt0.Add(2*time.Hour)); err == nil {
		t.Error("the old key must stop verifying after rotation")
	}
}

// A leak asks a different question than an offboarding: not "what happens next"
// but "what did it already do".
func TestCompromiseFlagsPriorEvents(t *testing.T) {
	s, _, pub := keyStore(t)
	s.EnsureRoom("core")
	s.RegisterKey("agent:claude-1", pub, kt0)

	// Appended explicitly before the suspected window — mustAppend uses the
	// package clock, which would land after it.
	if _, err := s.Append(core.Event{Room: "core", Author: "agent:claude-1",
		Kind: core.KindChat, Body: map[string]any{"text": "innocent"}},
		"k1", kt0); err != nil {
		t.Fatal(err)
	}

	// Two later events, after the suspected compromise.
	suspected := kt0.Add(time.Hour)
	for i, txt := range []string{"suspicious", "also suspicious"} {
		ev := core.Event{Room: "core", Author: "agent:claude-1", Kind: core.KindChat,
			Body: map[string]any{"text": txt}}
		if _, err := s.Append(ev, "k-late-"+string(rune('a'+i)), suspected.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.MarkCompromised("agent:claude-1", suspected); err != nil {
		t.Fatal(err)
	}

	flagged, err := s.FlaggedEvents("agent:claude-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(flagged) != 2 {
		t.Errorf("expected the 2 post-compromise events flagged, got %d", len(flagged))
	}
	for _, f := range flagged {
		if f.Text() == "innocent" {
			t.Error("events from before the suspected window must not be flagged")
		}
	}

	// A compromised key stops verifying outright.
	if err := s.VerifySignature("agent:claude-1", []byte(`{}`), make([]byte, 64), suspected.Add(time.Hour)); err == nil {
		t.Error("a compromised key must not verify")
	}
}

// When the compromise time is unknown, the whole history is flagged:
// over-flagging is recoverable, under-flagging is not.
func TestUnknownCompromiseTimeFlagsEverything(t *testing.T) {
	s, _, pub := keyStore(t)
	s.EnsureRoom("core")
	s.RegisterKey("agent:x", pub, kt0)

	for i := range 3 {
		if _, err := s.Append(core.Event{Room: "core", Author: "agent:x",
			Kind: core.KindChat, Body: map[string]any{"text": "e"}},
			"u"+string(rune('a'+i)), kt0.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}

	k, _ := s.KeyFor("agent:x")
	if err := s.MarkCompromised("agent:x", k.ActiveFrom); err != nil {
		t.Fatal(err)
	}
	flagged, _ := s.FlaggedEvents("agent:x")
	if len(flagged) != 3 {
		t.Errorf("unknown compromise time must flag the whole history, got %d of 3", len(flagged))
	}
}

func TestRevokeAndCompromiseNeedARegisteredKey(t *testing.T) {
	s, _, _ := keyStore(t)
	if err := s.RevokeKey("ghost", kt0); err == nil {
		t.Error("revoking an unregistered actor must error")
	}
	if err := s.MarkCompromised("ghost", kt0); err == nil {
		t.Error("flagging an unregistered actor must error")
	}
}

// A key can be both revoked and compromised, and the two verdicts differ:
// revoked sends a human to re-enrol the seat, compromised says stop now.
// Reporting the milder one would send someone to re-enrol a key that is
// known to be in another party's hands.
func TestCompromiseOutranksRevocation(t *testing.T) {
	s, priv, pub := keyStore(t)
	s.RegisterKey("agent:both", pub, kt0)
	s.RevokeKey("agent:both", kt0.Add(time.Hour))
	s.MarkCompromised("agent:both", kt0)

	raw := []byte(`{"a":1}`)
	err := s.VerifySignature("agent:both", raw, ed25519.Sign(priv, raw), kt0.Add(2*time.Hour))
	var af AuthFailure
	if !errors.As(err, &af) {
		t.Fatalf("expected an AuthFailure, got %v", err)
	}
	if af.Invariant != "key.compromised" {
		t.Errorf("compromise must outrank revocation, got %s", af.Invariant)
	}

	if status, flagged := s.KeyStatus("agent:both", kt0.Add(2*time.Hour)); status != "compromised" || !flagged {
		t.Errorf("KeyStatus must agree: got %s flagged=%v", status, flagged)
	}
}

// Actor.IsAgent decides how a post is read and which budgets apply, so a seat
// called "sarah-ops" must not be enrollable: it would be neither agent nor
// human to every rule that checks the prefix.
func TestEnrolmentRequiresANamespace(t *testing.T) {
	st, _, pub := keyStore(t)

	for _, bad := range []string{"sarah-ops", "bcm", "agent:", "human:", ""} {
		if _, err := st.MintInvite(bad, time.Now()); err == nil {
			t.Errorf("minting an invite for %q should be refused at mint time", bad)
		}
		if err := st.RegisterKey(bad, pub, time.Now()); err == nil {
			t.Errorf("registering a key for %q should be refused", bad)
		}
	}
	for _, ok := range []string{"agent:c1", "human:sarah", "agent:bcm/claude-1"} {
		if err := st.RegisterKey(ok, pub, time.Now()); err != nil {
			t.Errorf("%q is a valid seat: %v", ok, err)
		}
	}
}

// The roster is what recipient.unknown is checked against, so both paths onto
// the hub — enrolment and a first post — must land a seat on it.
func TestBothEnrolmentAndPostingPutASeatOnTheRoster(t *testing.T) {
	st, _, pub := keyStore(t)
	if err := st.EnsureRoom("core"); err != nil {
		t.Fatal(err)
	}

	if err := st.RegisterKey("agent:enrolled", pub, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(core.Event{Room: "core", Author: "human:poster",
		Kind: core.KindChat, Body: map[string]any{"text": "hi"},
		Lane: core.LaneOf(core.KindChat)}, "p1", time.Now()); err != nil {
		t.Fatal(err)
	}

	for _, actor := range []string{"agent:enrolled", "human:poster"} {
		if !st.ActorEnrolled(actor) {
			t.Errorf("%s is not addressable, so a correct --to would be refused", actor)
		}
	}
	if st.ActorEnrolled("human:nobody") {
		t.Error("a seat nobody has ever seen must not be addressable")
	}

	rows, err := st.Actors()
	if err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	for _, r := range rows {
		status[r.Actor] = r.KeyStatus
	}
	if status["agent:enrolled"] != "active" {
		t.Errorf("an enrolled seat should read active, got %q", status["agent:enrolled"])
	}
	// Seen posting on an -insecure hub, never enrolled: addressable, but nothing
	// it posted was ever proven to come from it, and the roster has to say so.
	if status["human:poster"] != "unsigned" {
		t.Errorf("a seat that only ever posted unsigned should read unsigned, got %q",
			status["human:poster"])
	}
}
