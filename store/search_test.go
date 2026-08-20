package store

import (
	"testing"
	"time"

	"github.com/escherize/comms/core"
)

// seedRoom plants the kind of content a real room holds: prose an agent would
// write, not keyword bait.
func seedRoom(t *testing.T) (*Store, map[string]int64) {
	t.Helper()
	s := newStore(t)
	seqs := map[string]int64{}
	add := func(key string, kind core.Kind, author core.Actor, text string) {
		body := map[string]any{"text": text}
		if kind == core.KindFinding {
			body["severity"] = "p2"
		}
		seq, err := s.Append(core.Event{Room: "core", Author: author, Kind: kind,
			Body: body, Lane: core.LaneOf(kind)}, key, t0)
		if err != nil {
			t.Fatal(err)
		}
		seqs[key] = seq
	}

	add("coldcache", core.KindFinding, "agent:claude-1",
		"auth suite fails on cold cache: TokenCache.warm() runs after the first assertion")
	add("vec", core.KindTIL, "agent:codex-3",
		"sqlite-vec rejects bodies over 8k tokens; chunk before embed")
	add("deref", core.KindFinding, "agent:claude-1",
		"nil deref on second retry in auth.py:88")
	add("backoff", core.KindFinding, "agent:claude-1",
		"retry budget is read before the backoff is applied")
	add("migration", core.KindQuestion, "agent:claude-2",
		"migration 0031 assumes 0029 ran; is it safe to reorder?")
	add("flake", core.KindTIL, "human:bcm",
		"the flaky test helper leaks a temp dir on failure paths")
	return s, seqs
}

// The queries an agent actually types. Each asserts a specific event, because
// "some hits" is not the property — finding the right one is.
func TestNaturalQueriesFindTheirEvent(t *testing.T) {
	s, seqs := seedRoom(t)

	cases := []struct {
		query string
		want  string
	}{
		// The exact query the skill file teaches first.
		{"flaky auth cold cache", "coldcache"},
		{"flaky auth", "coldcache"},
		{"cold cache", "coldcache"},
		{"TokenCache warm", "coldcache"},
		{"sqlite-vec chunk", "vec"},
		{"auth.py:88", "deref"},
		{"retry backoff budget", "backoff"},
		{"migration reorder", "migration"},
	}

	for _, c := range cases {
		t.Run(c.query, func(t *testing.T) {
			hits, err := s.Search(c.query, "", "", "", "", nil, 20)
			if err != nil {
				t.Fatalf("search errored: %v", err)
			}
			if len(hits) == 0 {
				t.Fatalf("%q found nothing; the room contains the answer", c.query)
			}
			want := seqs[c.want]
			for _, h := range hits {
				if h.Seq == want {
					return
				}
			}
			t.Errorf("%q did not return seq %d; got %v", c.query, want, seqList(hits))
		})
	}
}

// The property the AND bug violated: adding a term must never remove a hit that
// still matches an earlier term. Recall may only grow.
func TestAddingTermsNeverRemovesAHit(t *testing.T) {
	s, seqs := seedRoom(t)
	target := seqs["coldcache"]

	growing := []string{
		"cache",
		"cold cache",
		"auth cold cache",
		"flaky auth cold cache",
		"flaky auth cold cache nonexistentword",
		"flaky auth cold cache nonexistentword anotherabsentterm",
	}

	for _, q := range growing {
		hits, err := s.Search(q, "", "", "", "", nil, 50)
		if err != nil {
			t.Fatalf("%q errored: %v", q, err)
		}
		var found bool
		for _, h := range hits {
			if h.Seq == target {
				found = true
			}
		}
		if !found {
			t.Errorf("%q lost the cold-cache finding; adding a term must not cut recall", q)
		}
	}
}

// The literal-token quoting ticket 03 added must survive the OR change: a
// hyphen is FTS5's NOT and a colon is a column filter.
func TestLiteralTokensStillMatch(t *testing.T) {
	s, seqs := seedRoom(t)

	for _, c := range []struct{ query, want string }{
		{"sqlite-vec", "vec"},
		{"auth.py:88", "deref"},
	} {
		hits, err := s.Search(c.query, "", "", "", "", nil, 10)
		if err != nil {
			t.Fatalf("%q errored: %v", c.query, err)
		}
		var found bool
		for _, h := range hits {
			if h.Seq == seqs[c.want] {
				found = true
			}
		}
		if !found {
			t.Errorf("%q must match literally, got %v", c.query, seqList(hits))
		}
	}

	// The other direction: a token that is genuinely absent finds nothing.
	if hits, _ := s.Search("kubernetes", "", "", "", "", nil, 10); len(hits) != 0 {
		t.Errorf("an absent term must return nothing, got %d hits", len(hits))
	}
}

// Results order by relevance, not by seq, and the score is exposed.
func TestResultsOrderByRankNotSeq(t *testing.T) {
	s, seqs := seedRoom(t)

	// A query strongly matching a late event must put it first.
	hits, err := s.Search("cold cache TokenCache", "", "", "", "", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) < 2 {
		t.Skip("need at least two hits to prove ordering")
	}
	if hits[0].Seq != seqs["coldcache"] {
		t.Errorf("the best match must come first, got seq %d", hits[0].Seq)
	}
	if hits[0].Rank == 0 {
		t.Error("each hit must carry its rank")
	}
	if hits[0].Rank < hits[1].Rank {
		t.Errorf("results must be ordered best-first: %.2f then %.2f", hits[0].Rank, hits[1].Rank)
	}
}

func seqList(rs []Record) []int64 {
	out := make([]int64, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Seq)
	}
	return out
}

var _ = time.Now
