package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/bcm/agent_comms/core"
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

	a, err := s.Append(ev(core.KindChat, "bcm", "one"), "i1", t0)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	b, err := s.Append(ev(core.KindChat, "bcm", "two"), "i2", t0)
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

	first, err := s.Append(ev(core.KindChat, "bcm", "hello"), "same-key", t0)
	if err != nil {
		t.Fatalf("first append: %v", err)
	}

	second, err := s.Append(ev(core.KindChat, "bcm", "hello"), "same-key", t0)
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
	before, err := s1.Append(ev(core.KindChat, "bcm", "x"), "i1", t0)
	if err != nil {
		t.Fatal(err)
	}
	s1.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	after, err := s2.Append(ev(core.KindChat, "bcm", "y"), "i2", t0)
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
	seq, err := s.Append(ev(core.KindChat, "bcm", "immutable"), "i1", t0)
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

	if _, err := s.Append(ev(core.KindChat, "bcm", "before"), "i1", t0); err != nil {
		t.Fatal(err)
	}
	secret, err := s.Append(ev(core.KindChat, "bcm", "sk-live-DO-NOT-LEAK"), "i2", t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(ev(core.KindChat, "bcm", "after"), "i3", t0); err != nil {
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
	seq, err := s.Append(ev(core.KindChat, "bcm", "hunter2 password leak"), "i1", t0)
	if err != nil {
		t.Fatal(err)
	}

	hits, err := s.Search("hunter2", "", "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected the event to be searchable, got %d hits", len(hits))
	}

	if err := s.Purge(seq); err != nil {
		t.Fatal(err)
	}
	hits, err = s.Search("hunter2", "", "", "", 10)
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

	hits, err := s.Search("deref", "", "", "", 10)
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

	mustAppend(t, s, core.Event{Room: "core", Author: "bcm", Kind: core.KindChat,
		Body: map[string]any{"text": "migration order"}}, "i1")
	mustAppend(t, s, core.Event{Room: "bash", Author: "agent:claude-1", Kind: core.KindFinding,
		Body: map[string]any{"text": "migration order", "severity": "p1"}}, "i2")

	all, _ := s.Search("migration", "", "", "", 10)
	if len(all) != 2 {
		t.Fatalf("unfiltered: expected 2, got %d", len(all))
	}

	byRoom, _ := s.Search("migration", "bash", "", "", 10)
	if len(byRoom) != 1 || byRoom[0].Room != "bash" {
		t.Errorf("room filter failed: %+v", byRoom)
	}

	byKind, _ := s.Search("migration", "", "finding", "", 10)
	if len(byKind) != 1 || byKind[0].Kind != core.KindFinding {
		t.Errorf("kind filter failed: %+v", byKind)
	}

	byAuthor, _ := s.Search("migration", "", "", "bcm", 10)
	if len(byAuthor) != 1 || byAuthor[0].Author != "bcm" {
		t.Errorf("author filter failed: %+v", byAuthor)
	}
}

// SSE resume: a reconnecting client asks for everything after the last seq it
// saw and must get no gaps and no duplicates.
func TestSinceResumesWithoutGapOrDuplicate(t *testing.T) {
	s := newStore(t)
	var seqs []int64
	for i, text := range []string{"a", "b", "c", "d"} {
		seq := mustAppend(t, s, ev(core.KindChat, "bcm", text), string(rune('a'+i)))
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
	mustAppend(t, s, ev(core.KindChat, "bcm", "in core"), "i1")
	mustAppend(t, s, core.Event{Room: "other", Author: "bcm", Kind: core.KindChat,
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
		Body: map[string]any{"text": "?"}, Recipient: "bcm", Lane: core.Addressed}, "i1")

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
	mustAppend(t, s1, ev(core.KindChat, "bcm", "before restart"), "i1")
	s1.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	mustAppend(t, s2, ev(core.KindChat, "bcm", "after restart"), "i2")

	if err := s2.Verify(); err != nil {
		t.Errorf("chain must verify across a restart gap: %v", err)
	}
}

func TestVerifyDetectsTampering(t *testing.T) {
	s := newStore(t)
	mustAppend(t, s, ev(core.KindChat, "bcm", "one"), "i1")
	mustAppend(t, s, ev(core.KindChat, "bcm", "two"), "i2")

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
