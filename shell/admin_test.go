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

	"github.com/bcm/agent_comms/store"
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

	r := httptest.NewRequest("GET", "/caps?actor=human:bcm", nil)
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
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r2)
	if !strings.Contains(w2.Body.String(), `"capabilities":[]`) {
		t.Errorf("empty capabilities must serialize as [], got %s", w2.Body.String())
	}
}
