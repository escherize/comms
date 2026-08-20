// Package store is the append-only event log and the decision projections it
// feeds. It is the only authoritative state in the system; everything else is a
// projection, rebuildable by replay.
//
// Two tables hold an event: an immutable, chained envelope and a separate body
// blob. The chain covers body_hash rather than the body, so purging a leaked
// secret drops only the blob — no rewrite, no disabled trigger, and the chain
// still verifies end to end.
package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/escherize/comms/core"
	_ "modernc.org/sqlite"
)

// SeqJump is how far seq advances on every startup. A restore can lose the
// log's tail, and a resumed counter would reissue a value that is still live
// elsewhere as a fencing token. Gaps are free; collisions are not.
const SeqJump = 10_000

// Record is a stored event: the envelope plus its body, if the body still
// exists. A purged body leaves Body nil and BodyErased true.
type Record struct {
	Seq        int64
	ServerTS   time.Time
	Room       string
	Author     core.Actor
	Kind       core.Kind
	Recipient  core.Actor
	Lane       core.Lane
	Refs       []string
	Body       map[string]any
	BodyHash   string
	PrevHash   string
	BodyErased bool
	Attach     []core.Attachment
	Redacted   bool
	RedactedBy string

	// Rank is the bm25 score when this record came from a search. SQLite
	// returns lower-is-better, so it is negated here: bigger means a better
	// match, which is what a reader expects a rank column to mean.
	Rank float64
}

// Text returns the body's text field, or "" when the body is absent.
func (r Record) Text() string {
	if r.Body == nil {
		return ""
	}
	s, _ := r.Body["text"].(string)
	return s
}

// URL returns the body's url field, or "". Legacy pr.link rows carry a url
// instead of text; the kind is retired (ADR-0016 step 1) but the log keeps them.
func (r Record) URL() string {
	if r.Body == nil {
		return ""
	}
	s, _ := r.Body["url"].(string)
	return s
}

// Severity returns the body's severity field, or "".
// About names what the event concerns: a ticket, a file, a ref.
func (r Record) About() string {
	if r.Body == nil {
		return ""
	}
	a, _ := r.Body["about"].(string)
	return a
}

func (r Record) Severity() string {
	if r.Body == nil {
		return ""
	}
	s, _ := r.Body["severity"].(string)
	return s
}

// Store owns the log. A single writer serializes every append, which is what
// gives total order without consensus.
type Store struct {
	db *sql.DB
}

// ErrDuplicate reports that an idempotency key has already been used. The
// caller answers the retry from the log rather than re-deciding it.
type ErrDuplicate struct{ Seq int64 }

func (e ErrDuplicate) Error() string {
	return fmt.Sprintf("idempotency key already applied at seq %d", e.Seq)
}

// ErrIdemConflict reports the same idempotency key arriving with different
// content. Answering that as a duplicate would return someone else's seq and
// silently discard the post — data loss reported as success.
type ErrIdemConflict struct{ Seq int64 }

func (e ErrIdemConflict) Error() string {
	return fmt.Sprintf("idempotency key already used at seq %d with different content", e.Seq)
}

