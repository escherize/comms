package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/escherize/comms/core"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.EnsureRoom("core"); err != nil {
		t.Fatalf("ensure room: %v", err)
	}
	return s
}

func ev(kind core.Kind, author core.Actor, text string) core.Event {
	return core.Event{
		Room: "core", Author: author, Kind: kind,
		Body: map[string]any{"text": text}, Lane: core.LaneOf(kind),
	}
}

var t0 = time.Date(2026, 8, 6, 14, 0, 0, 0, time.UTC)

func TestAppendAssignsIncreasingSeq(t *testing.T) {
	s := newStore(t)

	a, err := s.Append(ev(core.KindChat, "human:bcm", "one"), "i1", t0)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	b, err := s.Append(ev(core.KindChat, "human:bcm", "two"), "i2", t0)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if b <= a {
		t.Errorf("seq must increase: got %d then %d", a, b)
	}
}

// A timed-out-but-succeeded retry must return the original outcome, not create a
// second event. This is the everyday failure the whole idempotency rule exists
// to prevent.
func TestIdempotencyKeyReturnsOriginalSeq(t *testing.T) {
	s := newStore(t)

	first, err := s.Append(ev(core.KindChat, "human:bcm", "hello"), "same-key", t0)
	if err != nil {
		t.Fatalf("first append: %v", err)
	}

	second, err := s.Append(ev(core.KindChat, "human:bcm", "hello"), "same-key", t0)
	var dup ErrDuplicate
	if !errors.As(err, &dup) {
		t.Fatalf("expected ErrDuplicate, got %v", err)
	}
	if second != first || dup.Seq != first {
		t.Errorf("retry must resolve to the original seq %d, got %d", first, second)
	}

	got, err := s.Since("core", 0, 100)
	if err != nil {
		t.Fatalf("since: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("retry created a duplicate event: %d events in the log", len(got))
	}
}

// ADR-0002: a restore can lose the tail, and a resumed counter would reissue a
// value still live elsewhere as a fencing token.
func TestSeqJumpsForwardOnReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jump.db")

	s1, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s1.EnsureRoom("core"); err != nil {
		t.Fatal(err)
	}
	before, err := s1.Append(ev(core.KindChat, "human:bcm", "x"), "i1", t0)
	if err != nil {
		t.Fatal(err)
	}
	s1.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	after, err := s2.Append(ev(core.KindChat, "human:bcm", "y"), "i2", t0)
	if err != nil {
		t.Fatal(err)
	}

	if gap := after - before; gap < SeqJump {
		t.Errorf("seq must jump at least %d on restart, jumped %d", SeqJump, gap)
	}
}

// The triggers are the reason the DB can stay inspectable with sqlite3 without
// the integrity story being a convention.
func TestEnvelopeIsAppendOnly(t *testing.T) {
	s := newStore(t)
	seq, err := s.Append(ev(core.KindChat, "human:bcm", "immutable"), "i1", t0)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.db.Exec(`UPDATE envelope SET author = 'mallory' WHERE seq = ?`, seq); err == nil {
		t.Error("UPDATE on envelope must be refused by trigger")
	}
	if _, err := s.db.Exec(`DELETE FROM envelope WHERE seq = ?`, seq); err == nil {
		t.Error("DELETE on envelope must be refused by trigger")
	}
}

// ADR-0003: purge drops the blob only. The chain must still verify, and the
// record must still attest that a body with that hash was here.
func TestPurgeErasesBodyButChainStillVerifies(t *testing.T) {
	s := newStore(t)

	if _, err := s.Append(ev(core.KindChat, "human:bcm", "before"), "i1", t0); err != nil {
		t.Fatal(err)
	}
	secret, err := s.Append(ev(core.KindChat, "human:bcm", "sk-live-DO-NOT-LEAK"), "i2", t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(ev(core.KindChat, "human:bcm", "after"), "i3", t0); err != nil {
		t.Fatal(err)
	}

	if err := s.Verify(); err != nil {
		t.Fatalf("chain must verify before purge: %v", err)
	}

	before, _ := s.Since("core", 0, 100)
	var hashBefore string
	for _, r := range before {
		if r.Seq == secret {
			hashBefore = r.BodyHash
		}
	}

	if err := s.Purge(secret); err != nil {
		t.Fatalf("purge: %v", err)
	}

	if err := s.Verify(); err != nil {
		t.Fatalf("chain must still verify after purge: %v", err)
	}

	after, _ := s.Since("core", 0, 100)
	if len(after) != 3 {
		t.Fatalf("purge must not remove the event, got %d records", len(after))
	}
	for _, r := range after {
		if r.Seq != secret {
			continue
		}
		if !r.BodyErased {
			t.Error("purged record must report its body erased")
		}
		if r.Text() != "" {
			t.Errorf("purged body must be gone, got %q", r.Text())
		}
		if r.BodyHash != hashBefore {
			t.Error("purge must preserve body_hash so erasure stays attested")
		}
	}
}

// The purged secret must not survive in the derived search index either.
func TestPurgeRemovesFromSearch(t *testing.T) {
	s := newStore(t)
	seq, err := s.Append(ev(core.KindChat, "human:bcm", "hunter2 password leak"), "i1", t0)
	if err != nil {
		t.Fatal(err)
	}

	hits, err := s.Search("hunter2", "", "", "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected the event to be searchable, got %d hits", len(hits))
	}

	if err := s.Purge(seq); err != nil {
		t.Fatal(err)
	}
	hits, err = s.Search("hunter2", "", "", "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("purged text must not survive in search, got %d hits", len(hits))
	}
}

