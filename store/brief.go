package store

import (
	"time"

	"github.com/escherize/comms/core"
)

// Brief is what an agent reads before it decides anything: what is in flight,
// who has gone quiet, and which questions are still waiting. One call, so the
// alternative — reading the whole room and inferring all three — stops being
// the cheaper option.
type Brief struct {
	Room      string         `json:"room"`
	Head      int64          `json:"head"`
	Events    int            `json:"events"`
	Working   []Working      `json:"working"`
	Ambient   map[string]int `json:"ambient"`
	Questions []OpenQuestion `json:"questions"`
	AsOf      time.Time      `json:"as_of"`

	// Recent is what the room already knows, not how much of it there is. A
	// second agent arriving into an active room was told "ambient chat 1, head
	// 50000" and discovered only after posting its own first finding that
	// seventeen were already there — by which point the knowledge that would
	// have changed what it did had arrived too late.
	Recent    []Brief_Entry `json:"recent"`
	Addressed []Brief_Entry `json:"addressed"`
	Delivery  []Delivered   `json:"delivery"`
}

// Brief_Entry is one line of what the room already contains.
type Brief_Entry struct {
	Seq       int64  `json:"seq"`
	Kind      string `json:"kind"`
	Author    string `json:"author"`
	Recipient string `json:"recipient,omitempty"`
	About     string `json:"about,omitempty"`
	Severity  string `json:"severity,omitempty"`
	Text      string `json:"text"`
}

// Working is one actor's progress, with the stall verdict already applied so
// every reader gets the same answer from the same window.
type Working struct {
	Author  string    `json:"author"`
	Step    int       `json:"step"`
	Of      int       `json:"of"`
	Note    string    `json:"note"`
	Updated time.Time `json:"updated"`
	Stalled bool      `json:"stalled"`
	QuietMS int64     `json:"quiet_ms"`
}

// OpenQuestion is a question and its fate. Answered ones carry the answering
// seq; unanswered ones carry how long they have waited, because "unanswered"
// without an age does not tell an agent whether to ask again or wait.
type OpenQuestion struct {
	Seq       int64  `json:"seq"`
	Author    string `json:"author"`
	Recipient string `json:"recipient,omitempty"`
	Text      string `json:"text,omitempty"`
	Answered  bool   `json:"answered"`
	AnswerSeq int64  `json:"answer_seq,omitempty"`
	// WaitingMS is always present on an open question, zero included: an absent
	// field and a fresh question are the same JSON, and the reader has to tell
	// "nobody has answered for an hour" from "asked a second ago".
	WaitingMS int64 `json:"waiting_ms"`
}

// RoomBrief reads the decision projections, never the log. The question fold is
// maintained in the append transaction, so this is an indexed read rather than
// a json_each scan over every event's refs.
func (s *Store) RoomBrief(room string, now time.Time) (Brief, error) {
	b := Brief{Room: room, Ambient: map[string]int{}, AsOf: now.UTC()}

	if err := s.db.QueryRow(
		`SELECT COALESCE(MAX(seq),0), COUNT(*) FROM envelope WHERE room = ?`, room,
	).Scan(&b.Head, &b.Events); err != nil {
		return b, err
	}

	rows, err := s.db.Query(
		`SELECT kind, COUNT(*) FROM envelope WHERE room = ? AND lane = ? GROUP BY kind`,
		room, int(core.Ambient))
	if err != nil {
		return b, err
	}
	for rows.Next() {
		var kind string
		var n int
		if err := rows.Scan(&kind, &n); err != nil {
			rows.Close()
			return b, err
		}
		b.Ambient[kind] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return b, err
	}

	progress, err := s.ProgressFor(room)
	if err != nil {
		return b, err
	}
	for _, p := range progress {
		b.Working = append(b.Working, Working{
			Author: p.Author, Step: p.Step, Of: p.Of, Note: p.Note,
			Updated: p.Updated.UTC(),
			Stalled: p.Stalled(now, StallWindow),
			QuietMS: now.Sub(p.Updated).Milliseconds(),
		})
	}

	// The last of what is worth knowing, in both lanes. Bounded because a brief
	// that is the room is not a brief.
	if recs, err := s.Latest(room, 400); err == nil {
		for i := len(recs) - 1; i >= 0 && (len(b.Recent) < 8 || len(b.Addressed) < 8); i-- {
			r := recs[i]
			if r.Redacted || r.BodyErased {
				continue
			}
			entry := Brief_Entry{
				Seq: r.Seq, Kind: string(r.Kind), Author: string(r.Author),
				Recipient: string(r.Recipient), About: r.About(),
				Severity: r.Severity(), Text: truncateEntry(r.Text(), 120),
			}
			switch {
			case r.Lane == core.Addressed && len(b.Addressed) < 8:
				b.Addressed = append(b.Addressed, entry)
			case r.Kind == core.KindFinding || r.Kind == core.KindTIL:
				if len(b.Recent) < 8 {
					b.Recent = append(b.Recent, entry)
				}
			}
		}
	}
	if d, err := s.DeliveryFor(room); err == nil {
		b.Delivery = d
	}

	qrows, err := s.db.Query(
		`SELECT q.seq, q.author, q.recipient, q.asked_at, q.answer_seq,
		        COALESCE(json_extract(b.json, '$.text'), '')
		   FROM question q LEFT JOIN body b ON b.seq = q.seq
		  WHERE q.room = ?
		  ORDER BY q.answer_seq = 0 DESC, q.seq`, room)
	if err != nil {
		return b, err
	}
	defer qrows.Close()
	for qrows.Next() {
		var q OpenQuestion
		var asked string
		if err := qrows.Scan(&q.Seq, &q.Author, &q.Recipient, &asked, &q.AnswerSeq, &q.Text); err != nil {
			return b, err
		}
		q.Answered = q.AnswerSeq != 0
		if !q.Answered {
			if at, err := time.Parse(time.RFC3339Nano, asked); err == nil {
				q.WaitingMS = now.Sub(at).Milliseconds()
			}
		}
		b.Questions = append(b.Questions, q)
	}
	return b, qrows.Err()
}

