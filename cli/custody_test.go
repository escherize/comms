package cli

import (
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The seat file is authoritative and the environment is not. Redirecting a
// signing client at another hub needs no key material and leaves nothing in
// argv, which is what makes it worth refusing rather than logging.
func TestASeatRefusesToSignForAnotherServer(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)

	if got := PinnedServer(seat); got != srv.URL {
		t.Fatalf("enrolment must pin the hub, got %q", got)
	}

	var c capture
	env := c.env(t, "http://evil.example", "")
	code := Run(env, []string{"post", "til", "--as", seat, "--text", "x"})
	if code != ExitUsage {
		t.Fatalf("a redirected seat must be refused, got %d: %s", code, c.out.String())
	}
	if l := lines(t, &c); l[len(l)-1]["invariant"] != "server.mismatch" {
		t.Errorf("want server.mismatch, got %v", l[len(l)-1])
	}

	// A trailing slash is the same hub, not a different one.
	var ok capture
	if code := Run(ok.env(t, srv.URL+"/", ""), []string{"post", "til", "--as", seat,
		"--text", "same hub"}); code != ExitOK {
		t.Errorf("a trailing slash must not read as a different server: %s", ok.out.String())
	}
}

// attach reads a file and posts its contents, so an unbounded path turns one
// prompt-injected line into exfiltration of anything the agent can read.
func TestAttachRefusesAPathOutsideTheTree(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)

	outside := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(outside, []byte("PLACEHOLDER-NOT-A-KEY"), 0o600); err != nil {
		t.Fatal(err)
	}

	var c capture
	if code := Run(c.env(t, srv.URL, ""), []string{"attach", outside}); code != ExitUsage {
		t.Fatalf("a path outside the tree must be refused, got %d", code)
	}
	if l := lines(t, &c); l[len(l)-1]["invariant"] != "attach.outside_tree" {
		t.Errorf("want attach.outside_tree, got %v", l[len(l)-1])
	}

	// A symlink out of the tree is still out of the tree.
	link := "link-to-outside.md"
	if err := os.Symlink(outside, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	defer os.Remove(link)
	var s capture
	if code := Run(s.env(t, srv.URL, ""), []string{"attach", link}); code != ExitUsage {
		t.Error("a symlink out of the tree must be refused too")
	}

	// stdin is the documented path and carries no path at all.
	var in capture
	if code := Run(in.env(t, srv.URL, "# a report\n"), []string{"attach", "-"}); code != ExitOK {
		t.Errorf("stdin must still work: %s", in.out.String())
	}
}

// Transport failure reports success. An exit code that reads as failure is an
// instruction to every harness in existence to run the command again, and there
// is no --idem to make that safe.
func TestTransportFailureSpoolsAndReportsSuccess(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)
	claimed = stdinClaim{}

	// One address that goes away and comes back, which is what actually happens.
	// A seat is pinned to a hub, so a spool for a different hub is not a case
	// the client should paper over.
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	var broken atomic.Bool
	broken.Store(true)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if broken.Load() {
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, err := hj.Hijack(); err == nil {
					conn.Close() // dropped mid-request: a transport failure, not a rejection
					return
				}
			}
			w.WriteHeader(500)
			return
		}
		httputil.NewSingleHostReverseProxy(target).ServeHTTP(w, r)
	}))
	defer proxy.Close()
	if err := PinServer(seat, proxy.URL); err != nil {
		t.Fatal(err)
	}

	var c capture
	code := Run(c.env(t, proxy.URL, ""), []string{"post", "finding", "--as", seat,
		"--severity", "p1", "--text", "the suite is red"})
	if code != ExitOK {
		t.Fatalf("a held command must exit 0; %d tells a harness to run it again", code)
	}
	last := lines(t, &c)
	term := last[len(last)-1]
	if term["outcome"] != "spooled" {
		t.Fatalf("want outcome spooled, got %v", term)
	}
	if !strings.Contains(term["next"].(string), "Do not re-run") {
		t.Error("the reply must say not to re-run; that is the whole reason for exit 0")
	}
	if n := len(SpooledFor(seat)); n != 1 {
		t.Fatalf("want 1 held command, got %d", n)
	}

	// The hub comes back at the same address.
	broken.Store(false)
	var next capture
	if code := Run(next.env(t, proxy.URL, ""), []string{"post", "til", "--as", seat,
		"--text", "back online"}); code != ExitOK {
		t.Fatalf("the draining write failed: %s", next.out.String())
	}
	if !strings.Contains(next.out.String(), `"type":"drained"`) {
		t.Error("a drain must be reported, not silent")
	}
	if n := len(SpooledFor(seat)); n != 0 {
		t.Errorf("the spool should be empty, %d left", n)
	}

	recs, err := st.Since("core", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var red int
	for _, r := range recs {
		if strings.Contains(r.Text(), "the suite is red") {
			red++
		}
	}
	if red != 1 {
		t.Errorf("the held finding must land exactly once, got %d", red)
	}
}

