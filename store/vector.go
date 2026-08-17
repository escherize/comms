package store

import (
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// The semantic lane.
//
// ARCHITECTURE names sqlite-vec. It is not reachable from here: ADR-0009 picks
// modernc.org/sqlite, a pure-Go transpilation of SQLite with no C ABI, and
// sqlite-vec is a loadable C extension. The choice is between giving up the
// pure-Go build — which is what makes this a single static binary with no cgo
// toolchain — and doing the arithmetic in Go.
//
// The arithmetic is not the expensive part at this size. A room of 10^5 events
// at 384 dimensions is 150MB of float32 read once and scored in a few tens of
// milliseconds; the embedding call that produced the query vector costs more
// than the scan over every vector it is compared against. When a room is large
// enough for that to stop being true, the fix is an ANN index, not a different
// SQLite driver.

const vectorSchema = `
CREATE TABLE IF NOT EXISTS vector (
  seq   INTEGER PRIMARY KEY REFERENCES envelope(seq),
  dim   INTEGER NOT NULL,
  vec   BLOB    NOT NULL,
  model TEXT    NOT NULL,
  at    TEXT    NOT NULL
);

-- One poison event must not stall the lane, so failures are counted and the
-- watermark moves past them. Three attempts, then it is dead-lettered and
-- visible: an event nobody can embed is a fact about the room, not a secret
-- kept by the worker.
CREATE TABLE IF NOT EXISTS embed_failure (
  seq      INTEGER PRIMARY KEY REFERENCES envelope(seq),
  attempts INTEGER NOT NULL DEFAULT 0,
  last     TEXT    NOT NULL DEFAULT '',
  at       TEXT    NOT NULL
);

CREATE TABLE IF NOT EXISTS embed_state (key TEXT PRIMARY KEY, value TEXT NOT NULL);
`

// EmbedAttempts is how many times one event is tried before it is dead-lettered.
const EmbedAttempts = 3

// Vector is one stored embedding.
type Vector struct {
	Seq   int64
	Model string
	Vec   []float32
}

// PutVector stores an embedding and clears any failure record for that seq: a
// retry that succeeds is not still a failure.
func (s *Store) PutVector(seq int64, vec []float32, model string, now time.Time) error {
	if len(vec) == 0 {
		return errors.New("refusing to store an empty embedding")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`INSERT INTO vector(seq, dim, vec, model, at) VALUES(?,?,?,?,?)
		 ON CONFLICT(seq) DO UPDATE SET dim=excluded.dim, vec=excluded.vec,
		   model=excluded.model, at=excluded.at`,
		seq, len(vec), encodeVector(vec), model, now.UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM embed_failure WHERE seq = ?`, seq); err != nil {
		return err
	}
	return tx.Commit()
}

// RecordEmbedFailure counts one failed attempt and reports the total. At
// EmbedAttempts the caller dead-letters it and moves on.
func (s *Store) RecordEmbedFailure(seq int64, cause string, now time.Time) (attempts int, err error) {
	ts := now.UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(
		`INSERT INTO embed_failure(seq, attempts, last, at) VALUES(?,1,?,?)
		 ON CONFLICT(seq) DO UPDATE SET attempts = embed_failure.attempts + 1,
		   last = excluded.last, at = excluded.at`, seq, cause, ts); err != nil {
		return 0, err
	}
	err = s.db.QueryRow(`SELECT attempts FROM embed_failure WHERE seq = ?`, seq).Scan(&attempts)
	return attempts, err
}

// DeadLettered is the visible list of events the lane gave up on.
type DeadLetter struct {
	Seq      int64  `json:"seq"`
	Attempts int    `json:"attempts"`
	Last     string `json:"last_error"`
	At       string `json:"at"`
}

func (s *Store) DeadLettered() ([]DeadLetter, error) {
	rows, err := s.db.Query(
		`SELECT seq, attempts, last, at FROM embed_failure
		  WHERE attempts >= ? ORDER BY seq`, EmbedAttempts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeadLetter
	for rows.Next() {
		var d DeadLetter
		if err := rows.Scan(&d.Seq, &d.Attempts, &d.Last, &d.At); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// EmbeddedThrough is the watermark: every event at or below it has been
// embedded, or given up on. It is what makes staleness visible instead of
// silent — a search over a lane that is an hour behind is a true result an
// agent will draw a false conclusion from.
func (s *Store) EmbeddedThrough() int64 {
	var v string
	if err := s.db.QueryRow(
		`SELECT value FROM embed_state WHERE key = 'watermark'`).Scan(&v); err != nil {
		return 0
	}
	var seq int64
	fmt.Sscanf(v, "%d", &seq)
	return seq
}

// SetEmbeddedThrough advances the watermark, never backwards: a backfill of old
// events must not make the lane claim it is behind where it already is.
func (s *Store) SetEmbeddedThrough(seq int64) error {
	if seq <= s.EmbeddedThrough() {
		return nil
	}
	_, err := s.db.Exec(
		`INSERT INTO embed_state(key, value) VALUES('watermark', ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, fmt.Sprint(seq))
	return err
}

// PendingEmbeds returns events after the watermark that still need a vector,
// oldest first, skipping ones already dead-lettered.
func (s *Store) PendingEmbeds(limit int) ([]Record, error) {
	rows, err := s.db.Query(`
		SELECT e.seq, e.server_ts, e.room, e.author, e.kind, e.recipient, e.lane,
		       e.refs, e.body_hash, e.prev_hash, b.json, e.attach
		FROM envelope e LEFT JOIN body b ON b.seq = e.seq
		WHERE e.seq > ?
		  AND e.seq NOT IN (SELECT seq FROM vector)
		  AND e.seq NOT IN (SELECT seq FROM embed_failure WHERE attempts >= ?)
		  -- A suppressed or erased body has nothing to embed and never will.
		  -- Without this a rebuild manufactures dead letters for events that
		  -- are gone on purpose, and the list that should mean "something is
		  -- wrong" fills with entries that mean "something worked".
		  AND e.seq NOT IN (SELECT seq FROM redacted)
		  AND b.json IS NOT NULL
		ORDER BY e.seq LIMIT ?`, s.EmbeddedThrough(), EmbedAttempts, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRecords(rows)
}

// PendingFrom is the backfill query: everything from a seq that has no vector,
// regardless of the watermark.
func (s *Store) PendingFrom(seq int64, limit int) ([]Record, error) {
	rows, err := s.db.Query(`
		SELECT e.seq, e.server_ts, e.room, e.author, e.kind, e.recipient, e.lane,
		       e.refs, e.body_hash, e.prev_hash, b.json, e.attach
		FROM envelope e LEFT JOIN body b ON b.seq = e.seq
		WHERE e.seq >= ? ORDER BY e.seq LIMIT ?`, seq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRecords(rows)
}

// DropVectorsFrom clears the lane from a seq so a reembed rebuilds it, and
// clears the failure counts with it: a backfill is a fresh attempt, not a
// fourth one.
func (s *Store) DropVectorsFrom(seq int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM vector WHERE seq >= ?`, seq); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM embed_failure WHERE seq >= ?`, seq); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`INSERT INTO embed_state(key, value) VALUES('watermark', ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, fmt.Sprint(seq-1)); err != nil {
		return err
	}
	return tx.Commit()
}

// MinSimilarity is the floor below which a vector is not a hit.
//
// Without one, "nearest" means "every event in the room, ordered" — and an
// unrelated entry comes back ranked third with a similarity of zero, which is
// the semantic lane asserting a relationship that does not exist. A reader
// cannot tell that from a weak-but-real match, so the lane must not offer it.
//
// Zero cosine means no shared direction at all. The floor sits just above it
// rather than at some tuned value, because the right threshold depends on the
// model and this one is a stand-in; what must not be model-dependent is that
// "no relationship" never renders as a rank.
const MinSimilarity = 1e-6

// VectorHit is one semantically similar event.
type VectorHit struct {
	Seq   int64
	Score float64
}

// NearestVectors scores every vector in a room against the query. Brute force,
// deliberately: see the note at the top of this file.
// NearestVectors runs the semantic lane. allow is the same room allow-list as
// Search: nil is every room, a non-nil slice confines results to those rooms,
// an empty slice matches nothing — the reading seat's membership applied at the
// source so the semantic lane cannot surface a non-member room's content.
func (s *Store) NearestVectors(query []float32, room string, allow []string, limit int) ([]VectorHit, error) {
	if len(query) == 0 {
		return nil, nil
	}
	if allow != nil && len(allow) == 0 {
		return nil, nil
	}
	args := []any{room, room}
	allowClause := ""
	if len(allow) > 0 {
		ph := make([]string, len(allow))
		for i, rm := range allow {
			ph[i] = "?"
			args = append(args, rm)
		}
		allowClause = " AND e.room IN (" + strings.Join(ph, ",") + ")"
	}
	rows, err := s.db.Query(`
		SELECT v.seq, v.vec FROM vector v JOIN envelope e ON e.seq = v.seq
		WHERE (? = '' OR e.room = ?)`+allowClause+`
		  AND v.seq NOT IN (SELECT seq FROM redacted)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hits []VectorHit
	for rows.Next() {
		var seq int64
		var blob []byte
		if err := rows.Scan(&seq, &blob); err != nil {
			return nil, err
		}
		vec := decodeVector(blob)
		if len(vec) != len(query) {
			// A vector from a different model is not comparable. Skipping it is
			// right: a score computed across models is a number with no meaning.
			continue
		}
		score := cosine(query, vec)
		if score <= MinSimilarity {
			continue
		}
		hits = append(hits, VectorHit{Seq: seq, Score: score})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Descending by score; ties by seq so the order is stable.
	for i := 1; i < len(hits); i++ {
		for j := i; j > 0 && (hits[j].Score > hits[j-1].Score ||
			(hits[j].Score == hits[j-1].Score && hits[j].Seq < hits[j-1].Seq)); j-- {
			hits[j], hits[j-1] = hits[j-1], hits[j]
		}
	}
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// DropVector removes one event's embedding. Redaction calls this in the same
// transaction as the suppression: an embedding is derived from the secret and
// must not outlive it.
func (s *Store) DropVector(seq int64) error {
	_, err := s.db.Exec(`DELETE FROM vector WHERE seq = ?`, seq)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return nil
}

func encodeVector(v []float32) []byte {
	out := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(out[4*i:], math.Float32bits(f))
	}
	return out
}

func decodeVector(b []byte) []float32 {
	out := make([]float32, len(b)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return out
}

// cosine is similarity, not distance: 1 is identical, 0 unrelated.
func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
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
