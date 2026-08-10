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

// gatedServer is a hub with -read-auth on and one enrolled seat. The clock is
// a pointer so tests can age challenges and sessions.
func gatedServer(t *testing.T) (http.Handler, *store.Store, ed25519.PrivateKey, *time.Time) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "gated.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
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
	sv := New(st, func() time.Time { return now })
	sv.ReadAuth = true
	return sv.Routes(), st, priv, &now
}

// gated sends one request from a non-loopback address, which is what a real
// deployment sees. httptest.NewRequest's default RemoteAddr is 192.0.2.1.
func gated(h http.Handler, method, path string, hdr map[string]string, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// mintSession walks the whole flow: fetch a challenge, sign it, trade it in.
func mintSession(t *testing.T, h http.Handler, actor string, priv ed25519.PrivateKey) (status int, out map[string]any, resp *httptest.ResponseRecorder) {
	t.Helper()
	cw := gated(h, "GET", "/session/challenge", nil, "")
	if cw.Code != http.StatusOK {
		t.Fatalf("challenge: %d %s", cw.Code, cw.Body.String())
	}
	var ch struct {
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(cw.Body.Bytes(), &ch); err != nil || ch.Challenge == "" {
		t.Fatalf("no challenge in %s", cw.Body.String())
	}

	payload, _ := json.Marshal(map[string]any{"actor": actor, "challenge": ch.Challenge})
	sig := hex.EncodeToString(ed25519.Sign(priv, payload))
	resp = gated(h, "POST", "/session",
		map[string]string{"Content-Type": "application/json", "X-Signature": sig},
		string(payload))
	out = map[string]any{}
	_ = json.Unmarshal(resp.Body.Bytes(), &out)
	return resp.Code, out, resp
}

func TestReadsStayOpenWithoutReadAuth(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "open.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.EnsureRoom("core"); err != nil {
		t.Fatal(err)
	}
	h := New(st, nil).Routes()
	if w := gated(h, "GET", "/index", nil, ""); w.Code != http.StatusOK {
		t.Errorf("with read auth off, an anonymous read must work, got %d", w.Code)
	}
}

func TestReadAuthRefusesAnonymousReads(t *testing.T) {
	h, _, _, _ := gatedServer(t)
	for _, path := range []string{"/index", "/rooms", "/actors", "/stream?room=core", "/search?q=x"} {
		w := gated(h, "GET", path, nil, "")
		if w.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without a session must 401, got %d", path, w.Code)
		}
		if !strings.Contains(w.Body.String(), "session.required") {
			t.Errorf("GET %s should name session.required, got %s", path, w.Body.String())
		}
	}
	// The unauthenticated writes are gated too: without this, anyone could
	// still fill the artifact store or move another seat's cursor.
	if w := gated(h, "POST", "/artifacts",
		map[string]string{"Content-Type": "text/markdown"}, "# x"); w.Code != http.StatusUnauthorized {
		t.Errorf("POST /artifacts without a session must 401, got %d", w.Code)
	}
	if w := gated(h, "POST", "/delivered",
		map[string]string{"Content-Type": "application/json"},
		`{"actor":"human:bcm","room":"core","addressed_through":1}`); w.Code != http.StatusUnauthorized {
		t.Errorf("POST /delivered without a session must 401, got %d", w.Code)
	}
}

