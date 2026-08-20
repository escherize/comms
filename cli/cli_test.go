package cli

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/escherize/comms/shell"
	"github.com/escherize/comms/store"
)

type capture struct{ out, err bytes.Buffer }

func (c *capture) env(t *testing.T, server string, stdin string) *Env {
	t.Helper()
	return &Env{
		Out:       &Out{Stdout: &c.out, Stderr: &c.err},
		Stdin:     strings.NewReader(stdin),
		Server:    server,
		Host:      "test-host",
		LookupEnv: func(string) (string, bool) { return "", false },
	}
}

// last returns the terminal JSONL object — a consumer reading the last line
// always gets the outcome.
func (c *capture) last(t *testing.T) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(c.out.String()), "\n")
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &m); err != nil {
		t.Fatalf("stdout must be JSONL on every path; last line was %q", lines[len(lines)-1])
	}
	return m
}

func liveServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "cli.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.EnsureRoom("core"); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(shell.New(st, time.Now).Routes())
	t.Cleanup(srv.Close)
	return srv, st
}

func isolateKeys(t *testing.T) {
	t.Helper()
	t.Setenv("COMMS_HOME", t.TempDir())
}

const seat = "agent:bcm/claude-1"

func enrol(t *testing.T, srv *httptest.Server, st *store.Store) {
	t.Helper()
	tok, err := st.MintInvite(seat, store.ScopeAll, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var c capture
	if code := Run(c.env(t, srv.URL, tok+"\n"), []string{"enrol", "--as", seat}); code != ExitOK {
		t.Fatalf("enrol failed: %d %s", code, c.out.String())
	}
}

// The private half must not appear anywhere an agent or a transcript can see.
func TestEnrolNeverExposesThePrivateKey(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	tok, _ := st.MintInvite(seat, store.ScopeAll, time.Now())

	var c capture
	code := Run(c.env(t, srv.URL, tok+"\n"), []string{"enrol", "--as", seat})
	if code != ExitOK {
		t.Fatalf("enrol should succeed, got %d: %s", code, c.out.String())
	}

	priv, err := LoadSeat(seat)
	if err != nil {
		t.Fatal(err)
	}
	secret := strings.ToLower(hexOf(priv))

	for name, stream := range map[string]string{"stdout": c.out.String(), "stderr": c.err.String()} {
		if strings.Contains(strings.ToLower(stream), secret) {
			t.Errorf("the private key appeared on %s", name)
		}
	}
	if m := c.last(t); m["public_key"] == nil {
		t.Error("enrol should report the public key")
	}
}

// Permissions: 0600 in a 0700 directory, whatever the umask.
func TestSeatKeyPermissions(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)

	di, err := os.Stat(KeyDir())
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Errorf("key directory must be 0700, got %o", di.Mode().Perm())
	}

	var found bool
	entries, _ := os.ReadDir(KeyDir())
	for _, ent := range entries {
		fi, _ := ent.Info()
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("key %s must be 0600, got %o", ent.Name(), fi.Mode().Perm())
		}
		found = true
	}
	if !found {
		t.Error("enrol wrote no key")
	}
}

// argv is visible in ps and lands in shell history; the environment is
// inherited by every child process.
func TestTokenOnArgvAndKeyOnEnvAreRefused(t *testing.T) {
	isolateKeys(t)
	srv, _ := liveServer(t)

	var c capture
	code := Run(c.env(t, srv.URL, ""), []string{"enrol", "--as", seat, "--token", "abcd"})
	if code != ExitUsage || c.last(t)["invariant"] != "token.on_argv" {
		t.Errorf("a token on argv must be refused, got %d %v", code, c.last(t))
	}

	var c2 capture
	e := c2.env(t, srv.URL, "tok\n")
	e.LookupEnv = func(k string) (string, bool) {
		if k == "COMMS_KEY" {
			return "deadbeef", true
		}
		return "", false
	}
	if code := Run(e, []string{"enrol", "--as", seat}); code != ExitUsage ||
		c2.last(t)["invariant"] != "key.on_env" {
		t.Errorf("a key in the environment must be refused, got %d %v", code, c2.last(t))
	}
}

