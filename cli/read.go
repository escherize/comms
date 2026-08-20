package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// readDeadline bounds a single read. The stream sends a `: ping` comment every
// 25s, so any of them resets this — a healthy idle stream must not trip it.
const readDeadline = 60 * time.Second

// maxWait caps --wait so a stuck agent surfaces instead of silently occupying a
// tool slot for an hour.
const maxWait = 30 * time.Minute

// frame is one thing the read lane sent us.
type frame struct {
	Event string
	Data  map[string]any
	Seq   int64
}

// readOpts is what both verbs need. They differ only in lane and filter.
type readOpts struct {
	Actor     string
	Room      string
	Lane      Lane
	Recipient string
	Author    string
	Full      bool
	Peek      bool
	Wait      time.Duration
	UntilRefs string
	// From replays from a seq instead of the cursor. Replay never moves the
	// cursor: re-reading is not reading, and a lead reconstructing an hour of
	// its crew's findings must not lose its place doing it.
	From  int64
	Since time.Duration
}

// readRefused is a non-200 answer to a read: the server refused, so retrying
// cannot help. Distinct from a transport error, which fixes itself.
type readRefused struct {
	status int
	room   string
}

func (r *readRefused) Error() string {
	return fmt.Sprintf("this seat cannot read %s (HTTP %d): the room does not exist "+
		"or the seat is not a member — comms room lists yours", r.room, r.status)
}

// drain reads until the caught-up sentinel, or until wait expires. It never
// hangs on a quiet room: caught-up always arrives, on an empty room too.
func drain(e *Env, o readOpts) (events []frame, meta map[string]any, err error) {
	q := url.Values{}
	q.Set("room", o.Room)
	if o.Recipient != "" {
		q.Set("recipient", o.Recipient)
	}
	after := Cursor(o.Actor, o.Room, o.Lane)
	if o.From > 0 {
		after = o.From - 1 // inclusive: --from 50014 prints 50014
	}
	if o.Since > 0 {
		after = 0 // the server has no time index; filter locally below
		q.Set("since", fmt.Sprint(time.Now().Add(-o.Since).UTC().Format(time.RFC3339)))
	}

	// No client timeout: the deadline is enforced per-read below, so a ping
	// counts toward liveness and a long --wait is not cut short by the client.
	resp, err := doRead(e, &http.Client{}, func() (*http.Request, error) {
		req, err := http.NewRequest("GET", e.Server+"/stream?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		if after > 0 {
			req.Header.Set("Last-Event-ID", fmt.Sprint(after))
		}
		return req, nil
	})
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	// A refusal parsed as an empty stream was an unwinnable retry loop: the
	// server's 404 (unknown room, or not a member — hidden alike) drained to
	// zero events and "read again". Surface it as its own error so callers can
	// stop retrying.
	if resp.StatusCode != http.StatusOK {
		return nil, nil, &readRefused{status: resp.StatusCode, room: o.Room}
	}

	meta = map[string]any{}
	deadline := time.Now().Add(readDeadline)
	hardStop := time.Time{}
	if o.Wait > 0 {
		hardStop = time.Now().Add(o.Wait)
	}

	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()

	var event string
	var seq int64
	caughtUp := false
	waitDone := false

	for {
		remaining := time.Until(deadline)
		if !hardStop.IsZero() {
			if untilStop := time.Until(hardStop); untilStop < remaining {
				remaining = untilStop
			}
		}
		if remaining <= 0 {
			if !caughtUp {
				return events, meta, fmt.Errorf(
					"the stream went quiet for %s mid-read; nothing was lost and the cursor did not advance — run the same command again", readDeadline)
			}
			return events, meta, nil
		}

		select {
		case line, open := <-lines:
			if !open {
				meta["gap_possible"] = true
				return events, meta, nil
			}
			switch {
			case strings.HasPrefix(line, ": "):
				// A ping proves the stream is alive.
				deadline = time.Now().Add(readDeadline)
			case strings.HasPrefix(line, "id: "):
				fmt.Sscanf(strings.TrimPrefix(line, "id: "), "%d", &seq)
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				deadline = time.Now().Add(readDeadline)
				var d map[string]any
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &d) != nil {
					continue
				}
				switch event {
				case "hello":
					meta["boot"] = d["boot"]
				case "truncated":
					// Surfaced as a fact, never inferred from a seq delta.
					meta["truncated"] = true
					meta["delivered_through_seq"] = d["delivered_through_seq"]
				case "lagged":
					meta["gap_possible"] = true
					return events, meta, nil
				case "caught-up":
					caughtUp = true
					meta["caught_up_seq"] = d["seq"]
					// History satisfies a wait too: if the backlog already
					// held what the wait was for, hand it all over now.
					// Waiting past caught-up is only for a reader whose
					// target has not arrived yet.
					if o.Wait == 0 || waitDone {
						return events, meta, nil
					}
				case "event":
					f := frame{Event: event, Data: d, Seq: seq}
					if !matchesLocal(f, o) {
						continue
					}
					events = append(events, f)
					if o.Wait > 0 && satisfiesWait(f, o) {
						// Ending the wait on a *backlog* event made every
						// catch-up a one-event-per-call crawl (three study
						// agents filed "wait does not wait"). History drains
						// to caught-up first; only a live arrival returns
						// immediately.
						if caughtUp {
							return events, meta, nil
						}
						waitDone = true
					}
				}
			}
		case <-time.After(remaining):
			if !caughtUp {
				return events, meta, fmt.Errorf(
					"the stream went quiet for %s mid-read; nothing was lost and the cursor did not advance — run the same command again", readDeadline)
			}
			return events, meta, nil
		}
	}
}

