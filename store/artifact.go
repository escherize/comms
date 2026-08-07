package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"

	"errors"
	"github.com/bcm/agent_comms/core"
	"regexp"
	"time"
)

// hashPattern is what a valid artifact reference looks like. Checking shape
// before touching the DB means a malformed hash is a parse rejection rather
// than a lookup miss.
var hashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ValidHash reports whether s is a well-formed artifact hash.
func ValidHash(s string) bool { return hashPattern.MatchString(s) }

// PutArtifact stores GFM content-addressed and returns its hash. Identical
// content stores once, so an agent re-uploading the same report is free.
func (s *Store) PutArtifact(content []byte, now time.Time) (string, error) {
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])
	_, err := s.db.Exec(
		`INSERT INTO artifact(hash, bytes, size, created) VALUES(?,?,?,?)
		 ON CONFLICT(hash) DO NOTHING`,
		hash, content, len(content), now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return "", err
	}
	return hash, nil
}

// GetArtifact returns stored content. A missing hash — including one whose
// blob was dropped by a purge — reports not found.
func (s *Store) GetArtifact(hash string) ([]byte, bool) {
	if !ValidHash(hash) {
		return nil, false
	}
	var b []byte
	err := s.db.QueryRow(`SELECT bytes FROM artifact WHERE hash = ?`, hash).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return nil, false
	}
	return b, true
}

// ArtifactExists backs the decider's attachment check. It is a decision
// projection read: an event may not reference content that is not stored.
func (s *Store) ArtifactExists(hash string) bool {
	if !ValidHash(hash) {
		return false
	}
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM artifact WHERE hash = ?`, hash).Scan(&n)
	return n > 0
}

// ArtifactSize reports a stored artifact's byte count, for the row label.
func (s *Store) ArtifactSize(hash string) int {
	var n int
	_ = s.db.QueryRow(`SELECT size FROM artifact WHERE hash = ?`, hash).Scan(&n)
	return n
}

// PurgeArtifact drops a blob permanently. Callers reach it through Purge, so a
// redaction takes the event's attachments with its body: a secret pasted into a
// report must not outlive the redaction that erased the message.
func (s *Store) PurgeArtifact(hash string) error {
	_, err := s.db.Exec(`DELETE FROM artifact WHERE hash = ?`, hash)
	return err
}

// Progress is the current state of one working actor, folded from its status
// events rather than replayed from them.
type Progress struct {
	Author  string
	Step    int
	Of      int
	Note    string
	Updated time.Time
}

// StallWindow is the one definition of "quiet long enough to be worth saying".
// It lives here, beside the projection it is evaluated against, so the room
// brief and the rendered ledger cannot disagree about who is stalled.
const StallWindow = 15 * time.Minute

// Stalled reports whether this actor has gone quiet past the given window. It
// is evaluated against the server clock, never a client timestamp.
func (p Progress) Stalled(now time.Time, window time.Duration) bool {
	return now.Sub(p.Updated) > window
}

// RecordProgress folds one status event into the projection.
func (s *Store) RecordProgress(room, author string, step, of int, note string, now time.Time) error {
	_, err := s.db.Exec(
		`INSERT INTO progress(room, author, step, of, note, server_ts) VALUES(?,?,?,?,?,?)
		 ON CONFLICT(room, author) DO UPDATE SET
		   step = excluded.step, of = excluded.of,
		   note = excluded.note, server_ts = excluded.server_ts`,
		room, author, step, of, note, now.UTC().Format(time.RFC3339Nano))
	return err
}

// ProgressFor returns every actor with recorded progress in a room, oldest
// update first so the stalest is visible.
func (s *Store) ProgressFor(room string) ([]Progress, error) {
	rows, err := s.db.Query(
		`SELECT author, step, of, note, server_ts FROM progress
		 WHERE room = ? ORDER BY server_ts`, room)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Progress
	for rows.Next() {
		var p Progress
		var ts string
		if err := rows.Scan(&p.Author, &p.Step, &p.Of, &p.Note, &ts); err != nil {
			return nil, err
		}
		p.Updated, _ = time.Parse(time.RFC3339Nano, ts)
		out = append(out, p)
	}
	return out, rows.Err()
}

// Step and Of expose a status event's progress fields for rendering.
func (r Record) Step() int { return intField(r.Body, "step") }
func (r Record) Of() int   { return intField(r.Body, "of") }

func intField(body map[string]any, key string) int {
	if body == nil {
		return 0
	}
	switch v := body[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

// ApplyRedaction folds a redact event into the projection and drops the target
// from search in the same transaction. Suppression that left the text findable
// would be worse than no redaction at all, because the room implies it worked.
func (s *Store) ApplyRedaction(targetSeq, bySeq int64, byActor string, now time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`INSERT INTO redacted(seq, by_actor, by_seq, server_ts) VALUES(?,?,?,?)
		 ON CONFLICT(seq) DO NOTHING`,
		targetSeq, byActor, bySeq, now.UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	// The embedding dies with the body, in the same transaction. It is derived
	// from the secret; an embedding that outlives a redaction is the secret in
	// a form nobody thinks to look at. This is ticket 08's remaining criterion,
	// satisfied here because the transaction that suppresses is the only place
	// it can be satisfied.
	if _, err := tx.Exec(`DELETE FROM vector WHERE seq = ?`, targetSeq); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM search WHERE seq = ?`, targetSeq); err != nil {
		return err
	}

	// Attachments die with the body, in the same transaction. A secret pasted
	// into an attached stack trace is the same secret; leaving the blob served
	// while blanking the row hides the leak instead of closing it.
	var attach string
	if err := tx.QueryRow(`SELECT attach FROM envelope WHERE seq = ?`, targetSeq).Scan(&attach); err == nil {
		var atts []core.Attachment
		_ = json.Unmarshal([]byte(attach), &atts)
		for _, a := range atts {
			// Only drop a blob no surviving event still references.
			var others int
			_ = tx.QueryRow(
				`SELECT COUNT(*) FROM envelope e
				 WHERE e.seq != ? AND e.attach LIKE ?
				   AND e.seq NOT IN (SELECT seq FROM redacted)`,
				targetSeq, "%"+a.Hash+"%").Scan(&others)
			if others == 0 {
				if _, err := tx.Exec(`DELETE FROM artifact WHERE hash = ?`, a.Hash); err != nil {
					return err
				}
			}
		}
	}

	return tx.Commit()
}

// IsRedacted backs both the renderer and the decider.
func (s *Store) IsRedacted(seq int64) bool {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM redacted WHERE seq = ?`, seq).Scan(&n)
	return n > 0
}

// Redaction reports who suppressed an event and when.
func (s *Store) Redaction(seq int64) (actor string, at time.Time, ok bool) {
	var ts string
	err := s.db.QueryRow(`SELECT by_actor, server_ts FROM redacted WHERE seq = ?`, seq).
		Scan(&actor, &ts)
	if err != nil {
		return "", time.Time{}, false
	}
	at, _ = time.Parse(time.RFC3339Nano, ts)
	return actor, at, true
}
