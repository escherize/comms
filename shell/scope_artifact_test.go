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

// An artifact is content-addressed and cross-room: the same hash can be
// referenced from any room. Access to /a/{hash} must therefore be decided by
// membership in a room that references it — a raw hash is not a bypass around
// room scoping. A scoped seat reaches an artifact referenced in its room and is
// 404'd for one referenced only in a room it cannot see.
func artifactScopedServer(t *testing.T) (h http.Handler, sarahTok string, secretHash, commsHash string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "art.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	for _, r := range []string{"comms", "secret"} {
		if err := st.EnsureRoom(r); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)

	ownerPub, _, _ := ed25519.GenerateKey(nil)
	sarahPub, sarahPriv, _ := ed25519.GenerateKey(nil)
	if err := st.RegisterKey("human:owner", ownerPub, now); err != nil {
		t.Fatal(err)
	}
	if err := st.RegisterKey("human:sarah", sarahPub, now); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMembership("human:owner", "*", "test", now); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMembership("human:sarah", "comms", "human:owner", now); err != nil {
		t.Fatal(err)
	}

	// Store two artifacts; reference one from comms, the other only from secret.
	commsHash, err = st.PutArtifact([]byte("# comms report\nnothing secret\n"), now)
	if err != nil {
		t.Fatal(err)
	}
	secretHash, err = st.PutArtifact([]byte("# secret report\nsk-live-DO-NOT-LEAK\n"), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(core.Event{Room: "comms", Author: "human:owner",
		Kind: core.Kind("finding"), Body: map[string]any{"text": "see report", "severity": "p2"},
		Attachments: []core.Attachment{{Hash: commsHash, Title: "comms.md"}},
		Lane:        core.Ambient}, "a-comms", now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(core.Event{Room: "secret", Author: "human:owner",
		Kind: core.Kind("finding"), Body: map[string]any{"text": "see report", "severity": "p2"},
		Attachments: []core.Attachment{{Hash: secretHash, Title: "secret.md"}},
		Lane:        core.Ambient}, "a-secret", now); err != nil {
		t.Fatal(err)
	}

	sv := New(st, func() time.Time { return now })
	h = sv.Routes()
	_, sOut, _ := mintSession(t, h, "human:sarah", sarahPriv)
	sarahTok, _ = sOut["token"].(string)
	if sarahTok == "" {
		t.Fatal("sarah must get a session")
	}
	return h, sarahTok, secretHash, commsHash
}

func TestArtifactAccessFollowsRoomMembership(t *testing.T) {
	h, sarahTok, secretHash, commsHash := artifactScopedServer(t)

	// sarah is a member of comms: the artifact referenced there is hers to read.
	if w := gated(h, "GET", "/a/"+commsHash, sess(sarahTok), ""); w.Code != http.StatusOK {
		t.Errorf("an artifact referenced in a member room must be served, got %d", w.Code)
	}

	// The secret room's artifact is 404 — a raw hash is not a bypass, and the
	// 404 does not distinguish "no such artifact" from "not yours to see".
	w := gated(h, "GET", "/a/"+secretHash, sess(sarahTok), "")
	if w.Code != http.StatusNotFound {
		t.Errorf("an artifact referenced only in a non-member room must 404, got %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "DO-NOT-LEAK") {
		t.Error("a refused artifact must not leak its content")
	}
}

// A loopback operator reaches every artifact — the full view.
func TestArtifactAccessLoopback(t *testing.T) {
	h, _, secretHash, _ := artifactScopedServer(t)

	r := httptest.NewRequest("GET", "/a/"+secretHash, nil)
	r.RemoteAddr = "127.0.0.1:5000" // loopback: the operator view
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("loopback must reach every artifact, got %d", w.Code)
	}
}

// A hash nobody references at all is a plain 404, never served — an artifact
// with no referencing event has no room, so no membership can admit it.
func TestUnreferencedArtifactIsNotServed(t *testing.T) {
	h, sarahTok, _, _ := artifactScopedServer(t)

	st2, _ := store.Open(filepath.Join(t.TempDir(), "orphan.db"))
	defer st2.Close()
	orphan, _ := st2.PutArtifact([]byte("# orphan\n"), time.Now())

	// The orphan hash exists in a different store; against this hub it is simply
	// unknown, so a member session still 404s.
	if w := gated(h, "GET", "/a/"+orphan, sess(sarahTok), ""); w.Code != http.StatusNotFound {
		t.Errorf("an artifact referenced by no event must 404, got %d", w.Code)
	}
}
