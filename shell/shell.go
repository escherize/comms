// Package shell is the imperative shell: HTTP, SSE, clock, and rendering.
//
// It parses untrusted input into core types exactly once (parse, don't
// validate), authenticates, calls the pure decider, appends what the decider
// returns, and fans the result out. Windows and budgets live here because they
// need a clock and the core has none.
package shell

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

	// RequireSignature makes authentication mandatory. It defaults to on; the
	// only way to turn it off is an explicit flag, so an unauthenticated
	// deployment is a decision someone made rather than one they inherited.
	RequireSignature bool

	limit   *limiter
	correct *corrections
}

// PostsPerMinute and PostBurst bound one seat. The burst is what an agent
// posting a finished batch of findings legitimately needs; the rate is what a
// room can be read at.
const (
	PostsPerMinute = 60
	PostBurst      = 20
)

func New(st *store.Store, now Clock) *Server {
	if now == nil {
		now = time.Now
	}
	return &Server{st: st, now: now, subs: map[chan store.Record]string{},
		RequireSignature: true,
		limit:            newLimiter(PostsPerMinute, PostBurst, now),
		correct:          newCorrections()}
}

func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /commands", s.postCommand)
	mux.HandleFunc("POST /keys", s.postKey)
	mux.HandleFunc("POST /artifacts", s.postArtifact)
	mux.HandleFunc("GET /a/{hash}", s.getArtifact)
	mux.HandleFunc("GET /stream", s.stream)
	mux.HandleFunc("GET /rooms", s.roomsList)
	mux.HandleFunc("GET /rooms/{name}", s.roomBrief)
	mux.HandleFunc("GET /actors", s.actorsList)
	mux.HandleFunc("GET /search", s.searchPage)
	mux.HandleFunc("GET /", s.roomPage)
	return mux
}