// Read-your-writes for the lexical lane: there is never a "wait for the index"
// step.
func TestEventIsSearchableImmediately(t *testing.T) {
	s := newStore(t)
	if _, err := s.Append(ev(core.KindFinding, "agent:claude-1", "nil deref on retry"), "i1", t0); err != nil {
		t.Fatal(err)
	}

	hits, err := s.Search("deref", "", "", "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("event must be findable the moment it is posted, got %d hits", len(hits))
	}
	if hits[0].Text() != "nil deref on retry" {
		t.Errorf("unexpected hit: %q", hits[0].Text())
	}
}

func TestSearchFilters(t *testing.T) {
	s := newStore(t)
	if err := s.EnsureRoom("bash"); err != nil {
		t.Fatal(err)
	}

	mustAppend(t, s, core.Event{Room: "core", Author: "human:bcm", Kind: core.KindChat,
		Body: map[string]any{"text": "migration order"}}, "i1")
	mustAppend(t, s, core.Event{Room: "bash", Author: "agent:claude-1", Kind: core.KindFinding,
		Body: map[string]any{"text": "migration order", "severity": "p1"}}, "i2")

	all, _ := s.Search("migration", "", "", "", "", 10)
	if len(all) != 2 {
		t.Fatalf("unfiltered: expected 2, got %d", len(all))
	}

	byRoom, _ := s.Search("migration", "bash", "", "", "", 10)
	if len(byRoom) != 1 || byRoom[0].Room != "bash" {
		t.Errorf("room filter failed: %+v", byRoom)
	}

	byKind, _ := s.Search("migration", "", "finding", "", "", 10)
	if len(byKind) != 1 || byKind[0].Kind != core.KindFinding {
		t.Errorf("kind filter failed: %+v", byKind)
	}

	byAuthor, _ := s.Search("migration", "", "", "human:bcm", "", 10)
	if len(byAuthor) != 1 || byAuthor[0].Author != "human:bcm" {
		t.Errorf("author filter failed: %+v", byAuthor)
	}
}

// SSE resume: a reconnecting client asks for everything after the last seq it
// saw and must get no gaps and no duplicates.
func TestSinceResumesWithoutGapOrDuplicate(t *testing.T) {
	s := newStore(t)
	var seqs []int64
	for i, text := range []string{"a", "b", "c", "d"} {
		seq := mustAppend(t, s, ev(core.KindChat, "human:bcm", text), string(rune('a'+i)))
		seqs = append(seqs, seq)
	}

	tail, err := s.Since("core", seqs[1], 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 2 {
		t.Fatalf("expected 2 records after seq %d, got %d", seqs[1], len(tail))
	}
	if tail[0].Seq != seqs[2] || tail[1].Seq != seqs[3] {
		t.Errorf("resume returned wrong records: %d, %d", tail[0].Seq, tail[1].Seq)
	}
}

func TestSinceIsRoomScoped(t *testing.T) {
	s := newStore(t)
	if err := s.EnsureRoom("other"); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, s, ev(core.KindChat, "human:bcm", "in core"), "i1")
	mustAppend(t, s, core.Event{Room: "other", Author: "human:bcm", Kind: core.KindChat,
		Body: map[string]any{"text": "in other"}}, "i2")

	got, _ := s.Since("core", 0, 100)
	if len(got) != 1 || got[0].Room != "core" {
		t.Errorf("Since must be room-scoped, got %d records", len(got))
	}
}

func TestRoomProjection(t *testing.T) {
	s := newStore(t)
	if s.RoomExists("nope") {
		t.Error("unknown room must not exist")
	}
	if !s.RoomExists("core") {
		t.Error("created room must exist")
	}
	if err := s.EnsureRoom("core"); err != nil {
		t.Errorf("EnsureRoom must be idempotent: %v", err)
	}
	rooms, _ := s.Rooms()
	if len(rooms) != 1 {
		t.Errorf("expected 1 room, got %v", rooms)
	}
}