const schema = `
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;

-- The envelope: chained and immutable.
CREATE TABLE IF NOT EXISTS envelope (
  seq        INTEGER PRIMARY KEY,
  server_ts  TEXT    NOT NULL,
  room       TEXT    NOT NULL,
  author     TEXT    NOT NULL,
  kind       TEXT    NOT NULL,
  recipient  TEXT    NOT NULL DEFAULT '',
  lane       INTEGER NOT NULL,
  refs       TEXT    NOT NULL DEFAULT '[]',
  idem       TEXT    NOT NULL UNIQUE,
  idem_hash  TEXT    NOT NULL DEFAULT '',
  body_hash  TEXT    NOT NULL,
  prev_hash  TEXT    NOT NULL,
  attach     TEXT    NOT NULL DEFAULT '[]'
);

-- The body: droppable without touching the chain.
CREATE TABLE IF NOT EXISTS body (
  seq  INTEGER PRIMARY KEY REFERENCES envelope(seq),
  json TEXT NOT NULL
);

-- Append-only, enforced rather than asserted. The DB stays inspectable with
-- sqlite3, which is a virtue and also a write path — hence these.
CREATE TRIGGER IF NOT EXISTS envelope_no_update
BEFORE UPDATE ON envelope
BEGIN SELECT RAISE(ABORT, 'envelope is append-only'); END;

CREATE TRIGGER IF NOT EXISTS envelope_no_delete
BEFORE DELETE ON envelope
BEGIN SELECT RAISE(ABORT, 'envelope is append-only'); END;

-- Decision projections. Updated in the same transaction as the append, so the
-- decider reads them read-your-writes consistent.
CREATE TABLE IF NOT EXISTS room (
  name    TEXT PRIMARY KEY,
  created TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS envelope_room_seq ON envelope(room, seq);

-- Derived projection: lexical search, updated in the append transaction so an
-- event is findable the moment it is posted.
CREATE VIRTUAL TABLE IF NOT EXISTS search USING fts5(
  text, author UNINDEXED, kind UNINDEXED, room UNINDEXED, seq UNINDEXED
);

CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
` + keySchema + inviteSchema + redactSchema + `

-- Artifacts: content-addressed GFM, deduped by hash, referenced by events.
-- Stored beside the log so litestream covers them with no second backup path.
CREATE TABLE IF NOT EXISTS artifact (
  hash    TEXT PRIMARY KEY,
  bytes   BLOB NOT NULL,
  size    INTEGER NOT NULL,
  created TEXT NOT NULL
);

-- Progress: a decision projection folding the latest status per author, so the
-- room can show where an agent is without replaying its status events.
CREATE TABLE IF NOT EXISTS progress (
  room      TEXT NOT NULL,
  author    TEXT NOT NULL,
  step      INTEGER NOT NULL DEFAULT 0,
  of        INTEGER NOT NULL DEFAULT 0,
  note      TEXT NOT NULL DEFAULT '',
  server_ts TEXT NOT NULL,
  seq       INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (room, author)
);

-- The roster. A seat lands here on enrolment, and also on its first accepted
-- append, so an unsigned demo post makes its author addressable without a key.
-- It is what recipient.unknown is checked against: a typo has never posted.
CREATE TABLE IF NOT EXISTS actor (
  actor      TEXT PRIMARY KEY,
  first_seen TEXT NOT NULL,
  source     TEXT NOT NULL          -- 'enrolment' | 'post'
);

-- Question -> answer folding, maintained in the append transaction. A json_each
-- scan over refs at read time is O(events) per room view; ARCHITECTURE flags it
-- past 10^5, and orientation is the one call an agent makes before every task.
CREATE TABLE IF NOT EXISTS question (
  seq        INTEGER PRIMARY KEY,
  room       TEXT NOT NULL,
  author     TEXT NOT NULL,
  recipient  TEXT NOT NULL DEFAULT '',
  asked_at   TEXT NOT NULL,
  answer_seq INTEGER NOT NULL DEFAULT 0,
  answered_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS question_room ON question(room, answer_seq);
`

// Open prepares the store and advances seq past any live fencing tokens lost to
// a restore.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	// One writer. The whole ordering story depends on it.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(deliverySchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("delivery schema: %w", err)
	}
	if _, err := db.Exec(capabilitySchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("capability schema: %w", err)
	}
	if _, err := db.Exec(membershipSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("membership schema: %w", err)
	}
	if _, err := db.Exec(artifactRefSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("artifact_ref schema: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("schema: %w", err)
	}

	// CREATE TABLE IF NOT EXISTS does nothing to a table that already exists, so
	// a column added later has to be added explicitly. Adding a column that is
	// already there is the expected case, not an error.
	for _, alter := range []string{
		`ALTER TABLE progress ADD COLUMN seq INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE invite ADD COLUMN expires TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE invite ADD COLUMN scope TEXT NOT NULL DEFAULT 'all'`,
	} {
		if _, err := db.Exec(alter); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			db.Close()
			return nil, fmt.Errorf("migrate: %w", err)
		}
	}

	// Grandfather every already-enrolled seat to all rooms. Before room scoping
	// existed, every seat could post and read everywhere; membership must not
	// silently take that away on upgrade. INSERT OR IGNORE only writes a '*' row
	// for a seat that holds no membership at all, so this is a one-time backfill
	// that a later run — or a seat deliberately scoped since — does not disturb.
	if _, err := db.Exec(
		`INSERT OR IGNORE INTO membership(actor, room, granted_at, granted_by)
		 SELECT actor, '*', ?, 'grandfather' FROM actor_key
		 WHERE actor NOT IN (SELECT actor FROM membership)`,
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		db.Close()
		return nil, fmt.Errorf("grandfather membership: %w", err)
	}

	// Backfill artifact_ref from existing attachments, one time. An index built
	// only going forward would leave every pre-upgrade artifact reachable by any
	// authenticated seat via its raw hash — the exact bypass this closes. Empty
	// on a fresh hub, so it costs nothing there; on an existing one it folds the
	// attach column into the index once, and the INSERT OR IGNORE makes a repeat
	// run a no-op.
	if err := backfillArtifactRefs(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("backfill artifact_ref: %w", err)
	}

	s := &Store{db: db}
	if err := s.jumpSeq(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// jumpSeq advances the next-seq watermark past the current head plus SeqJump.
func (s *Store) jumpSeq() error {
	var head int64
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM envelope`).Scan(&head); err != nil {
		return err
	}
	var stored int64
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = 'next_seq'`).Scan(&stored)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	next := head
	if stored > next {
		next = stored
	}
	next += SeqJump
	_, err = s.db.Exec(
		`INSERT INTO meta(key, value) VALUES('next_seq', ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, next)
	return err
}

// EnsureRoom creates a room if it does not exist.
func (s *Store) EnsureRoom(name string) error {
	_, err := s.db.Exec(
		`INSERT INTO room(name, created) VALUES(?, ?) ON CONFLICT(name) DO NOTHING`,
		name, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// RoomExists backs the decider's decision projection.
func (s *Store) RoomExists(name string) bool {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM room WHERE name = ?`, name).Scan(&n)
	return n > 0
}

