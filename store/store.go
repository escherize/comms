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
	"strings"
	"time"

	"github.com/bcm/agent_comms/core"
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
}

// Text returns the body's text field, or "" when the body is absent.
func (r Record) Text() string {
	if r.Body == nil {
		return ""
	}
	s, _ := r.Body["text"].(string)
	return s
}

// URL returns the body's url field, or "". Kinds like pr.link carry a url
// instead of text.
func (r Record) URL() string {
	if r.Body == nil {
		return ""
	}
	s, _ := r.Body["url"].(string)
	return s
}

// Severity returns the body's severity field, or "".
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
  PRIMARY KEY (room, author)
);
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

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("schema: %w", err)
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

// EventKind backs the decider's ref lookups.
func (s *Store) EventKind(ref string) (core.Kind, bool) {
	var k string
	err := s.db.QueryRow(`SELECT kind FROM envelope WHERE seq = ?`, ref).Scan(&k)
	if err != nil {
		return "", false
	}
	return core.Kind(k), true
}

// Append writes one accepted event and every projection that must stay in step
// with it, in a single transaction. It returns the assigned seq.
//
// A repeated idem key returns ErrDuplicate carrying the original seq, so the
// retry is answered from the log instead of being decided again.
func (s *Store) Append(ev core.Event, idem string, now time.Time) (int64, error) {
	if existing, ok := s.seqForIdem(idem); ok {
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
		`INSERT INTO envelope(seq, server_ts, room, author, kind, recipient, lane, refs, idem, body_hash, prev_hash, attach)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		next, ts, ev.Room, string(ev.Author), string(ev.Kind), string(ev.Recipient),
		int(ev.Lane), string(refsJSON), idem, bodyHash, chainPrev, string(attachJSON))
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
	for _, a := range ev.Attachments {
		indexed += "\n" + a.Title
		var blob []byte
		if err := tx.QueryRow(`SELECT bytes FROM artifact WHERE hash = ?`, a.Hash).Scan(&blob); err == nil {
			indexed += "\n" + string(blob)
		}
	}
	if _, err := tx.Exec(
		`INSERT INTO search(text, author, kind, room, seq) VALUES(?,?,?,?,?)`,
		indexed, string(ev.Author), string(ev.Kind), ev.Room, next); err != nil {
		return 0, err
	}

	// Decision projection, same transaction: a status event folds into the
	// author's current progress rather than being replayed to reconstruct it.
	if ev.Kind == core.KindStatus {
		step, _ := ev.Body["step"].(float64)
		of, _ := ev.Body["of"].(float64)
		if _, err := tx.Exec(
			`INSERT INTO progress(room, author, step, of, note, server_ts) VALUES(?,?,?,?,?,?)
			 ON CONFLICT(room, author) DO UPDATE SET
			   step = excluded.step, of = excluded.of,
			   note = excluded.note, server_ts = excluded.server_ts`,
			ev.Room, string(ev.Author), int(step), int(of), text, ts); err != nil {
			return 0, err
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

func (s *Store) seqForIdem(idem string) (int64, bool) {
	var seq int64
	err := s.db.QueryRow(`SELECT seq FROM envelope WHERE idem = ?`, idem).Scan(&seq)
	if err != nil {
		return 0, false
	}
	return seq, true
}

// Since returns a room's records with seq greater than after, oldest first.
// Passing 0 returns the whole room.
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
// filter — so each token is quoted into a literal and the tokens AND together.
// Without this, ordinary developer vocabulary silently returns nothing.
func ftsQuery(raw string) string {
	fields := strings.Fields(raw)
	quoted := make([]string, 0, len(fields))
	for _, f := range fields {
		quoted = append(quoted, `"`+strings.ReplaceAll(f, `"`, `""`)+`"`)
	}
	return strings.Join(quoted, " ")
}

// Search runs the lexical lane. Filters are applied after the FTS match.
func (s *Store) Search(query, room, kind, author, since string, limit int) ([]Record, error) {
	q := ftsQuery(query)
	if q == "" {
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
	if kind != "" {
		where = append(where, "e.kind = ?")
		args = append(args, kind)
	}
	if author != "" {
		where = append(where, "e.author = ?")
		args = append(args, author)
	}
	if since != "" {
		// A date or a full timestamp; RFC3339 sorts lexically, so a plain
		// comparison is correct without parsing.
		where = append(where, "e.server_ts >= ?")
		args = append(args, since)
	}
	clause := ""
	if len(where) > 0 {
		clause = " AND " + strings.Join(where, " AND ")
	}
	args = append(args, limit)

	rows, err := s.db.Query(`
		SELECT e.seq, e.server_ts, e.room, e.author, e.kind, e.recipient, e.lane,
		       e.refs, e.body_hash, e.prev_hash, b.json, e.attach
		FROM search s
		JOIN envelope e ON e.seq = s.seq
		LEFT JOIN body b ON b.seq = e.seq
		WHERE search MATCH ?`+clause+`
		ORDER BY rank
		LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRecords(rows)
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
func (s *Store) Purge(seq int64) error {
	// Attachments die with the body. A secret pasted into a report must not
	// outlive the redaction that erased the message carrying it.
	if recs, err := s.bySeq(seq); err == nil {
		for _, r := range recs {
			for _, a := range r.Attach {
				if err := s.PurgeArtifact(a.Hash); err != nil {
					return err
				}
			}
		}
	}
	if _, err := s.db.Exec(`DELETE FROM body WHERE seq = ?`, seq); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM search WHERE seq = ?`, seq)
	return err
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
