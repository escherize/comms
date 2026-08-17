package shell

import (
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/escherize/comms/core"
	"github.com/escherize/comms/store"
)

// scopedServer is a read-auth hub with two seats: an all-rooms owner and a
// seat scoped to one room. Both rooms have content. It returns the handler,
// the store, and a session token for each seat. Read-auth is forced on so the
// session actor is always resolvable — the whole point of step 5 is that a
// read is filtered by the session's seat.
func scopedServer(t *testing.T) (h http.Handler, ownerTok, sarahTok string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "scoped.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	for _, r := range []string{"comms", "secret"} {
		if err := st.EnsureRoom(r); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)

	ownerPub, ownerPriv, _ := ed25519.GenerateKey(nil)
	sarahPub, sarahPriv, _ := ed25519.GenerateKey(nil)
	if err := st.RegisterKey("human:owner", ownerPub, now); err != nil {
		t.Fatal(err)
	}
	if err := st.RegisterKey("human:sarah", sarahPub, now); err != nil {
		t.Fatal(err)
	}
	// owner sees everything; sarah is scoped to comms only.
	if err := st.AddMembership("human:owner", "*", "test", now); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMembership("human:sarah", "comms", "human:owner", now); err != nil {
		t.Fatal(err)
	}

	// Seed one event in each room, authored by the owner (a member of both).
	for _, room := range []string{"comms", "secret"} {
		if _, err := st.Append(core.Event{Room: room, Author: "human:owner",
			Kind: core.KindChat, Body: map[string]any{"text": "hi from " + room},
			Lane: core.LaneOf(core.KindChat)}, "seed-"+room, now); err != nil {
			t.Fatal(err)
		}
	}

	sv := New(st, func() time.Time { return now })
	h = sv.Routes()

	_, oOut, _ := mintSession(t, h, "human:owner", ownerPriv)
	_, sOut, _ := mintSession(t, h, "human:sarah", sarahPriv)
	ownerTok, _ = oOut["token"].(string)
	sarahTok, _ = sOut["token"].(string)
	if ownerTok == "" || sarahTok == "" {
		t.Fatal("both seats must get a session")
	}
	return h, ownerTok, sarahTok
}

func sess(tok string) map[string]string { return map[string]string{SessionHeader: tok} }

// jsess is a session that also asks for JSON, so /rooms and /rooms/{name}
// return their API payload rather than redirecting a browser to the room page.
func jsess(tok string) map[string]string {
	return map[string]string{SessionHeader: tok, "Accept": "application/json"}
}

// The room list a seat sees is its own rooms. sarah, scoped to comms, must not
// even learn that "secret" exists.
func TestRoomsListIsFilteredToMembership(t *testing.T) {
	h, ownerTok, sarahTok := scopedServer(t)

	owner := gated(h, "GET", "/rooms", jsess(ownerTok), "")
	if !strings.Contains(owner.Body.String(), "comms") || !strings.Contains(owner.Body.String(), "secret") {
		t.Errorf("the all-rooms owner must see every room, got %s", owner.Body.String())
	}

	sarah := gated(h, "GET", "/rooms", jsess(sarahTok), "")
	if !strings.Contains(sarah.Body.String(), "comms") {
		t.Errorf("sarah must see her own room, got %s", sarah.Body.String())
	}
	if strings.Contains(sarah.Body.String(), "secret") {
		t.Errorf("sarah must not see a room she is not a member of, got %s", sarah.Body.String())
	}
}

// Fetching a room a seat is not a member of is a 404 — never a 200 with content,
// never a 403 that would confirm the room exists.
func TestRoomPageAndBriefRefuseNonMemberRooms(t *testing.T) {
	h, _, sarahTok := scopedServer(t)

	// sarah's own room renders.
	if w := gated(h, "GET", "/?room=comms", sess(sarahTok), ""); w.Code != http.StatusOK {
		t.Errorf("sarah's own room must render, got %d", w.Code)
	}
	// The room she is scoped away from is a 404 — content and existence both hidden.
	if w := gated(h, "GET", "/?room=secret", sess(sarahTok), ""); w.Code != http.StatusNotFound {
		t.Errorf("a non-member room page must 404, got %d", w.Code)
	}
	if w := gated(h, "GET", "/rooms/secret", jsess(sarahTok), ""); w.Code != http.StatusNotFound {
		t.Errorf("a non-member room brief must 404, got %d", w.Code)
	}
	if body := gated(h, "GET", "/?room=secret", sess(sarahTok), "").Body.String(); strings.Contains(body, "hi from secret") {
		t.Error("a non-member room page must not leak content")
	}
}