// Rooms lists rooms in creation order.
func (s *Store) Rooms() ([]string, error) {
	rows, err := s.db.Query(`SELECT name FROM room ORDER BY created, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// RoomHeads is each room's newest seq. The rail's unread marks compare these
// against what a browser has seen; a room with no events is simply absent.
func (s *Store) RoomHeads() (map[string]int64, error) {
	rows, err := s.db.Query(`SELECT room, MAX(seq) FROM envelope GROUP BY room`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var room string
		var head int64
		if err := rows.Scan(&room, &head); err != nil {
			return nil, err
		}
		out[room] = head
	}
	return out, rows.Err()
}

// replyIntoExchange reports whether any ref names an in-room addressed event
// between exactly these two seats — i.e. the post continues an existing
// exchange. A reply must not open a waiting-on item of its own: an agent
// answering a human's ask would otherwise open a spurious "the human owes a
// response" row the human never asked for.
func replyIntoExchange(tx *sql.Tx, room string, refs []string, author, recipient string) bool {
	for _, ref := range refs {
		var a, r, rm string
		if err := tx.QueryRow(`SELECT author, recipient, room FROM envelope WHERE seq = ?`, ref).
			Scan(&a, &r, &rm); err != nil || rm != room || r == "" {
			continue
		}
		if (a == author && r == recipient) || (a == recipient && r == author) {
			return true
		}
	}
	return false
}

// EventRecipient backs the decider's reply-routing: who a prior event was
// addressed to, "" when it was ambient.
func (s *Store) EventRecipient(ref string) (core.Actor, bool) {
	var r string
	err := s.db.QueryRow(`SELECT recipient FROM envelope WHERE seq = ?`, ref).Scan(&r)
	if err != nil {
		return "", false
	}
	return core.Actor(r), true
}

// EventAuthor backs the decider's redaction authorization.
func (s *Store) EventAuthor(ref string) (core.Actor, bool) {
	var a string
	if err := s.db.QueryRow(`SELECT author FROM envelope WHERE seq = ?`, ref).Scan(&a); err != nil {
		return "", false
	}
	return core.Actor(a), true
}

// EventRoom backs the decider's same-room check for redaction targets.
func (s *Store) EventRoom(ref string) (string, bool) {
	var r string
	if err := s.db.QueryRow(`SELECT room FROM envelope WHERE seq = ?`, ref).Scan(&r); err != nil {
		return "", false
	}
	return r, true
}

// IsRedactedRef is the string-keyed form the decider uses.
func (s *Store) IsRedactedRef(ref string) bool {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM redacted WHERE seq = ?`, ref).Scan(&n)
	return n > 0
}

