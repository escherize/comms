package store

import (
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/escherize/comms/core"
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
// ValidActor reports whether a seat carries a namespace. Actor.IsAgent decides
// which provenance banner a reader sees and which lane budgets apply, so a seat
// enrolled as "sarah-ops" would be treated as neither agent nor human by a rule
// that only checks the prefix.
func ValidActor(actor string) error {
	switch {
	case strings.HasPrefix(actor, "agent:") && len(actor) > len("agent:"):
		return nil
	case strings.HasPrefix(actor, "human:") && len(actor) > len("human:"):
		return nil
	}
	return errors.New("seat " + actor + " has no namespace; enrol as agent:<name> or " +
		"human:<name>, because whether a seat is an agent decides how its posts are read")
}

// ActorEnrolled reports whether a seat has ever held a key. Revoked seats stay
// addressable: someone offboarded can still be the recipient of a handoff
// already in flight, and rejecting that would lose the record of who held it.
func (s *Store) ActorEnrolled(actor string) bool {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM actor WHERE actor = ?`, actor).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

// Actors lists every enrolled seat with its key status, newest enrolment first.
func (s *Store) Actors() ([]ActorRow, error) {
	rows, err := s.db.Query(
		`SELECT a.actor, a.first_seen,
		        COALESCE(k.revoked_at, ''), COALESCE(k.compromised, ''),
		        CASE WHEN k.actor IS NULL THEN 0 ELSE 1 END
		   FROM actor a LEFT JOIN actor_key k ON k.actor = a.actor
		  ORDER BY a.first_seen, a.actor`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ActorRow
	for rows.Next() {
		var a ActorRow
		var revoked, compromised string
		var hasKey int
		if err := rows.Scan(&a.Actor, &a.EnrolledAt, &revoked, &compromised, &hasKey); err != nil {
			return nil, err
		}
		switch {
		case compromised != "":
			a.KeyStatus = "compromised"
		case revoked != "":
			a.KeyStatus = "revoked"
		case hasKey == 0:
			// Seen posting on an -insecure hub, never enrolled. Addressable, but
			// nothing it posted was ever proven to come from it.
			a.KeyStatus = "unsigned"
		default:
			a.KeyStatus = "active"
		}
		a.IsAgent = strings.HasPrefix(a.Actor, "agent:")
		out = append(out, a)
	}
	return out, rows.Err()
}

// ActorRow is one seat on the roster.
type ActorRow struct {
	Actor      string `json:"actor"`
	KeyStatus  string `json:"key_status"`
	EnrolledAt string `json:"enrolled_at"`
	IsAgent    bool   `json:"is_agent"`
}

func (s *Store) RegisterKey(actor string, pub ed25519.PublicKey, now time.Time) error {
	if err := ValidActor(actor); err != nil {
		return err
	}
	if _, err := s.db.Exec(
		`INSERT INTO actor(actor, first_seen, source) VALUES(?,?,'enrolment')
		 ON CONFLICT(actor) DO UPDATE SET source = 'enrolment'`,
		actor, now.UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	_, err := s.db.Exec(
		`INSERT INTO actor_key(actor, public_key, active_from, revoked_at, compromised)
		 VALUES(?,?,?,'','')
		 ON CONFLICT(actor) DO UPDATE SET
		   public_key = excluded.public_key,
		   active_from = excluded.active_from,
		   revoked_at = '',
		   -- Revocation is routine and rotation clears it. Compromise is not:
		   -- it is a claim about what a key already did, and a new key does not
		   -- make that untrue. MarkCompromised is cleared by nothing.
		   compromised = actor_key.compromised`,
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
  used_at TEXT NOT NULL DEFAULT '',
  expires TEXT NOT NULL DEFAULT '',
  -- The rooms redeeming this token grants. 'all' is the wildcard (a '*'
  -- membership row on redemption); a comma-separated list scopes the seat.
  -- Defaults to 'all' so an unscoped invite stays a superuser invite,
  -- backward compatible with every token minted before scoping existed.
  scope   TEXT NOT NULL DEFAULT 'all'
);
`

// ScopeAll is the invite scope that grants every room — the '*' membership
// wildcard, and the default for an unscoped invite.
const ScopeAll = "all"

// InviteTTL bounds how long an enrolment token is worth pasting. An invite
// with no expiry is a permanent credential sitting in whatever channel it was
// sent through.
const InviteTTL = 24 * time.Hour

// MintInvite creates a one-time enrolment token for an actor, scoped to a set
// of rooms ("all" for every room). The operator hands it over out of band; it
// is the only way to register a key over HTTP. Re-minting for the same actor
// supersedes the old token, so changing an unredeemed invite's scope is just
// minting again — there is no separate edit.
func (s *Store) MintInvite(actor, scope string, now time.Time) (string, error) {
	// Refuse at mint time, not at redeem: the operator is here now, and the
	// agent that would hit it is not.
	if err := ValidActor(actor); err != nil {
		return "", err
	}
	if scope == "" {
		scope = ScopeAll
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)

	// One live invite per actor. Two outstanding tokens for one seat means a
	// token in a three-month-old scrollback still enrols after the seat was
	// offboarded — minting a new one retires the old.
	if _, err := s.db.Exec(
		`UPDATE invite SET used_at = ? WHERE actor = ? AND used_at = ''`,
		"superseded:"+now.UTC().Format(time.RFC3339Nano), actor); err != nil {
		return "", err
	}

	_, err := s.db.Exec(
		`INSERT INTO invite(token, actor, created, expires, scope) VALUES(?,?,?,?,?)`,
		token, actor, now.UTC().Format(time.RFC3339Nano),
		now.Add(InviteTTL).UTC().Format(time.RFC3339Nano), scope)
	if err != nil {
		return "", err
	}
	return token, nil
}

// EnrolledSeats counts seats that have ever registered a key. Zero is the
// first-run signal: a hub nobody has joined yet.
func (s *Store) EnrolledSeats() int {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM actor_key`).Scan(&n); err != nil {
		return 0
	}
	return n
}

// MintBootstrapInvite creates the first-seat token, minted only by serve and
// only when the hub is empty. It binds to whichever actor redeems it, because
// on an empty hub there is nobody to name yet — and RedeemInvite refuses it
// the moment any seat is enrolled, so it cannot outlive its purpose.
func (s *Store) MintBootstrapInvite(now time.Time) (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)
	// One live bootstrap token, same rule as one live invite per actor: a
	// restart mints a fresh one and the one in old scrollback dies.
	if _, err := s.db.Exec(
		`UPDATE invite SET used_at = ? WHERE actor = '*' AND used_at = ''`,
		"superseded:"+now.UTC().Format(time.RFC3339Nano)); err != nil {
		return "", err
	}
	// The first seat owns the hub, so the bootstrap invite is always all-rooms:
	// there is no one to scope it against yet, and the owner is the one who will
	// scope everybody else.
	_, err := s.db.Exec(
		`INSERT INTO invite(token, actor, created, expires, scope) VALUES(?,'*',?,?,?)`,
		token, now.UTC().Format(time.RFC3339Nano),
		now.Add(InviteTTL).UTC().Format(time.RFC3339Nano), ScopeAll)
	if err != nil {
		return "", err
	}
	return token, nil
}

// InviteActor reports which seat a live token enrols and the rooms it grants,
// spending nothing. The holder already has the whole credential; naming its
// seat is what lets the composer set the right actor instead of making the
// person guess, and the scope is what lets it show what the token grants
// before anyone pastes it. "*" as the actor means a bootstrap token: the
// redeemer names the seat. Scope is "all" or a comma-separated room list.
func (s *Store) InviteActor(token string, now time.Time) (actor, scope string, err error) {
	var usedAt, expires string
	err = s.db.QueryRow(
		`SELECT actor, used_at, COALESCE(expires,''), COALESCE(scope,'all') FROM invite WHERE token = ?`, token).
		Scan(&actor, &usedAt, &expires, &scope)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", errors.New("unknown token")
	}
	if err != nil {
		return "", "", err
	}
	if usedAt != "" {
		return "", "", errors.New("token already used")
	}
	if expires != "" {
		if exp, perr := time.Parse(time.RFC3339Nano, expires); perr == nil && now.After(exp) {
			return "", "", errors.New("token expired")
		}
	}
	return actor, scope, nil
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

	var forActor, usedAt, expires, scope string
	err = tx.QueryRow(
		`SELECT actor, used_at, COALESCE(expires,''), COALESCE(scope,'all') FROM invite WHERE token = ?`, token).
		Scan(&forActor, &usedAt, &expires, &scope)
	if errors.Is(err, sql.ErrNoRows) {
		// Almost always the operator minted it against a different database
		// than the server is running. The token is fine; it is in another file.
		return errors.New("this token does not exist in the database this server is " +
			"running. That usually means it was minted with a different --db: the " +
			"server prints the file it is serving at startup, and --invite prints the " +
			"file it minted into. Mint it against the same one, or point the server " +
			"at the database the token is in")
	}
	if err != nil {
		return err
	}
	if usedAt != "" {
		return errors.New("enrolment token already used")
	}
	bootstrap := false
	if forActor == "*" {
		// A bootstrap token names its seat at redemption — but only onto an
		// empty hub. Once anyone is enrolled, real invites exist, and a
		// floating bind-anyone token is a liability, not a convenience.
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM actor_key`).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return errors.New("this first-seat token only works on a hub with no seats, " +
				"and someone is already enrolled — ask them (or the operator) for an invite")
		}
		bootstrap = true
	} else if forActor != actor {
		return fmt.Errorf("enrolment token was minted for %s and cannot enrol %s — "+
			"enrol with --as %s, or mint a token for %s", forActor, actor, forActor, actor)
	}
	if expires != "" {
		exp, perr := time.Parse(time.RFC3339Nano, expires)
		if perr == nil && now.After(exp) {
			return fmt.Errorf("enrolment token expired %s ago; ask the operator for another",
				now.Sub(exp).Round(time.Minute))
		}
	}
	// Rotation after revocation is an operator act. An unspent invite must not
	// be able to undo an offboarding, and a compromised key must not be cleared
	// by anything an agent can run.
	var revoked, compromised string
	switch err := tx.QueryRow(
		`SELECT revoked_at, compromised FROM actor_key WHERE actor = ?`, actor).
		Scan(&revoked, &compromised); {
	case errors.Is(err, sql.ErrNoRows): // first enrolment
	case err != nil:
		return err
	case compromised != "":
		return errors.New("key for " + actor + " is marked compromised; " +
			"an invite cannot clear that. The operator re-enrols the seat deliberately")
	case revoked != "":
		return errors.New("seat " + actor + " was revoked; " +
			"an invite cannot undo an offboarding. The operator re-enrols the seat deliberately")
	}

	if err := ValidActor(actor); err != nil {
		return err
	}

	ts := now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`UPDATE invite SET used_at = ? WHERE token = ?`, ts, token); err != nil {
		return err
	}
	// The roster, same transaction as the key. An enrolled seat that is not
	// addressable would make recipient.unknown reject the people who did
	// everything right.
	if _, err := tx.Exec(
		`INSERT INTO actor(actor, first_seen, source) VALUES(?,?,'enrolment')
		 ON CONFLICT(actor) DO UPDATE SET source = 'enrolment'`, actor, ts); err != nil {
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
	// The first seat claims the hub, so it owns the hub: grant it the invite
	// capability in the same transaction that enrols it, so it can bring the
	// rest of the team on from the browser (gear -> invite) without anyone
	// running an operator command on the box. Granted by "bootstrap" to mark
	// that no human deliberately conferred it — it came with claiming an empty
	// hub. Later seats get no capability by default; the owner grants them.
	if bootstrap {
		if _, err := tx.Exec(
			`INSERT INTO capability(actor, capability, granted_at, granted_by)
			 VALUES(?, 'invite', ?, 'bootstrap')
			 ON CONFLICT(actor, capability) DO NOTHING`,
			actor, ts); err != nil {
			return err
		}
	}

	// Bind the seat's room membership in the same transaction as its key, so a
	// crash cannot enrol a seat that belongs to no room. The invite's scope is
	// the source: "all" (and every bootstrap token) writes the '*' wildcard;
	// otherwise one row per named room. No room-existence check — a scope may
	// name a room not created yet, and validating here would leak which rooms
	// exist to whoever holds the token. Rooms are lowercased/comma-split the
	// same way the invite stored them; blanks are dropped.
	for _, room := range membershipRooms(scope) {
		if _, err := tx.Exec(
			`INSERT INTO membership(actor, room, granted_at, granted_by)
			 VALUES(?,?,?,'invite')
			 ON CONFLICT(actor, room) DO NOTHING`,
			actor, room, ts); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// membershipRooms turns an invite scope into the membership rows it grants.
// "all" (or empty) becomes the single '*' wildcard; anything else splits on
// commas, trims, drops blanks, and dedupes. A scope that is somehow all-blank
// falls back to '*' rather than enrolling a seat into nothing — an invite that
// granted no rooms would be a seat that cannot act, which is never what minting
// one meant.
func membershipRooms(scope string) []string {
	scope = strings.TrimSpace(scope)
	if scope == "" || scope == ScopeAll || scope == membershipRoomAll {
		return []string{membershipRoomAll}
	}
	seen := map[string]bool{}
	var rooms []string
	for _, r := range strings.Split(scope, ",") {
		r = strings.TrimSpace(r)
		if r == "" || seen[r] {
			continue
		}
		seen[r] = true
		rooms = append(rooms, r)
	}
	if len(rooms) == 0 {
		return []string{membershipRoomAll}
	}
	return rooms
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

// ResolveActor expands a namespace-less shorthand against the roster, so
// `--to sarah` and the browser's `/ask @sarah` mean the same seat without each
// client inventing its own rule. It returns every candidate: one match is the
// answer, several are an ambiguity the caller must report rather than pick.
func (s *Store) ResolveActor(name string) []string {
	if name == "" || ValidActor(name) == nil {
		return []string{name}
	}
	rows, err := s.db.Query(
		`SELECT actor FROM actor WHERE actor = 'agent:' || ? OR actor = 'human:' || ?
		 ORDER BY actor`, name, name)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil
		}
		out = append(out, a)
	}
	return out
}

// capability is granted by the operator, never by an agent. It is a table
// rather than a flag on actor_key because a capability is a grant with a
// grantor, and "who gave the digest bot this" is the question asked during an
// incident.
const capabilitySchema = `
CREATE TABLE IF NOT EXISTS capability (
  actor      TEXT NOT NULL,
  capability TEXT NOT NULL,
  granted_at TEXT NOT NULL,
  granted_by TEXT NOT NULL,
  PRIMARY KEY (actor, capability)
);
`

// Grant gives a seat a capability. There is no verb for this: it is an
// operator act on the server binary, so no agent can reach it.
func (s *Store) Grant(actor, capability, by string, now time.Time) error {
	if err := ValidActor(actor); err != nil {
		return err
	}
	_, err := s.db.Exec(
		`INSERT INTO capability(actor, capability, granted_at, granted_by) VALUES(?,?,?,?)
		 ON CONFLICT(actor, capability) DO NOTHING`,
		actor, capability, now.UTC().Format(time.RFC3339Nano), by)
	return err
}

// Capabilities lists what a seat holds, for the settings page to decide which
// panels to draw. Authorization never reads this: every admin action re-proves
// the capability server-side on the signed request.
func (s *Store) Capabilities(actor string) []string {
	rows, err := s.db.Query(
		`SELECT capability FROM capability WHERE actor = ? ORDER BY capability`, actor)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var caps []string
	for rows.Next() {
		var c string
		if rows.Scan(&c) == nil {
			caps = append(caps, c)
		}
	}
	return caps
}

// HasCapability is the decision projection the core reads.
func (s *Store) HasCapability(actor, capability string) bool {
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM capability WHERE actor = ? AND capability = ?`,
		actor, capability).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

// membership is a seat's room scope: which rooms it may post into and read.
// It is a record with a grantor, exactly like capability — the same reason
// applies, "who let this seat into this room" is an incident question — and
// deliberately NOT a fold over the log, so -rebuild does not touch it. The
// row room='*' is the wildcard: an all-rooms seat, which is what an unscoped
// invite and every grandfathered seat holds. A scoped seat holds one row per
// room instead.
const membershipRoomAll = "*"

const membershipSchema = `
CREATE TABLE IF NOT EXISTS membership (
  actor      TEXT NOT NULL,
  room       TEXT NOT NULL,
  granted_at TEXT NOT NULL,
  granted_by TEXT NOT NULL,
  PRIMARY KEY (actor, room)
);
`

// AddMembership puts a seat in a room. '*' means all rooms. Idempotent, like
// Grant: re-adding a room a seat already holds is a no-op, so redeeming and
// backfilling can both write without checking first.
func (s *Store) AddMembership(actor, room, by string, now time.Time) error {
	if err := ValidActor(actor); err != nil {
		return err
	}
	_, err := s.db.Exec(
		`INSERT INTO membership(actor, room, granted_at, granted_by) VALUES(?,?,?,?)
		 ON CONFLICT(actor, room) DO NOTHING`,
		actor, room, now.UTC().Format(time.RFC3339Nano), by)
	return err
}

// Memberships lists a seat's rooms, for the settings page and for the
// no-leak error that names a refused poster's own rooms. '*' comes back as
// itself; the caller renders it as "all rooms". Authorization never reads
// this — IsMember is the decision projection.
func (s *Store) Memberships(actor string) []string {
	rows, err := s.db.Query(
		`SELECT room FROM membership WHERE actor = ? ORDER BY room`, actor)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var rooms []string
	for rows.Next() {
		var r string
		if rows.Scan(&r) == nil {
			rooms = append(rooms, r)
		}
	}
	return rooms
}

// IsMember is the decision projection the core reads: may this seat act in
// this room. A '*' row answers yes for every room; otherwise the exact room
// must be present. A seat with no rows at all is a member of nothing.
func (s *Store) IsMember(actor, room string) bool {
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM membership WHERE actor = ? AND (room = ? OR room = '*')`,
		actor, room).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

// AnyScopedMember reports whether any seat holds a non-wildcard membership —
// i.e. someone is scoped to specific rooms rather than all. The read gate
// uses it to decide whether reads must be authenticated hub-wide: a scoped
// seat existing is a promise of privacy that open reads would break. Callers
// cache the result and refresh it when a scoped row is written, rather than
// asking per request.
func (s *Store) AnyScopedMember() bool {
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM membership WHERE room != '*' LIMIT 1`).Scan(&n); err != nil {
		return false
	}
	return n > 0
}
