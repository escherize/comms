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
	"io"
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
	mux.HandleFunc("POST /artifacts", s.postArtifact)
	mux.HandleFunc("GET /a/{hash}", s.getArtifact)
	mux.HandleFunc("GET /stream", s.stream)
	mux.HandleFunc("GET /search", s.searchPage)
	mux.HandleFunc("GET /", s.roomPage)
	return mux
}

// postArtifact stores GFM content-addressed and returns its hash. Only markdown
// is accepted: a stored HTML blob would be markup noise in the search index and,
// far worse, agent-authored script one render away from a human's session
// (ADR-0011).
func (s *Server) postArtifact(w http.ResponseWriter, r *http.Request) {
	if ct := r.Header.Get("Content-Type"); ct != "" &&
		!strings.HasPrefix(ct, "text/markdown") && !strings.HasPrefix(ct, "text/plain") {
		writeJSON(w, http.StatusUnsupportedMediaType, rejectedResponse{
			"media_type.unsupported",
			"artifacts are stored as GitHub-Flavored Markdown; got " + ct,
			"Content-Type: text/markdown",
		})
		return
	}

	content, err := readLimited(r.Body, 4<<20)
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge,
			rejectedResponse{"artifact.too_large", err.Error(), ""})
		return
	}
	if len(content) == 0 {
		writeJSON(w, http.StatusBadRequest,
			rejectedResponse{"artifact.empty", "an artifact needs content", ""})
		return
	}

	hash, err := s.st.PutArtifact(content, s.now())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError,
			rejectedResponse{"artifact.store_failed", err.Error(), ""})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hash": hash, "size": len(content)})
}

// getArtifact renders stored markdown as sanitized HTML. The stored bytes are
// never served raw.
func (s *Server) getArtifact(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	content, ok := s.st.GetArtifact(hash)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Defence in depth behind the sanitizer: even if something slipped through
	// the allowlist, the page may not load or execute anything external.
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; img-src data:; sandbox")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	fmt.Fprint(w, artifactPage(hash, RenderMarkdown(content)))
}

func readLimited(rc io.Reader, max int) ([]byte, error) {
	buf := make([]byte, 0, 8192)
	chunk := make([]byte, 8192)
	for {
		n, err := rc.Read(chunk)
		buf = append(buf, chunk[:n]...)
		if len(buf) > max {
			return nil, fmt.Errorf("content exceeds %d bytes", max)
		}
		if err != nil {
			return buf, nil
		}
	}
}

// ---------------------------------------------------------------- parse

// wireCommand is the untrusted shape. It becomes a core.Command exactly once,
// here, via a total function returning an error rather than a boolean.
type wireCommand struct {
	Room        string         `json:"room"`
	Author      string         `json:"author"`
	Kind        string         `json:"kind"`
	Body        map[string]any `json:"body"`
	Refs        []string       `json:"refs"`
	Idem        string         `json:"idem"`
	Recipient   string         `json:"recipient"`
	Attachments []struct {
		Hash  string `json:"hash"`
		Title string `json:"title"`
	} `json:"attachments"`
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
	// Attachment hashes are shape-checked here, at the parse boundary, so a
	// malformed reference is a typed parse failure rather than a DB miss later.
	var atts []core.Attachment
	for _, a := range w.Attachments {
		if !store.ValidHash(a.Hash) {
			return core.Command{}, fmt.Errorf(
				"attachment hash must be 64 lowercase hex chars, got %q", a.Hash)
		}
		atts = append(atts, core.Attachment{Hash: a.Hash, Title: a.Title})
	}

	return core.Command{
		Room:        w.Room,
		Author:      core.Actor(w.Author),
		Kind:        core.Kind(w.Kind),
		Body:        w.Body,
		Refs:        w.Refs,
		Idem:        w.Idem,
		Recipient:   core.Actor(w.Recipient),
		Attachments: atts,
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

	state := core.State{
		RoomExists:     s.st.RoomExists,
		EventKind:      s.st.EventKind,
		ArtifactExists: s.st.ArtifactExists,
	}
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
	case core.KindStatus:
		return `{"text": string, "step": int?, "of": int?}`
	case core.KindChat, core.KindTIL, core.KindDigest:
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