// Delivery: how far each seat has drained its addressed lane.
//
// A handoff is typed, signed, permanent, addressed — and completely advisory.
// In the 2026-08-07 study a coordinator handed out two slices, both landed in
// under a second, and both agents worked a third; it found out six minutes
// later by reading their findings. The room could not represent "I got this and
// I am not doing it", so divergence and silence looked identical.
//
// This is scoped to the **addressed** lane on purpose. Private ambient read
// state is a real design position, not an oversight: an agent should not be
// judged on cursor position and a human should not feel watched. What is
// published is only whether responsibility that was transferred has been
// picked up, which is the thing the sender is entitled to know.
//
// It is not an event. Delivery is operational state — true now, uninteresting
// in six months — and putting it in the log would double the log's volume with
// rows nobody will ever search for.
const deliverySchema = `
CREATE TABLE IF NOT EXISTS delivery (
  actor TEXT NOT NULL,
  room  TEXT NOT NULL,
  addressed_through INTEGER NOT NULL DEFAULT 0,
  at    TEXT NOT NULL,
  PRIMARY KEY (actor, room)
);
`

// MarkDelivered records that a seat has drained its addressed lane to a seq.
// It never moves backwards: a client replaying an old window must not make the
// room believe less has been read than has been.
func (s *Store) MarkDelivered(actor, room string, through int64, now time.Time) error {
	_, err := s.db.Exec(
		`INSERT INTO delivery(actor, room, addressed_through, at) VALUES(?,?,?,?)
		 ON CONFLICT(actor, room) DO UPDATE SET
		   addressed_through = MAX(delivery.addressed_through, excluded.addressed_through),
		   at = CASE WHEN excluded.addressed_through > delivery.addressed_through
		             THEN excluded.at ELSE delivery.at END`,
		actor, room, through, now.UTC().Format(time.RFC3339Nano))
	return err
}

// Delivered is one seat's addressed watermark.
type Delivered struct {
	Actor   string `json:"actor"`
	Through int64  `json:"addressed_through"`
	At      string `json:"at"`
	// Pending is what is addressed to this seat above the watermark: the number
	// the sender actually wants, since "drained to 50011" means nothing without
	// knowing what came after.
	Pending int `json:"pending"`
}

// DeliveryFor reports, per seat with anything addressed to it in this room, how
// much of that it has drained.
func (s *Store) DeliveryFor(room string) ([]Delivered, error) {
	rows, err := s.db.Query(`
		SELECT e.recipient,
		       COALESCE(d.addressed_through, 0),
		       COALESCE(d.at, ''),
		       SUM(CASE WHEN e.seq > COALESCE(d.addressed_through, 0) THEN 1 ELSE 0 END)
		  FROM envelope e
		  LEFT JOIN delivery d ON d.actor = e.recipient AND d.room = e.room
		 WHERE e.room = ? AND e.recipient <> '' AND e.lane = ?
		 GROUP BY e.recipient
		 ORDER BY e.recipient`, room, int(core.Addressed))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Delivered
	for rows.Next() {
		var d Delivered
		if err := rows.Scan(&d.Actor, &d.Through, &d.At, &d.Pending); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// truncateEntry clips a brief line on a rune boundary.
func truncateEntry(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