func TestReadAuthServesTheUnlockPageToBrowsers(t *testing.T) {
	h, _, _, _ := gatedServer(t)
	w := gated(h, "GET", "/", map[string]string{"Accept": "text/html,application/xhtml+xml"}, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("browser without a session must get 401, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("the 401 body for a browser must be a page, got %s", ct)
	}
	if !strings.Contains(w.Body.String(), "read session") {
		t.Errorf("the unlock page should say what it is for")
	}
}

func TestAnEnrolledKeyMintsASessionThatUnlocksReads(t *testing.T) {
	h, _, priv, _ := gatedServer(t)
	code, out, resp := mintSession(t, h, "human:bcm", priv)
	if code != http.StatusOK {
		t.Fatalf("session should be granted: %d %v", code, out)
	}
	token, _ := out["token"].(string)
	if token == "" {
		t.Fatal("a granted session carries a token")
	}

	if w := gated(h, "GET", "/index", map[string]string{SessionHeader: token}, ""); w.Code != http.StatusOK {
		t.Errorf("the header transport must unlock reads, got %d: %s", w.Code, w.Body.String())
	}

	// The cookie is the browser's transport for the same token.
	cookies := resp.Result().Cookies()
	var cookie string
	for _, c := range cookies {
		if c.Name == SessionCookie {
			cookie = c.Name + "=" + c.Value
		}
	}
	if cookie == "" {
		t.Fatal("granting a session must also set the cookie")
	}
	if w := gated(h, "GET", "/index", map[string]string{"Cookie": cookie}, ""); w.Code != http.StatusOK {
		t.Errorf("the cookie transport must unlock reads, got %d", w.Code)
	}
}

func TestAChallengeIsSingleUse(t *testing.T) {
	h, _, priv, _ := gatedServer(t)

	cw := gated(h, "GET", "/session/challenge", nil, "")
	var ch struct {
		Challenge string `json:"challenge"`
	}
	_ = json.Unmarshal(cw.Body.Bytes(), &ch)
	payload, _ := json.Marshal(map[string]any{"actor": "human:bcm", "challenge": ch.Challenge})
	sig := hex.EncodeToString(ed25519.Sign(priv, payload))
	hdr := map[string]string{"Content-Type": "application/json", "X-Signature": sig}

	if w := gated(h, "POST", "/session", hdr, string(payload)); w.Code != http.StatusOK {
		t.Fatalf("first redemption should succeed, got %d", w.Code)
	}
	replay := gated(h, "POST", "/session", hdr, string(payload))
	if replay.Code != http.StatusForbidden {
		t.Errorf("replaying a captured signature must fail, got %d", replay.Code)
	}
	if !strings.Contains(replay.Body.String(), "challenge.unknown") {
		t.Errorf("the replay refusal should name the challenge, got %s", replay.Body.String())
	}
}

func TestAnUnenrolledKeyCannotMintASession(t *testing.T) {
	h, _, _, _ := gatedServer(t)
	_, stranger, _ := ed25519.GenerateKey(nil)
	code, out, _ := mintSession(t, h, "human:mallory", stranger)
	if code != http.StatusUnauthorized {
		t.Errorf("an unenrolled actor must be refused, got %d %v", code, out)
	}
}

func TestARevokedKeyCannotMintASession(t *testing.T) {
	h, st, priv, now := gatedServer(t)
	if err := st.RevokeKey("human:bcm", *now); err != nil {
		t.Fatal(err)
	}
	code, out, _ := mintSession(t, h, "human:bcm", priv)
	if code != http.StatusUnauthorized {
		t.Errorf("a revoked key must not open a read session, got %d %v", code, out)
	}
}

func TestSessionsExpire(t *testing.T) {
	h, _, priv, now := gatedServer(t)
	_, out, _ := mintSession(t, h, "human:bcm", priv)
	token, _ := out["token"].(string)

	*now = now.Add(sessionTTL + time.Minute)
	if w := gated(h, "GET", "/index", map[string]string{SessionHeader: token}, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("an expired session must be refused, got %d", w.Code)
	}
}

func TestAnExpiredChallengeIsRefused(t *testing.T) {
	h, _, priv, now := gatedServer(t)

	cw := gated(h, "GET", "/session/challenge", nil, "")
	var ch struct {
		Challenge string `json:"challenge"`
	}
	_ = json.Unmarshal(cw.Body.Bytes(), &ch)

	*now = now.Add(challengeTTL + time.Minute)
	payload, _ := json.Marshal(map[string]any{"actor": "human:bcm", "challenge": ch.Challenge})
	sig := hex.EncodeToString(ed25519.Sign(priv, payload))
	w := gated(h, "POST", "/session",
		map[string]string{"Content-Type": "application/json", "X-Signature": sig},
		string(payload))
	if w.Code != http.StatusForbidden {
		t.Errorf("a stale challenge must be refused, got %d", w.Code)
	}
}

// Loopback bypasses the gate the same way it may mint invites: being on the
// box is holding the database.
func TestLoopbackBypassesReadAuth(t *testing.T) {
	h, _, _, _ := gatedServer(t)
	r := httptest.NewRequest("GET", "/index", nil)
	r.RemoteAddr = "127.0.0.1:54321"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("loopback reads must not need a session, got %d", w.Code)
	}
}

// Routes that re-prove identity on every call stay open: gating them behind a
// session would add nothing and break enrolment's bootstrap.
func TestSelfAuthenticatingRoutesPassTheGate(t *testing.T) {
	h, _, priv, _ := gatedServer(t)
	body := `{"room":"core","author":"human:bcm","kind":"chat","body":{"text":"hi"},"idem":"g1"}`
	sig := hex.EncodeToString(ed25519.Sign(priv, []byte(body)))
	w := gated(h, "POST", "/commands",
		map[string]string{"Content-Type": "application/json", "X-Signature": sig}, body)
	if w.Code != http.StatusOK {
		t.Errorf("a signed command needs no session, got %d: %s", w.Code, w.Body.String())
	}
}