func TestEventKindLookup(t *testing.T) {
	s := newStore(t)
	seq := mustAppend(t, s, core.Event{Room: "core", Author: "agent:c1", Kind: core.KindQuestion,
		Body: map[string]any{"text": "?"}, Recipient: "human:bcm", Lane: core.Addressed}, "i1")

	k, ok := s.EventKind(itoa(seq))
	if !ok || k != core.KindQuestion {
		t.Errorf("EventKind(%d) = %v, %v; want question, true", seq, k, ok)
	}
	if _, ok := s.EventKind("999999"); ok {
		t.Error("unknown ref must report not found")
	}
}

// Verify must tolerate the intentional seq gap a restart introduces.
func TestVerifyToleratesSeqGaps(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gap.db")

	s1, _ := Open(path)
	s1.EnsureRoom("core")
	mustAppend(t, s1, ev(core.KindChat, "human:bcm", "before restart"), "i1")
	s1.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	mustAppend(t, s2, ev(core.KindChat, "human:bcm", "after restart"), "i2")

	if err := s2.Verify(); err != nil {
		t.Errorf("chain must verify across a restart gap: %v", err)
	}
}

func TestVerifyDetectsTampering(t *testing.T) {
	s := newStore(t)
	mustAppend(t, s, ev(core.KindChat, "human:bcm", "one"), "i1")
	mustAppend(t, s, ev(core.KindChat, "human:bcm", "two"), "i2")

	if err := s.Verify(); err != nil {
		t.Fatalf("clean chain must verify: %v", err)
	}

	// Triggers block UPDATE, so tamper the way an attacker with file access
	// would have to: drop the triggers first. The chain must still notice.
	if _, err := s.db.Exec(`DROP TRIGGER envelope_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE envelope SET prev_hash = 'forged' WHERE seq = (SELECT MAX(seq) FROM envelope)`); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(); err == nil {
		t.Error("Verify must detect a forged prev_hash")
	}
}

func mustAppend(t *testing.T, s *Store, e core.Event, idem string) int64 {
	t.Helper()
	if e.Lane == 0 && e.Kind != "" {
		e.Lane = core.LaneOf(e.Kind)
	}
	seq, err := s.Append(e, idem, t0)
	if err != nil {
		t.Fatalf("append %s: %v", idem, err)
	}
	return seq
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// The since: filter — the one a bug-bash room needs most, to cut a search to
// today's hunt. RFC3339 sorts lexically, so a plain comparison is correct.
func TestSearchSinceFilter(t *testing.T) {
	s := newStore(t)
	old := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)

	if _, err := s.Append(ev(core.KindFinding, "human:bcm", "migration order old"), "s1", old); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(ev(core.KindFinding, "human:bcm", "migration order new"), "s2", recent); err != nil {
		t.Fatal(err)
	}

	all, _ := s.Search("migration", "", "", "", "", 10)
	if len(all) != 2 {
		t.Fatalf("unfiltered: want 2, got %d", len(all))
	}

	sinceRecent, _ := s.Search("migration", "", "", "", "2026-08-05", 10)
	if len(sinceRecent) != 1 {
		t.Fatalf("since: want 1 hit after 2026-08-05, got %d", len(sinceRecent))
	}
	if sinceRecent[0].Text() != "migration order new" {
		t.Errorf("since: returned the wrong record: %q", sinceRecent[0].Text())
	}

	// A full timestamp works too, and composes with the other filters.
	composed, _ := s.Search("migration", "core", "finding", "human:bcm", "2026-08-05T00:00:00Z", 10)
	if len(composed) != 1 {
		t.Errorf("filters must compose: want 1, got %d", len(composed))
	}
}

// A redacted body must not survive in search, the same as a purged one.
func TestRedactedBodyLeavesSearch(t *testing.T) {
	s := newStore(t)
	seq := mustAppend(t, s, ev(core.KindChat, "human:bcm", "hunter2 secret"), "r1")

	if hits, _ := s.Search("hunter2", "", "", "", "", 10); len(hits) != 1 {
		t.Fatal("setup: should be searchable before redaction")
	}
	if err := s.ApplyRedaction(seq, seq+1, "human:bcm", t0); err != nil {
		t.Fatal(err)
	}
	if hits, _ := s.Search("hunter2", "", "", "", "", 10); len(hits) != 0 {
		t.Errorf("a redacted body must not survive in search, got %d hits", len(hits))
	}

	recs, _ := s.Since("core", 0, 10)
	if len(recs) != 1 {
		t.Fatalf("redaction must not remove the event, got %d", len(recs))
	}
	if !recs[0].Redacted || recs[0].Text() != "" {
		t.Error("a redacted record must report itself redacted with no body")
	}
	if recs[0].RedactedBy != "human:bcm" {
		t.Errorf("redaction must record who did it, got %q", recs[0].RedactedBy)
	}
}

