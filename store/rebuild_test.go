package store

import (
	"fmt"
	"math/rand"
	"path/filepath"
	"testing"
	"time"

	"github.com/escherize/comms/core"
)

// projectionState is everything the log determines, in a form two runs can be
// compared by. If a rebuild produces this and the incremental path produced
// something else, one of them is wrong and the log is the tiebreaker.
type projectionState struct {
	Progress  []string
	Questions []string
	Actors    []string
	Search    []string
}

func snapshotProjections(t *testing.T, s *Store) projectionState {
	t.Helper()
	var out projectionState

	rows, err := s.db.Query(
		`SELECT room, author, step, of, note, seq FROM progress ORDER BY room, author`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var room, author, note string
		var step, of int
		var seq int64
		if err := rows.Scan(&room, &author, &step, &of, &note, &seq); err != nil {
			t.Fatal(err)
		}
		out.Progress = append(out.Progress,
			fmt.Sprintf("%s/%s %d/%d %q @%d", room, author, step, of, note, seq))
	}
	rows.Close()

	rows, err = s.db.Query(
		`SELECT seq, room, author, recipient, answer_seq FROM question ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var seq, answer int64
		var room, author, recipient string
		if err := rows.Scan(&seq, &room, &author, &recipient, &answer); err != nil {
			t.Fatal(err)
		}
		out.Questions = append(out.Questions,
			fmt.Sprintf("%d %s/%s ->%s answered=%d", seq, room, author, recipient, answer))
	}
	rows.Close()

	rows, err = s.db.Query(`SELECT actor, source FROM actor ORDER BY actor`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var actor, source string
		if err := rows.Scan(&actor, &source); err != nil {
			t.Fatal(err)
		}
		out.Actors = append(out.Actors, actor+" "+source)
	}
	rows.Close()

	rows, err = s.db.Query(`SELECT seq, kind, room FROM search ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var seq int64
		var kind, room string
		if err := rows.Scan(&seq, &kind, &room); err != nil {
			t.Fatal(err)
		}
		out.Search = append(out.Search, fmt.Sprintf("%d %s %s", seq, kind, room))
	}
	rows.Close()

	return out
}

func diffState(t *testing.T, what string, a, b []string) {
	t.Helper()
	if len(a) != len(b) {
		t.Errorf("%s: incremental has %d rows, rebuild has %d", what, len(a), len(b))
	}
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			t.Errorf("%s[%d]:\n  incremental: %s\n  rebuild:     %s", what, i, a[i], b[i])
		}
	}
}

// A generated log, folded two ways, must produce the same projections.
//
// "Projections are pure folds over the log" is asserted in three documents and
// was enforced nowhere. Append maintains them incrementally and Rebuild folds
// them again; two expressions of one rule drift, and this is what catches it.
func TestRebuildEqualsTheIncrementalFold(t *testing.T) {
	for _, seed := range []int64{1, 2, 3, 7, 11, 42} {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			s := openAt(t, filepath.Join(t.TempDir(), "gen.db"))
			generateLog(t, s, rand.New(rand.NewSource(seed)), 120)

			before := snapshotProjections(t, s)
			if err := s.Rebuild(); err != nil {
				t.Fatalf("rebuild: %v", err)
			}
			after := snapshotProjections(t, s)

			diffState(t, "progress", before.Progress, after.Progress)
			diffState(t, "questions", before.Questions, after.Questions)
			diffState(t, "actors", before.Actors, after.Actors)
			diffState(t, "search", before.Search, after.Search)
		})
	}
}

// A rebuild is idempotent: folding twice is folding once. If it were not, the
// recovery tool would be a source of drift rather than a cure for it.
func TestRebuildIsIdempotent(t *testing.T) {
	s := openAt(t, filepath.Join(t.TempDir(), "idem.db"))
	generateLog(t, s, rand.New(rand.NewSource(99)), 80)

	if err := s.Rebuild(); err != nil {
		t.Fatal(err)
	}
	once := snapshotProjections(t, s)
	if err := s.Rebuild(); err != nil {
		t.Fatal(err)
	}
	twice := snapshotProjections(t, s)

	diffState(t, "progress", once.Progress, twice.Progress)
	diffState(t, "questions", once.Questions, twice.Questions)
	diffState(t, "search", once.Search, twice.Search)
}

