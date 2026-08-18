package shell

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/escherize/comms/store"
)

func adminServer(t *testing.T) (http.Handler, *store.Store, ed25519.PrivateKey) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "admin.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.EnsureRoom("core"); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	pub, priv, _ := ed25519.GenerateKey(nil)
	if err := st.RegisterKey("human:bcm", pub, now); err != nil {
		t.Fatal(err)
	}
	return New(st, func() time.Time { return now }).Routes(), st, priv
}

// postRoomAs signs the exact bytes sent, from a non-loopback address.
func postRoomAs(h http.Handler, body string, priv ed25519.PrivateKey) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/rooms", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if priv != nil {
		r.Header.Set("X-Signature", hex.EncodeToString(ed25519.Sign(priv, []byte(body))))
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestRoomCreationIsCapabilityOrLoopback(t *testing.T) {
	h, st, priv := adminServer(t)
	body := `{"name":"bash","as":"human:bcm"}`

	if w := postRoomAs(h, body, priv); w.Code != http.StatusForbidden {
		t.Errorf("a seat without the capability must be refused, got %d: %s", w.Code, w.Body.String())
	}

	if err := st.Grant("human:bcm", CapInvite, "operator", time.Now()); err != nil {
		t.Fatal(err)
	}
	if w := postRoomAs(h, body, priv); w.Code != http.StatusOK {
		t.Errorf("holding the capability and signing must create, got %d: %s", w.Code, w.Body.String())
	}
	if !st.RoomExists("bash") {
		t.Error("the room should exist after creation")
	}

	// Idempotent: creating an existing room reports it rather than failing,
	// so a double-submit is not an error a human has to interpret.
	if w := postRoomAs(h, body, priv); w.Code != http.StatusOK ||
		!strings.Contains(w.Body.String(), "exists") {
		t.Errorf("re-creating should say exists, got %d: %s", w.Code, w.Body.String())
	}

	// Loopback needs no capability: being on the box is holding the database.
	r := httptest.NewRequest("POST", "/rooms", strings.NewReader(`{"name":"drills"}`))
	r.RemoteAddr = "127.0.0.1:9"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("loopback creation should work, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRoomNamesAreValidated(t *testing.T) {
	h, st, priv := adminServer(t)
	if err := st.Grant("human:bcm", CapInvite, "operator", time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "Core", "a room", "x/y", strings.Repeat("a", 33)} {
		raw, _ := json.Marshal(map[string]string{"name": bad, "as": "human:bcm"})
		if w := postRoomAs(h, string(raw), priv); w.Code != http.StatusUnprocessableEntity {
			t.Errorf("name %q should be rejected, got %d", bad, w.Code)
		}
	}
}

func TestCapsListsWhatASeatHolds(t *testing.T) {
	h, st, _ := adminServer(t)
	if err := st.Grant("human:bcm", CapInvite, "operator", time.Now()); err != nil {
		t.Fatal(err)
	}

	// /caps is a gated read (it reports who holds what — not for anonymous
	// eyes); the operator/browser reaches it from loopback or with a session.
	// These requests come from loopback, the operator view.
	r := httptest.NewRequest("GET", "/caps?actor=human:bcm", nil)
	r.RemoteAddr = "127.0.0.1:5000"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var out struct {
		Capabilities []string `json:"capabilities"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("caps must be JSON: %v", err)
	}
	if len(out.Capabilities) != 1 || out.Capabilities[0] != "invite" {
		t.Errorf("want [invite], got %v", out.Capabilities)
	}

	// A seat with nothing gets an empty list, not null: the browser iterates it.
	r2 := httptest.NewRequest("GET", "/caps?actor=human:nobody", nil)
	r2.RemoteAddr = "127.0.0.1:5000"
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r2)
	if !strings.Contains(w2.Body.String(), `"capabilities":[]`) {
		t.Errorf("empty capabilities must serialize as [], got %s", w2.Body.String())
	}
}

// postInviteAs signs an /invite body from a non-loopback address, like a
// remote admin minting through the capability.
func postInviteAs(h http.Handler, body string, priv ed25519.PrivateKey) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/invite", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if priv != nil {
		r.Header.Set("X-Signature", hex.EncodeToString(ed25519.Sign(priv, []byte(body))))
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// A scoped admin may only mint invites within its own rooms; an all-rooms
// admin and loopback may mint anything. Without this, a scoped seat holding the
// invite capability could grant itself reach it does not have — invite a seat
// into a room it cannot see, then enrol as that seat.
func TestScopedAdminMintsOnlyWithinItsRooms(t *testing.T) {
	h, st, priv := adminServer(t)
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	for _, r := range []string{"comms", "ops", "secret"} {
		if err := st.EnsureRoom(r); err != nil {
			t.Fatal(err)
		}
	}
	// human:bcm holds the invite capability but is scoped to comms + ops only.
	if err := st.Grant("human:bcm", CapInvite, "operator", now); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMembership("human:bcm", "comms", "operator", now); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMembership("human:bcm", "ops", "operator", now); err != nil {
		t.Fatal(err)
	}

	// Within its rooms: allowed.
	ok := postInviteAs(h, `{"actor":"human:sarah","as":"human:bcm","rooms":"comms,ops"}`, priv)
	if ok.Code != http.StatusOK {
		t.Errorf("a scoped admin must mint within its rooms, got %d: %s", ok.Code, ok.Body.String())
	}

	// Outside its rooms: refused, naming the room it lacks.
	no := postInviteAs(h, `{"actor":"human:sarah","as":"human:bcm","rooms":"comms,secret"}`, priv)
	if no.Code != http.StatusForbidden || !strings.Contains(no.Body.String(), "invite.scope_exceeds_grant") {
		t.Errorf("minting into a non-member room must be refused, got %d: %s", no.Code, no.Body.String())
	}
	if !strings.Contains(no.Body.String(), "secret") {
		t.Errorf("the refusal should name the room the admin lacks, got %s", no.Body.String())
	}

	// A scoped admin cannot mint an all-rooms invite — that exceeds its grant.
	allReq := postInviteAs(h, `{"actor":"human:sarah","as":"human:bcm","rooms":"all"}`, priv)
	if allReq.Code != http.StatusForbidden {
		t.Errorf("a scoped admin minting 'all' must be refused, got %d: %s", allReq.Code, allReq.Body.String())
	}
}

// An all-rooms admin (and loopback) may mint any scope.
func TestAllRoomsAdminMintsAnyScope(t *testing.T) {
	h, st, priv := adminServer(t)
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	if err := st.EnsureRoom("secret"); err != nil {
		t.Fatal(err)
	}
	if err := st.Grant("human:bcm", CapInvite, "operator", now); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMembership("human:bcm", "*", "operator", now); err != nil {
		t.Fatal(err)
	}
	w := postInviteAs(h, `{"actor":"human:sarah","as":"human:bcm","rooms":"secret"}`, priv)
	if w.Code != http.StatusOK {
		t.Errorf("an all-rooms admin must mint any scope, got %d: %s", w.Code, w.Body.String())
	}

	// Loopback mints anything with no capability at all.
	r := httptest.NewRequest("POST", "/invite", strings.NewReader(`{"actor":"human:eve","rooms":"secret"}`))
	r.RemoteAddr = "127.0.0.1:9"
	lw := httptest.NewRecorder()
	h.ServeHTTP(lw, r)
	if lw.Code != http.StatusOK {
		t.Errorf("loopback must mint any scope, got %d: %s", lw.Code, lw.Body.String())
	}
}

// A scoped admin minting from loopback is still bound by its own rooms: naming
// a seat (--as) means identity wins over locality. Only a bare loopback mint
// with no named seat is the operator, who grants anything.
func TestScopedAdminOnLoopbackStillBounded(t *testing.T) {
	h, st, priv := adminServer(t)
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	for _, r := range []string{"comms", "secret"} {
		if err := st.EnsureRoom(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Grant("human:bcm", CapInvite, "operator", now); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMembership("human:bcm", "comms", "operator", now); err != nil {
		t.Fatal(err)
	}

	// From loopback, but naming itself as a scoped seat: the subset rule holds.
	body := `{"actor":"human:sarah","as":"human:bcm","rooms":"secret"}`
	r := httptest.NewRequest("POST", "/invite", strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:9"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Signature", hex.EncodeToString(ed25519.Sign(priv, []byte(body))))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "scope_exceeds_grant") {
		t.Errorf("a scoped admin on loopback must still be bounded, got %d: %s", w.Code, w.Body.String())
	}

	// A bare loopback mint (no --as) is the operator: any scope.
	bare := httptest.NewRequest("POST", "/invite", strings.NewReader(`{"actor":"human:eve","rooms":"secret"}`))
	bare.RemoteAddr = "127.0.0.1:9"
	bw := httptest.NewRecorder()
	h.ServeHTTP(bw, bare)
	if bw.Code != http.StatusOK {
		t.Errorf("a bare loopback operator mint must grant any scope, got %d: %s", bw.Code, bw.Body.String())
	}
}

// A superuser is a seat holding both all-rooms membership and the invite
// capability. Minting a superuser is an escalation unless the granter is
// itself one: an all-rooms admin lacking the capability, and a scoped admin,
// are both refused. Loopback and an actual superuser may mint one.
func TestSuperuserMintRequiresSuperuser(t *testing.T) {
	h, st, priv := adminServer(t)
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)

	// human:bcm holds the invite capability but is scoped (not all-rooms):
	// it may mint within its rooms, never a superuser.
	if err := st.Grant("human:bcm", CapInvite, "operator", now); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureRoom("comms"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMembership("human:bcm", "comms", "operator", now); err != nil {
		t.Fatal(err)
	}
	no := postInviteAs(h, `{"actor":"human:x","as":"human:bcm","rooms":"superuser"}`, priv)
	if no.Code != http.StatusForbidden || !strings.Contains(no.Body.String(), "scope_exceeds_grant") {
		t.Errorf("a scoped admin must not mint a superuser, got %d: %s", no.Code, no.Body.String())
	}

	// Grant bcm all-rooms too — now it holds '*' AND invite, which IS a
	// superuser, so it may mint another.
	if err := st.AddMembership("human:bcm", "*", "operator", now); err != nil {
		t.Fatal(err)
	}
	ok := postInviteAs(h, `{"actor":"human:y","as":"human:bcm","rooms":"superuser"}`, priv)
	if ok.Code != http.StatusOK {
		t.Errorf("a superuser (all-rooms + invite) must mint another, got %d: %s", ok.Code, ok.Body.String())
	}

	// Loopback (the operator) may mint a superuser with no seat at all.
	r := httptest.NewRequest("POST", "/invite", strings.NewReader(`{"actor":"human:root","rooms":"superuser"}`))
	r.RemoteAddr = "127.0.0.1:9"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("loopback must mint a superuser, got %d: %s", w.Code, w.Body.String())
	}
}

// A deployed hub is dialled over loopback when minting (fly ssh console), so
// the client cannot know the public hostname. The hub can: --public-url rides
// back on the invite response and the CLI composes the setup link from it.
func TestInviteResponseCarriesPublicURL(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "pub.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	srv := New(st, func() time.Time { return now })
	srv.PublicURL = "https://hub.example"
	h := srv.Routes()

	r := httptest.NewRequest("POST", "/invite", strings.NewReader(`{"actor":"human:sarah"}`))
	r.RemoteAddr = "127.0.0.1:9"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("loopback mint must succeed, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"public_url":"https://hub.example"`) {
		t.Errorf("invite response must carry the hub's public URL, got %s", w.Body.String())
	}
}
