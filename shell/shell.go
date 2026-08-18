// Package shell is the imperative shell: HTTP, SSE, clock, and rendering.
//
// It parses untrusted input into core types exactly once (parse, don't
// validate), authenticates, calls the pure decider, appends what the decider
// returns, and fans the result out. Windows and budgets live here because they
// need a clock and the core has none.
package shell

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/escherize/comms/core"
	"github.com/escherize/comms/store"
)

// Clock is an internal adapter seam. Tests replace it with a fake; lease expiry
// and windowing are functions of it, never of a client-supplied timestamp.
type Clock func() time.Time

// Server is the command surface and the room renderer.
type Server struct {
	st   *store.Store
	now  Clock
	mu   sync.Mutex
	subs map[chan store.Record]string // subscriber -> room
	// navSubs are the browser streams' nav refreshers: on room creation each
	// gets a nudge and rebuilds its own room nav, filtered by its own reader's
	// membership — the patch is computed per subscriber, so scoping holds.
	navSubs map[chan struct{}]navSub

	// RequireSignature makes authentication mandatory. It defaults to on; the
	// only way to turn it off is an explicit flag, so an unauthenticated
	// deployment is a decision someone made rather than one they inherited.
	RequireSignature bool

	// PublicURL is the base URL clients should use to reach this hub from
	// outside (--public-url). A loopback mint composes invite links from the
	// address it dialled, which on a deployed hub is 127.0.0.1; when set, the
	// invite response carries this instead. Empty means "no better answer".
	PublicURL string

	limit    *limiter
	correct  *corrections
	escalate *escalations
	posting  *posting
	embed    *embedder
	sess     *sessions
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
	sv := &Server{st: st, now: now, subs: map[chan store.Record]string{},
		navSubs:          map[chan struct{}]navSub{},
		RequireSignature: true,
		limit:            newLimiter(PostsPerMinute, PostBurst, now),
		correct:          newCorrections(),
		escalate:         newEscalations(now),
		posting:          newPosting(now),
		sess:             newSessions(now)}
	// The lane ships wired to a stand-in rather than dark. An adapter seam
	// nobody runs is a seam nobody knows is broken, and the watermark, the
	// retries and the fusion are all real whatever produces the numbers.
	sv.embed = newEmbedder(st, HashEmbedder{}, sv.now)
	return sv
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /commands", s.postCommand)
	mux.HandleFunc("POST /keys", s.postKey)
	mux.HandleFunc("POST /artifacts", s.postArtifact)
	mux.HandleFunc("GET /a/{hash}", s.getArtifact)
	mux.HandleFunc("GET /stream", s.stream)
	mux.HandleFunc("GET /rooms", s.roomsList)
	mux.HandleFunc("GET /rooms/{name}", s.roomBrief)
	mux.HandleFunc("GET /actors", s.actorsList)
	mux.HandleFunc("POST /escalate", s.postEscalation)
	mux.HandleFunc("GET /index", s.indexStatus)
	mux.HandleFunc("POST /delivered", s.postDelivered)
	mux.HandleFunc("POST /invite", s.postInvite)
	mux.HandleFunc("POST /invites/whose", s.whoseInvite)
	mux.HandleFunc("GET /session/challenge", s.getChallenge)
	mux.HandleFunc("POST /session", s.postSession)
	mux.HandleFunc("GET /caps", s.getCaps)
	mux.HandleFunc("POST /rooms", s.postRoom)
	mux.HandleFunc("GET /search", s.searchPage)
	mux.HandleFunc("GET /", s.roomPage)
	// Reads are always authenticated. A permanent, secret-bearing log is not
	// served unauthenticated, and room scoping is meaningless without read
	// attribution — even locally, where distinct agents share one loopback. The
	// gate self-authenticates the enrol/session routes and passes loopback with
	// the full operator view, so the CLI and browser onboard transparently; a
	// no-session browser read gets the unlock page, never content. There is no
	// open-read mode and no flag to reintroduce one.
	return s.readGate(mux)
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
			"mint one with: comms invite <actor>"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"actor": req.Actor, "enrolled": true})
}

