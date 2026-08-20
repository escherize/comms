package store

import (
	"database/sql"
	"fmt"
	"strconv"
	"time"

	"github.com/escherize/comms/core"
)

// Rebuild recomputes every log-derived projection by folding the log again.
//
// Ticket 10 asked for snapshots so startup could replay from a checkpoint
// rather than from the beginning. There is nothing to replay: the projections
// here are rows in the same file as the log, written in the same transaction as
// the append, so a restart already starts with them. That is what ARCHITECTURE
// means by a decision projection being read-your-writes — the cost of it is
// paid on write, not on boot.
//
// What that design does need, and did not have, is a way to check the claim.
// "Projections are pure folds over the log" is asserted in three documents and
// enforced nowhere; an incremental update that drifts from the fold it claims to
// be is invisible until somebody queries the wrong number. Rebuild is that
// check, and it is also the recovery tool: a corrupted index, a schema change,
// or a restore that lands mid-transaction are all repaired by folding again.
//
// It touches only what the log determines. Keys, invites and capabilities come
// from operator acts that are not events, so rebuilding them would erase them —
// which is the difference between a projection and a record.
func (s *Store) Rebuild() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, stmt := range []string{
		`DELETE FROM search`,
		`DELETE FROM progress`,
		`DELETE FROM question`,
		`DELETE FROM artifact_ref`,
		`DELETE FROM actor WHERE source = 'post'`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("clearing projections: %w", err)
		}
	}

	rows, err := tx.Query(`
		SELECT e.seq, e.server_ts, e.room, e.author, e.kind, e.recipient, e.lane,
		       e.refs, e.body_hash, e.prev_hash, b.json, e.attach
		FROM envelope e LEFT JOIN body b ON b.seq = e.seq
		ORDER BY e.seq`)
	if err != nil {
		return err
	}
	recs, err := scanRecords(rows)
	rows.Close()
	if err != nil {
		return err
	}
	// Which bodies are suppressed, read inside the transaction. Without this
	// the fold sees every body as present and puts redacted text back into the
	// index — the suppression undone by the tool that exists to repair things.
	//
	// It must be this transaction and not s.db: the pool is one connection by
	// design (single writer, and the whole ordering story depends on it), so a
	// query issued on the store while a transaction is open waits for a
	// connection the transaction is holding, and the rebuild deadlocks against
	// itself.
	suppressed := map[int64]bool{}
	rrows, err := tx.Query(`SELECT seq FROM redacted`)
	if err != nil {
		return err
	}
	for rrows.Next() {
		var seq int64
		if err := rrows.Scan(&seq); err != nil {
			rrows.Close()
			return err
		}
		suppressed[seq] = true
	}
	rrows.Close()
	if err := rrows.Err(); err != nil {
		return err
	}
	for i := range recs {
		if suppressed[recs[i].Seq] {
			recs[i].Redacted = true
		}
	}

	for _, rec := range recs {
		if err := foldInto(tx, rec); err != nil {
			return fmt.Errorf("folding %d: %w", rec.Seq, err)
		}
	}
	return tx.Commit()
}

