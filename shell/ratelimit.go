package shell

import (
	"strings"
	"sync"
	"time"

	"github.com/escherize/comms/core"
)

// An unattended agent can fill the log at wire speed: 700 signed posts landed
// in 227ms against this server with nothing in the way. The limit is per key,
// because the thing being bounded is one seat's loop, not the hub's capacity.
//
// It is a token bucket rather than a fixed window: a fixed window admits twice
// the nominal rate across a boundary, and the burst it allows is exactly the
// burst an agent posting a finished batch of findings legitimately needs.
type limiter struct {
	mu      sync.Mutex
	buckets map[core.Actor]*bucket
	rate    float64 // tokens per second
	burst   float64
	now     func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newLimiter(perMinute int, burst int, now func() time.Time) *limiter {
	return &limiter{
		buckets: map[core.Actor]*bucket{},
		rate:    float64(perMinute) / 60,
		burst:   float64(burst),
		now:     now,
	}
}

// allow reports whether this actor may post now, and how long to wait if not.
func (l *limiter) allow(actor core.Actor, kind core.Kind) (bool, time.Duration) {
	// ADR-0008: budgets never touch task.* or offer.*. Work coordination is not
	// chatter, and a rate limit that could stall a claim would turn a busy room
	// into a stuck one.
	if exemptFromBudget(kind) {
		return true, 0
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	b, ok := l.buckets[actor]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[actor] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	// Time until one whole token exists.
	wait := time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
	return false, wait
}

// exemptFromBudget names the kinds that coordinate work rather than fill a
// room. It is a prefix rule so a new task.* kind is exempt by construction
// rather than by remembering to add it here.
func exemptFromBudget(kind core.Kind) bool {
	k := string(kind)
	return strings.HasPrefix(k, "task.") || strings.HasPrefix(k, "offer.")
}

// corrections counts consecutive rejections of the same invariant per seat.
//
// docs/CLI.md promises "at most two self-corrections, then exit 4". The client
// cannot keep that promise: a self-correction is a fresh process with a fresh
// idem, so there is no lineage in the client to count. The server has one — it
// sees every rejection this seat has had — so the counter lives here, which
// also makes the rule true for the browser rather than only for the CLI.
type corrections struct {
	mu   sync.Mutex
	runs map[core.Actor]*run
}

type run struct {
	invariant string
	n         int
}

func newCorrections() *corrections {
	return &corrections{runs: map[core.Actor]*run{}}
}

// rejected records a rejection and reports whether this seat has now failed the
// same way often enough to stop. An agent that self-corrects forever without
// succeeding is not self-correcting; it is a flood with good manners.
func (c *corrections) rejected(actor core.Actor, invariant string) (attempts int, exhausted bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.runs[actor]
	if !ok || r.invariant != invariant {
		r = &run{invariant: invariant}
		c.runs[actor] = r
	}
	r.n++
	return r.n, r.n > maxSelfCorrections
}

// accepted clears the run: the seat got somewhere, so the count starts over.
func (c *corrections) accepted(actor core.Actor) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.runs, actor)
}

// maxSelfCorrections is two, matching docs/CLI.md. The first rejection teaches
// the schema; the second says the agent misread it; a third says the invariant
// is not what the agent thinks it is, and no further attempt will discover that.
const maxSelfCorrections = 2

// PostingBudget is how many ambient entries one seat may add to a room per
// window before it has to start summarizing.
//
// This is not the rate limiter above, and the difference is the remedy. The
// limiter guards wire speed — 700 signed posts landed in 227ms against this
// server — and its answer is "wait 800ms". A posting budget guards attention:
// thirty findings an hour is not too fast to accept, it is too many to read,
// and waiting does not help. Its answer is "say it once, together".
//
// Nothing is dropped either way. The agent still holds what it was going to
// say, which is the whole reason the refusal can ask for a summary instead.
const (
	PostingBudget = 30
	PostingWindow = time.Hour
)

// posting is the ambient spend ledger, per seat per room. Per room because a
// bug bash is a room, and a busy hour there should not silence the same agent
// in core.
type posting struct {
	mu    sync.Mutex
	spent map[postingKey][]time.Time
	now   func() time.Time
}

type postingKey struct {
	actor core.Actor
	room  string
}

func newPosting(now func() time.Time) *posting {
	return &posting{spent: map[postingKey][]time.Time{}, now: now}
}

// charge records an ambient post and reports what is left. Addressed kinds are
// not charged here: they are priced by the rate limit and by the fact that a
// person is named, which is a cost the author already feels.
func (p *posting) charge(actor core.Actor, room string, kind core.Kind) (remaining int, oldest time.Duration, ok bool) {
	if exemptFromBudget(kind) || core.LaneOf(kind) == core.Addressed {
		return PostingBudget, 0, true
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	key := postingKey{actor, room}
	now := p.now()
	cutoff := now.Add(-PostingWindow)
	var live []time.Time
	for _, at := range p.spent[key] {
		if at.After(cutoff) {
			live = append(live, at)
		}
	}

	if len(live) >= PostingBudget {
		p.spent[key] = live
		return 0, live[0].Add(PostingWindow).Sub(now), false
	}
	live = append(live, now)
	p.spent[key] = live
	return PostingBudget - len(live), 0, true
}

// release refunds the newest stamp when the charged post kept nothing — a
// rejection, replay, or failed append. Without it a refused command burned an
// ambient slot, and the refusal's "nothing was lost" was false about the
// budget. Mirrors charge's exemptions, so an uncharged kind cannot refund a
// stamp some other post paid for.
func (p *posting) release(actor core.Actor, room string, kind core.Kind) {
	if exemptFromBudget(kind) || core.LaneOf(kind) == core.Addressed {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	key := postingKey{actor, room}
	if n := len(p.spent[key]); n > 0 {
		p.spent[key] = p.spent[key][:n-1]
	}
}