// whoseInvite answers which seat a live token enrols, spending nothing.
// POST because a token belongs in a body, never a URL; self-authenticating
// because the token is the credential, same as /keys. This is what lets the
// composer set its actor from a pasted token instead of making the person
// match two fields by hand.
func (s *Server) whoseInvite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	raw, err := readLimited(r.Body, 1024)
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge,
			rejectedResponse{"body.too_large", err.Error(), ""})
		return
	}
	if err := json.Unmarshal(raw, &req); err != nil || strings.TrimSpace(req.Token) == "" {
		writeJSON(w, http.StatusBadRequest,
			rejectedResponse{"token.required", "name the token to look up", ""})
		return
	}
	actor, scope, err := s.st.InviteActor(strings.TrimSpace(req.Token), s.now())
	if err != nil {
		writeJSON(w, http.StatusNotFound, rejectedResponse{
			"token.unknown", err.Error(),
			"mint one with: comms invite <actor>"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "outcome": "read", "actor": actor, "scope": scope})
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

// getArtifact serves stored markdown: rendered as sanitized HTML for a
// browser, or the raw bytes under an explicit Accept: text/markdown (the
// CLI's fetch). HTML is never served unsanitized.
func (s *Server) getArtifact(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	// A raw hash is not a bypass around room scoping: the reader must be a member
	// of some room that references this artifact. The 404 is the same whether the
	// artifact is unknown, unreferenced, or simply not the reader's to see — the
	// hash discloses nothing about content in a room they cannot read.
	if !s.mayReadArtifact(reader(r), hash) {
		http.NotFound(w, r)
		return
	}
	content, ok := s.st.GetArtifact(hash)
	if !ok {
		http.NotFound(w, r)
		return
	}
	// The CLI reads the stored markdown itself; only browsers get HTML, and
	// only sanitized. text/markdown is inert in a browser (nosniff, no
	// execution), so raw-under-explicit-Accept keeps the never-raw-HTML stance.
	if strings.Contains(r.Header.Get("Accept"), "text/markdown") {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(content)
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
				// Say what happened to the event. `spooled` and `dropped` both
				// name the fate of the bytes; a throttle that does not is the
				// one reply where an agent cannot tell whether to post again.
				"applied": false,
				"kept":    false,
				"next":    "this post was not kept: sleep retry_after_ms, then post it again",
			})
			return
		}
	}

	// The posting budget. Distinct from the rate limit above and refused
	// differently, because the remedy differs: too fast is answered by waiting,
	// too much is answered by saying it once.
	if s.posting != nil {
		if remaining, oldest, ok := s.posting.charge(cmd.Author, cmd.Room, cmd.Kind); !ok {
			_ = remaining
			writeJSON(w, http.StatusTooManyRequests, map[string]any{
				"ok": false, "outcome": "throttled", "exit": 6,
				"invariant": "budget.exhausted",
				"detail": fmt.Sprintf(
					"this seat has added %d ambient entries to %s in the last %s. "+
						"Nothing was posted and nothing was lost",
					PostingBudget, cmd.Room, PostingWindow),
				"retry_after_ms": oldest.Milliseconds(),
				"kept":           false,
				"next": "you are not posting too fast, you are posting too much to read. " +
					"Combine what is left into one summarizing finding and post that, or " +
					"attach the detail and post the summary. task.* and offer.* are never " +
					"budgeted, so work coordination is unaffected",
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

	state := s.decisionState()
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
				"next": "stop correcting and ask a person: comms ask --to <human> " +
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
				fmt.Sprintf("this key was used at seq %d for a different command. That is "+
					"almost always a re-run with an edited flag: the key is derived from what "+
					"you are posting, so changing the text or the severity and running again "+
					"produces a new key, while reusing --idem does not. Either drop --idem and "+
					"let the key follow the content, or pass a --idem that names this post",
					conflict.Seq),
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

		// A redact event suppresses its target inside Append's own
		// transaction (store.Append folds it), so a committed redact event is
		// a suppressed target by construction — no second transaction, no
		// crash window between the claim and the act.

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
	// Every append also nudges the rails: pages in OTHER rooms learn this
	// room's head moved and mark it unread. The nudge channel holds one
	// pending signal, so a burst costs each page a single rebuild.
	// ponytail: a rail rebuild is two small queries per subscriber per nudge;
	// per-room dirty tracking if hubs ever grow past small-team size.
	for ch := range s.navSubs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// navSub names the identity a nav refresher renders for.
type navSub struct{ reader, room string }

// notifyNav nudges every browser stream to rebuild its room nav. Non-blocking:
// the channel holds one pending nudge, and a second while one is pending is
// the same nudge — the subscriber rebuilds from current state either way.
func (s *Server) notifyNav() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.navSubs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// railFor renders the room rail this reader may see. It is the live
// counterpart of the render-time rail in render.go — same anchors, same
// membership filter, same heads — so a pushed rail replaces the served one
// verbatim and the browser's unread marks stay honest.
func (s *Server) railFor(readerSeat, current string) string {
	rooms, err := s.st.Rooms()
	if err != nil {
		return ""
	}
	heads, _ := s.st.RoomHeads()
	return railLinks(s.visibleRooms(readerSeat, rooms), current, heads)
}

// bootID identifies one run of this process. A client learns a restart as a
// fact rather than inferring it from a seq delta, which ADR-0010 forbids —
// seq is gappy by design, so a delta means nothing.
var bootID = fmt.Sprintf("%d", time.Now().UnixNano())

// backlogCeiling is how much history one connection replays. Past it the client
// is told where it stopped, because a silent truncation is indistinguishable
// from a quiet room.
const backlogCeiling = 500

// canRead reports whether the reading seat may see a room. An empty reader is
// the full view — loopback or a self-authenticating route, where the gate
// attached no seat — so it sees everything, matching the operator trust the
// gate already grants those paths. Otherwise membership decides, and a
// '*'-scoped seat is a member of every room.
func (s *Server) canRead(reader, room string) bool {
	if reader == "" {
		return true
	}
	return s.st.IsMember(reader, room)
}

// visibleRooms filters a room list to what the reading seat may see. The full
// view (empty reader) passes through unchanged.
func (s *Server) visibleRooms(reader string, rooms []string) []string {
	if reader == "" {
		return rooms
	}
	out := rooms[:0:0]
	for _, room := range rooms {
		if s.st.IsMember(reader, room) {
			out = append(out, room)
		}
	}
	return out
}

// mayReadArtifact reports whether the reading seat may fetch an artifact by
// hash. The full view (empty reader — loopback / self-auth) may read any
// referenced artifact. Otherwise the seat must be a member of at least one room
// that references the hash through a live (non-redacted) event; an artifact no
// live event references is served to nobody.
func (s *Server) mayReadArtifact(reader, hash string) bool {
	rooms := s.st.ArtifactRooms(hash)
	if len(rooms) == 0 {
		return false
	}
	if reader == "" {
		return true
	}
	for _, room := range rooms {
		if s.st.IsMember(reader, room) {
			return true
		}
	}
	return false
}

// readerRooms is the room allow-list a search must be confined to: the reading
// seat's own rooms. An empty slice means unrestricted — the full view (loopback
// / self-auth), or a '*'-scoped seat, both of which may search every room.
// Scoping the query at the source, rather than filtering results after, means a
// non-member room's content never enters the result set at all.
func (s *Server) readerRooms(reader string) []string {
	if reader == "" {
		return nil
	}
	var rooms []string
	for _, r := range s.st.Memberships(reader) {
		if r == "*" {
			return nil // an all-rooms seat searches everywhere
		}
		rooms = append(rooms, r)
	}
	// A seat with no rooms gets a non-nil, empty allow-list — it searches
	// nothing, distinct from the nil "search everything" of the full view.
	if rooms == nil {
		return []string{}
	}
	return rooms
}

func (s *Server) roomsList(w http.ResponseWriter, r *http.Request) {
	rooms, err := s.st.Rooms()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError,
			rejectedResponse{"rooms.failed", err.Error(), ""})
		return
	}
	rooms = s.visibleRooms(reader(r), rooms)
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
	// A non-member is refused before a single event is delivered — the live-read
	// equivalent of the room page 404, and existence-hiding for the same reason:
	// a 403 would confirm the room. This covers both the SSE and JSON lanes,
	// since both flow through here.
	if !s.canRead(reader(r), room) {
		http.NotFound(w, r)
		return
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

	// The same filters the JSON lane applies. They were applied on one path and
	// not the other, and the symptom was a live search page appending events
	// that did not match its query — a filter that holds for a program reading
	// JSON and not for the person reading the page.
	recipient := r.URL.Query().Get("recipient")
	kindFilter := r.URL.Query().Get("kind")
	queryFilter := r.URL.Query().Get("q")

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Subscribe BEFORE reading the backlog. The old order (backlog first,
	// subscribe second) had a window where an event fanned out between the
	// snapshot and the registration reached neither — a silent gap the client
	// cannot detect, because seq is gappy by design. Registered first, an
	// event can arrive twice (once buffered live, once in the backlog); the
	// seq high-water below drops the duplicate.
	ch := make(chan store.Record, 64)
	navCh := make(chan struct{}, 1)
	s.mu.Lock()
	s.subs[ch] = room
	// Only room pages get nav pushes: the search page shares this stream (with
	// q=) but its header nav is rooms|search, and a room-links replacement
	// would clobber it.
	if queryFilter == "" {
		s.navSubs[navCh] = navSub{reader: reader(r), room: room}
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.subs, ch)
		delete(s.navSubs, navCh)
		s.mu.Unlock()
	}()

	// Resume: replay everything after the client's last seen seq. Paged, not
	// capped — the browser lane has no re-request protocol, so a cap would be
	// a permanent hole for any tab more than a page behind.
	lastSeq := lastEventID(r)
	for {
		backlog, err := s.st.Since(room, lastSeq, 500)
		if err != nil || len(backlog) == 0 {
			break
		}
		for _, rec := range backlog {
			if lastSeq = rec.Seq; !s.matchesFilter(rec, recipient, kindFilter, queryFilter) {
				continue
			}
			writeSSE(w, rec, queryFilter != "")
		}
		flusher.Flush()
		if len(backlog) < 500 {
			break
		}
	}

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
			// Buffered during the backlog replay and already sent from it.
			if rec.Seq <= lastSeq {
				continue
			}
			lastSeq = rec.Seq
			if !s.matchesFilter(rec, recipient, kindFilter, queryFilter) {
				continue
			}
			writeSSE(w, rec, queryFilter != "")
			flusher.Flush()
		case <-navCh:
			// A room was created. Rebuild this page's nav for this reader —
			// the membership filter runs per subscriber, so a room the reader
			// may not see never reaches its page.
			if rail := s.railFor(reader(r), room); rail != "" {
				writeNavSSE(w, rail)
				flusher.Flush()
			}
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// writeNavSSE pushes a replacement for the room rail. No id: line — rail
// refreshes carry no seq and must not disturb Last-Event-ID resume.
func writeNavSSE(w http.ResponseWriter, rail string) {
	fmt.Fprint(w, "event: datastar-patch-elements\n")
	fmt.Fprint(w, "data: mode replace\n")
	fmt.Fprint(w, "data: selector nav.rail\n")
	fmt.Fprintf(w, "data: elements %s\n", rail)
	fmt.Fprint(w, "\n")
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
	// A search page is a room view with one more filter on it. Without this the
	// results are a snapshot that goes stale the moment a matching event lands
	// — which during a bug bash is immediately, and silently.
	queryFilter := r.URL.Query().Get("q")

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

	// Subscribe before the backlog snapshot, for the same no-silent-gap
	// reason as the datastar lane: an event fanned out between snapshot and
	// registration must land in the buffered channel, not nowhere. The seq
	// high-water below drops the resulting duplicates.
	ch := make(chan store.Record, 64)
	s.mu.Lock()
	s.subs[ch] = room
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}()

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
		if !s.matchesFilter(rec, recipient, kindFilter, queryFilter) {
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
			// Buffered during the backlog replay and already sent from it.
			if rec.Seq <= lastSeq {
				continue
			}
			lastSeq = rec.Seq
			if !s.matchesFilter(rec, recipient, kindFilter, queryFilter) {
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

func (s *Server) matchesFilter(rec store.Record, recipient, kind, query string) bool {
	if recipient != "" && string(rec.Recipient) != recipient {
		return false
	}
	if kind != "" && string(rec.Kind) != kind {
		return false
	}
	// The query is checked last and against the index, not the record: the
	// tokenizer that decided this event was a hit is the one that must decide
	// it again, or the live rows and the rendered ones disagree about what the
	// search meant.
	if query != "" && !s.st.MatchesQuery(rec.Seq, query) {
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
// writeSSE appends one row. asSearchHit picks the row shape: a search page
// renders folio, ranks, author, kind, entry, and a room row dropped into it
// lands in the wrong columns — the live rows would not line up with the ones
// the page was served with.
func writeSSE(w http.ResponseWriter, rec store.Record, asSearchHit bool) {
	row := renderRow(rec)
	if asSearchHit {
		row = searchRow(rec)
	}
	fmt.Fprintf(w, "id: %d\n", rec.Seq)
	fmt.Fprint(w, "event: datastar-patch-elements\n")
	fmt.Fprint(w, "data: mode append\n")
	fmt.Fprint(w, "data: selector #ledger-body\n")
	for _, line := range strings.Split(row, "\n") {
		fmt.Fprintf(w, "data: elements %s\n", line)
	}
	fmt.Fprint(w, "\n")
}

// roomBrief is the one call an agent makes before it decides anything. It reads
// decision projections only, so it stays an indexed read as the log grows.
func (s *Server) roomBrief(w http.ResponseWriter, r *http.Request) {
	room := r.PathValue("name")
	// Membership before existence, and the same 404 either way: a non-member and
	// a nonexistent room are indistinguishable to the reader, so scoping a seat
	// away from a room hides whether it exists.
	if !s.canRead(reader(r), room) || !s.st.RoomExists(room) {
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

// decisionState is the decider's view of the world, in one place. Two routes
// building it separately is two chances for one of them to forget a rule — and
// a rule missing from one path is a rule that does not exist.
func (s *Server) decisionState() core.State {
	return core.State{
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
		IsMember: func(a core.Actor, room string) bool {
			// A hub with no membership rows at all is unscoped — nobody has been
			// placed in rooms, so room scoping is not in force and every author
			// is a member. Any real hub has rows (grandfather writes one per
			// enrolled seat, redeem writes them on the way in), so this opens
			// only a fresh or -insecure hub that never enrolled anyone, which is
			// the pre-scoping behaviour. Once a single membership exists the
			// check is live.
			if !s.st.AnyMembership() {
				return true
			}
			return s.st.IsMember(string(a), room)
		},
		MemberRooms: func(a core.Actor) []string {
			return s.st.Memberships(string(a))
		},
	}
}

// postEscalation pulls one ambient entry into a person's attention, and charges
// for it. It is a route rather than a kind because escalating states no new
// fact — the finding already says what it says. What changes is who is expected
// to look, so what lands in the log is an ordinary addressed question
// referencing the entry, authored by the escalating seat and signed by its key
// like anything else. ADR-0008 prices this because fifteen agents share a room
// with five people, and an interrupt nobody pays for is one everybody sends.
func (s *Server) postEscalation(w http.ResponseWriter, r *http.Request) {
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
				rejectedResponse{"body.too_large", "escalation body exceeds 1MiB", ""})
			return
		}
	}

	var req struct {
		Room, Author, Refs, To, Text, Idem string
	}
	if err := json.Unmarshal(raw, &struct {
		Room   *string `json:"room"`
		Author *string `json:"author"`
		Refs   *string `json:"refs"`
		To     *string `json:"to"`
		Text   *string `json:"text"`
		Idem   *string `json:"idem"`
	}{&req.Room, &req.Author, &req.Refs, &req.To, &req.Text, &req.Idem}); err != nil {
		writeJSON(w, http.StatusBadRequest, rejectedResponse{"parse.failed", err.Error(), ""})
		return
	}

	if s.RequireSignature {
		sig, err := decodeSig(r.Header.Get("X-Signature"))
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, rejectedResponse{"signature.missing",
				"every escalation must carry X-Signature: a hex ed25519 signature over the request body",
				"X-Signature: <128 hex chars>"})
			return
		}
		if err := s.st.VerifySignature(core.Actor(req.Author), raw, sig, s.now()); err != nil {
			var af store.AuthFailure
			if errors.As(err, &af) {
				writeJSON(w, http.StatusUnauthorized, rejectedResponse{af.Invariant, af.Detail, ""})
				return
			}
			writeJSON(w, http.StatusUnauthorized,
				rejectedResponse{"signature.invalid", err.Error(), ""})
			return
		}
	}

	author := core.Actor(req.Author)
	if req.Refs == "" {
		writeJSON(w, http.StatusUnprocessableEntity, rejectedResponse{"refs.exactly_one",
			"escalate names the one entry a person should look at", ""})
		return
	}

	// Check before appending and charge after it applies. Checking first is what
	// makes the budget a budget — by the time an append has happened the
	// interrupt has happened. Charging only on a real append is what keeps a
	// replay free: escalating the same entry with the same words twice is one
	// act, and the second one interrupts nobody.
	_, retryAfter, ok := s.escalate.canSpend(author)
	if !ok {
		ms := retryAfter.Milliseconds()
		if ms < 1 {
			ms = 1
		}
		// Exit 6, not 4: the budget refills. "Stop, ask a human" would be wrong
		// about a condition that fixes itself on a clock, and the retry_after
		// is what makes the difference actionable.
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"ok": false, "outcome": "throttled", "exit": 6,
			"invariant": "escalation.exhausted",
			"detail": fmt.Sprintf(
				"this seat has spent all %d escalations in the last %s. Nothing was posted",
				EscalationBudget, EscalationWindow),
			"retry_after_ms": ms, "remaining": 0,
			"next": "the finding is already in the room and stays there; escalating is about " +
				"who looks now, not whether it is recorded. Wait for the window, or add the " +
				"evidence to a conversation you are already having with a person",
		})
		return
	}

	cmd := core.Command{
		Room: req.Room, Author: author, Kind: core.KindQuestion,
		Recipient: core.Actor(req.To), Refs: []string{req.Refs}, Idem: req.Idem,
		Body: map[string]any{"text": req.Text, "escalated": req.Refs},
	}
	if cmd.Room == "" {
		cmd.Room = "core"
	}
	if matches := s.st.ResolveActor(string(cmd.Recipient)); len(matches) == 1 {
		cmd.Recipient = core.Actor(matches[0])
	}

	events, rej := core.Decide(s.decisionState(), cmd)
	if rej != nil {
		writeJSON(w, http.StatusUnprocessableEntity,
			rejectedResponse{rej.Invariant, rej.Detail, schemaFor(cmd.Kind)})
		return
	}

	var seq int64
	for _, ev := range events {
		n, err := s.st.Append(ev, cmd.Idem, s.now())
		if err != nil {
			var dup store.ErrDuplicate
			if errors.As(err, &dup) {
				// A replay. Nothing was appended, nobody was interrupted, and
				// the budget is untouched.
				writeJSON(w, http.StatusOK, map[string]any{
					"ok": true, "outcome": "escalated", "seq": dup.Seq, "applied": false,
					"remaining": s.escalate.left(author),
					"detail":    "already escalated; nothing was posted and no budget was spent",
				})
				return
			}
			writeJSON(w, http.StatusInternalServerError,
				rejectedResponse{"append.failed", err.Error(), ""})
			return
		}
		seq = n
		s.fanout(ev.Room, seq)
	}

	// It landed, so it is charged.
	remaining, _, _ := s.escalate.spend(author)

	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "outcome": "escalated", "seq": seq, "applied": true,
		"remaining": remaining,
		"detail": fmt.Sprintf("%d escalation(s) left in this %s window",
			remaining, EscalationWindow),
	})
}

// StartEmbedder runs the semantic lane in the background until ctx ends. It is
// started by the operator surface rather than by New, so a test drives step()
// deterministically instead of racing a ticker.
func (s *Server) StartEmbedder(ctx context.Context, every time.Duration) {
	if s.embed == nil {
		return
	}
	go s.embed.run(ctx, every)
}

// Reembed rebuilds the semantic lane from a seq.
func (s *Server) Reembed(ctx context.Context, from int64) (int, error) {
	if s.embed == nil {
		return 0, errors.New("no embedder is configured")
	}
	return s.embed.Reembed(ctx, from)
}

// indexStatus is the lane's own page: how far behind it is, and everything it
// gave up on. A dead-letter list nobody can read is a list that does not exist.
func (s *Server) indexStatus(w http.ResponseWriter, r *http.Request) {
	watermark, at, stale := s.lagFor()
	dead, err := s.st.DeadLettered()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError,
			rejectedResponse{"index.failed", err.Error(), ""})
		return
	}
	if dead == nil {
		dead = []store.DeadLetter{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "outcome": "read",
		"embedded_through_seq": watermark,
		"current_to":           at.UTC().Format(time.RFC3339),
		"head":                 s.st.Head(),
		"stale":                stale,
		"dead_lettered":        dead,
		"detail": "events on the dead-letter list are absent from the semantic lane " +
			"and present in the lexical one; rebuild with: comms --reembed <seq>",
	})
}

