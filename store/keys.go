package store

import (
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/bcm/agent_comms/core"
)

const keySchema = `
-- Key validity is a decision projection: the shell reads it to authenticate,
-- so it must be current, never lagging.
CREATE TABLE IF NOT EXISTS actor_key (
  actor       TEXT PRIMARY KEY,
  public_key  TEXT NOT NULL,
  active_from TEXT NOT NULL,
  revoked_at  TEXT NOT NULL DEFAULT '',
  compromised TEXT NOT NULL DEFAULT ''  -- suspected_since; '' means not compromised
);
`

// KeyState is what the shell needs to decide whether to accept a signature.
type KeyState struct {
	Actor      string
	PublicKey  ed25519.PublicKey
	ActiveFrom time.Time
	RevokedAt  time.Time // zero when active
	Suspected  time.Time // zero when not compromised
}

// Revoked reports whether the key was revoked as of now.
func (k KeyState) Revoked(now time.Time) bool {
	return !k.RevokedAt.IsZero() && !now.Before(k.RevokedAt)
}

// Compromised reports whether an event authored at ts falls inside the
// suspected-compromise window. Routine revocation leaves history valid;
// compromise does not, because the question changes from "what happens next"
// to "what did it already do".
func (k KeyState) Compromised(ts time.Time) bool {
	return !k.Suspected.IsZero() && !ts.Before(k.Suspected)
}

// RegisterKey records an actor's public key. Rotation is register-then-revoke,
// so the same actor re-registering replaces the key and clears revocation.
func (s *Store) RegisterKey(actor string, pub ed25519.PublicKey, now time.Time) error {
	_, err := s.db.Exec(
		`INSERT INTO actor_key(actor, public_key, active_from, revoked_at, compromised)
		 VALUES(?,?,?,'','')
		 ON CONFLICT(actor) DO UPDATE SET
		   public_key = excluded.public_key,
		   active_from = excluded.active_from,
		   revoked_at = '', compromised = ''`,
		actor, hex.EncodeToString(pub), now.UTC().Format(time.RFC3339Nano))
	return err
}

// RevokeKey rejects the actor's future commands. History stays valid as of its
// server_ts, so offboarding does not erase the record.
func (s *Store) RevokeKey(actor string, now time.Time) error {
	res, err := s.db.Exec(`UPDATE actor_key SET revoked_at = ? WHERE actor = ?`,
		now.UTC().Format(time.RFC3339Nano), actor)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("no key registered for " + actor)
	}
	return nil
}

// MarkCompromised flags everything the key authored at or after suspectedSince.
// When the compromise time is unknown, callers pass the key's active_from:
// over-flagging is recoverable, under-flagging is not.
func (s *Store) MarkCompromised(actor string, suspectedSince time.Time) error {
	res, err := s.db.Exec(`UPDATE actor_key SET compromised = ? WHERE actor = ?`,
		suspectedSince.UTC().Format(time.RFC3339Nano), actor)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("no key registered for " + actor)
	}
	return nil
}

// KeyFor returns an actor's key state.
func (s *Store) KeyFor(actor string) (KeyState, bool) {
	var pub, from, revoked, comp string
	err := s.db.QueryRow(
		`SELECT public_key, active_from, revoked_at, compromised FROM actor_key WHERE actor = ?`,
		actor).Scan(&pub, &from, &revoked, &comp)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return KeyState{}, false
	}
	raw, err := hex.DecodeString(pub)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return KeyState{}, false
	}
	k := KeyState{Actor: actor, PublicKey: ed25519.PublicKey(raw)}
	k.ActiveFrom, _ = time.Parse(time.RFC3339Nano, from)
	if revoked != "" {
		k.RevokedAt, _ = time.Parse(time.RFC3339Nano, revoked)
	}
	if comp != "" {
		k.Suspected, _ = time.Parse(time.RFC3339Nano, comp)
	}
	return k, true
}