// Append writes one accepted event and every projection that must stay in step
// with it, in a single transaction. It returns the assigned seq.
//
// A repeated idem key returns ErrDuplicate carrying the original seq, so the
// retry is answered from the log instead of being decided again.
func (s *Store) Append(ev core.Event, idem string, now time.Time) (int64, error) {
	fingerprint := idemFingerprint(ev)
	if existing, storedHash, ok := s.seqForIdem(idem); ok {
		if storedHash != "" && storedHash != fingerprint {
			return existing, ErrIdemConflict{Seq: existing}
		}
		return existing, ErrDuplicate{Seq: existing}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var next int64
	if err := tx.QueryRow(`SELECT value FROM meta WHERE key = 'next_seq'`).Scan(&next); err != nil {
		return 0, fmt.Errorf("next_seq: %w", err)
	}

	// The genesis event chains to the empty string; every later event chains to
	// its predecessor's (prev_hash, body_hash) pair. Verify walks the same rule.
	var lastPrev, lastBody string
	chainPrev := ""
	err = tx.QueryRow(
		`SELECT prev_hash, body_hash FROM envelope ORDER BY seq DESC LIMIT 1`,
	).Scan(&lastPrev, &lastBody)
	switch {
	case errors.Is(err, sql.ErrNoRows): // genesis: chainPrev stays ""
	case err != nil:
		return 0, err
	default:
		chainPrev = hashChain(lastPrev, lastBody)
	}

	bodyJSON, err := json.Marshal(ev.Body)
	if err != nil {
		return 0, err
	}
	bodyHash := hashBytes(bodyJSON)

	refsJSON, err := json.Marshal(ev.Refs)
	if err != nil {
		return 0, err
	}
	attachJSON, err := json.Marshal(ev.Attachments)
	if err != nil {
		return 0, err
	}

	ts := now.UTC().Format(time.RFC3339Nano)
	_, err = tx.Exec(
		`INSERT INTO envelope(seq, server_ts, room, author, kind, recipient, lane, refs, idem, body_hash, prev_hash, attach, idem_hash)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		next, ts, ev.Room, string(ev.Author), string(ev.Kind), string(ev.Recipient),
		int(ev.Lane), string(refsJSON), idem, bodyHash, chainPrev, string(attachJSON),
		fingerprint)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`INSERT INTO body(seq, json) VALUES(?, ?)`, next, string(bodyJSON)); err != nil {
		return 0, err
	}

	// Derived projection, updated in the same transaction: read-your-writes for
	// lexical search means an event is findable the millisecond it is posted.
	// Artifact text is indexed alongside the event, so searching a report's
	// contents finds the event that carries it — the point of storing markdown.
	text, _ := ev.Body["text"].(string)
	indexed := text
	// `about` names what an event concerns — a ticket, a file, a ref. Indexing
	// it makes "everything on ticket 20" a search rather than a hope that
	// somebody spelt it the same way in prose.
	if about, _ := ev.Body["about"].(string); about != "" {
		indexed += "\n" + about
	}
	for _, a := range ev.Attachments {
		indexed += "\n" + a.Title
		var blob []byte
		if err := tx.QueryRow(`SELECT bytes FROM artifact WHERE hash = ?`, a.Hash).Scan(&blob); err == nil {
			indexed += "\n" + string(blob)
		}
		// Index the reference so /a/<hash> access can be decided by membership
		// in this event's room, without scanning every envelope's attach column.
		if err := addArtifactRef(tx, a.Hash, next, ev.Room); err != nil {
			return 0, err
		}
	}
	if _, err := tx.Exec(
		`INSERT INTO search(text, author, kind, room, seq) VALUES(?,?,?,?,?)`,
		indexed, string(ev.Author), string(ev.Kind), ev.Room, next); err != nil {
		return 0, err
	}

	// Decision projection, same transaction: a post carrying --step/--of folds
	// into the author's current progress rather than being replayed to
	// reconstruct it. The trigger is the body, not a kind (ADR-0020) — legacy
	// `status` events keep folding, including the step-less kind that carried
	// the counter forward; a new post without step/of leaves progress alone.
	step, hasStep := ev.Body["step"].(float64)
	of, hasOf := ev.Body["of"].(float64)
	if ev.Kind == core.Kind("status") || hasStep || hasOf {
		// A missing field is not a claim that it went to zero: --step 3 on a
		// 2-of-5 job means step 3 of 5, and a step-less legacy status carries
		// the author's whole counter forward. Each absent field individually
		// inherits its prior value rather than erasing it.
		if !hasStep || !hasOf {
			var priorStep, priorOf int
			if err := tx.QueryRow(
				`SELECT step, of FROM progress WHERE room = ? AND author = ?`,
				ev.Room, string(ev.Author)).Scan(&priorStep, &priorOf); err == nil {
				if !hasStep {
					step = float64(priorStep)
				}
				if !hasOf {
					of = float64(priorOf)
				}
			}
		}
		if _, err := tx.Exec(
			`INSERT INTO progress(room, author, step, of, note, server_ts, seq)
			 VALUES(?,?,?,?,?,?,?)
			 ON CONFLICT(room, author) DO UPDATE SET
			   -- Progress does not run backwards within one piece of work. A
			   -- --step 2 landing after a --step 5 moves the bar back and makes
			   -- a room read as though work were undone.
			   --
			   -- The guard is on the step, not on a timestamp or a seq, and that
			   -- is not a stylistic choice: a delayed status arrives with a
			   -- later server_ts and a higher seq, because the server stamps and
			   -- numbers at arrival. There is no trustworthy earlier time to
			   -- compare against — a client-declared one would be exactly the
			   -- adversarial created_at this design refuses. So the ordering
			   -- guard cannot come from ordering; it comes from the meaning of
			   -- the field.
			   --
			   -- A changed total is a new piece of work, and step 1 of 4 after
			   -- step 5 of 7 is progress, not a rewind.
			   step      = CASE WHEN excluded.of = progress.of AND excluded.step < progress.step
			                    THEN progress.step ELSE excluded.step END,
			   of        = excluded.of,
			   -- The note and the clock always move: the actor posted, so it is
			   -- alive, whatever the step says.
			   note      = excluded.note,
			   server_ts = excluded.server_ts,
			   seq       = excluded.seq`,
			ev.Room, string(ev.Author), int(step), int(of), text, ts, next); err != nil {
			return 0, err
		}
	}

	// The roster, same transaction. An author who posted is a seat that exists,
	// which is what makes recipient.unknown safe to enforce on a hub where the
	// browser posts unsigned.
	if _, err := tx.Exec(
		`INSERT INTO actor(actor, first_seen, source) VALUES(?,?,'post')
		 ON CONFLICT(actor) DO NOTHING`, string(ev.Author), ts); err != nil {
		return 0, err
	}

	// The waiting-on-a-person projection, same transaction. A reply routed
	// back to a question's author closes the question (ADR-0016 rule 2: the
	// ref carries the relationship; there is no answer kind). First reply
	// wins: a question is open or it is not, and later replies do not reopen
	// it. Legacy answer events match this same shape — recipient set to the
	// asker, ref to the question — so rebuild folds them identically.
	var closedARef bool
	if ev.Recipient != "" {
		for _, ref := range ev.Refs {
			res, err := tx.Exec(
				`UPDATE question SET answer_seq = ?, answered_at = ?
				 WHERE seq = ? AND answer_seq = 0 AND author = ?`,
				next, ts, ref, string(ev.Recipient))
			if err != nil {
				return 0, err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				closedARef = true
			}
		}
	}
	// What opens an item: a post addressed to a human seat — a person owes a
	// response — unless the post is a reply: it closed a row, or it continues
	// an exchange its refs already carry. Legacy `question` events open
	// regardless of namespace, so old logs rebuild exactly. ponytail: a
	// human-addressed ack nobody replies to sits open; per-thread state if
	// that ever matters.
	if ev.Kind == core.Kind("question") ||
		(ev.Kind == core.KindChat && !closedARef && ev.Recipient != "" && !ev.Recipient.IsAgent() &&
			!replyIntoExchange(tx, ev.Room, ev.Refs, string(ev.Author), string(ev.Recipient))) {
		if _, err := tx.Exec(
			`INSERT INTO question(seq, room, author, recipient, asked_at)
			 VALUES(?,?,?,?,?) ON CONFLICT(seq) DO NOTHING`,
			next, ev.Room, string(ev.Author), string(ev.Recipient), ts); err != nil {
			return 0, err
		}
	}

	// Redaction folds in the same transaction as its event. Committing the
	// redact event first and suppressing in a second transaction left a crash
	// window where the log claimed suppression that never happened — and
	// nothing on restart would re-apply it. Atomic here, re-derivable in
	// Rebuild: the redacted table is a true projection.
	if ev.Kind == core.KindRedact && len(ev.Refs) == 1 {
		if target, convErr := strconv.ParseInt(ev.Refs[0], 10, 64); convErr == nil {
			if err := applyRedactionTx(tx, target, next, string(ev.Author), now); err != nil {
				return 0, fmt.Errorf("redaction: %w", err)
			}
		}
	}

	if _, err := tx.Exec(`UPDATE meta SET value = ? WHERE key = 'next_seq'`, next+1); err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return next, nil
}

func (s *Store) seqForIdem(idem string) (int64, string, bool) {
	var seq int64
	var hash string
	err := s.db.QueryRow(`SELECT seq, idem_hash FROM envelope WHERE idem = ?`, idem).
		Scan(&seq, &hash)
	if err != nil {
		return 0, "", false
	}
	return seq, hash, true
}

// idemFingerprint identifies the content a key was used for, so a reused key
// carrying different content is distinguishable from a genuine retry.
func idemFingerprint(ev core.Event) string {
	body, _ := json.Marshal(ev.Body)
	refs, _ := json.Marshal(ev.Refs)
	// Attachments are content too: without them here, a reused key with a
	// changed --attach read as a replay and the new attachment silently
	// vanished.
	attach, _ := json.Marshal(ev.Attachments)
	return hashBytes([]byte(ev.Room + "\x00" + string(ev.Author) + "\x00" +
		string(ev.Kind) + "\x00" + string(ev.Recipient) + "\x00" +
		string(body) + "\x00" + string(refs) + "\x00" + string(attach)))
}

// Since returns a room's records with seq greater than after, oldest first.
// Passing 0 returns the whole room.
// Latest returns the newest `limit` events, oldest-first so a renderer can walk
// them in order. It exists because Since is the resume path — "everything after
// my cursor, capped" — and a room page that used it showed the *first* 500
// events forever: past 500, the page freezes on ancient history and the live
// tail is unreachable, while the SSE stream keeps appending to a head nobody
// can see the body of.
// MatchesQuery reports whether one event satisfies a search. It is a single
// indexed lookup against the same FTS table Search reads, so a live search page
// costs one row probe per arriving event rather than re-running the query.
func (s *Store) MatchesQuery(seq int64, query string) bool {
	fts := ftsQuery(query)
	if fts == "" {
		return false
	}
	var one int
	err := s.db.QueryRow(
		`SELECT 1 FROM search WHERE seq = ? AND search MATCH ? LIMIT 1`, seq, fts).Scan(&one)
	return err == nil
}

func (s *Store) Latest(room string, limit int) ([]Record, error) {
	rows, err := s.db.Query(`
		SELECT seq, server_ts, room, author, kind, recipient, lane,
		       refs, body_hash, prev_hash, json, attach FROM (
		  SELECT e.seq, e.server_ts, e.room, e.author, e.kind, e.recipient, e.lane,
		         e.refs, e.body_hash, e.prev_hash, b.json, e.attach
		  FROM envelope e LEFT JOIN body b ON b.seq = e.seq
		  WHERE e.room = ?
		  ORDER BY e.seq DESC
		  LIMIT ?
		) ORDER BY seq`, room, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	recs, err := scanRecords(rows)
	if err != nil {
		return nil, err
	}
	// The same suppression Since applies. A read path that skipped it would
	// serve a redacted body from the one surface a human actually opens.
	return s.markRedactions(recs), nil
}

func (s *Store) Since(room string, after int64, limit int) ([]Record, error) {
	rows, err := s.db.Query(`
		SELECT e.seq, e.server_ts, e.room, e.author, e.kind, e.recipient, e.lane,
		       e.refs, e.body_hash, e.prev_hash, b.json, e.attach
		FROM envelope e LEFT JOIN body b ON b.seq = e.seq
		WHERE e.room = ? AND e.seq > ?
		ORDER BY e.seq
		LIMIT ?`, room, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	recs, err := scanRecords(rows)
	if err != nil {
		return nil, err
	}
	return s.markRedactions(recs), nil
}

// ftsQuery makes a user's words safe for FTS5. Bare terms like "sqlite-vec" or
// "auth.py:88" are syntax to FTS5 — a hyphen reads as NOT, a colon as a column
// filter — so each token is quoted into a literal and the tokens OR together
// (see below for why OR). Without the quoting, ordinary developer vocabulary
// silently returns nothing.
func ftsQuery(raw string) string {
	fields := strings.Fields(raw)
	quoted := make([]string, 0, len(fields))
	for _, f := range fields {
		quoted = append(quoted, `"`+strings.ReplaceAll(f, `"`, `""`)+`"`)
	}
	// OR, not AND. ANDing every token meant one word the room happens not to
	// contain returned zero hits — so an agent searching before it posts
	// concluded nobody knew, and posted a duplicate. bm25 ranking already puts
	// the best match first; precision is the ranker's job, not the gate's.
	return strings.Join(quoted, " OR ")
}

// Search runs the lexical lane. allow is a room allow-list: nil means every
// room (the full view or a '*'-scoped seat), a non-nil slice confines the
// search to exactly those rooms, and an empty slice matches nothing. It is the
// reading seat's membership, applied at the source so a non-member room's
// content never enters the result set — scoping the query, not filtering after.
func (s *Store) Search(query, room, author, since string, allow []string, limit int) ([]Record, error) {
	q := ftsQuery(query)
	if q == "" {
		return nil, nil
	}
	// An empty (non-nil) allow-list is a seat that may read no room: no query
	// can return a hit, so short-circuit rather than build `IN ()`.
	if allow != nil && len(allow) == 0 {
		return nil, nil
	}
	var (
		where []string
		args  []any
	)
	args = append(args, q)
	if room != "" {
		where = append(where, "e.room = ?")
		args = append(args, room)
	}
	if len(allow) > 0 {
		ph := make([]string, len(allow))
		for i, rm := range allow {
			ph[i] = "?"
			args = append(args, rm)
		}
		where = append(where, "e.room IN ("+strings.Join(ph, ",")+")")
	}
	if author != "" {
		where = append(where, "e.author = ?")
		args = append(args, author)
	}
	if since != "" {
		// A date or a full timestamp; RFC3339 sorts lexically, so a plain
		// comparison is correct without parsing — with one trap: stored
		// stamps carry fractional seconds ("…50.123Z") and sort BEFORE a
		// second-precision "…50Z" because '.' < 'Z'. Stripping the caller's
		// trailing Z turns the compare into a prefix test, so the boundary
		// second's events are kept instead of silently dropped.
		where = append(where, "e.server_ts >= ?")
		args = append(args, strings.TrimSuffix(since, "Z"))
	}
	clause := ""
	if len(where) > 0 {
		clause = " AND " + strings.Join(where, " AND ")
	}
	args = append(args, limit)

	rows, err := s.db.Query(`
		SELECT e.seq, e.server_ts, e.room, e.author, e.kind, e.recipient, e.lane,
		       e.refs, e.body_hash, e.prev_hash, b.json, e.attach, bm25(search)
		FROM search
		JOIN envelope e ON e.seq = search.seq
		LEFT JOIN body b ON b.seq = e.seq
		WHERE search MATCH ?`+clause+`
		ORDER BY rank
		LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	recs, err := scanRanked(rows)
	if err != nil {
		return nil, err
	}
	return s.markRedactions(recs), nil
}

func scanRecords(rows *sql.Rows) ([]Record, error) {
	var out []Record
	for rows.Next() {
		var (
			r        Record
			ts, refs string
			author   string
			kind     string
			recip    string
			lane     int
			bodyJSON sql.NullString
			attach   string
		)
		if err := rows.Scan(&r.Seq, &ts, &r.Room, &author, &kind, &recip, &lane,
			&refs, &r.BodyHash, &r.PrevHash, &bodyJSON, &attach); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(attach), &r.Attach)
		r.ServerTS, _ = time.Parse(time.RFC3339Nano, ts)
		r.Author = core.Actor(author)
		r.Kind = core.Kind(kind)
		r.Recipient = core.Actor(recip)
		r.Lane = core.Lane(lane)
		_ = json.Unmarshal([]byte(refs), &r.Refs)
		if bodyJSON.Valid {
			_ = json.Unmarshal([]byte(bodyJSON.String), &r.Body)
		} else {
			r.BodyErased = true
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// markRedactions blanks suppressed bodies after scanning. Suppression is a
// projection, so it is applied on read rather than baked into the row.
func (s *Store) markRedactions(recs []Record) []Record {
	for i := range recs {
		if s.IsRedacted(recs[i].Seq) {
			recs[i].Body = nil
			recs[i].Redacted = true
			recs[i].RedactedBy, _, _ = s.Redaction(recs[i].Seq)
		}
	}
	return recs
}

// Purge erases a body permanently. The envelope, its hash, and the chain
// survive, so the log still verifies and now attests: a body with this hash was
// here and is gone.
// Purge erases a body permanently: the blob, its attachments, and its lexical
// index row, all in one transaction.
//
// One transaction because a partial erasure is worse than none — it reports
// success while the secret survives in whichever table the failure came after,
// and nobody looks again.
func (s *Store) Purge(seq int64) error {
	// Read the attachments before opening the transaction. The pool is one
	// connection by design, so a query issued while a transaction is open waits
	// for the connection that transaction holds.
	var hashes []string
	if recs, err := s.bySeq(seq); err == nil {
		for _, r := range recs {
			for _, a := range r.Attach {
				hashes = append(hashes, a.Hash)
			}
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Attachments die with the body. A secret pasted into a report must not
	// outlive the redaction that erased the message carrying it — but a blob
	// is content-addressed and shared: a surviving event referencing the same
	// hash would be left with a permanent 404, unrepairable in an append-only
	// log. Same guard redaction uses: drop only what nobody else references.
	for _, h := range hashes {
		var others int
		_ = tx.QueryRow(
			`SELECT COUNT(*) FROM envelope e
			 WHERE e.seq != ? AND e.attach LIKE ?
			   AND e.seq NOT IN (SELECT seq FROM redacted)`,
			seq, "%"+h+"%").Scan(&others)
		if others == 0 {
			if _, err := tx.Exec(`DELETE FROM artifact WHERE hash = ?`, h); err != nil {
				return err
			}
		}
	}
	for _, stmt := range []string{
		`DELETE FROM body WHERE seq = ?`,
		`DELETE FROM search WHERE seq = ?`,
		// The read grant dies with the body too. Redaction deletes this row;
		// purge did not, so a purged event kept granting /a/<hash> to its
		// rooms via ArtifactRooms — a stale scoping hole in an append-only log.
		`DELETE FROM artifact_ref WHERE seq = ?`,
	} {
		if _, err := tx.Exec(stmt, seq); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) bySeq(seq int64) ([]Record, error) {
	rows, err := s.db.Query(`
		SELECT e.seq, e.server_ts, e.room, e.author, e.kind, e.recipient, e.lane,
		       e.refs, e.body_hash, e.prev_hash, b.json, e.attach
		FROM envelope e LEFT JOIN body b ON b.seq = e.seq
		WHERE e.seq = ?`, seq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRecords(rows)
}

// Verify walks the chain by prev_hash. It deliberately does not check seq
// contiguity: seq is strictly increasing but gapped by design, and a
// contiguity assertion would report corruption after every legitimate restore.
func (s *Store) Verify() error {
	rows, err := s.db.Query(`SELECT seq, body_hash, prev_hash FROM envelope ORDER BY seq`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var lastPrev, lastBody string
	first := true
	for rows.Next() {
		var seq int64
		var bodyHash, prevHash string
		if err := rows.Scan(&seq, &bodyHash, &prevHash); err != nil {
			return err
		}
		want := ""
		if !first {
			want = hashChain(lastPrev, lastBody)
		}
		if prevHash != want {
			return fmt.Errorf("chain broken at seq %d: prev_hash %q, expected %q", seq, prevHash, want)
		}
		lastPrev, lastBody, first = prevHash, bodyHash, false
	}
	return rows.Err()
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hashChain(prev, body string) string {
	return hashBytes([]byte(prev + ":" + body))
}

// scanRanked reads the search shape, which carries a bm25 score after the
// columns scanRecords expects.
func scanRanked(rows *sql.Rows) ([]Record, error) {
	var out []Record
	for rows.Next() {
		var (
			r        Record
			ts, refs string
			author   string
			kind     string
			recip    string
			lane     int
			bodyJSON sql.NullString
			attach   string
			score    float64
		)
		if err := rows.Scan(&r.Seq, &ts, &r.Room, &author, &kind, &recip, &lane,
			&refs, &r.BodyHash, &r.PrevHash, &bodyJSON, &attach, &score); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(attach), &r.Attach)
		r.ServerTS, _ = time.Parse(time.RFC3339Nano, ts)
		r.Author = core.Actor(author)
		r.Kind = core.Kind(kind)
		r.Recipient = core.Actor(recip)
		r.Lane = core.Lane(lane)
		_ = json.Unmarshal([]byte(refs), &r.Refs)
		if bodyJSON.Valid {
			_ = json.Unmarshal([]byte(bodyJSON.String), &r.Body)
		} else {
			r.BodyErased = true
		}
		// SQLite's bm25 is lower-is-better; negate so a bigger number reads as
		// a better match, which is what a rank column implies.
		r.Rank = -score
		out = append(out, r)
	}
	return out, rows.Err()
}

// RecordAt loads one event by seq, with redaction applied. A hit whose body
// cannot be loaded is a row that says nothing.
func (s *Store) RecordAt(seq int64) (Record, bool) {
	rows, err := s.db.Query(`
		SELECT e.seq, e.server_ts, e.room, e.author, e.kind, e.recipient, e.lane,
		       e.refs, e.body_hash, e.prev_hash, b.json, e.attach
		FROM envelope e LEFT JOIN body b ON b.seq = e.seq
		WHERE e.seq = ?`, seq)
	if err != nil {
		return Record{}, false
	}
	defer rows.Close()
	recs, err := scanRecords(rows)
	if err != nil || len(recs) == 0 {
		return Record{}, false
	}
	return s.markRedactions(recs)[0], true
}

// Head is the highest seq in the log, across every room. The watermark is
// compared against it, so it must not be per-room: a lane that is current in
// core and an hour behind in bash is behind.
func (s *Store) Head() int64 {
	var head sql.NullInt64
	if err := s.db.QueryRow(`SELECT MAX(seq) FROM envelope`).Scan(&head); err != nil {
		return 0
	}
	return head.Int64
}

// ServerTSOf is when an event was appended, which is what turns a watermark
// into a sentence: "current to 14:32" rather than "behind by 812", since seq is
// gappy by design and a count of them means nothing to a reader.
func (s *Store) ServerTSOf(seq int64) (time.Time, bool) {
	var ts string
	if err := s.db.QueryRow(`SELECT server_ts FROM envelope WHERE seq = ?`, seq).Scan(&ts); err != nil {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, ts)
	return at, err == nil
}

// NextSeq is the seq the next append will take. A drill prints it beside the
// head to show the gap a restart opened: the jump is what makes a fencing
// token issued before a restore unissuable after one.
func (s *Store) NextSeq() int64 {
	var next int64
	if err := s.db.QueryRow(`SELECT value FROM meta WHERE key = 'next_seq'`).Scan(&next); err != nil {
		return 0
	}
	return next
}