// Keys, invites and capabilities are not events. Rebuilding them would erase
// them, which is the difference between a projection and a record.
func TestRebuildDoesNotTouchWhatTheLogDoesNotDetermine(t *testing.T) {
	s, _, pub := keyStore(t)
	if err := s.EnsureRoom("core"); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterKey("agent:kept", pub, kt0); err != nil {
		t.Fatal(err)
	}
	if err := s.Grant("agent:kept", core.CapDigest, "human:bcm", kt0); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMembership("agent:kept", "core", "human:bcm", kt0); err != nil {
		t.Fatal(err)
	}
	token, err := s.MintInvite("agent:pending", kt0)
	if err != nil {
		t.Fatal(err)
	}
	generateLog(t, s, rand.New(rand.NewSource(5)), 30)

	if err := s.Rebuild(); err != nil {
		t.Fatal(err)
	}

	if st, _ := s.KeyStatus("agent:kept", kt0); st != "active" {
		t.Errorf("a rebuild erased a registered key: status %q", st)
	}
	if !s.HasCapability("agent:kept", core.CapDigest) {
		t.Error("a rebuild erased a capability grant")
	}
	if err := s.RedeemInvite(token, "agent:pending", pub, kt0.Add(time.Minute)); err != nil {
		t.Errorf("a rebuild erased an unspent invite: %v", err)
	}
	// Membership is a record, not a fold over the log — the same property that
	// protects keys and capabilities protects it. A rebuild recomputes only
	// what the log determines, so a seat's rooms survive it.
	if !s.IsMember("agent:kept", "core") {
		t.Error("a rebuild erased a membership grant")
	}
}

// A redacted body stays out of the index after a repair. Otherwise the
// suppression is undone by the tool that exists to fix things.
func TestRebuildDoesNotResurrectARedactedBody(t *testing.T) {
	s := openAt(t, filepath.Join(t.TempDir(), "red.db"))
	secret, err := s.Append(core.Event{Room: "core", Author: "human:bcm",
		Kind: core.KindTIL, Body: map[string]any{"text": "token PLACEHOLDER-NOT-REAL"},
		Lane: core.LaneOf(core.KindTIL)}, "sec", kt0)
	if err != nil {
		t.Fatal(err)
	}
	// The suppression is a second step, the way the shell does it: the redact
	// event records the decision, ApplyRedaction carries it out.
	redactSeq, err := s.Append(core.Event{Room: "core", Author: "human:bcm",
		Kind: core.KindRedact, Refs: []string{itoa(secret)},
		Body: map[string]any{"text": "pasted a credential"},
		Lane: core.LaneOf(core.KindRedact)}, "red", kt0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyRedaction(secret, redactSeq, "human:bcm", kt0); err != nil {
		t.Fatal(err)
	}

	if hits, _ := s.Search("PLACEHOLDER", "core", "", "", "", 10); len(hits) != 0 {
		t.Fatalf("setup: the redaction should have cleared the index, got %d", len(hits))
	}
	if err := s.Rebuild(); err != nil {
		t.Fatal(err)
	}
	if hits, _ := s.Search("PLACEHOLDER", "core", "", "", "", 10); len(hits) != 0 {
		t.Errorf("the rebuild put a redacted body back in the index: %d hits", len(hits))
	}
}

// Every startup jumps seq, so a token issued before a restore can never be
// reissued after one. A lost tail is the case: the database goes back in time
// and the fencing tokens it already handed out did not.
func TestSeqJumpMakesAPostRestoreCollisionImpossible(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "restore.db")

	s := openAt(t, path)
	var issued []int64
	for i := 0; i < 5; i++ {
		seq, err := s.Append(core.Event{Room: "core", Author: "agent:c1",
			Kind: core.KindChat, Body: map[string]any{"text": "before"},
			Lane: core.LaneOf(core.KindChat)}, fmt.Sprintf("b%d", i), kt0)
		if err != nil {
			t.Fatal(err)
		}
		issued = append(issued, seq)
	}
	lastIssued := issued[len(issued)-1]
	s.Close()

	// The restore: a copy of the file from before the tail was written. The
	// seqs in `issued` past this point were handed out and are gone from disk.
	restored := openAt(t, path)
	defer restored.Close()

	next, err := restored.Append(core.Event{Room: "core", Author: "agent:c1",
		Kind: core.KindChat, Body: map[string]any{"text": "after"},
		Lane: core.LaneOf(core.KindChat)}, "after", kt0)
	if err != nil {
		t.Fatal(err)
	}
	if next <= lastIssued {
		t.Fatalf("a seq was reissued after a restart: %d was already handed out as %d",
			next, lastIssued)
	}
	if next-lastIssued < SeqJump {
		t.Errorf("the jump was %d, less than the %d a lost tail may span",
			next-lastIssued, SeqJump)
	}
}

