package shell

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bcm/agent_comms/core"
	"github.com/bcm/agent_comms/store"
)

// The digest bot.
//
// It is a goroutine that submits commands, not a privileged writer. Its posts
// go through parse → verify → decide → append like anyone's, and the only thing
// that distinguishes it is a capability grant the core checks (ticket 26): a
// digest is addressed by definition, so an agent that could post one could
// interrupt everyone for free, on a loop.
//
// What it produces is a fold over a window, and the window is what makes it
// worth reading. A summary of "everything" is the room; a summary of "since I
// last said anything" is news.

// DigestBot rolls a room's ambient activity into one addressed entry.
type DigestBot struct {
	Actor core.Actor
	Room  string
	// To is who the digest is addressed to. A digest with no recipient would be
	// ambient, which is a summary that interrupts nobody and is therefore a
	// second copy of the room.
	To core.Actor
	// Every is how often the bot considers posting. It is not how often it
	// posts: a quiet window produces nothing.
	Every time.Duration
}

// digestWindow is what changed since a point, folded into the shape a person
// reads: what shipped, what is stuck, what got learned.
type digestWindow struct {
	Since    int64
	Head     int64
	Shipped  []string
	Stuck    []string
	Learned  []string
	Findings map[string]int // severity -> count
	Open     []store.OpenQuestion
	Events   int
}

// empty reports a window with nothing worth interrupting anyone about. A digest
// that says "nothing happened" is a digest that teaches people to ignore
// digests, and the next one that matters arrives already discounted.
func (w digestWindow) empty() bool {
	return len(w.Shipped) == 0 && len(w.Stuck) == 0 && len(w.Learned) == 0 &&
		len(w.Findings) == 0 && len(w.Open) == 0
}

// lastDigest finds where the previous digest stopped, which is where this one
// starts. It reads the log rather than keeping a cursor: the log is the state,
// and a cursor would be a second place for "how far have I summarized" to live
// and disagree.
func lastDigestSeq(st *store.Store, room string, author core.Actor) int64 {
	recs, err := st.Latest(room, 2000)
	if err != nil {
		return 0
	}
	var last int64
	for _, r := range recs {
		if r.Kind == core.KindDigest && r.Author == author {
			last = r.Seq
		}
	}
	return last
}

// gather folds the window between two seqs.
func gather(st *store.Store, room string, since int64, now time.Time) digestWindow {
	w := digestWindow{Since: since, Findings: map[string]int{}}

	recs, err := st.Since(room, since, 2000)
	if err != nil {
		return w
	}
	for _, r := range recs {
		w.Head = r.Seq
		if r.Redacted || r.BodyErased {
			// A redacted body is not summarized. Re-stating it in a digest is
			// the suppression undone by the surface that exists to help people
			// catch up.
			continue
		}
		w.Events++
		switch r.Kind {
		case core.KindPRLink:
			if u := r.URL(); u != "" {
				w.Shipped = append(w.Shipped, u)
			}
		case core.KindTIL:
			w.Learned = append(w.Learned, truncate(r.Text(), 90))
		case core.KindFinding:
			sev := r.Severity()
			if sev == "" {
				sev = "p?"
			}
			w.Findings[sev]++
		}
	}

	// Stuck is not a kind. It is the progress projection saying somebody stopped
	// — the one thing in a digest nobody would otherwise notice, because an
	// agent that goes quiet posts nothing to notice.
	if progress, err := st.ProgressFor(room); err == nil {
		for _, p := range progress {
			if p.Stalled(now, store.StallWindow) {
				w.Stuck = append(w.Stuck, fmt.Sprintf("%s, %s at %d/%d",
					shortActor(core.Actor(p.Author)), since2(now, p.Updated), p.Step, p.Of))
			}
		}
		sort.Strings(w.Stuck)
	}

	if brief, err := st.RoomBrief(room, now); err == nil {
		for _, q := range brief.Questions {
			if !q.Answered && q.Seq > since {
				w.Open = append(w.Open, q)
			}
		}
	}
	return w
}

// render writes the window as prose a person reads once. Not a table: a digest
// is read in the room, inline, between other entries, and a table there is a
// wall the eye slides off.
func (w digestWindow) render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "since %d: %d entries.", w.Since, w.Events)

	if len(w.Shipped) > 0 {
		fmt.Fprintf(&b, " Shipped: %s.", strings.Join(w.Shipped, ", "))
	}
	if len(w.Findings) > 0 {
		var parts []string
		for _, sev := range []string{"p0", "p1", "p2", "p3", "p?"} {
			if n := w.Findings[sev]; n > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", n, sev))
			}
		}
		fmt.Fprintf(&b, " Findings: %s.", strings.Join(parts, ", "))
	}
	if len(w.Learned) > 0 {
		fmt.Fprintf(&b, " Learned: %s.", strings.Join(w.Learned, "; "))
	}
	if len(w.Open) > 0 {
		var parts []string
		for _, q := range w.Open {
			parts = append(parts, fmt.Sprintf("%d (%s)", q.Seq, shortActor(core.Actor(q.Author))))
		}
		fmt.Fprintf(&b, " Still unanswered: %s.", strings.Join(parts, ", "))
	}
	if len(w.Stuck) > 0 {
		fmt.Fprintf(&b, " Quiet: %s.", strings.Join(w.Stuck, "; "))
	}
	return b.String()
}

// step considers posting one digest and reports whether it did. It goes through
// the same decider every command goes through, so the capability check, the
// lane rule and the recipient rule all apply to it exactly as they would to an
// agent that tried this.
func (s *Server) digestStep(bot DigestBot, now time.Time) (bool, error) {
	since := lastDigestSeq(s.st, bot.Room, bot.Actor)
	w := gather(s.st, bot.Room, since, now)
	if w.empty() {
		return false, nil
	}

	cmd := core.Command{
		Room: bot.Room, Author: bot.Actor, Kind: core.KindDigest,
		Recipient: bot.To,
		Body:      map[string]any{"text": w.render(), "since": float64(since)},
		Idem:      fmt.Sprintf("digest-%s-%d", bot.Room, w.Head),
	}
	events, rej := core.Decide(s.decisionState(), cmd)
	if rej != nil {
		return false, fmt.Errorf("%s: %s", rej.Invariant, rej.Detail)
	}
	for _, ev := range events {
		seq, err := s.st.Append(ev, cmd.Idem, now)
		if err != nil {
			// A duplicate is the bot having already summarized this head, which
			// is the idempotency key doing its job rather than an error.
			if strings.Contains(err.Error(), "duplicate") {
				return false, nil
			}
			return false, err
		}
		s.fanout(ev.Room, seq)
	}
	return true, nil
}

// RunDigest drives the bot until ctx ends.
func (s *Server) RunDigest(ctx context.Context, bot DigestBot) {
	if bot.Every <= 0 {
		bot.Every = time.Hour
	}
	t := time.NewTicker(bot.Every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.digestStep(bot, s.now())
		}
	}
}

// since2 is the human phrasing of a gap, for the one line in a digest where a
// duration reads better than a timestamp.
func since2(now, then time.Time) string {
	d := now.Sub(then)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}