// Search never returns hits from a room the seat is not in, and a search
// explicitly scoped to a non-member room returns nothing rather than an error
// that confirms the room.
func TestSearchIsFilteredToMembership(t *testing.T) {
	h, _, sarahTok := scopedServer(t)

	// A search across everything returns only sarah's room's hits.
	all := gated(h, "GET", "/search?q=hi", sess(sarahTok), "").Body.String()
	if !strings.Contains(all, "hi from comms") {
		t.Errorf("sarah's search must find her own room's content, got %s", all)
	}
	if strings.Contains(all, "hi from secret") {
		t.Errorf("sarah's search must not surface a non-member room, got %s", all)
	}

	// A search pinned to the non-member room yields nothing, not a leak.
	pinned := gated(h, "GET", "/search?q=hi&room=secret", sess(sarahTok), "").Body.String()
	if strings.Contains(pinned, "hi from secret") {
		t.Errorf("searching a non-member room must return nothing, got %s", pinned)
	}
}

// The SSE stream refuses to subscribe a seat to a room it is not a member of,
// before any event is delivered — the live-read equivalent of the room page
// 404. (Only the refusal is asserted synchronously: a successful subscription
// holds the connection open, so the member path is covered by the owner/room
// tests rather than blocking here.)
func TestStreamRefusesNonMemberRoom(t *testing.T) {
	h, _, sarahTok := scopedServer(t)

	no := gated(h, "GET", "/stream?room=secret", sess(sarahTok), "")
	if no.Code != http.StatusNotFound {
		t.Errorf("a non-member stream subscription must 404, got %d", no.Code)
	}
	if strings.Contains(no.Body.String(), "hi from secret") {
		t.Error("a refused stream must not have delivered the room's backlog")
	}
}

// The owner, holding the all-rooms wildcard, sees and streams everything —
// scoping filters other seats, never the owner.
func TestAllRoomsSeatSeesEverything(t *testing.T) {
	h, ownerTok, _ := scopedServer(t)
	for _, path := range []string{"/?room=comms", "/?room=secret"} {
		if w := gated(h, "GET", path, sess(ownerTok), ""); w.Code != http.StatusOK {
			t.Errorf("the all-rooms owner must reach %s, got %d", path, w.Code)
		}
	}
	// The brief is the JSON API for a room; the owner reaches every room's.
	if w := gated(h, "GET", "/rooms/secret", jsess(ownerTok), ""); w.Code != http.StatusOK {
		t.Errorf("the all-rooms owner must reach /rooms/secret, got %d", w.Code)
	}
	body := gated(h, "GET", "/search?q=hi", sess(ownerTok), "").Body.String()
	if !strings.Contains(body, "hi from comms") || !strings.Contains(body, "hi from secret") {
		t.Errorf("the owner's search must span every room, got %s", body)
	}
}

// Reads are always authenticated — there is no open-read mode. A hub built with
// no flags refuses an anonymous read on every path. This is the polarity flip:
// a system holding a permanent, secret-bearing log does not serve it
// unauthenticated, and room scoping is meaningless without read attribution.
func TestReadsAreAlwaysAuthenticated(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "alwaysauth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.EnsureRoom("core"); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)

	sv := New(st, func() time.Time { return now }) // no flags: still authenticated
	h := sv.Routes()

	for _, path := range []string{"/index", "/rooms", "/actors", "/stream?room=core", "/search?q=x", "/?room=core"} {
		w := gated(h, "GET", path, nil, "")
		if w.Code != http.StatusUnauthorized {
			t.Errorf("an anonymous read of %s must be refused, got %d", path, w.Code)
		}
	}
}

// Loopback reads keep the full view: the gate has no seat for a loopback
// request (being on the box is operator trust), so it is never filtered as a
// member of nothing.
func TestLoopbackReadsAreNotFiltered(t *testing.T) {
	h, _, _ := scopedServer(t)
	r := httptest.NewRequest("GET", "/rooms?format=json", nil)
	r.RemoteAddr = "127.0.0.1:54321" // loopback: the operator view
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("loopback read must pass the gate, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "secret") {
		t.Errorf("a loopback read is the operator view and must see every room, got %s", w.Body.String())
	}
}
