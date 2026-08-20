package store

import (
	"crypto/ed25519"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/escherize/comms/core"
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
		if _, err := st.MintInvite(bad, ScopeAll, time.Now()); err == nil {
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
		Lane: core.Ambient}, "p1", time.Now()); err != nil {
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

// An unspent invite from a three-month-old scrollback must not be able to undo
// an offboarding. Rotation after revocation is an operator act.
func TestAnInviteCannotUndoARevocation(t *testing.T) {
	s, _, pub := keyStore(t)

	tok, err := s.MintInvite("agent:leaver", ScopeAll, kt0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RedeemInvite(tok, "agent:leaver", pub, kt0); err != nil {
		t.Fatal(err)
	}

	// The operator mints a replacement, then offboards the seat before it is
	// spent — the scrollback case.
	stale, err := s.MintInvite("agent:leaver", ScopeAll, kt0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeKey("agent:leaver", kt0.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}

	err = s.RedeemInvite(stale, "agent:leaver", pub, kt0.Add(3*time.Minute))
	if err == nil {
		t.Fatal("a token minted before a revocation must not re-enrol the seat")
	}
	if !strings.Contains(err.Error(), "revoked") {
		t.Errorf("the refusal must name the revocation, got %v", err)
	}
	if st, _ := s.KeyStatus("agent:leaver", kt0.Add(3*time.Minute)); st != "revoked" {
		t.Errorf("the seat must still be revoked, got %q", st)
	}
}

// A key marked compromised is a claim about what it already did, and a new key
// does not make that untrue.
func TestRotationDoesNotClearCompromise(t *testing.T) {
	s, _, pub := keyStore(t)
	if err := s.RegisterKey("agent:leaky", pub, kt0); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkCompromised("agent:leaky", kt0); err != nil {
		t.Fatal(err)
	}

	if err := s.RegisterKey("agent:leaky", pub, kt0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.KeyStatus("agent:leaky", kt0.Add(time.Hour)); st != "compromised" {
		t.Errorf("rotation cleared a compromise flag; got %q", st)
	}

	tok, _ := s.MintInvite("agent:leaky", ScopeAll, kt0.Add(time.Hour))
	if err := s.RedeemInvite(tok, "agent:leaky", pub, kt0.Add(2*time.Hour)); err == nil {
		t.Error("an invite must not clear a compromise either")
	}
}

// An invite with no expiry is a permanent credential sitting in whatever
// channel it was sent through.
func TestInvitesExpireAndTheRefusalSaysHowLongAgo(t *testing.T) {
	s, _, pub := keyStore(t)
	tok, err := s.MintInvite("agent:slow", ScopeAll, kt0)
	if err != nil {
		t.Fatal(err)
	}

	err = s.RedeemInvite(tok, "agent:slow", pub, kt0.Add(InviteTTL+time.Hour))
	if err == nil {
		t.Fatal("an expired token must be refused")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("the refusal must say it expired, got %v", err)
	}
	if !strings.Contains(err.Error(), "ago") {
		t.Errorf("the refusal must say how long ago, so the reader knows it is not a typo: %v", err)
	}

	// Just inside the window still works.
	fresh, _ := s.MintInvite("agent:quick", ScopeAll, kt0)
	if err := s.RedeemInvite(fresh, "agent:quick", pub, kt0.Add(InviteTTL-time.Minute)); err != nil {
		t.Errorf("a token inside its window must work: %v", err)
	}
}

// One live invite per actor: a second mint retires the first, so a token in an
// old scrollback stops working the moment a replacement is issued.
func TestMintingAnInviteRetiresTheOutstandingOne(t *testing.T) {
	s, _, pub := keyStore(t)
	first, _ := s.MintInvite("agent:two", ScopeAll, kt0)
	second, _ := s.MintInvite("agent:two", ScopeAll, kt0.Add(time.Minute))

	if err := s.RedeemInvite(first, "agent:two", pub, kt0.Add(2*time.Minute)); err == nil {
		t.Error("the superseded token must not enrol")
	}
	if err := s.RedeemInvite(second, "agent:two", pub, kt0.Add(3*time.Minute)); err != nil {
		t.Errorf("the live token must enrol: %v", err)
	}
}

// The bootstrap token binds to whoever redeems it, because an empty hub has
// nobody to name a token for — and it dies the moment the hub is not empty.
func TestBootstrapInviteEnrolsTheFirstSeatOnly(t *testing.T) {
	s, _, pub := keyStore(t)

	tok, err := s.MintBootstrapInvite(kt0)
	if err != nil {
		t.Fatal(err)
	}
	if s.EnrolledSeats() != 0 {
		t.Fatal("a fresh store must report zero enrolled seats")
	}
	if err := s.RedeemInvite(tok, "human:ada", pub, kt0.Add(time.Minute)); err != nil {
		t.Fatalf("the bootstrap token must enrol a seat it was not minted for: %v", err)
	}
	if s.EnrolledSeats() != 1 {
		t.Errorf("want one enrolled seat, got %d", s.EnrolledSeats())
	}
	// The first seat owns the hub, so it can invite the rest of the team from
	// the browser without an operator command on the box.
	if !s.HasCapability("human:ada", "invite") {
		t.Error("the first seat must be granted the invite capability on enrolment")
	}

	// A second bootstrap token is refused once anyone holds a key, even
	// though the token itself is unspent.
	tok2, err := s.MintBootstrapInvite(kt0.Add(2 * time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	pub2, _, _ := ed25519.GenerateKey(nil)
	err = s.RedeemInvite(tok2, "human:eve", pub2, kt0.Add(3*time.Minute))
	if err == nil {
		t.Fatal("a bootstrap token must not enrol onto a hub that has seats")
	}
	if !strings.Contains(err.Error(), "already enrolled") {
		t.Errorf("the refusal must say someone is already enrolled, got %v", err)
	}
}

// The auto-grant is the first seat's alone. A seat enrolled through an ordinary
// (non-bootstrap) invite gets no capability — the owner grants it deliberately,
// or it stays a plain member.
func TestOrdinaryEnrolmentGrantsNoCapability(t *testing.T) {
	s, _, pub := keyStore(t)

	boot, _ := s.MintBootstrapInvite(kt0)
	if err := s.RedeemInvite(boot, "human:owner", pub, kt0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// A normal invite, minted for a named seat, then redeemed.
	tok, err := s.MintInvite("agent:worker", ScopeAll, kt0.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	pub2, _, _ := ed25519.GenerateKey(nil)
	if err := s.RedeemInvite(tok, "agent:worker", pub2, kt0.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if s.HasCapability("agent:worker", "invite") {
		t.Error("a seat enrolled through an ordinary invite must not get the invite capability")
	}
}

// A restart mints a fresh bootstrap token; the one in old scrollback dies,
// same rule as one live invite per actor.
func TestMintingABootstrapInviteRetiresTheOutstandingOne(t *testing.T) {
	s, _, pub := keyStore(t)
	first, _ := s.MintBootstrapInvite(kt0)
	second, _ := s.MintBootstrapInvite(kt0.Add(time.Minute))

	if err := s.RedeemInvite(first, "human:ada", pub, kt0.Add(2*time.Minute)); err == nil {
		t.Error("the superseded bootstrap token must not enrol")
	}
	if err := s.RedeemInvite(second, "human:ada", pub, kt0.Add(3*time.Minute)); err != nil {
		t.Errorf("the live bootstrap token must enrol: %v", err)
	}
}

// The mismatch refusal names both actors: the guess it kills is "which seat
// was this token for", asked in a browser far from the operator's scrollback.
func TestActorMismatchRefusalNamesBothActors(t *testing.T) {
	s, _, pub := keyStore(t)
	tok, _ := s.MintInvite("human:ada", ScopeAll, kt0)
	err := s.RedeemInvite(tok, "human:eve", pub, kt0.Add(time.Minute))
	if err == nil {
		t.Fatal("a token must not enrol a different actor")
	}
	for _, want := range []string{"human:ada", "human:eve"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name %s, got %v", want, err)
		}
	}
}

// A capability is an operator grant. No verb reaches it.
func TestCapabilitiesAreGrantedNotClaimed(t *testing.T) {
	s, _, _ := keyStore(t)
	if s.HasCapability("agent:c1", "invite") {
		t.Error("no seat holds a capability by default")
	}
	if err := s.Grant("agent:admin-bot", "invite", "human:bcm", kt0); err != nil {
		t.Fatal(err)
	}
	if !s.HasCapability("agent:admin-bot", "invite") {
		t.Error("a granted capability must be visible to the decider")
	}
	if s.HasCapability("agent:admin-bot", "purge") {
		t.Error("a grant is per capability, not a blanket")
	}
	if err := s.Grant("admin-bot", "invite", "human:bcm", kt0); err == nil {
		t.Error("a grant to an un-namespaced seat must be refused")
	}
}

// Progress does not run backwards within one piece of work.
//
// The guard cannot be a timestamp or a seq, which is worth stating because the
// ticket proposed one: a delayed status arrives with a later server_ts and a
// higher seq, since the server stamps and numbers at arrival, and the only
// earlier time available would be a client-declared one — the adversarial
// created_at this design refuses. So the guard is on the meaning of the field.
func TestAnOutOfOrderStatusCannotRewindProgress(t *testing.T) {
	s, _, _ := keyStore(t)
	if err := s.EnsureRoom("core"); err != nil {
		t.Fatal(err)
	}
	mk := func(step, of int, idem string, at time.Time) {
		t.Helper()
		if _, err := s.Append(core.Event{Room: "core", Author: "agent:w",
			Kind: core.Kind("status"),
			Body: map[string]any{"text": "working", "step": float64(step), "of": float64(of)},
			Lane: core.Ambient}, idem, at); err != nil {
			t.Fatal(err)
		}
	}
	mk(5, 7, "s5", kt0)
	mk(2, 7, "s2", kt0.Add(time.Minute)) // delayed, lands after the one it precedes

	rows, err := s.ProgressFor("core")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("want one row, got %d", len(rows))
	}
	if rows[0].Step != 5 {
		t.Errorf("progress rewound to step %d; a late status must not move the bar back", rows[0].Step)
	}
	// The actor did post, so it is not stalled. Liveness is a separate question
	// from how far along the work is.
	if !rows[0].Updated.Equal(kt0.Add(time.Minute).UTC()) {
		t.Errorf("a post is evidence of life; the clock should have moved, got %v", rows[0].Updated)
	}

	// A new piece of work is not a rewind: step 1 of 4 after step 5 of 7 is
	// progress.
	mk(1, 4, "s-new", kt0.Add(2*time.Minute))
	rows, _ = s.ProgressFor("core")
	if rows[0].Step != 1 || rows[0].Of != 4 {
		t.Errorf("a new total must reset the bar, got %d/%d", rows[0].Step, rows[0].Of)
	}
}

// "still working on the migration" is not a claim that the work went back to
// step 0 of 0.
func TestAStepLessStatusCarriesTheCounterForward(t *testing.T) {
	s, _, _ := keyStore(t)
	if err := s.EnsureRoom("core"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(core.Event{Room: "core", Author: "agent:w", Kind: core.Kind("status"),
		Body: map[string]any{"text": "migrating", "step": float64(3), "of": float64(7)},
		Lane: core.Ambient}, "p1", kt0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(core.Event{Room: "core", Author: "agent:w", Kind: core.Kind("status"),
		Body: map[string]any{"text": "still migrating"},
		Lane: core.Ambient}, "p2", kt0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	rows, _ := s.ProgressFor("core")
	if len(rows) != 1 {
		t.Fatalf("want one row, got %d", len(rows))
	}
	if rows[0].Step != 3 || rows[0].Of != 7 {
		t.Errorf("a step-less status zeroed the counter: %d/%d", rows[0].Step, rows[0].Of)
	}
	if rows[0].Note != "still migrating" {
		t.Errorf("the note must still update, got %q", rows[0].Note)
	}
}

// Membership CRUD, the wildcard, and the read-gate signal. Room scoping hangs
// on these, so the semantics are pinned here before anything reads them.
func TestMembershipSemantics(t *testing.T) {
	s, _, pub := keyStore(t)

	// A scoped seat is a member of exactly the rooms it holds, nothing else.
	if err := s.RegisterKey("human:sarah", pub, kt0); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMembership("human:sarah", "comms", "human:owner", kt0); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMembership("human:sarah", "ops", "human:owner", kt0); err != nil {
		t.Fatal(err)
	}
	if !s.IsMember("human:sarah", "comms") || !s.IsMember("human:sarah", "ops") {
		t.Error("a scoped seat must be a member of its granted rooms")
	}
	if s.IsMember("human:sarah", "secret") {
		t.Error("a scoped seat must not be a member of a room it was not granted")
	}
	if got := s.Memberships("human:sarah"); len(got) != 2 {
		t.Errorf("want two rooms, got %v", got)
	}

	// AddMembership is idempotent, like Grant: re-adding is a no-op.
	if err := s.AddMembership("human:sarah", "comms", "human:owner", kt0); err != nil {
		t.Fatal(err)
	}
	if got := s.Memberships("human:sarah"); len(got) != 2 {
		t.Errorf("re-adding a held room must not duplicate it, got %v", got)
	}

	// The wildcard row is a member of every room, including ones not named.
	pub2, _, _ := ed25519.GenerateKey(nil)
	if err := s.RegisterKey("human:owner", pub2, kt0); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMembership("human:owner", membershipRoomAll, "grandfather", kt0); err != nil {
		t.Fatal(err)
	}
	for _, room := range []string{"comms", "ops", "secret", "anything"} {
		if !s.IsMember("human:owner", room) {
			t.Errorf("a '*' member must be a member of %q", room)
		}
	}

	// A seat with no rows at all is a member of nothing.
	if s.IsMember("human:nobody", "comms") {
		t.Error("a seat with no membership must be a member of nothing")
	}

	// AnyScopedMember distinguishes a hub with only all-rooms seats from one
	// where someone is scoped — the signal the read gate turns on.
	if !s.AnyScopedMember() {
		t.Error("sarah is scoped, so AnyScopedMember must be true")
	}
}

// An all-rooms seat only, no scoped seats: the read gate stays optional.
func TestAnyScopedMemberFalseWhenOnlyWildcard(t *testing.T) {
	s, _, pub := keyStore(t)
	if err := s.RegisterKey("human:owner", pub, kt0); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMembership("human:owner", membershipRoomAll, "grandfather", kt0); err != nil {
		t.Fatal(err)
	}
	if s.AnyScopedMember() {
		t.Error("only a '*' member exists, so AnyScopedMember must be false")
	}
}

// Existing seats are grandfathered to all rooms on open, so an upgrade never
// silently strips access that worked before scoping existed.
func TestOpenGrandfathersExistingSeatsToAllRooms(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "grand.db")

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	pub, _, _ := ed25519.GenerateKey(nil)
	// A seat enrolled the old way, with no membership written for it.
	if err := s.RegisterKey("agent:legacy", pub, kt0); err != nil {
		t.Fatal(err)
	}
	if s.IsMember("agent:legacy", "core") {
		t.Fatal("precondition: a freshly registered key holds no membership yet")
	}
	s.Close()

	// Reopening runs the one-time backfill: the legacy seat becomes an all-rooms
	// member, and does not get a second row on a further reopen.
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if !s2.IsMember("agent:legacy", "core") || !s2.IsMember("agent:legacy", "whatever") {
		t.Error("a grandfathered seat must be a member of every room")
	}
	if got := s2.Memberships("agent:legacy"); len(got) != 1 || got[0] != membershipRoomAll {
		t.Errorf("grandfather must write exactly one '*' row, got %v", got)
	}
}

// An invite's scope round-trips: what you mint is what InviteActor reports, so
// the composer can show a token's blast radius before anyone pastes it.
func TestInviteScopeRoundTrips(t *testing.T) {
	s, _, _ := keyStore(t)

	scoped, err := s.MintInvite("human:sarah", "comms,ops", kt0)
	if err != nil {
		t.Fatal(err)
	}
	actor, scope, err := s.InviteActor(scoped, kt0)
	if err != nil {
		t.Fatal(err)
	}
	if actor != "human:sarah" || scope != "comms,ops" {
		t.Errorf("want human:sarah / comms,ops, got %s / %s", actor, scope)
	}

	// An empty scope defaults to all — an unscoped invite is a superuser invite.
	unscoped, err := s.MintInvite("human:owner", "", kt0)
	if err != nil {
		t.Fatal(err)
	}
	if _, scope, _ := s.InviteActor(unscoped, kt0); scope != ScopeAll {
		t.Errorf("an empty scope must default to all, got %q", scope)
	}

	// The bootstrap token is always all-rooms.
	boot, err := s.MintBootstrapInvite(kt0)
	if err != nil {
		t.Fatal(err)
	}
	if actor, scope, _ := s.InviteActor(boot, kt0); actor != "*" || scope != ScopeAll {
		t.Errorf("bootstrap must be * / all, got %s / %s", actor, scope)
	}
}

// Redeeming an invite binds the seat's rooms from the invite's scope, in the
// same transaction that registers the key — so a seat is never enrolled into
// no room, and its rooms match what the token promised.
func TestRedeemBindsMembershipFromScope(t *testing.T) {
	s, _, pub := keyStore(t)

	// A scoped invite → membership in exactly those rooms, nothing else.
	scoped, _ := s.MintInvite("human:sarah", "comms, ops", kt0)
	if err := s.RedeemInvite(scoped, "human:sarah", pub, kt0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if !s.IsMember("human:sarah", "comms") || !s.IsMember("human:sarah", "ops") {
		t.Error("a scoped redeem must bind its rooms")
	}
	if s.IsMember("human:sarah", "secret") {
		t.Error("a scoped redeem must not bind rooms outside its scope")
	}
	if got := s.Memberships("human:sarah"); len(got) != 2 {
		t.Errorf("want two rooms, got %v", got)
	}

	// An all-rooms invite → the '*' wildcard.
	pub2, _, _ := ed25519.GenerateKey(nil)
	allTok, _ := s.MintInvite("human:owner", "all", kt0)
	if err := s.RedeemInvite(allTok, "human:owner", pub2, kt0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := s.Memberships("human:owner"); len(got) != 1 || got[0] != membershipRoomAll {
		t.Errorf("an all invite must bind exactly '*', got %v", got)
	}
	if !s.IsMember("human:owner", "anything") {
		t.Error("an all-rooms seat must be a member of every room")
	}

	// The bootstrap token binds all rooms.
	s2, _, pub3 := keyStore(t)
	boot, _ := s2.MintBootstrapInvite(kt0)
	if err := s2.RedeemInvite(boot, "human:first", pub3, kt0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := s2.Memberships("human:first"); len(got) != 1 || got[0] != membershipRoomAll {
		t.Errorf("bootstrap redeem must bind '*', got %v", got)
	}
}

// membershipRooms is the scope→rooms parser; its edge cases decide what a seat
// can touch, so they are pinned.
func TestMembershipRoomsParsing(t *testing.T) {
	cases := map[string][]string{
		"all":           {"*"},
		"":              {"*"},
		"*":             {"*"},
		"comms":         {"comms"},
		"comms,ops":     {"comms", "ops"},
		" comms , ops ": {"comms", "ops"},
		"comms,comms":   {"comms"}, // dedupe
		",,comms,,":     {"comms"}, // blanks dropped
		"  ,  ":         {"*"},     // all-blank falls back to '*', never nothing
	}
	for scope, want := range cases {
		got := membershipRooms(scope)
		if len(got) != len(want) {
			t.Errorf("membershipRooms(%q) = %v, want %v", scope, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("membershipRooms(%q) = %v, want %v", scope, got, want)
				break
			}
		}
	}
}

// A superuser invite grants all rooms AND the invite capability in one redeem —
// the "this seat runs the hub" grant, distinct from a plain all-rooms invite
// which sees everything but cannot mint.
func TestSuperuserInviteGrantsRoomsAndCapability(t *testing.T) {
	s, _, pub := keyStore(t)

	tok, err := s.MintInvite("human:admin", ScopeSuperuser, kt0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RedeemInvite(tok, "human:admin", pub, kt0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if !s.IsMember("human:admin", "anything") {
		t.Error("a superuser must be an all-rooms member")
	}
	if got := s.Memberships("human:admin"); len(got) != 1 || got[0] != membershipRoomAll {
		t.Errorf("a superuser must hold exactly the '*' membership, got %v", got)
	}
	if !s.HasCapability("human:admin", "invite") {
		t.Error("a superuser must hold the invite capability")
	}

	// A plain all-rooms invite is NOT a superuser: rooms yes, capability no.
	pub2, _, _ := ed25519.GenerateKey(nil)
	allTok, _ := s.MintInvite("human:reader", "all", kt0)
	if err := s.RedeemInvite(allTok, "human:reader", pub2, kt0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if !s.IsMember("human:reader", "anything") {
		t.Error("an all-rooms seat must be a member of every room")
	}
	if s.HasCapability("human:reader", "invite") {
		t.Error("a plain all-rooms invite must NOT grant the invite capability — membership and capability are orthogonal")
	}
}