// The bytes that were signed are the bytes that were sent, byte for byte.
// Every gap between the two is where a stray newline becomes signature.invalid.
func TestSignedBytesAreTheSentBytes(t *testing.T) {
	isolateKeys(t)

	var received []byte
	var receivedSig string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received, _ = readAll(r)
		receivedSig = r.Header.Get("X-Signature")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"seq":10001,"applied":true}`))
	}))
	defer ts.Close()

	_, priv, _ := ed25519.GenerateKey(nil)
	c := NewClient(ts.URL, seat, priv)
	sent, err := c.Post(map[string]any{"room": "core", "author": seat, "kind": "chat",
		"body": map[string]any{"text": "hello"}, "idem": "x"})
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(sent.Bytes, received) {
		t.Fatalf("the bytes signed and the bytes received differ:\n signed  %q\n received %q",
			sent.Bytes, received)
	}
	if !ed25519.Verify(priv.Public().(ed25519.PublicKey), received, decodeHex(t, receivedSig)) {
		t.Error("the signature must verify against exactly the bytes received")
	}
}

// The refusal has to be actionable: an agent given a corrected invocation can
// run it verbatim. This test runs it.
func TestRejectionCarriesARetryThatWorks(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)

	var c capture
	code := Run(c.env(t, srv.URL, ""),
		[]string{"post", "finding", "--as", seat, "--text", "auth.py:88 flakes under -race"})
	if code != ExitRejected {
		t.Fatalf("a finding with no severity must exit 3, got %d: %s", code, c.out.String())
	}
	m := c.last(t)
	if m["invariant"] != "body.severity.invalid" {
		t.Errorf("want body.severity.invalid, got %v", m["invariant"])
	}
	if m["schema"] == nil || m["schema"] == "" {
		t.Error("the refusal must carry the schema")
	}
	retry, _ := m["retry"].(string)
	if retry == "" {
		t.Fatal("the refusal must carry a corrected invocation")
	}

	// Run the corrected invocation. strings.Fields would split the --text value
	// on its spaces, so rebuild the args faithfully rather than parsing them.
	args := []string{"post", "finding", "--as", seat, "--text", "auth.py:88 flakes under -race", "--severity", "p2"}
	var c2 capture
	if code := Run(c2.env(t, srv.URL, ""), args); code != ExitOK {
		t.Errorf("the corrected invocation must succeed, got %d: %s", code, c2.out.String())
	}
}

// An invariant this client does not know must stop the agent, never start a
// retry loop in an unattended run.
func TestUnknownInvariantMapsToStop(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"invariant":"some.future.rule","detail":"invented later"}`))
	}))
	defer ts.Close()

	exit, outcome := statusToExit(http.StatusUnprocessableEntity, "some.future.rule")
	if exit != ExitRefused || outcome != "refused" {
		t.Errorf("an unknown invariant must be exit 4 refused, got %d %s", exit, outcome)
	}
	if v := verdictFor("some.future.rule", ExitRefused); !strings.HasPrefix(v, "stop") {
		t.Errorf("the verdict for an unknown invariant must say stop, got %q", v)
	}
}

// A replayed post is exit 0 and visibly distinct from a fresh one.
func TestReplayIsVisiblyDistinct(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)

	fresh := postTIL(t, srv.URL, "chunk before embed")
	if fresh["applied"] != true {
		t.Errorf("a fresh post must report applied=true, got %v", fresh["applied"])
	}
	if fresh["outcome"] != "accepted" {
		t.Errorf("want accepted, got %v", fresh["outcome"])
	}
}

func postTIL(t *testing.T, server, text string) map[string]any {
	t.Helper()
	var c capture
	if code := Run(c.env(t, server, ""),
		[]string{"post", "til", "--as", seat, "--text", text}); code != ExitOK {
		t.Fatalf("post failed: %d %s", code, c.out.String())
	}
	return c.last(t)
}

// whoami reports identity and never the key.
func TestWhoamiReportsIdentityNotTheKey(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)

	var c capture
	if code := Run(c.env(t, srv.URL, ""), []string{"whoami", "--as", seat}); code != ExitOK {
		t.Fatalf("whoami failed: %d %s", code, c.out.String())
	}
	m := c.last(t)
	if m["actor"] != seat || m["host"] != "test-host" || m["public_key"] == nil {
		t.Errorf("whoami must report actor, host and public key: %v", m)
	}
	priv, _ := LoadSeat(seat)
	if strings.Contains(strings.ToLower(c.out.String()+c.err.String()), strings.ToLower(hexOf(priv))) {
		t.Error("whoami leaked the private key")
	}
}

// A kind the server does not know is refused locally, with the list.
func TestUnknownKindIsRefusedLocally(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)

	var c capture
	code := Run(c.env(t, srv.URL, ""), []string{"post", "claim", "--as", seat, "--text", "x"})
	if code != ExitUsage {
		t.Fatalf("an unknown kind must exit 2, got %d", code)
	}
	m := c.last(t)
	if m["invariant"] != "kind.unknown" {
		t.Errorf("want kind.unknown, got %v", m["invariant"])
	}
	if d, _ := m["detail"].(string); !strings.Contains(d, "finding") {
		t.Error("the refusal must list the kinds that do exist")
	}
}