// postKey enrols an actor's public key against a one-time invite token.
//
// The token is what stops this being trust-on-first-use: without it, whoever
// POSTed an actor name first would own it, including yours. The operator mints
// tokens with -invite and hands them over out of band.
func (s *Server) postKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Actor     string `json:"actor"`
		PublicKey string `json:"public_key"`
		Token     string `json:"token"`
	}
	raw, err := readLimited(r.Body, 8192)
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge,
			rejectedResponse{"body.too_large", err.Error(), ""})
		return
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		writeJSON(w, http.StatusBadRequest,
			rejectedResponse{"parse.failed", err.Error(), ""})
		return
	}

	pub, err := hex.DecodeString(req.PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		writeJSON(w, http.StatusBadRequest, rejectedResponse{
			"public_key.invalid",
			fmt.Sprintf("public_key must be %d hex-encoded bytes", ed25519.PublicKeySize), ""})
		return
	}
	if req.Actor == "" {
		writeJSON(w, http.StatusBadRequest,
			rejectedResponse{"actor.required", "enrolment names an actor", ""})
		return
	}

	if err := s.st.RedeemInvite(req.Token, req.Actor, pub, s.now()); err != nil {
		writeJSON(w, http.StatusForbidden, rejectedResponse{
			"enrolment.refused", err.Error(),
			"mint one with: agent_comms -invite <actor>"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"actor": req.Actor, "enrolled": true})
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

// decodeSig parses the hex signature header.
func decodeSig(h string) ([]byte, error) {
	if h == "" {
		return nil, errors.New("no X-Signature header")
	}
	sig, err := hex.DecodeString(strings.TrimSpace(h))
	if err != nil {
		return nil, fmt.Errorf("X-Signature must be hex: %w", err)
	}
	if len(sig) != ed25519.SignatureSize {
		return nil, fmt.Errorf("X-Signature must be %d bytes, got %d",
			ed25519.SignatureSize, len(sig))
	}
	return sig, nil
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

	// Authentication is the shell's job and happens before the decider sees
	// anything: is this signature valid for a registered, unrevoked key. The
	// signature covers the exact bytes received, not a re-serialized object,
	// so a swapped room or author fails here rather than being trusted.
	// Authorization — may this actor do this, in this state — is entirely the
	// core's, below.
	if s.RequireSignature {
		sig, err := decodeSig(r.Header.Get("X-Signature"))
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, rejectedResponse{
				"signature.missing",
				"every command must carry X-Signature: a hex ed25519 signature over the request body",
				"X-Signature: <128 hex chars>"})
			return
		}
		if err := s.st.VerifySignature(cmd.Author, raw, sig, s.now()); err != nil {
			var af store.AuthFailure
			if errors.As(err, &af) {
				writeJSON(w, http.StatusUnauthorized,
					rejectedResponse{af.Invariant, af.Detail, ""})
				return
			}
			writeJSON(w, http.StatusUnauthorized,
				rejectedResponse{"signature.invalid", err.Error(), ""})
			return
		}
	}

	// Per-key rate limit. 429 carries retry_after_ms because "slow down" without
	// a number is an invitation to guess, and every agent guesses differently.
	if s.limit != nil {
		if ok, wait := s.limit.allow(cmd.Author, cmd.Kind); !ok {
			ms := wait.Milliseconds()
			if ms < 1 {
				ms = 1
			}
			w.Header().Set("Retry-After", fmt.Sprint((ms+999)/1000))
			writeJSON(w, http.StatusTooManyRequests, map[string]any{
				"ok": false, "outcome": "throttled", "exit": 6,
				"invariant":      "rate.exceeded",
				"detail":         "this seat is posting faster than the room can be read",
				"retry_after_ms": ms,
				"next":           "sleep retry_after_ms, then batch what you were going to say",
			})
			return
		}
	}

	// A shorthand recipient is expanded here, at the boundary, so the browser's
	// /ask @sarah and the client's --to sarah name the same seat. The core sees
	// a canonical actor or a rejection, never a guess.
	if cmd.Recipient != "" {
		switch matches := s.st.ResolveActor(string(cmd.Recipient)); len(matches) {
		case 1:
			cmd.Recipient = core.Actor(matches[0])
		case 0:
			// Leave it; the decider reports recipient.unknown with its own detail.
		default:
			writeJSON(w, http.StatusUnprocessableEntity, rejectedResponse{
				"recipient.ambiguous",
				string(cmd.Recipient) + " matches " + strings.Join(matches, " and ") +
					"; name the seat in full", ""})
			return
		}
	}

	state := core.State{
		RoomExists:     s.st.RoomExists,
		EventKind:      s.st.EventKind,
		ArtifactExists: s.st.ArtifactExists,
		EventAuthor:    s.st.EventAuthor,
		EventRoom:      s.st.EventRoom,
		IsRedacted:     s.st.IsRedactedRef,
		HasCapability: func(a core.Actor, capability string) bool {
			return s.st.HasCapability(string(a), capability)
		},
		ActorEnrolled: func(a core.Actor) bool {
			return s.st.ActorEnrolled(string(a))
		},
	}
	events, rej := core.Decide(state, cmd)
	if rej != nil {
		attempts, exhausted := s.correct.rejected(cmd.Author, rej.Invariant)
		if exhausted {
			// Not a different rejection — the same one, for the third time. The
			// schema is not the problem any more; the agent's model of the rule
			// is, and another attempt cannot discover that.
			writeJSON(w, http.StatusConflict, map[string]any{
				"ok": false, "outcome": "refused", "exit": 4,
				"invariant": rej.Invariant, "detail": rej.Detail,
				"schema":   schemaFor(cmd.Kind),
				"attempts": attempts,
				"next": "stop correcting and ask a person: agent_comms ask --to <human> " +
					"--text \"I keep getting " + rej.Invariant + " on a " + string(cmd.Kind) + "\"",
			})
			return
		}
		writeJSON(w, http.StatusUnprocessableEntity,
			rejectedResponse{rej.Invariant, rej.Detail, schemaFor(cmd.Kind)})
		return
	}
	s.correct.accepted(cmd.Author)

	var last int64
	for _, ev := range events {
		seq, err := s.st.Append(ev, cmd.Idem, s.now())

		// The same key with different content is a conflict, not a retry.
		// Answering it as a duplicate returned the first post's seq and
		// discarded this one — data loss reported as success.
		var conflict store.ErrIdemConflict
		if errors.As(err, &conflict) {
			writeJSON(w, http.StatusConflict, rejectedResponse{
				"idem.conflict",
				fmt.Sprintf("idempotency key already used at seq %d with different content; "+
					"mint a new key for each distinct post", conflict.Seq),
				""})
			return
		}

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

		// A redact event must actually suppress its target. Rendering it
		// struck-through while leaving the body readable and searchable is
		// worse than no redaction, because the room implies it worked.
		if ev.Kind == core.KindRedact && len(ev.Refs) == 1 {
			if target, convErr := strconv.ParseInt(ev.Refs[0], 10, 64); convErr == nil {
				if err := s.st.ApplyRedaction(target, seq, string(ev.Author), s.now()); err != nil {
					writeJSON(w, http.StatusInternalServerError,
						rejectedResponse{"redaction.failed", err.Error(), ""})
					return
				}
			}
		}

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
		default:
			// A full channel means this subscriber is behind. Dropping the
			// event silently is the one thing we must not do: seq is gappy by
			// design, so the client cannot detect the hole, which would
			// falsify the no-gaps guarantee. Close it instead — the reader
			// reports "lagged" and reconnects with Last-Event-ID to replay.
			close(ch)
			delete(s.subs, ch)
		}
	}
}

