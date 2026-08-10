package cli

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/escherize/comms/shell"
	"github.com/escherize/comms/store"
)

// remoteHub is a real hub seen from a non-loopback address, which is what
// makes the capability check on minting reachable at all: loopback may
// always mint, and httptest connects over loopback.
func remoteHub(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "via.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.EnsureRoom("core"); err != nil {
		t.Fatal(err)
	}
	h := shell.New(st, time.Now).Routes()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = "203.0.113.9:50000"
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, st
}

// One session, one seat: a session mints its own through a bootstrap seat's
// key, in one process, with no token on any pipe.
func TestASessionEnrolsItsOwnSeatViaABootstrap(t *testing.T) {
	isolateKeys(t)
	srv, st := remoteHub(t)
	enrol(t, srv, st) // the bootstrap: `seat`, enrolled via a piped token
	if err := st.Grant(seat, shell.CapInvite, "operator", time.Now()); err != nil {
		t.Fatal(err)
	}

	var c capture
	code := Run(c.env(t, srv.URL, ""),
		[]string{"enrol", "--as", "agent:bcm/claude-s7", "--via", seat})
	if code != ExitOK {
		t.Fatalf("enrol --via should mint and redeem in one process, got %d: %s %s",
			code, c.out.String(), c.err.String())
	}
	if !st.ActorEnrolled("agent:bcm/claude-s7") {
		t.Error("the new seat should be on the roster")
	}
	if !HasSeat("agent:bcm/claude-s7") {
		t.Error("the new seat's key should be in the keyring")
	}
	if strings.Contains(c.out.String(), `"token"`) {
		t.Error("the minted token must not appear on stdout; it was spent in-process")
	}
}

func TestViaWithoutTheCapabilityIsRefusedWithTheGrant(t *testing.T) {
	isolateKeys(t)
	srv, st := remoteHub(t)
	enrol(t, srv, st) // enrolled, but never granted invite

	var c capture
	code := Run(c.env(t, srv.URL, ""),
		[]string{"enrol", "--as", "agent:bcm/claude-s8", "--via", seat})
	if code == ExitOK {
		t.Fatal("a via seat without the invite capability must be refused")
	}
	if !strings.Contains(c.out.String(), "invite") {
		t.Errorf("the refusal should point at the capability, got %s", c.out.String())
	}
	if st.ActorEnrolled("agent:bcm/claude-s8") {
		t.Error("nothing should have been enrolled")
	}
}

func TestViaWithoutALocalKeyFailsBeforeTheNetwork(t *testing.T) {
	isolateKeys(t)
	srv, _ := remoteHub(t)

	var c capture
	code := Run(c.env(t, srv.URL, ""),
		[]string{"enrol", "--as", "agent:bcm/claude-s9", "--via", "agent:bcm/ghost"})
	if code == ExitOK {
		t.Fatal("--via needs the via seat's key on this machine")
	}
	if !strings.Contains(c.out.String(), "no key") {
		t.Errorf("the refusal should say the key is missing, got %s", c.out.String())
	}
}