// A status is dropped rather than held: it describes now, and a late one
// describes a moment that has passed. The projection guard is the belt; this is
// the braces.
func TestAStatusIsDroppedNotSpooled(t *testing.T) {
	isolateKeys(t)
	if err := Spool("agent:x", "http://h", "status", "i1", []byte(`{}`), "sig", time.Now()); err != nil {
		t.Fatal(err)
	}
	if n := len(SpooledFor("agent:x")); n != 0 {
		t.Errorf("a status must never be spooled, got %d held", n)
	}
}

// A revoked seat must not keep a queue of signed bytes that lands the moment
// somebody re-enrols it.
func TestRevocationDropsTheSpool(t *testing.T) {
	isolateKeys(t)
	for _, idem := range []string{"a", "b"} {
		if err := Spool(seat, "http://h", "finding", idem, []byte(`{}`), "sig", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(SpooledFor(seat)); n != 2 {
		t.Fatalf("setup: want 2 held, got %d", n)
	}
	if dropped := DropSpool(seat); dropped != 2 {
		t.Errorf("want 2 dropped, got %d", dropped)
	}
	if n := len(SpooledFor(seat)); n != 0 {
		t.Errorf("the spool must be empty after revocation, %d left", n)
	}
}

// Spool files hold signed bytes, so their mode is the same promise the key file
// makes.
func TestSpoolFilesAreOwnerOnly(t *testing.T) {
	isolateKeys(t)
	if err := Spool(seat, "http://h", "finding", "i1", []byte(`{}`), "sig", time.Now()); err != nil {
		t.Fatal(err)
	}
	paths := SpooledFor(seat)
	if len(paths) != 1 {
		t.Fatalf("want 1 held, got %d", len(paths))
	}
	info, err := os.Stat(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("spool file mode is %v, want 0600", info.Mode().Perm())
	}
	dir, err := os.Stat(spoolDir())
	if err != nil {
		t.Fatal(err)
	}
	if dir.Mode().Perm() != 0o700 {
		t.Errorf("spool dir mode is %v, want 0700", dir.Mode().Perm())
	}
}

// --dry-run must not print the signature. It is a portable, replayable
// capability over exactly those bytes: anything that reads the transcript could
// post as this seat.
func TestDryRunPrintsADigestNotTheSignature(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)
	claimed = stdinClaim{}

	var c capture
	if code := Run(c.env(t, srv.URL, ""), []string{"post", "til", "--as", seat,
		"--text", "dry", "--dry-run"}); code != ExitOK {
		t.Fatalf("dry-run failed: %s", c.out.String())
	}
	out := c.out.String()
	if strings.Contains(out, `"signature":`) {
		t.Error("--dry-run printed a signature; that is a replayable capability in a transcript")
	}
	if !strings.Contains(out, "signature_sha256") {
		t.Error("--dry-run must print a digest so two runs can be compared")
	}

	l := lines(t, &c)
	path, _ := l[0]["bytes_path"].(string)
	if path == "" {
		t.Fatal("--dry-run must write the exact bytes to a file and name it")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the named file must exist: %v", err)
	}
	if !strings.Contains(string(raw), `"kind":"til"`) {
		t.Error("the file must hold the exact bytes that would have been posted")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("dry-run file mode is %v, want 0600", info.Mode().Perm())
	}
}