// FlaggedEvents lists a compromised key's events for review. This is the
// question a leak actually raises: not what happens next, but what already did.
func (s *Store) FlaggedEvents(actor string) ([]Record, error) {
	k, ok := s.KeyFor(actor)
	if !ok || k.Suspected.IsZero() {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT e.seq, e.server_ts, e.room, e.author, e.kind, e.recipient, e.lane,
		       e.refs, e.body_hash, e.prev_hash, b.json, e.attach
		FROM envelope e LEFT JOIN body b ON b.seq = e.seq
		WHERE e.author = ? AND e.server_ts >= ?
		ORDER BY e.seq`,
		actor, k.Suspected.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRecords(rows)
}

// SignedBytes is the exact byte sequence a signature covers. It binds author,
// room, kind, refs, idem, and body together, so a swapped room or kind fails
// verification. Deliberately not a re-serialized object — JSON canonicalization
// (key order, unicode, float representation) is a footgun, so callers sign what
// they post and the server verifies what it received.
func SignedBytes(raw []byte) []byte { return raw }

// AuthFailure names which of the four conditions refused a command. They were
// one string, which made "stop forever, this seat is revoked" and "the client
// has a bug" indistinguishable to the agent that had to decide what to do next.
type AuthFailure struct {
	Invariant string
	Detail    string
}

func (a AuthFailure) Error() string { return a.Invariant + ": " + a.Detail }

// VerifySignature checks a detached signature over the posted bytes for an actor whose
// key is registered, unrevoked, and uncompromised as of now.
func (s *Store) VerifySignature(actor core.Actor, raw, sig []byte, now time.Time) error {
	k, ok := s.KeyFor(string(actor))
	if !ok {
		return AuthFailure{"key.unknown",
			"no key registered for " + string(actor) + "; a human must enrol this seat"}
	}
	// Compromise is checked before revocation. A key can be both, and the two
	// verdicts differ: revoked means "a human re-enrols this seat", compromised
	// means "stop and tell a human now". Reporting the milder one would send an
	// agent to re-enrol a key that is known to be in someone else's hands.
	if k.Compromised(now) {
		return AuthFailure{"key.compromised",
			"the key for " + string(actor) + " is marked compromised; stop and tell a human"}
	}
	if k.Revoked(now) {
		return AuthFailure{"key.revoked",
			"the key for " + string(actor) + " was revoked; a human must re-enrol this seat"}
	}
	if !ed25519.Verify(k.PublicKey, SignedBytes(raw), sig) {
		return AuthFailure{"signature.invalid",
			"the signature does not verify against the bytes received; this is a client bug, not a key problem"}
	}
	return nil
}

// KeyStatus reports a key's state for the read lane, so an event written by a
// since-compromised key does not read exactly like any other.
func (s *Store) KeyStatus(actor string, at time.Time) (status string, flagged bool) {
	k, ok := s.KeyFor(actor)
	if !ok {
		return "unknown", false
	}
	// Same precedence as VerifySignature: compromised outranks revoked.
	switch {
	case k.Compromised(at):
		return "compromised", true
	case !k.RevokedAt.IsZero():
		return "revoked", false
	}
	return "active", false
}

const inviteSchema = `
-- Enrolment invites. Without these, /keys would be trust-on-first-use and
-- whoever claimed an actor name first would own it — including yours.
CREATE TABLE IF NOT EXISTS invite (
  token   TEXT PRIMARY KEY,
  actor   TEXT NOT NULL,
  created TEXT NOT NULL,
  used_at TEXT NOT NULL DEFAULT ''
);
`

// MintInvite creates a one-time enrolment token for an actor. The operator
// hands it over out of band; it is the only way to register a key over HTTP.
func (s *Store) MintInvite(actor string, now time.Time) (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)
	_, err := s.db.Exec(
		`INSERT INTO invite(token, actor, created) VALUES(?,?,?)`,
		token, actor, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return "", err
	}
	return token, nil
}

// RedeemInvite consumes a token for an actor, returning an error if the token
// is unknown, already used, or issued for a different actor. Redemption and
// key registration happen in one transaction, so a crash cannot burn a token
// without registering the key it was for.
func (s *Store) RedeemInvite(token, actor string, pub ed25519.PublicKey, now time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var forActor, usedAt string
	err = tx.QueryRow(`SELECT actor, used_at FROM invite WHERE token = ?`, token).
		Scan(&forActor, &usedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("unknown enrolment token")
	}
	if err != nil {
		return err
	}
	if usedAt != "" {
		return errors.New("enrolment token already used")
	}
	if forActor != actor {
		return errors.New("enrolment token was issued for a different actor")
	}

	ts := now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`UPDATE invite SET used_at = ? WHERE token = ?`, ts, token); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`INSERT INTO actor_key(actor, public_key, active_from, revoked_at, compromised)
		 VALUES(?,?,?,'','')
		 ON CONFLICT(actor) DO UPDATE SET
		   public_key = excluded.public_key, active_from = excluded.active_from,
		   revoked_at = '', compromised = ''`,
		actor, hex.EncodeToString(pub), ts); err != nil {
		return err
	}
	return tx.Commit()
}

const redactSchema = `
-- Redaction is a decision projection folded from redact events. The envelope is
-- append-only, so suppression cannot be a column update on the target; it is
-- state derived from a later event, which is what "corrections are new entries"
-- means in practice.
CREATE TABLE IF NOT EXISTS redacted (
  seq        INTEGER PRIMARY KEY,
  by_actor   TEXT NOT NULL,
  by_seq     INTEGER NOT NULL,
  server_ts  TEXT NOT NULL
);
`