// generateLog writes a randomized but valid log: statuses that advance and
// occasionally restart, questions that are sometimes answered, findings, TILs,
// and redactions of things already posted.
func generateLog(t *testing.T, s *Store, rng *rand.Rand, n int) {
	t.Helper()
	rooms := []string{"core", "bash"}
	for _, r := range rooms {
		if err := s.EnsureRoom(r); err != nil {
			t.Fatal(err)
		}
	}
	authors := []core.Actor{"agent:c1", "agent:c2", "human:bcm"}
	var questions []int64
	var posted []int64
	at := kt0

	for i := 0; i < n; i++ {
		at = at.Add(time.Duration(rng.Intn(600)) * time.Second)
		room := rooms[rng.Intn(len(rooms))]
		author := authors[rng.Intn(len(authors))]
		ev := core.Event{Room: room, Author: author}

		switch rng.Intn(6) {
		case 0:
			ev.Kind = core.KindStatus
			body := map[string]any{"text": fmt.Sprintf("step note %d", i)}
			if rng.Intn(4) > 0 {
				body["step"] = float64(rng.Intn(8))
				body["of"] = float64(4 + rng.Intn(5))
			}
			ev.Body = body
		case 1:
			ev.Kind = core.KindQuestion
			ev.Recipient = "human:bcm"
			ev.Body = map[string]any{"text": fmt.Sprintf("question %d?", i)}
		case 2:
			if len(questions) == 0 {
				continue
			}
			ev.Kind = core.KindAnswer
			target := questions[rng.Intn(len(questions))]
			ev.Refs = []string{itoa(target)}
			ev.Recipient = "agent:c1"
			ev.Body = map[string]any{"text": fmt.Sprintf("answer %d", i)}
		case 3:
			ev.Kind = core.KindFinding
			ev.Body = map[string]any{
				"text":     fmt.Sprintf("finding %d about the cache", i),
				"severity": []string{"p0", "p1", "p2", "p3"}[rng.Intn(4)],
			}
		case 4:
			ev.Kind = core.KindTIL
			ev.Body = map[string]any{"text": fmt.Sprintf("lesson %d worth keeping", i)}
		case 5:
			if len(posted) == 0 || rng.Intn(3) > 0 {
				ev.Kind = core.KindChat
				ev.Body = map[string]any{"text": fmt.Sprintf("chatter %d", i)}
				break
			}
			ev.Kind = core.KindRedact
			ev.Author = author
			ev.Refs = []string{itoa(posted[rng.Intn(len(posted))])}
			ev.Body = map[string]any{"text": "suppressed"}
		}

		ev.Lane = core.LaneOf(ev.Kind)
		if core.LaneOf(ev.Kind) == core.Ambient {
			ev.Recipient = ""
		}
		seq, err := s.Append(ev, fmt.Sprintf("gen-%d", i), at)
		if err != nil {
			continue // an invalid combination the generator produced; not the subject
		}
		posted = append(posted, seq)
		if ev.Kind == core.KindQuestion {
			questions = append(questions, seq)
		}
	}
}

func openAt(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
