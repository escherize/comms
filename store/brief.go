package store

import (
	"time"

	"github.com/bcm/agent_comms/core"
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
