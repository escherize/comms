package store

import (
	"strings"
	"testing"
	"time"

	"github.com/escherize/comms/core"
)

// A missing counter field inherits its prior value individually: --step 3 on a
// 2-of-5 job is step 3 of 5, not 3 of 0. The zeroing was a real regression a
// review crew caught (study 7, seq 10036).
func TestProgressStepOnlyKeepsThePriorTotal(t *testing.T) {
	s := newStore(t)
	if _, err := s.Append(core.Event{Room: "core", Author: "agent:c1", Kind: core.KindChat,
		Body: map[string]any{"text": "starting", "step": float64(2), "of": float64(5)}}, "p1", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(core.Event{Room: "core", Author: "agent:c1", Kind: core.KindChat,
		Body: map[string]any{"text": "step three", "step": float64(3)}}, "p2", t0); err != nil {
		t.Fatal(err)
	}
	ps, err := s.ProgressFor("core")
	if err != nil || len(ps) != 1 {
		t.Fatalf("want one progress row, got %d (%v)", len(ps), err)
	}
	if ps[0].Step != 3 || ps[0].Of != 5 {
		t.Errorf("step-only update must keep the total: got %d/%d, want 3/5", ps[0].Step, ps[0].Of)
	}
}

// A reply must not open a waiting-on item: an agent answering a human's ask
// routes back to the human, and that reply continuing the exchange is not the
// human owing a second response (study 7, seq 10039).
func TestAReplyIntoAnExchangeOpensNoQuestion(t *testing.T) {
	s := newStore(t)
	ask, err := s.Append(core.Event{Room: "core", Author: "human:bcm", Kind: core.KindChat,
		Body: map[string]any{"text": "can you take LIN-9?"}, Recipient: "agent:c1",
		Lane: core.Addressed}, "q1", t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(core.Event{Room: "core", Author: "agent:c1", Kind: core.KindChat,
		Body: map[string]any{"text": "yes, on it"}, Recipient: "human:bcm",
		Refs: []string{itoa(ask)}, Lane: core.Addressed}, "r1", t0); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM question`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("a reply into an existing exchange opened %d waiting-on item(s); want 0", n)
	}
	// A fresh human-addressed post with no exchange behind it still opens one.
	if _, err := s.Append(core.Event{Room: "core", Author: "agent:c1", Kind: core.KindChat,
		Body: map[string]any{"text": "@human:bcm is 0031 safe?"}, Recipient: "human:bcm",
		Lane: core.Addressed}, "q2", t0); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM question WHERE answer_seq = 0`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("a fresh ask must open exactly one item, got %d", n)
	}
}

// Rebuild refills the artifact access index it clears — its doc always
// promised "drop and refill"; the refill half was missing (study 7, seq 10024).
func TestRebuildRefillsArtifactRefs(t *testing.T) {
	s := newStore(t)
	hash, err := s.PutArtifact([]byte("# report\n"), t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(core.Event{Room: "core", Author: "agent:c1", Kind: core.KindChat,
		Body:        map[string]any{"text": "report attached"},
		Attachments: []core.Attachment{{Hash: hash, Title: "r.md"}}}, "a1", t0); err != nil {
		t.Fatal(err)
	}
	// Corrupt the index the way a bad restore would.
	if _, err := s.db.Exec(`DELETE FROM artifact_ref`); err != nil {
		t.Fatal(err)
	}
	if err := s.Rebuild(); err != nil {
		t.Fatal(err)
	}
	if rooms := s.ArtifactRooms(hash); len(rooms) != 1 || rooms[0] != "core" {
		t.Errorf("rebuild must refill artifact_ref, got rooms %v", rooms)
	}
}

// The roster carries a derived last-seen (ADR-0019): the seat's newest post's
// server_ts, empty for a seat that never posted.
func TestRosterCarriesDerivedLastSeen(t *testing.T) {
	s := newStore(t)
	if err := s.RegisterKey("agent:quiet", make([]byte, 32), t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(core.Event{Room: "core", Author: "agent:busy", Kind: core.KindChat,
		Body: map[string]any{"text": "here"}}, "ls1", t0); err != nil {
		t.Fatal(err)
	}
	later := t0.Add(2 * time.Hour)
	if _, err := s.Append(core.Event{Room: "core", Author: "agent:busy", Kind: core.KindChat,
		Body: map[string]any{"text": "still here"}}, "ls2", later); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Actors()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, r := range rows {
		seen[r.Actor] = r.LastSeen
	}
	if seen["agent:quiet"] != "" {
		t.Errorf("a never-posted seat must have empty last-seen, got %q", seen["agent:quiet"])
	}
	if !strings.HasPrefix(seen["agent:busy"], later.UTC().Format("2006-01-02T15")) {
		t.Errorf("last-seen must be the NEWEST post's server_ts, got %q (want ~%s)",
			seen["agent:busy"], later.UTC().Format(time.RFC3339))
	}
}
