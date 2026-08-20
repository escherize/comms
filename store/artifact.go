package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"

	"errors"
	"github.com/escherize/comms/core"
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

// artifact_ref maps an artifact hash to the events (and their rooms) that
// reference it. An artifact is content-addressed and cross-room, so who may
// read /a/<hash> is decided by membership in a room that references it — a raw
// hash must not be a bypass around room scoping. This index makes that check an
// indexed lookup rather than a LIKE scan over every envelope's attach column.
// It is a derived index (a fold over envelope.attach), so -rebuild may drop and
// refill it, unlike keys/capabilities/membership which are records.
const artifactRefSchema = `
CREATE TABLE IF NOT EXISTS artifact_ref (
  hash TEXT NOT NULL,
  seq  INTEGER NOT NULL,
  room TEXT NOT NULL,
  PRIMARY KEY (hash, seq)
);
CREATE INDEX IF NOT EXISTS artifact_ref_hash ON artifact_ref(hash);
`

// addArtifactRef records that event seq in room references hash. Called inside
// the Append transaction, once per attachment.
func addArtifactRef(tx *sql.Tx, hash string, seq int64, room string) error {
	_, err := tx.Exec(
		`INSERT INTO artifact_ref(hash, seq, room) VALUES(?,?,?)
		 ON CONFLICT(hash, seq) DO NOTHING`,
		hash, seq, room)
	return err
}

// backfillArtifactRefs folds existing envelope.attach rows into artifact_ref,
// once. It runs against the raw *sql.DB at Open, before the Store exists, so it
// only inserts what is not already indexed — a hub already migrated re-runs it
// as a no-op. It also feeds -rebuild's index refill.
func backfillArtifactRefs(db *sql.DB) error {
	// Only rows the index does not already cover, so a second run does nothing.
	rows, err := db.Query(
		`SELECT seq, room, attach FROM envelope
		 WHERE attach != '[]' AND attach != ''
		   AND seq NOT IN (SELECT seq FROM artifact_ref)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type ref struct {
		seq  int64
		room string
		hash string
	}
	var refs []ref
	for rows.Next() {
		var seq int64
		var room, attach string
		if err := rows.Scan(&seq, &room, &attach); err != nil {
			return err
		}
		var atts []core.Attachment
		if json.Unmarshal([]byte(attach), &atts) != nil {
			continue
		}
		for _, a := range atts {
			refs = append(refs, ref{seq, room, a.Hash})
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range refs {
		if _, err := db.Exec(
			`INSERT INTO artifact_ref(hash, seq, room) VALUES(?,?,?)
			 ON CONFLICT(hash, seq) DO NOTHING`, r.hash, r.seq, r.room); err != nil {
			return err
		}
	}
	return nil
}

// ArtifactRooms returns the rooms of the non-redacted events that reference a
// hash. A reader may fetch the artifact iff it is a member of one of these; an
// empty result means no live event references it, so it is served to nobody.
func (s *Store) ArtifactRooms(hash string) []string {
	rows, err := s.db.Query(
		`SELECT DISTINCT room FROM artifact_ref
		 WHERE hash = ? AND seq NOT IN (SELECT seq FROM redacted)`, hash)
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
//
// Append calls the tx form inline, so the redact event and its suppression
// commit atomically — a crash can never leave a committed redact event whose
// target is still served. This wrapper remains for callers holding no tx.
func (s *Store) ApplyRedaction(targetSeq, bySeq int64, byActor string, now time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := applyRedactionTx(tx, targetSeq, bySeq, byActor, now); err != nil {
		return err
	}
	return tx.Commit()
}

func applyRedactionTx(tx *sql.Tx, targetSeq, bySeq int64, byActor string, now time.Time) error {
	if _, err := tx.Exec(
		`INSERT INTO redacted(seq, by_actor, by_seq, server_ts) VALUES(?,?,?,?)
		 ON CONFLICT(seq) DO NOTHING`,
		targetSeq, byActor, bySeq, now.UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM search WHERE seq = ?`, targetSeq); err != nil {
		return err
	}
	// The artifact reference dies with the body too: a redacted event must stop
	// granting /a/<hash> access. ArtifactRooms already excludes redacted seqs, so
	// this is belt-and-suspenders — but it also keeps the index from growing
	// stale rows a purge would otherwise leave behind.
	if _, err := tx.Exec(`DELETE FROM artifact_ref WHERE seq = ?`, targetSeq); err != nil {
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

	return nil
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