// foldInto applies one event to the projections. Append does the same work
// inline; this is the same fold expressed once more, and the property test
// exists precisely because two expressions of one rule drift.
func foldInto(tx *sql.Tx, rec Record) error {
	ts := rec.ServerTS.UTC().Format(time.RFC3339Nano)

	if _, err := tx.Exec(
		`INSERT INTO actor(actor, first_seen, source) VALUES(?,?,'post')
		 ON CONFLICT(actor) DO NOTHING`, string(rec.Author), ts); err != nil {
		return err
	}

	// The search index, including attachment text, exactly as Append builds it.
	indexed := rec.Text()
	if about := rec.About(); about != "" {
		indexed += "\n" + about
	}
	for _, a := range rec.Attach {
		indexed += "\n" + a.Title
		var blob []byte
		if err := tx.QueryRow(`SELECT bytes FROM artifact WHERE hash = ?`, a.Hash).
			Scan(&blob); err == nil {
			indexed += "\n" + string(blob)
		}
	}
	// A redacted body is absent from the index. Rebuilding it back in would be
	// the suppression undone by the repair.
	if !rec.Redacted && !rec.BodyErased {
		if _, err := tx.Exec(
			`INSERT INTO search(text, author, kind, room, seq) VALUES(?,?,?,?,?)`,
			indexed, string(rec.Author), string(rec.Kind), rec.Room, rec.Seq); err != nil {
			return err
		}
	}

	// The artifact access index, exactly as Append writes it — its own doc
	// promises "-rebuild may drop and refill it", and until this line the
	// refill half was missing. Skipped for a purged event: purge dropped its
	// refs, and re-granting a room access through an erased event would undo
	// the erasure. A redacted target's rows are deleted again when its redact
	// event folds, same as the incremental path.
	if !rec.BodyErased {
		for _, a := range rec.Attach {
			if err := addArtifactRef(tx, a.Hash, rec.Seq, rec.Room); err != nil {
				return err
			}
		}
	}

	// Progress, exactly as Append folds it: the trigger is body-key presence,
	// not value — an explicit step:0 folds, and each absent field individually
	// carries its prior value forward. The two folds diverging here is exactly
	// what the rebuild-equivalence property test exists to catch.
	stepF, hasStep := rec.Body["step"].(float64)
	ofF, hasOf := rec.Body["of"].(float64)
	if rec.Kind == core.Kind("status") || hasStep || hasOf {
		step, of := int(stepF), int(ofF)
		if !hasStep || !hasOf {
			var priorStep, priorOf int
			if err := tx.QueryRow(
				`SELECT step, of FROM progress WHERE room = ? AND author = ?`,
				rec.Room, string(rec.Author)).Scan(&priorStep, &priorOf); err == nil {
				if !hasStep {
					step = priorStep
				}
				if !hasOf {
					of = priorOf
				}
			}
		}
		if _, err := tx.Exec(
			`INSERT INTO progress(room, author, step, of, note, server_ts, seq)
			 VALUES(?,?,?,?,?,?,?)
			 ON CONFLICT(room, author) DO UPDATE SET
			   step = CASE WHEN excluded.of = progress.of AND excluded.step < progress.step
			                THEN progress.step ELSE excluded.step END,
			   of = excluded.of, note = excluded.note,
			   server_ts = excluded.server_ts, seq = excluded.seq`,
			rec.Room, string(rec.Author), step, of, rec.Text(), ts, rec.Seq); err != nil {
			return err
		}
	}

	if rec.Kind == core.KindRedact {
		// Redaction is re-derived from the log, not merely preserved: a
		// redacted row lost to a crash or a restore comes back on rebuild.
		// The fold runs in seq order, so the target's search row (re-inserted
		// when the target was folded) is deleted again here.
		if len(rec.Refs) == 1 {
			if target, convErr := strconv.ParseInt(rec.Refs[0], 10, 64); convErr == nil {
				if err := applyRedactionTx(tx, target, rec.Seq, string(rec.Author), rec.ServerTS); err != nil {
					return err
				}
			}
		}
		return nil
	}

	// The waiting-on-a-person fold, exactly as Append does it: a routed reply
	// closes, a human-addressed post that closed nothing opens, and legacy
	// `question` events open regardless of namespace.
	var closedARef bool
	if rec.Recipient != "" {
		for _, ref := range rec.Refs {
			res, err := tx.Exec(
				`UPDATE question SET answer_seq = ?, answered_at = ?
				 WHERE seq = ? AND answer_seq = 0 AND author = ?`,
				rec.Seq, ts, ref, string(rec.Recipient))
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				closedARef = true
			}
		}
	}
	if rec.Kind == core.Kind("question") ||
		(rec.Kind == core.KindChat && !closedARef && rec.Recipient != "" && !rec.Recipient.IsAgent() &&
			!replyIntoExchange(tx, rec.Room, rec.Refs, string(rec.Author), string(rec.Recipient))) {
		if _, err := tx.Exec(
			`INSERT INTO question(seq, room, author, recipient, asked_at)
			 VALUES(?,?,?,?,?) ON CONFLICT(seq) DO NOTHING`,
			rec.Seq, rec.Room, string(rec.Author), string(rec.Recipient), ts); err != nil {
			return err
		}
	}
	return nil
}