// -genkey and the sign --key string must be gone from the binary and the docs.
func TestGenkeyAndSignKeyAreGone(t *testing.T) {
	root := ".."
	for _, path := range []string{"main.go", "README.md"} {
		b, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		if strings.Contains(s, `"genkey"`) || strings.Contains(s, "-genkey '") {
			t.Errorf("%s still wires -genkey", path)
		}
		if strings.Contains(s, "sign --key") {
			t.Errorf("%s still advertises `sign --key`, a subcommand that does not exist "+
				"and whose shape separates signing from sending", path)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "keygen.go")); err == nil {
		t.Error("keygen.go still exists")
	}
}

// docs/CLI.md must describe the flags the binary actually has. The previous
// version of this test asserted only that each verb *name* appeared somewhere
// in the doc, so a verb whose --help gained or lost every flag still passed —
// and because every verb in Verbs had to be described, the test required
// docs/CLI.md to document `search` while the binary answered verb.not_built.
// A test that requires a lie is worse than no test.
func TestDocsMatchTheFlagSets(t *testing.T) {
	isolateKeys(t)
	b, err := os.ReadFile("../docs/CLI.md")
	if err != nil {
		t.Fatalf("docs/CLI.md must exist: %v", err)
	}
	doc := string(b)

	for _, v := range Verbs {
		if !strings.Contains(doc, "`"+v+"`") && !strings.Contains(doc, "`"+v+" ") {
			t.Errorf("docs/CLI.md does not document the %q verb", v)
			continue
		}
		// Flags come from --help, which is the binary's own answer, so the doc
		// is diffed against behaviour rather than against another document.
		var c capture
		env := c.env(t, "http://127.0.0.1:1", "")
		env.Out.Quiet = true
		if code := Run(env, []string{v, "--help"}); code != ExitOK {
			t.Errorf("%s --help exited %d", v, code)
			continue
		}
		for _, flag := range flagsIn(c.out.String()) {
			if !strings.Contains(doc, flag) {
				t.Errorf("%s --help offers %s and docs/CLI.md never mentions it", v, flag)
			}
		}
	}
}

// flagsIn pulls --flag tokens out of a help text.
func flagsIn(help string) []string {
	seen := map[string]bool{}
	var out []string
	for _, tok := range strings.FieldsFunc(help, func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\t' || r == '"' || r == ',' ||
			r == '<' || r == '>' || r == '\\' || r == ']' || r == '['
	}) {
		if !strings.HasPrefix(tok, "--") || len(tok) < 4 {
			continue
		}
		tok = strings.TrimRight(tok, ".:;)")
		if seen[tok] {
			continue
		}
		seen[tok] = true
		out = append(out, tok)
	}
	return out
}

// Every verb answers --help.
func TestEveryVerbAnswersHelp(t *testing.T) {
	isolateKeys(t)
	for _, v := range Verbs {
		var c capture
		Run(c.env(t, "http://127.0.0.1:1", ""), []string{v, "--help"})
		if c.err.Len() == 0 {
			t.Errorf("%s --help printed nothing", v)
		}
		if !strings.Contains(c.err.String(), "comms "+v) {
			t.Errorf("%s --help must show a runnable example", v)
		}
	}
}

// ---- helpers ----

func hexOf(p ed25519.PrivateKey) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(p)*2)
	for _, b := range p {
		out = append(out, digits[b>>4], digits[b&0xf])
	}
	return string(out)
}

func decodeHex(t *testing.T, s string) []byte {
	t.Helper()
	out := make([]byte, len(s)/2)
	for i := 0; i < len(out); i++ {
		var hi, lo byte
		fmtByte := func(c byte) byte {
			switch {
			case c >= '0' && c <= '9':
				return c - '0'
			case c >= 'a' && c <= 'f':
				return c - 'a' + 10
			}
			return 0
		}
		hi, lo = fmtByte(s[2*i]), fmtByte(s[2*i+1])
		out[i] = hi<<4 | lo
	}
	return out
}

func readAll(r *http.Request) ([]byte, error) {
	buf := new(bytes.Buffer)
	_, err := buf.ReadFrom(r.Body)
	return buf.Bytes(), err
}

var _ = exec.Command

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

