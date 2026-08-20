package shell

import (
	"context"
	"crypto/sha256"
	"errors"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/escherize/comms/store"
)

// Embedder is the seam. Which model produces the numbers is a decision this
// package does not make and does not need to: the watermark, the retry count,
// the dead-letter list and the fusion are all properties of the pipeline, and
// every one of them is testable against a fake that returns arithmetic.
//
// Model() is part of the interface because a vector is only comparable to
// another vector from the same model. Storing it beside the numbers is what
// lets the lane skip incomparable rows instead of scoring across models and
// returning a number with no meaning.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	Model() string
}

// HashEmbedder is a deterministic stand-in, not a model. It hashes tokens into
// a fixed number of buckets, which makes texts that share vocabulary score
// close together — enough to drive every property of the pipeline, and not
// enough to be mistaken for semantics.
//
// It ships rather than being test-only so the lane is exercised by default:
// an adapter seam nobody runs is a seam nobody knows is broken.
type HashEmbedder struct{ Dim int }

func (h HashEmbedder) Model() string { return "hash-v1" }

func (h HashEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("nothing to embed")
	}
	dim := h.Dim
	if dim == 0 {
		dim = 256
	}
	vec := make([]float32, dim)
	for _, tok := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !('a' <= r && r <= 'z') && !('0' <= r && r <= '9')
	}) {
		sum := sha256.Sum256([]byte(tok))
		idx := (int(sum[0])<<8 | int(sum[1])) % dim
		vec[idx] += 1
	}
	var norm float64
	for _, f := range vec {
		norm += float64(f) * float64(f)
	}
	if norm == 0 {
		return nil, errors.New("nothing to embed")
	}
	norm = math.Sqrt(norm)
	for i := range vec {
		vec[i] = float32(float64(vec[i]) / norm)
	}
	return vec, nil
}

// embedder is the background worker. It is eventually consistent by design and
// says so: the watermark is published rather than hidden, because a search over
// a lane that is an hour behind is a true result read as a false conclusion.
type embedder struct {
	st    *store.Store
	model Embedder
	now   func() time.Time

	mu     sync.Mutex
	paused bool
}

func newEmbedder(st *store.Store, model Embedder, now func() time.Time) *embedder {
	return &embedder{st: st, model: model, now: now}
}

// pause stops the worker doing anything. Tests use it to hold the lane behind
// and watch the page say so.
func (e *embedder) pause(on bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.paused = on
}

func (e *embedder) isPaused() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.paused
}

// step embeds one batch and returns how many it handled. It advances the
// watermark past events it gave up on, because one poison event that stalls the
// lane takes the whole room's semantic search down with it.
func (e *embedder) step(ctx context.Context, batch int) int {
	if e.isPaused() {
		return 0
	}
	pending, err := e.st.PendingEmbeds(batch)
	if err != nil || len(pending) == 0 {
		return 0
	}

	done := 0
	for _, rec := range pending {
		text := rec.Text()
		vec, err := e.model.Embed(ctx, text)
		if err != nil {
			attempts, ferr := e.st.RecordEmbedFailure(rec.Seq, err.Error(), e.now())
			if ferr != nil {
				return done
			}
			if attempts < store.EmbedAttempts {
				// Leave the watermark where it is: this one may yet succeed,
				// and moving past it would silently drop it from the lane.
				return done
			}
			// Given up on. The watermark moves so the lane keeps up, and the
			// event lands on a list somebody can read.
			if err := e.st.SetEmbeddedThrough(rec.Seq); err != nil {
				return done
			}
			done++
			continue
		}
		if err := e.st.PutVector(rec.Seq, vec, e.model.Model(), e.now()); err != nil {
			return done
		}
		if err := e.st.SetEmbeddedThrough(rec.Seq); err != nil {
			return done
		}
		done++
	}
	return done
}

// run drives the worker until the context ends.
func (e *embedder) run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for e.step(ctx, 32) > 0 {
			}
		}
	}
}

// Reembed rebuilds the lane from a seq. It is a rebuild, not a repair: the
// vectors and the failure counts from that point are dropped first, so an event
// that failed three times under a broken model gets three fresh attempts under
// the new one.
func (e *embedder) Reembed(ctx context.Context, from int64) (int, error) {
	if err := e.st.DropVectorsFrom(from); err != nil {
		return 0, err
	}
	total := 0
	for {
		n := e.step(ctx, 64)
		if n == 0 {
			return total, nil
		}
		total += n
	}
}

// lagFor reports how far behind the lane is, as a duration rather than a seq
// count: "current to 14:32" is a sentence a person can act on and "behind by
// 812" is not, because seq is gappy by design.
func (s *Server) lagFor() (watermark int64, at time.Time, stale bool) {
	watermark = s.st.EmbeddedThrough()
	head := s.st.Head()
	if watermark >= head {
		return watermark, s.now(), false
	}
	if ts, ok := s.st.ServerTSOf(watermark); ok {
		at = ts
	}
	return watermark, at, true
}

// Fused is one hit with both ranks visible. Both, not a single blended number:
// a result that ranked 1st lexically and 40th semantically is a different fact
// about the room than one that ranked 20th in each, and a fused score alone
// throws that away.
type Fused struct {
	Rec      store.Record
	LexRank  int     // 1-based; 0 means the lexical lane did not return it
	VecRank  int     // 1-based; 0 means the semantic lane did not return it
	VecScore float64 // cosine similarity, when the semantic lane returned it
	Score    float64 // reciprocal rank fusion
}