// postDelivered records how far a seat has drained its addressed lane. It is
// not a command and produces no event: delivery is operational state, true now
// and uninteresting in six months, and putting it in the log would double the
// log's volume with rows nobody will search for.
func (s *Server) postDelivered(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Actor   string `json:"actor"`
		Room    string `json:"room"`
		Through int64  `json:"addressed_through"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, rejectedResponse{"parse.failed", err.Error(), ""})
		return
	}
	if req.Actor == "" || req.Room == "" {
		writeJSON(w, http.StatusUnprocessableEntity, rejectedResponse{"actor.required",
			"a delivery receipt names the seat and the room", ""})
		return
	}
	// Identity wins over the body: a session names the seat whose watermark
	// may move. Without this, any authenticated seat could mark another
	// seat's addressed lane drained — the delivery signal falsified for
	// exactly the reader it exists to protect. A seatless request (the
	// operator on loopback) may still name any seat.
	if who := reader(r); who != "" && who != req.Actor {
		writeJSON(w, http.StatusForbidden, rejectedResponse{"delivery.not_yours",
			"a delivery receipt moves only your own watermark: this session is " +
				who + ", the receipt names " + req.Actor, ""})
		return
	}
	if err := s.st.MarkDelivered(req.Actor, req.Room, req.Through, s.now()); err != nil {
		writeJSON(w, http.StatusInternalServerError,
			rejectedResponse{"delivery.failed", err.Error(), ""})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "outcome": "recorded", "addressed_through": req.Through,
	})
}

// postInvite mints an enrolment token from the running hub.
//
// The operator flag `-invite` opens a database by path, and the path defaults
// to ./comms.db. That put a real token into a file no hub had ever opened
// three times in one day, and every fix — a clearer message, a hard refusal, an
// environment variable — was another thing to remember. This removes the thing
// to remember: the token is minted by the process that will redeem it, so there
// is no second database for it to land in.
//
// Who may mint: loopback, or a seat holding the invite capability. Loopback
// because it is exactly the trust the operator flags already assume — being on
// the box is holding the database — and a capability so a human working from a
// laptop can be granted it deliberately rather than by being on the network.
func (s *Server) postInvite(w http.ResponseWriter, r *http.Request) {
	raw := make([]byte, 0, 1024)
	buf := make([]byte, 1024)
	for {
		n, err := r.Body.Read(buf)
		raw = append(raw, buf[:n]...)
		if err != nil || len(raw) > 1<<16 {
			break
		}
	}
	var req struct {
		Actor string `json:"actor"`
		As    string `json:"as"`
		Rooms string `json:"rooms"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, rejectedResponse{"parse.failed", err.Error(), ""})
		return
	}
	if req.Actor == "" {
		writeJSON(w, http.StatusUnprocessableEntity, rejectedResponse{"actor.required",
			"name the seat to invite: agent:<human>/<name> or human:<name>", ""})
		return
	}

	if !s.mayInvite(r, req.As, raw) {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"ok": false, "outcome": "refused", "exit": 4,
			"invariant": "invite.not_authorized",
			"detail": "minting an enrolment token is an operator act. Run it on the " +
				"machine serving the hub, or hold the invite capability",
			"next": "on the hub: comms invite " + req.Actor,
		})
		return
	}

	scope := req.Rooms
	if scope == "" {
		scope = store.ScopeAll
	}
	// A scoped admin may only mint within its own rooms — otherwise it could
	// grant itself reach it does not have by inviting a seat into a room it
	// cannot see and enrolling as that seat. Loopback (the operator) and an
	// all-rooms admin may mint anything; that is what over() below allows.
	if over := s.scopeExceedsGranter(r, req.As, scope); over != "" {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"ok": false, "outcome": "refused", "exit": 4,
			"invariant": "invite.scope_exceeds_grant",
			"detail": "you can only invite into rooms you are a member of; you are not " +
				"a member of: " + over,
			"next": "mint within your own rooms, or ask an all-rooms admin",
		})
		return
	}

	token, err := s.st.MintInvite(req.Actor, scope, s.now())
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity,
			rejectedResponse{"invite.refused", err.Error(), ""})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "outcome": "invited", "actor": req.Actor, "token": token, "scope": scope,
		"public_url": s.PublicURL,
		"detail": "one use. It exists in the database this hub is serving, which is " +
			"the point of minting it here",
	})
}

