package shell

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bcm/agent_comms/core"
	"github.com/bcm/agent_comms/store"
)

// poisonEmbedder fails on one text and succeeds on everything else. The seam
// exists so this is possible: none of the pipeline's properties depend on what
// produces the numbers.
type poisonEmbedder struct {
	poison string
	calls  map[string]int
}

func (p *poisonEmbedder) Model() string { return "poison-v1" }

func (p *poisonEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if p.calls == nil {
		p.calls = map[string]int{}
	}
	p.calls[text]++
	if strings.Contains(text, p.poison) {
		return nil, errors.New("this one always fails")
	}
	return HashEmbedder{}.Embed(ctx, text)
}

var seedCounter int64

func seedFor(t *testing.T, st *store.Store, texts ...string) []int64 {
	t.Helper()
	var seqs []int64
	for _, txt := range texts {
		// A fresh key per event, across calls: reusing one is an idem conflict,
		// which is the store correctly refusing to lose a post.
		seedCounter++
		seq, err := st.Append(core.Event{Room: "core", Author: "agent:c1",
			Kind: core.KindTIL, Body: map[string]any{"text": txt},
			Lane: core.LaneOf(core.KindTIL)}, "seed-"+itoa(seedCounter), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		seqs = append(seqs, seq)
	}
	return seqs
}

// One poison event must not stall the lane. After three attempts it is given
// up on, the watermark moves past it, and it lands somewhere a person can read.
func TestAPoisonEventAdvancesTheWatermarkAndIsDeadLettered(t *testing.T) {
	_, st, _ := newServerFull(t, time.Millisecond)
	seqs := seedFor(t, st, "the first entry", "POISON in this one", "the third entry")

	model := &poisonEmbedder{poison: "POISON"}
	e := newEmbedder(st, model, time.Now)

	// Three passes: the first two leave the watermark stuck at the poison, the
	// third gives up and lets the lane move on.
	for i := 0; i < store.EmbedAttempts; i++ {
		for e.step(context.Background(), 10) > 0 {
		}
	}

	if got := st.EmbeddedThrough(); got < seqs[2] {
		t.Errorf("the lane stalled on the poison event: watermark %d, head %d", got, seqs[2])
	}
	if model.calls["POISON in this one"] != store.EmbedAttempts {
		t.Errorf("want exactly %d attempts on the poison, got %d",
			store.EmbedAttempts, model.calls["POISON in this one"])
	}

	dead, err := st.DeadLettered()
	if err != nil {
		t.Fatal(err)
	}
	if len(dead) != 1 || dead[0].Seq != seqs[1] {
		t.Fatalf("the poison must be visible on a dead-letter list, got %+v", dead)
	}
	if dead[0].Last == "" {
		t.Error("the dead letter must carry why it failed")
	}

	// The events either side of it are embedded.
	hits, err := st.NearestVectors(mustEmbed(t, "the third entry"), "core", 10)
	if err != nil {
		t.Fatal(err)
	}
	var sawThird bool
	for _, h := range hits {
		if h.Seq == seqs[2] {
			sawThird = true
		}
		if h.Seq == seqs[1] {
			t.Error("the poison event must not be in the lane")
		}
	}
	if !sawThird {
		t.Error("events after the poison must still be embedded")
	}
}

// The watermark is not a stalling guess: while an event may still succeed, the
// lane waits for it rather than skipping it silently.
func TestTheWatermarkDoesNotSkipAnEventThatMayStillSucceed(t *testing.T) {
	_, st, _ := newServerFull(t, time.Millisecond)
	seqs := seedFor(t, st, "first", "POISON here", "third")
	e := newEmbedder(st, &poisonEmbedder{poison: "POISON"}, time.Now)

	e.step(context.Background(), 10)
	if got := st.EmbeddedThrough(); got >= seqs[1] {
		t.Errorf("the watermark passed an event that has attempts left: %d", got)
	}
	if got := st.EmbeddedThrough(); got != seqs[0] {
		t.Errorf("the watermark should sit on the last success, got %d want %d", got, seqs[0])
	}
	_ = seqs[2]
}

// A stale lane says so. A search over an index an hour behind is a true result
// an agent draws a false conclusion from.
func TestAPausedEmbedderMakesTheLaneReportStale(t *testing.T) {
	srv, st, sv := newServerFull(t, time.Millisecond)
	seedFor(t, st, "the cold cache flake is run order")

	// Nothing embedded yet: the lane is behind the log.
	_, _, stale := sv.lagFor()
	if !stale {
		t.Fatal("an unembedded log must read as stale")
	}
	page := getPage(t, srv.URL+"/search?q=flake")
	if !strings.Contains(page, "stale") {
		t.Error("the page must say the semantic lane is behind")
	}

	// Catch it up, and it stops claiming to be behind.
	e := sv.embed
	for e.step(context.Background(), 32) > 0 {
	}
	if _, _, stale := sv.lagFor(); stale {
		t.Error("a caught-up lane must not report stale")
	}
	page = getPage(t, srv.URL+"/search?q=flake")
	if !strings.Contains(page, "index current to") {
		t.Error("a caught-up lane must publish its watermark as a time")
	}

	// Paused, and new events arrive: behind again, and saying so.
	e.pause(true)
	seedFor(t, st, "a second entry nobody embedded")
	if _, _, stale := sv.lagFor(); !stale {
		t.Error("a paused embedder with new events must read as stale")
	}
	e.pause(false)
}

// Both ranks per hit, because a result that ranked 1st lexically and 40th
// semantically is a different fact than one that ranked 20th in each.
func TestFusionShowsBothRanks(t *testing.T) {
	lex := []store.Record{{Seq: 10}, {Seq: 20}}
	vec := []store.VectorHit{{Seq: 20, Score: 0.9}, {Seq: 30, Score: 0.7}}
	byseq := map[int64]store.Record{10: {Seq: 10}, 20: {Seq: 20}, 30: {Seq: 30}}

	fused := fuse(lex, vec, byseq, 10)
	if len(fused) != 3 {
		t.Fatalf("the union of both lanes is 3 hits, got %d", len(fused))
	}

	got := map[int64]Fused{}
	for _, f := range fused {
		got[f.Rec.Seq] = f
	}
	if got[10].LexRank != 1 || got[10].VecRank != 0 {
		t.Errorf("a lexical-only hit must show its rank and no vector rank: %+v", got[10])
	}
	if got[30].LexRank != 0 || got[30].VecRank != 2 {
		t.Errorf("a semantic-only hit must show its rank and no lexical rank: %+v", got[30])
	}
	if got[20].LexRank != 2 || got[20].VecRank != 1 {
		t.Errorf("a hit in both lanes must carry both ranks: %+v", got[20])
	}

	// Being in both lanes outranks being first in one, which is the property
	// reciprocal rank fusion exists for.
	if fused[0].Rec.Seq != 20 {
		t.Errorf("the hit in both lanes should rank first, got seq %d", fused[0].Rec.Seq)
	}
	if got[20].VecScore != 0.9 {
		t.Errorf("the similarity must survive fusion, got %v", got[20].VecScore)
	}
}

// A rebuild is a fresh attempt, not a fourth one: an event that failed three
// times under a broken model gets three more under the new one.
func TestReembedRebuildsTheLaneAndResetsFailures(t *testing.T) {
	_, st, _ := newServerFull(t, time.Millisecond)
	seqs := seedFor(t, st, "alpha entry", "POISON entry", "gamma entry")

	broken := &poisonEmbedder{poison: "POISON"}
	e := newEmbedder(st, broken, time.Now)
	for i := 0; i < store.EmbedAttempts; i++ {
		for e.step(context.Background(), 10) > 0 {
		}
	}
	if dead, _ := st.DeadLettered(); len(dead) != 1 {
		t.Fatalf("setup: want one dead letter, got %d", len(dead))
	}

	// A model that can embed everything, and a rebuild from the start.
	fixed := newEmbedder(st, HashEmbedder{}, time.Now)
	n, err := fixed.Reembed(context.Background(), seqs[0])
	if err != nil {
		t.Fatal(err)
	}
	if n != len(seqs) {
		t.Errorf("the rebuild should have covered all %d events, did %d", len(seqs), n)
	}
	if dead, _ := st.DeadLettered(); len(dead) != 0 {
		t.Errorf("a rebuild clears the failure counts, %d left", len(dead))
	}
	if got := st.EmbeddedThrough(); got != seqs[len(seqs)-1] {
		t.Errorf("the watermark should be at the head after a rebuild, got %d", got)
	}

	// And the previously-poisoned event is now in the lane.
	hits, _ := st.NearestVectors(mustEmbed(t, "POISON entry"), "core", 10)
	var found bool
	for _, h := range hits {
		if h.Seq == seqs[1] {
			found = true
		}
	}
	if !found {
		t.Error("the rebuilt lane must contain the event the old model choked on")
	}
}

// An embedding is derived from the body. It must not outlive a redaction — the
// remaining criterion of ticket 08, satisfied in the transaction that suppresses.
func TestRedactionDropsTheEmbedding(t *testing.T) {
	srv, st, sv := newServerFull(t, time.Millisecond)
	code, out := post(t, srv, cmd("chat", "the password is PLACEHOLDER-NOT-REAL", "sec1"))
	if code != 200 {
		t.Fatalf("setup: %v", out)
	}
	target := int64(out["seq"].(float64))

	e := sv.embed
	for e.step(context.Background(), 32) > 0 {
	}
	hits, _ := st.NearestVectors(mustEmbed(t, "the password is PLACEHOLDER-NOT-REAL"), "core", 10)
	var before bool
	for _, h := range hits {
		if h.Seq == target {
			before = true
		}
	}
	if !before {
		t.Fatal("setup: the event should be in the lane before redaction")
	}

	// Redaction is author-only, and cmd() posts as human:bcm.
	code, out = post(t, srv, `{"room":"core","author":"human:bcm","kind":"redact",`+
		`"body":{"text":"pasted a credential"},"refs":["`+itoa(target)+`"],"idem":"red1"}`)
	if code != 200 {
		t.Fatalf("redact should be accepted: %v", out)
	}

	hits, _ = st.NearestVectors(mustEmbed(t, "the password is PLACEHOLDER-NOT-REAL"), "core", 10)
	for _, h := range hits {
		if h.Seq == target {
			t.Error("the embedding outlived the redaction; it is the secret in a form " +
				"nobody thinks to look at")
		}
	}
}

func mustEmbed(t *testing.T, text string) []float32 {
	t.Helper()
	v, err := HashEmbedder{}.Embed(context.Background(), text)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// "Nearest" without a floor means "every event in the room, ordered". An
// unrelated entry then comes back ranked third with a similarity of zero, which
// is the lane asserting a relationship that does not exist — and a reader
// cannot tell that from a weak-but-real match.
func TestAnUnrelatedEventIsNotASemanticHit(t *testing.T) {
	_, st, sv := newServerFull(t, time.Millisecond)
	seqs := seedFor(t, st,
		"the auth suite fails on a cold cache because warm runs late",
		"the deploy pipeline pushes images to the registry")
	for sv.embed.step(context.Background(), 32) > 0 {
	}

	hits, err := st.NearestVectors(mustEmbed(t, "cold cache"), "core", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.Seq == seqs[1] {
			t.Errorf("an event sharing no vocabulary came back as a hit at similarity %v",
				h.Score)
		}
		if h.Score <= store.MinSimilarity {
			t.Errorf("seq %d scored %v, at or below the floor", h.Seq, h.Score)
		}
	}
	var sawRelated bool
	for _, h := range hits {
		if h.Seq == seqs[0] {
			sawRelated = true
		}
	}
	if !sawRelated {
		t.Error("the floor must not exclude a real match")
	}
}

// A purge erases the body permanently, and the embedding is part of the body:
// it is derived from it, so it is the secret in a form nobody thinks to look
// at, and it survives every surface suppression covers.
func TestPurgeLeavesNoEmbeddingAndARebuildDoesNotResurrectOne(t *testing.T) {
	srv, st, sv := newServerFull(t, time.Millisecond)
	secret := "the token is PLACEHOLDER-NOT-REAL and it must not survive"

	code, out := post(t, srv, cmd("chat", secret, "p1"))
	if code != 200 {
		t.Fatalf("setup: %v", out)
	}
	target := int64(out["seq"].(float64))

	for sv.embed.step(context.Background(), 32) > 0 {
	}
	if hits, _ := st.NearestVectors(mustEmbed(t, secret), "core", 10); len(hits) == 0 {
		t.Fatal("setup: the event should be in the semantic lane before the purge")
	}

	if err := st.Purge(target); err != nil {
		t.Fatalf("purge: %v", err)
	}

	// Gone from the semantic lane.
	hits, _ := st.NearestVectors(mustEmbed(t, secret), "core", 10)
	for _, h := range hits {
		if h.Seq == target {
			t.Error("the embedding outlived the purge")
		}
	}
	// And from the lexical one.
	if lex, _ := st.Search("PLACEHOLDER", "core", "", "", "", 10); len(lex) != 0 {
		t.Errorf("the purged body is still in the lexical index: %d hits", len(lex))
	}

	// A rebuild of the semantic lane must not put it back. There is no body to
	// embed, and trying would either fail three times or — worse — embed an
	// empty string and call it done.
	if _, err := sv.embed.Reembed(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	hits, _ = st.NearestVectors(mustEmbed(t, secret), "core", 10)
	for _, h := range hits {
		if h.Seq == target {
			t.Error("a rebuild resurrected the embedding of a purged body")
		}
	}

	// And a rebuild of the projections does not put the text back either.
	if err := st.Rebuild(); err != nil {
		t.Fatal(err)
	}
	if lex, _ := st.Search("PLACEHOLDER", "core", "", "", "", 10); len(lex) != 0 {
		t.Errorf("a projection rebuild resurrected a purged body: %d hits", len(lex))
	}
}

// A purged body is not a pending embed. Without that, a rebuild manufactures
// dead letters for events that are gone on purpose, and the list that should
// mean "something is wrong" fills with entries that mean "something worked".
func TestAPurgedBodyIsNotAPendingEmbed(t *testing.T) {
	srv, st, sv := newServerFull(t, time.Millisecond)
	_, out := post(t, srv, cmd("chat", "something to purge", "pp1"))
	target := int64(out["seq"].(float64))
	post(t, srv, cmd("til", "something to keep", "pp2"))

	if err := st.Purge(target); err != nil {
		t.Fatal(err)
	}
	if _, err := sv.embed.Reembed(context.Background(), 0); err != nil {
		t.Fatal(err)
	}

	pending, err := st.PendingEmbeds(50)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pending {
		if p.Seq == target {
			t.Error("a purged body is still queued for embedding")
		}
	}
	// The surviving event is embedded, so the skip is targeted rather than
	// a blanket refusal to work.
	if hits, _ := st.NearestVectors(mustEmbed(t, "something to keep"), "core", 10); len(hits) == 0 {
		t.Error("the rebuild skipped events it should have embedded")
	}
}