// rrfK damps the difference between the top ranks. 60 is the value the original
// reciprocal-rank-fusion paper uses, and its effect is that being 1st rather
// than 2nd is worth much less than being in a lane at all — which is what makes
// fusion robust to two lanes whose scores are not comparable.
const rrfK = 60.0

// fuse merges the lexical and semantic lanes by reciprocal rank. It never
// compares a bm25 score to a cosine similarity: they are different units, and
// the only thing the two lanes agree on is order.
func fuse(lex []store.Record, vec []store.VectorHit, byseq map[int64]store.Record, limit int) []Fused {
	out := map[int64]*Fused{}

	for i, rec := range lex {
		f := out[rec.Seq]
		if f == nil {
			f = &Fused{Rec: rec}
			out[rec.Seq] = f
		}
		f.LexRank = i + 1
		f.Score += 1 / (rrfK + float64(i+1))
	}
	for i, hit := range vec {
		f := out[hit.Seq]
		if f == nil {
			rec, ok := byseq[hit.Seq]
			if !ok {
				// The semantic lane found an event the caller could not load —
				// redacted between the two queries, most likely. Dropping it is
				// right: a hit with no body is a row that says nothing.
				continue
			}
			f = &Fused{Rec: rec}
			out[hit.Seq] = f
		}
		f.VecRank = i + 1
		f.VecScore = hit.Score
		f.Score += 1 / (rrfK + float64(i+1))
	}

	fused := make([]Fused, 0, len(out))
	for _, f := range out {
		fused = append(fused, *f)
	}
	// Descending by fused score, ties by seq so the order is stable.
	for i := 1; i < len(fused); i++ {
		for j := i; j > 0 && (fused[j].Score > fused[j-1].Score ||
			(fused[j].Score == fused[j-1].Score && fused[j].Rec.Seq < fused[j-1].Rec.Seq)); j-- {
			fused[j], fused[j-1] = fused[j-1], fused[j]
		}
	}
	if len(fused) > limit {
		fused = fused[:limit]
	}
	return fused
}

// searchBoth runs the lexical lane, and the semantic lane when it has anything
// to say, and reports what each lane actually did — including that the semantic
// one was skipped, and why.
func (s *Server) searchBoth(ctx context.Context, q, room, kind, author, since string, allow []string, limit int) ([]Fused, []store.LaneStatus, error) {
	lex, err := s.st.Search(q, room, kind, author, since, allow, limit)
	if err != nil {
		return nil, nil, err
	}

	lanes := []store.LaneStatus{{Name: "lexical", State: "searched"}}
	byseq := map[int64]store.Record{}
	for _, r := range lex {
		byseq[r.Seq] = r
	}

	if s.embed == nil {
		lanes = append(lanes, store.LaneStatus{Name: "vector", State: "unbuilt",
			Detail: "no embedder is configured; these results are lexical only"})
		return fuse(lex, nil, byseq, limit), lanes, nil
	}

	qvec, err := s.embed.model.Embed(ctx, q)
	if err != nil {
		lanes = append(lanes, store.LaneStatus{Name: "vector", State: "failed",
			Detail: "the query could not be embedded; these results are lexical only"})
		return fuse(lex, nil, byseq, limit), lanes, nil
	}
	hits, err := s.st.NearestVectors(qvec, room, allow, limit)
	if err != nil {
		lanes = append(lanes, store.LaneStatus{Name: "vector", State: "failed",
			Detail: err.Error()})
		return fuse(lex, nil, byseq, limit), lanes, nil
	}

	// Load the bodies the semantic lane found that the lexical one did not.
	for _, h := range hits {
		if _, ok := byseq[h.Seq]; ok {
			continue
		}
		if rec, ok := s.st.RecordAt(h.Seq); ok {
			byseq[h.Seq] = rec
		}
	}

	// The lexical lane honoured kind/author/since in SQL; the vector lane got
	// only (room, allow, limit). Its hits must pass the same predicates, or
	// every documented filter silently leaks excluded events back in.
	if kind != "" || author != "" || since != "" {
		kept := hits[:0]
		for _, h := range hits {
			rec, ok := byseq[h.Seq]
			if !ok {
				continue
			}
			if kind != "" && string(rec.Kind) != kind {
				continue
			}
			if author != "" && string(rec.Author) != author {
				continue
			}
			// Mirror Search's SQL boundary exactly: it compares the stored
			// server_ts (RFC3339Nano) against the caller's since with the
			// trailing Z stripped, so the boundary second's sub-second events
			// are kept rather than dropped. Formatting as RFC3339 (or comparing
			// the un-stripped since) leaked ~1s of events past the lexical lane.
			if since != "" && rec.ServerTS.UTC().Format(time.RFC3339Nano) < strings.TrimSuffix(since, "Z") {
				continue
			}
			kept = append(kept, h)
		}
		hits = kept
	}

	watermark, at, stale := s.lagFor()
	vec := store.LaneStatus{Name: "vector", State: "searched",
		Detail: "index current to " + at.UTC().Format("15:04:05")}
	if stale {
		vec.State = "stale"
		vec.Detail = "index current to " + at.UTC().Format("15:04:05") +
			" (seq " + itoa64(watermark) + "); newer events are in the lexical results only"
	}
	lanes = append(lanes, vec)
	return fuse(lex, hits, byseq, limit), lanes, nil
}

func itoa64(n int64) string {
	return strconv.FormatInt(n, 10)
}