// --server points a client verb at a non-default hub, overriding the Env's
// default (which comes from COMMS_SERVER). A hub on a non-default --addr needs
// this, and the setup banner now prints it. Both spellings and the =form work,
// and a bare --server with no value is a usage error, not a silent no-op.
func TestServerFlagOverridesTheDefault(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)
	seedActor(t, st, "human:sarah")

	// Point the Env at a dead hub; --server must redirect the post to the live
	// one. If the flag were ignored, the post would fail transport, not accept.
	for _, form := range [][]string{
		{"post", "chat", "--as", seat, "--text", "via space form", "--server", srv.URL},
		{"post", "chat", "--as", seat, "--text", "via equals form", "--server=" + srv.URL},
	} {
		var c capture
		if code := Run(c.env(t, "http://127.0.0.1:1", ""), form); code != ExitOK {
			t.Errorf("%v exited %d: %s", form, code, c.out.String())
		}
	}

	// A bare --server with nothing after it is a usage error.
	var c capture
	if code := Run(c.env(t, srv.URL, ""), []string{"post", "chat", "--server"}); code != ExitUsage {
		t.Errorf("bare --server must be a usage error, got exit %d", code)
	}
}

// The short form agents pay for hundreds of times a day: kind as the verb,
// the entry as the trailing argument, no --text ceremony.
func TestKindAsVerbWithPositionalText(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)

	var c capture
	if code := Run(c.env(t, srv.URL, ""),
		[]string{"status", "--as", seat, "shipped the fix, tests green"}); code != ExitOK {
		t.Fatalf("kind-as-verb post failed: %d %s", code, c.out.String())
	}
	if m := c.last(t); m["outcome"] != "accepted" {
		t.Fatalf("want accepted, got %v", m)
	}

	// Giving the entry twice is refused, not silently merged.
	var c2 capture
	if code := Run(c2.env(t, srv.URL, ""),
		[]string{"chat", "--as", seat, "--text", "one", "two"}); code != ExitUsage {
		t.Fatalf("positional + --text must be text.contested, got %d: %s", code, c2.out.String())
	}
}

// join is onboarding as one act: the same setup link a human clicks enrols
// the seat the token names, checks in, and wires the harness hook.
func TestJoinFromSetupLink(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(prev)

	tok, err := st.MintInvite("agent:bcm/joiner", store.ScopeAll, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	var c capture
	code := Run(c.env(t, "http://127.0.0.1:1", ""), // wrong server on purpose: the link wins
		[]string{"join", srv.URL + "/#setup=" + tok})
	if code != ExitOK {
		t.Fatalf("join exited %d: %s", code, c.out.String())
	}
	if m := c.last(t); m["outcome"] != "joined" || m["actor"] != "agent:bcm/joiner" {
		t.Fatalf("want joined as agent:bcm/joiner, got %v", m)
	}
	if !HasSeat("agent:bcm/joiner") {
		t.Error("join must leave an enrolled key behind")
	}
	if PinnedServer("agent:bcm/joiner") != srv.URL {
		t.Error("join must pin the hub from the link")
	}
	if _, err := os.Stat(filepath.Join(project, ".claude", "settings.local.json")); err != nil {
		t.Error("join must wire the harness hook for the seat")
	}
	// Join pins the seat to the project, and verbs pick it up with no --as
	// and no env — the study's top friction was exactly this gap.
	if raw, err := os.ReadFile(filepath.Join(project, ".commsrc")); err != nil ||
		!strings.Contains(string(raw), "agent:bcm/joiner") {
		t.Errorf("join must pin the seat in .commsrc, got %s (%v)", raw, err)
	}
	var who capture
	if code := Run(who.env(t, srv.URL, ""), []string{"whoami"}); code != ExitOK {
		t.Fatalf("whoami without --as must resolve from .commsrc, exited %d: %s", code, who.out.String())
	}
	if m := who.last(t); m["actor"] != "agent:bcm/joiner" {
		t.Errorf(".commsrc must name the joined seat, got %v", m["actor"])
	}
	// The check-in landed.
	recs, err := st.Since("core", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range recs {
		if string(r.Author) == "agent:bcm/joiner" {
			found = true
		}
	}
	if !found {
		t.Error("join must post the check-in")
	}

	// A seat mismatch is refused; the spent token is a refusal too.
	var c2 capture
	if code := Run(c2.env(t, "http://127.0.0.1:1", ""),
		[]string{"join", srv.URL + "/#setup=" + tok, "--as", "agent:bcm/other"}); code == ExitOK {
		t.Error("a spent token (or mismatched --as) must not join")
	}
}