// matchesLocal applies the filters the server does not: author is a client-side
// cut, which is why it implies --peek.
func matchesLocal(f frame, o readOpts) bool {
	if o.Author != "" && f.Data["author"] != o.Author {
		return false
	}
	if o.Since > 0 {
		ts, ok := f.Data["ts"].(string)
		if !ok {
			return false
		}
		at, err := time.Parse(time.RFC3339, ts)
		if err != nil || at.Before(time.Now().Add(-o.Since)) {
			return false
		}
	}
	return true
}

func satisfiesWait(f frame, o readOpts) bool {
	if o.UntilRefs != "" {
		refs, _ := f.Data["refs"].([]any)
		var hit bool
		for _, r := range refs {
			if fmt.Sprint(r) == o.UntilRefs {
				hit = true
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

// emit prints the events and the terminal object, and advances the cursor
// unless this was a peek.
func emit(e *Env, o readOpts, events []frame, meta map[string]any) int {
	var highest int64
	for _, f := range events {
		if o.Full {
			e.Out.Line(f.Data)
		} else {
			e.Out.Line(compact(f))
		}
		if f.Seq > highest {
			highest = f.Seq
		}
	}

	// A filtered read must not advance a cursor past events it did not print,
	// or the unprinted ones are lost silently.
	advanced := int64(0)
	if !o.Peek {
		if seq, ok := meta["caught_up_seq"].(float64); ok && int64(seq) > highest {
			highest = int64(seq)
		}
		if highest > 0 {
			if err := SaveCursor(o.Actor, o.Room, o.Lane, highest); err != nil {
				return e.Out.Fail(ExitInternal, "internal", "cursor.unwritable", err.Error())
			}
			advanced = highest
			// Tell the room that responsibility handed to this seat has been
			// picked up. Addressed only: ambient read state stays private,
			// because an agent should not be judged on cursor position.
			if o.Lane == LaneAddressed {
				reportDelivered(e, o.Actor, o.Room, highest)
			}
		}
	}

	r := Result{Outcome: "read", Count: len(events)}
	term := map[string]any{
		"ok": true, "outcome": r.Outcome, "count": r.Count,
		"room": o.Room, "lane": string(o.Lane),
	}
	// count:0 has two meanings and an agent has to tell them apart: "I am
	// current" is a reason to work, "nobody has said anything ever" is a reason
	// to check whether the other seat has started.
	if len(events) == 0 {
		if head, ok := meta["caught_up_seq"].(float64); ok && head > 0 {
			term["state"] = "caught-up"
			term["head"] = int64(head)
			term["detail"] = "nothing new since your cursor; the room has content above it"
		} else {
			term["state"] = "empty"
			term["detail"] = "no events in this room and lane at all"
		}
	}

	if advanced > 0 {
		term["cursor"] = advanced
	}
	if o.Peek {
		term["peek"] = true
		term["detail"] = "a filtered read does not advance the cursor"
	}
	// A replay says so last: it is the more specific reason the cursor stayed
	// put, and the reader should be told re-reading is not reading.
	if o.From > 0 || o.Since > 0 {
		term["replay"] = true
		term["detail"] = "a replay does not advance the cursor; re-reading is not reading"
	}
	if meta["truncated"] == true {
		term["truncated"] = true
		term["delivered_through_seq"] = meta["delivered_through_seq"]
		term["next"] = "read again to continue from where the backlog stopped"
	}
	if meta["gap_possible"] == true {
		term["gap_possible"] = true
		term["next"] = "read again; the stream ended early and events may be unread"
	}
	if boot, ok := meta["boot"]; ok {
		term["boot"] = boot
	}
	e.Out.Line(term)
	e.Out.Note("%d event(s) in %s", len(events), o.Room)
	return ExitOK
}

// compact is one line per event: enough to decide whether to fetch the body.
func compact(f frame) map[string]any {
	out := map[string]any{
		"type": "event", "seq": f.Data["seq"], "author": f.Data["author"],
		"kind": f.Data["kind"], "lane": f.Data["lane"],
	}
	if r, ok := f.Data["recipient"]; ok && r != "" {
		out["recipient"] = r
	}
	if body, ok := f.Data["body"].(map[string]any); ok {
		if txt, ok := body["text"].(string); ok {
			preview, clipped := truncateText(txt, 120)
			out["preview"] = preview
			if clipped {
				// An ellipsis alone reads as authorial style. An agent that
				// mistakes a clipped handoff for a garbled one asks its lead to
				// re-send a message that arrived intact — which is what happened
				// on 2026-08-07.
				out["truncated"] = true
				out["full_chars"] = len(txt)
				out["next"] = fmt.Sprintf(
					"read it whole: comms read --from %v --full", f.Data["seq"])
			}
		}
		if sev, ok := body["severity"]; ok {
			out["severity"] = sev
		}
	}
	if f.Data["redacted"] == true {
		out["redacted"] = true
	}
	if ks, ok := f.Data["author_key_status"]; ok && ks != "active" && ks != "" {
		out["author_key_status"] = ks
	}
	if f.Data["flagged"] == true {
		out["flagged"] = true
	}
	if att, ok := f.Data["attach"].([]any); ok && len(att) > 0 {
		out["attachments"] = len(att)
	}
	return out
}

// truncateText clips to a rune boundary. Slicing bytes splits a multi-byte
// rune, and the resulting lone continuation byte renders as a replacement
// character in every surface — terminal, browser, room row — which is
// indistinguishable from the ellipsis that belongs there. The corruption is
// invisible exactly where someone would look for it.
func truncateText(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s, false
	}
	return string(runes[:n-1]) + "…", true
}

// first drops the clipped flag where the caller only wants the text.
func first(s string, _ bool) string { return s }

// reportDelivered posts a delivery receipt. It is best effort by design: a
// receipt that failed to send must not fail the read that earned it, because
// the events have already been printed and the cursor has already moved.
func reportDelivered(e *Env, actor, room string, through int64) {
	body, err := json.Marshal(map[string]any{
		"actor": actor, "room": room, "addressed_through": through,
	})
	if err != nil {
		return
	}
	if resp, err := doRead(e, nil, func() (*http.Request, error) {
		req, err := http.NewRequest("POST", e.Server+"/delivered", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	}); err == nil {
		resp.Body.Close()
	}
}
