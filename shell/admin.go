package shell

// The admin surface of the settings page. Every action here re-proves the
// invite capability on a signed request — the panel visibility that GET /caps
// feeds the browser is presentation, never authorization.

import (
	"encoding/json"
	"net/http"
	"regexp"
)

// getCaps reports what a seat holds so the settings page knows which panels to
// draw. Capabilities are roster facts, not secrets: they say what a seat may
// do, the same way /actors says who is enrolled.
func (s *Server) getCaps(w http.ResponseWriter, r *http.Request) {
	actor := r.URL.Query().Get("actor")
	if actor == "" {
		writeJSON(w, http.StatusUnprocessableEntity,
			rejectedResponse{"actor.required", "caps are per seat: ?actor=<seat>", ""})
		return
	}
	caps := s.st.Capabilities(actor)
	if caps == nil {
		caps = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "outcome": "read", "actor": actor, "capabilities": caps,
	})
}

// roomName keeps room names URL-safe and unambiguous. The startup -rooms flag
// trusts the operator's shell; this route takes names from a browser field.
var roomName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// postRoom creates a room. Creation is guarded by the same trust as minting an
// invite — loopback, or a seat holding the invite capability and proving it —
// because both acts shape who works where. There is no delete and never will
// be: the log is append-only, and a room's history outlives anyone's wish to
// tidy it.
func (s *Server) postRoom(w http.ResponseWriter, r *http.Request) {
	raw, err := readLimited(r.Body, 4096)
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge,
			rejectedResponse{"body.too_large", err.Error(), ""})
		return
	}
	var req struct {
		Name string `json:"name"`
		As   string `json:"as"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		writeJSON(w, http.StatusBadRequest,
			rejectedResponse{"parse.failed", err.Error(), ""})
		return
	}
	if !roomName.MatchString(req.Name) {
		writeJSON(w, http.StatusUnprocessableEntity, rejectedResponse{
			"room.name_invalid",
			"a room name is 1-32 of a-z 0-9 - _, starting with a letter or digit",
			""})
		return
	}
	if !s.mayInvite(r, req.As, raw) {
		writeJSON(w, http.StatusForbidden, rejectedResponse{
			"room.not_authorized",
			"creating a room is an operator act: be on the hub, or hold the invite capability",
			"on the hub: agent-comms -grant-invite <seat>"})
		return
	}
	if s.st.RoomExists(req.Name) {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "outcome": "exists", "room": req.Name})
		return
	}
	if err := s.st.EnsureRoom(req.Name); err != nil {
		writeJSON(w, http.StatusInternalServerError,
			rejectedResponse{"room.create_failed", err.Error(), ""})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "outcome": "created", "room": req.Name,
		"detail": "rooms are created, never destroyed",
	})
}