// bootID identifies one run of this process. A client learns a restart as a
// fact rather than inferring it from a seq delta, which ADR-0010 forbids —
// seq is gappy by design, so a delta means nothing.
var bootID = fmt.Sprintf("%d", time.Now().UnixNano())

// backlogCeiling is how much history one connection replays. Past it the client
// is told where it stopped, because a silent truncation is indistinguishable
// from a quiet room.
const backlogCeiling = 500

func (s *Server) roomsList(w http.ResponseWriter, r *http.Request) {
	rooms, err := s.st.Rooms()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError,
			rejectedResponse{"rooms.failed", err.Error(), ""})
		return
	}
	if !wantsJSON(r) {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "outcome": "read", "rooms": rooms, "count": len(rooms),
	})
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	room := r.URL.Query().Get("room")
	if room == "" {
		room = "core"
	}
	if wantsJSON(r) {
		s.streamJSON(w, r, room)
		return
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
		case rec, open := <-ch:
			if !open {
				// Closed because this subscriber fell behind. The browser
				// reconnects on its own and replays from Last-Event-ID.
				return
			}
			writeSSE(w, rec)
			flusher.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// streamJSON is the read lane for a non-browser client. It carries the same
// id: {seq} and the same Last-Event-ID resume as the datastar lane, which is
// left byte-unchanged.
func (s *Server) streamJSON(w http.ResponseWriter, r *http.Request, room string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	recipient := r.URL.Query().Get("recipient")
	kindFilter := r.URL.Query().Get("kind")

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	// The opening frame states the boot id, so a restart is a fact the client
	// is told rather than one it guesses from a gap.
	writeFrame(w, "hello", 0, map[string]any{
		"type": "hello", "boot": bootID, "room": room,
		"backlog_ceiling": backlogCeiling,
	})
	flusher.Flush()

	after := lastEventID(r)
	backlog, err := s.st.Since(room, after, backlogCeiling+1)
	if err != nil {
		writeFrame(w, "error", 0, map[string]any{
			"type": "error", "invariant": "read.failed", "detail": err.Error()})
		flusher.Flush()
		return
	}

	truncated := len(backlog) > backlogCeiling
	if truncated {
		backlog = backlog[:backlogCeiling]
	}
	var lastSeq int64 = after
	for _, rec := range backlog {
		if !matchesFilter(rec, recipient, kindFilter) {
			lastSeq = rec.Seq
			continue
		}
		writeFrame(w, "event", rec.Seq, s.eventFrame(rec))
		lastSeq = rec.Seq
	}
	if truncated {
		// Name where the client stopped. Without this the ceiling is a hole
		// the client cannot see, because seq is gappy by design.
		writeFrame(w, "truncated", lastSeq, map[string]any{
			"type": "truncated", "first_undelivered_seq": lastSeq,
			"detail": "backlog stopped at the ceiling; reconnect with Last-Event-ID to continue",
		})
	}

	// Exactly one caught-up frame, between backlog and live, on every
	// connection including an empty room. It is the only way a process that
	// must exit can tell "I have read the history" from "the room is quiet".
	writeFrame(w, "caught-up", lastSeq, map[string]any{
		"type": "caught-up", "seq": lastSeq, "room": room,
	})
	flusher.Flush()

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
		case rec, open := <-ch:
			if !open {
				// The writer closed us because we fell behind. Say so, and let
				// the client reconnect and replay rather than lose events.
				writeFrame(w, "lagged", 0, map[string]any{
					"type": "lagged",
					"detail": "this subscriber fell behind and was closed; " +
						"reconnect with Last-Event-ID to replay what was missed",
				})
				flusher.Flush()
				return
			}
			if !matchesFilter(rec, recipient, kindFilter) {
				continue
			}
			writeFrame(w, "event", rec.Seq, s.eventFrame(rec))
			flusher.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func matchesFilter(rec store.Record, recipient, kind string) bool {
	if recipient != "" && string(rec.Recipient) != recipient {
		return false
	}
	if kind != "" && string(rec.Kind) != kind {
		return false
	}
	return true
}

// eventFrame adds the key status the read lane owes its reader.
func (s *Server) eventFrame(rec store.Record) eventJSON {
	ev := toEventJSON(rec)
	ev.AuthorKeyStatus, ev.Flagged = s.st.KeyStatus(string(rec.Author), rec.ServerTS)
	return ev
}

func writeFrame(w http.ResponseWriter, event string, seq int64, payload any) {
	if seq > 0 {
		fmt.Fprintf(w, "id: %d\n", seq)
	}
	fmt.Fprintf(w, "event: %s\n", event)
	b, _ := json.Marshal(payload)
	fmt.Fprintf(w, "data: %s\n\n", b)
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

// roomBrief is the one call an agent makes before it decides anything. It reads
// decision projections only, so it stays an indexed read as the log grows.
func (s *Server) roomBrief(w http.ResponseWriter, r *http.Request) {
	room := r.PathValue("name")
	if !s.st.RoomExists(room) {
		writeJSON(w, http.StatusNotFound, rejectedResponse{"room.unknown",
			"no room " + room + "; GET /rooms lists them", ""})
		return
	}
	if !wantsJSON(r) {
		http.Redirect(w, r, "/?room="+url.QueryEscape(room), http.StatusFound)
		return
	}
	brief, err := s.st.RoomBrief(room, s.now())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError,
			rejectedResponse{"brief.failed", err.Error(), ""})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "outcome": "read", "brief": brief,
	})
}

// actorsList is the roster recipient.unknown is checked against, so an agent
// that gets the rejection can find the spelling it should have used.
func (s *Server) actorsList(w http.ResponseWriter, r *http.Request) {
	actors, err := s.st.Actors()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError,
			rejectedResponse{"actors.failed", err.Error(), ""})
		return
	}
	if !wantsJSON(r) {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "outcome": "read", "actors": actors, "count": len(actors),
	})
}