// The same key with different content is a conflict, not a duplicate. Answering
// it as a duplicate returned the first post's seq and silently discarded the
// second — data loss reported as success.
func TestIdemReuseWithDifferentContentConflicts(t *testing.T) {
	s := newStore(t)

	first, err := s.Append(ev(core.KindChat, "human:bcm", "ORIGINAL"), "k", t0)
	if err != nil {
		t.Fatal(err)
	}

	// Genuine retry: identical content, same key. Still a duplicate.
	if _, err := s.Append(ev(core.KindChat, "human:bcm", "ORIGINAL"), "k", t0); err == nil {
		t.Error("an identical retry should report ErrDuplicate")
	} else {
		var dup ErrDuplicate
		if !errors.As(err, &dup) {
			t.Errorf("identical retry should be ErrDuplicate, got %T", err)
		}
	}

	// Different content, same key: must not be silently swallowed.
	_, err = s.Append(ev(core.KindChat, "human:bcm", "COMPLETELY DIFFERENT"), "k", t0)
	var conflict ErrIdemConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("reuse with different content must conflict, got %v", err)
	}
	if conflict.Seq != first {
		t.Errorf("the conflict should name the seq that holds the key, got %d", conflict.Seq)
	}
}

// ANDing every token meant one absent word returned zero hits against a room
// that held the answer, so an agent searching before posting concluded nobody
// knew and posted a duplicate.
func TestSearchDoesNotRequireEveryToken(t *testing.T) {
	s := newStore(t)
	mustAppend(t, s, ev(core.KindTIL, "agent:c1", "sqlite-vec rejects long bodies"), "q1")

	// Check the error: discarding it turns a SQL failure into "no hits", which
	// is the exact confusion this ticket exists to remove.
	exact, err := s.Search("sqlite-vec rejects long bodies", "", "", "", "", 10)
	if err != nil {
		t.Fatalf("search errored: %v", err)
	}
	if len(exact) != 1 {
		t.Fatalf("the exact phrase must match, got %d", len(exact))
	}

	// One word the room does not contain must not zero the result.
	loose, err := s.Search("sqlite-vec long bodies missing", "", "", "", "", 10)
	if err != nil {
		t.Fatalf("search errored: %v", err)
	}
	if len(loose) != 1 {
		t.Errorf("a query with one absent word must still find the record, got %d hits", len(loose))
	}
}

// A database created before a column existed must still open. CREATE TABLE IF
// NOT EXISTS does nothing to a table that already exists, so every column added
// after the first release is invisible to every database already in the field —
// and the only symptom is "no such column" at query time, from whichever query
// happens to run first.
//
// The suite otherwise only ever creates new databases, which is exactly why
// this went unnoticed until a live hub hit it.
func TestADatabaseFromAnEarlierSchemaStillOpens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")

	// Build the shape the schema had before progress.seq and invite.expires.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`
		CREATE TABLE progress (
		  room TEXT NOT NULL, author TEXT NOT NULL,
		  step INTEGER NOT NULL DEFAULT 0, of INTEGER NOT NULL DEFAULT 0,
		  note TEXT NOT NULL DEFAULT '', server_ts TEXT NOT NULL,
		  PRIMARY KEY (room, author));
		CREATE TABLE invite (
		  token TEXT PRIMARY KEY, actor TEXT NOT NULL,
		  created TEXT NOT NULL, used_at TEXT NOT NULL DEFAULT '');
		INSERT INTO progress(room, author, step, of, note, server_ts)
		  VALUES('core','agent:old',2,5,'mid-migration','2026-08-01T00:00:00Z');
	`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("an existing database must open after a column is added: %v", err)
	}
	defer s.Close()

	// The added columns must be usable, not merely present.
	if err := s.EnsureRoom("core"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(core.Event{Room: "core", Author: "agent:old",
		Kind: core.KindStatus, Body: map[string]any{"text": "resumed", "step": float64(3), "of": float64(5)},
		Lane: core.LaneOf(core.KindStatus)}, "mig1", time.Now()); err != nil {
		t.Fatalf("append after migration: %v", err)
	}
	if _, err := s.MintInvite("agent:old", ScopeAll, time.Now()); err != nil {
		t.Fatalf("invite after migration: %v", err)
	}

	// And the data that was already there survives.
	rows, err := s.ProgressFor("core")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Step != 3 {
		t.Errorf("the migrated row should have folded forward, got %+v", rows)
	}
}
