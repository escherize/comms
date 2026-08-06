// Package shell is the imperative shell: HTTP, SSE, clock, and rendering.
//
// It parses untrusted input into core types exactly once (parse, don't
// validate), authenticates, calls the pure decider, appends what the decider
// returns, and fans the result out. Windows and budgets live here because they
// need a clock and the core has none.
package shell

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bcm/agent_comms/core"
	"github.com/bcm/agent_comms/store"
)

// Clock is an internal adapter seam. Tests replace it with a fake; lease expiry
// and windowing are functions of it, never of a client-supplied timestamp.
type Clock func() time.Time

// Server is the command surface and the room renderer.
type Server struct {
	st    *store.Store
	now   Clock
	mu    sync.Mutex
	subs  map[chan store.Record]string // subscriber -> room
	rooms []string
}

func New(st *store.Store, now Clock) *Server {
	if now == nil {
		now = time.Now
	}
	return &Server{st: st, now: now, subs: map[chan store.Record]string{}}
}

func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /commands", s.postCommand)
	mux.HandleFunc("GET /stream", s.stream)
	mux.HandleFunc("GET /search", s.searchPage)
	mux.HandleFunc("GET /", s.roomPage)
	return mux
}

// ---------------------------------------------------------------- parse

// wireCommand is the untrusted shape. It becomes a core.Command exactly once,
// here, via a total function returning an error rather than a boolean.
type wireCommand struct {
	Room      string         `json:"room"`
	Author    string         `json:"author"`
	Kind      string         `json:"kind"`
	Body      map[string]any `json:"body"`
	Refs      []string       `json:"refs"`
	Idem      string         `json:"idem"`
	Recipient string         `json:"recipient"`
}

func parseCommand(raw []byte) (core.Command, error) {
	var w wireCommand
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil {
		return core.Command{}, fmt.Errorf("malformed command: %w", err)
	}
	if w.Body == nil {
		w.Body = map[string]any{}
	}
	return core.Command{
		Room:      w.Room,
		Author:    core.Actor(w.Author),
		Kind:      core.Kind(w.Kind),
		Body:      w.Body,
		Refs:      w.Refs,
		Idem:      w.Idem,
		Recipient: core.Actor(w.Recipient),
	}, nil
}

// ---------------------------------------------------------------- commands

type acceptedResponse struct {
	Seq     int64 `json:"seq"`
	Applied bool  `json:"applied"` // false when an idempotency key replayed
}

type rejectedResponse struct {
	Invariant string `json:"invariant"`
	Detail    string `json:"detail"`
	Schema    string `json:"schema,omitempty"`
}

func (s *Server) postCommand(w http.ResponseWriter, r *http.Request) {
	raw := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	for {
		n, err := r.Body.Read(buf)
		raw = append(raw, buf[:n]...)
		if err != nil {
			break
		}
		if len(raw) > 1<<20 {
			writeJSON(w, http.StatusRequestEntityTooLarge,
				rejectedResponse{"body.too_large", "command body exceeds 1MiB", ""})
			return
		}
	}

	cmd, err := parseCommand(raw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest,
			rejectedResponse{"parse.failed", err.Error(), ""})
		return
	}

	state := core.State{RoomExists: s.st.RoomExists, EventKind: s.st.EventKind}
	events, rej := core.Decide(state, cmd)
	if rej != nil {
		writeJSON(w, http.StatusUnprocessableEntity,
			rejectedResponse{rej.Invariant, rej.Detail, schemaFor(cmd.Kind)})
		return
	}

	var last int64
	for _, ev := range events {
		seq, err := s.st.Append(ev, cmd.Idem, s.now())
		var dup store.ErrDuplicate
		if errors.As(err, &dup) {
			// The retry is answered from the log rather than re-decided.
			writeJSON(w, http.StatusOK, acceptedResponse{Seq: dup.Seq, Applied: false})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError,
				rejectedResponse{"append.failed", err.Error(), ""})
			return
		}
		last = seq
		s.fanout(ev.Room, seq)
	}
	writeJSON(w, http.StatusOK, acceptedResponse{Seq: last, Applied: true})
}

// schemaFor lets a rejected agent self-correct without a human.
func schemaFor(k core.Kind) string {
	switch k {
	case core.KindFinding:
		return `{"text": string, "severity": "p0"|"p1"|"p2"|"p3"}`
	case core.KindPRLink:
		return `{"url": string}`
	case core.KindQuestion, core.KindAnswer, core.KindHandoff:
		return `{"text": string} + recipient required`
	case core.KindChat, core.KindTIL, core.KindStatus, core.KindDigest:
		return `{"text": string}`
	case core.KindRedact:
		return `{"text": string} + refs must name exactly one event`
	}
	return ""
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// ---------------------------------------------------------------- SSE

func (s *Server) fanout(room string, seq int64) {
	recs, err := s.st.Since(room, seq-1, 1)
	if err != nil || len(recs) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch, subRoom := range s.subs {
		if subRoom != room {
			continue
		}
		select {
		case ch <- recs[0]:
		default: // a slow subscriber resumes by Last-Event-ID rather than blocking the writer
		}
	}
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	room := r.URL.Query().Get("room")
	if room == "" {
		room = "core"
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Resume: replay everything after the client's last seen seq, so a
	// reconnect never silently drops events.
	after := lastEventID(r)
	if backlog, err := s.st.Since(room, after, 500); err == nil {
		for _, rec := range backlog {
			writeSSE(w, rec)
		}
		flusher.Flush()
	}

	ch := make(chan store.Record, 64)
	s.mu.Lock()
	s.subs[ch] = room
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}()

	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case rec := <-ch:
			writeSSE(w, rec)
			flusher.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func lastEventID(r *http.Request) int64 {
	v := r.Header.Get("Last-Event-ID")
	if v == "" {
		v = r.URL.Query().Get("after")
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// writeSSE emits a datastar element patch: the row's HTML appended to the
// ledger body. id: is the seq, which is what makes Last-Event-ID resume work.
func writeSSE(w http.ResponseWriter, rec store.Record) {
	fmt.Fprintf(w, "id: %d\n", rec.Seq)
	fmt.Fprint(w, "event: datastar-patch-elements\n")
	fmt.Fprint(w, "data: mode append\n")
	fmt.Fprint(w, "data: selector #ledger-body\n")
	for _, line := range strings.Split(renderRow(rec), "\n") {
		fmt.Fprintf(w, "data: elements %s\n", line)
	}
	fmt.Fprint(w, "\n")
}
