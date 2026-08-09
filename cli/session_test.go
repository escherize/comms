package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bcm/agent_comms/shell"
	"github.com/bcm/agent_comms/store"
)

// gatedHub is a real hub with -read-auth on. httptest connects over loopback,
// which the gate trusts the way invite minting does — so the wrapper stamps a
// routable address on every request, making the client prove itself the way it
// would against a deployed hub.
func gatedHub(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "gated-cli.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.EnsureRoom("core"); err != nil {
		t.Fatal(err)
	}
	sv := shell.New(st, time.Now)
	sv.ReadAuth = true
	h := sv.Routes()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = "203.0.113.9:50000"
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, st
}

// The whole point of riding on enrolment: a seat that can post can read, and
// the session is established without any new verb, flag, or step.
func TestReadEstablishesASessionWhenTheHubDemandsOne(t *testing.T) {
	isolateKeys(t)
	srv, st := gatedHub(t)
	enrol(t, srv, st)

	var c capture
	code := Run(c.env(t, srv.URL, ""), []string{"read", "--as", seat})
	if code != ExitOK {
		t.Fatalf("read against a gated hub should self-establish a session, got %d: %s %s",
			code, c.out.String(), c.err.String())
	}

	if _, err := os.Stat(sessionFile(srv.URL, seat)); err != nil {
		t.Errorf("the session should be cached for the next read: %v", err)
	}
}

func TestTheCachedSessionIsReusedNotReminted(t *testing.T) {
	isolateKeys(t)
	srv, st := gatedHub(t)
	enrol(t, srv, st)

	var first capture
	if code := Run(first.env(t, srv.URL, ""), []string{"read", "--as", seat}); code != ExitOK {
		t.Fatalf("first read: %d", code)
	}
	before, err := os.ReadFile(sessionFile(srv.URL, seat))
	if err != nil {
		t.Fatal(err)
	}

	var second capture
	if code := Run(second.env(t, srv.URL, ""), []string{"read", "--as", seat}); code != ExitOK {
		t.Fatalf("second read: %d", code)
	}
	after, _ := os.ReadFile(sessionFile(srv.URL, seat))
	if string(before) != string(after) {
		t.Error("a live session must be reused, not replaced on every read")
	}
}

// A seat that never enrolled gets the server's refusal, and the client's error
// says what to do rather than looping on the 401.
func TestAnUnenrolledSeatIsRefusedWithGuidance(t *testing.T) {
	isolateKeys(t)
	srv, _ := gatedHub(t)

	var c capture
	code := Run(c.env(t, srv.URL, ""), []string{"read", "--as", "human:stranger"})
	if code == ExitOK {
		t.Fatalf("a seat with no key must not read a gated hub: %s", c.out.String())
	}
	combined := c.out.String() + c.err.String()
	if !strings.Contains(combined, "session") && !strings.Contains(combined, "enrol") {
		t.Errorf("the refusal should point at enrolment or the session, got %s", combined)
	}
}

// A restarted hub forgets every session; the client must re-establish from the
// stale token without surfacing an error.
func TestAStaleSessionIsReplacedTransparently(t *testing.T) {
	isolateKeys(t)
	srv, st := gatedHub(t)
	enrol(t, srv, st)

	saveSession(srv.URL, seat, "stale-token-from-before-the-restart")

	var c capture
	if code := Run(c.env(t, srv.URL, ""), []string{"read", "--as", seat}); code != ExitOK {
		t.Fatalf("a stale session must be replaced, not fatal, got %d: %s", code, c.out.String())
	}
	after, _ := os.ReadFile(sessionFile(srv.URL, seat))
	if strings.Contains(string(after), "stale-token") {
		t.Error("the stale token should have been overwritten")
	}
}