// scopeExceedsGranter returns the comma-joined rooms in the requested scope that
// the minter is not a member of, or "" if the mint is within the minter's
// grant. Loopback (the operator) and an all-rooms minter may grant any scope,
// so both return "". A minter granting "all" while itself scoped is refused —
// it cannot hand out reach it does not hold.
func (s *Server) scopeExceedsGranter(r *http.Request, as, scope string) string {
	// A bare loopback mint with no named seat is the operator on the box, who
	// grants anything. But once a seat is named — `--as sarah`, even locally —
	// identity wins over locality: sarah is bound by her own membership, the
	// same rule that scopes her reads. Otherwise a scoped seat on the box could
	// mint itself into any room and enrol as that seat.
	if as == "" && isLoopback(r.RemoteAddr) {
		return ""
	}
	// Minting a superuser (all rooms + invite capability) is an escalation
	// unless the granter is itself a superuser. An all-rooms admin without the
	// capability sees everything but cannot hand out the capability it does not
	// hold; only a seat that already runs the hub mints another that does.
	if scope == store.ScopeSuperuser {
		if as != "" && s.st.IsMember(as, "*") && s.st.HasCapability(as, CapInvite) {
			return ""
		}
		return "superuser (all rooms + invite capability)"
	}
	if as != "" && s.st.IsMember(as, "*") {
		return "" // an all-rooms admin grants any room scope, from anywhere
	}
	// A scoped admin granting "all" is exceeding its grant by definition.
	if scope == store.ScopeAll || scope == "*" {
		return store.ScopeAll
	}
	var over []string
	for _, room := range strings.Split(scope, ",") {
		room = strings.TrimSpace(room)
		if room == "" {
			continue
		}
		if !s.st.IsMember(as, room) {
			over = append(over, room)
		}
	}
	return strings.Join(over, ", ")
}

// mayInvite is loopback, or a seat holding the capability and proving it.
func (s *Server) mayInvite(r *http.Request, as string, raw []byte) bool {
	if isLoopback(r.RemoteAddr) {
		return true
	}
	if as == "" || !s.st.HasCapability(as, CapInvite) {
		return false
	}
	sig, err := decodeSig(r.Header.Get("X-Signature"))
	if err != nil {
		return false
	}
	return s.st.VerifySignature(core.Actor(as), raw, sig, s.now()) == nil
}

// CapInvite lets a seat mint enrolment tokens without being on the box.
const CapInvite = "invite"

func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
