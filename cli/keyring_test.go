package cli

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A seat enrolled by a pre-rename binary lives under ~/.config/agent_comms.
// The whole-directory rename only fires when ~/.config/comms does not exist,
// so a machine that already has the new directory must adopt the key per-seat
// — telling the seat to re-enrol dead-ends, the invite token was single-use.
func TestLoadSeatAdoptsLegacyKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("COMMS_HOME", "")

	// The new directory already exists, which blocks the whole-dir rename.
	if err := os.MkdirAll(filepath.Join(home, ".config", "comms"), 0o700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(home, ".config", "agent_comms", "keys")
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	actor := "agent:bcm/bot"
	legacy := filepath.Join(old, safeName(actor)+".key")
	if err := os.WriteFile(legacy, []byte(hex.EncodeToString(priv)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LoadSeat(actor)
	if err != nil {
		t.Fatalf("seat stranded by config-dir rename: %v", err)
	}
	if !got.Equal(priv) {
		t.Fatal("adopted key does not match the legacy key")
	}
	if !HasSeat(actor) {
		t.Fatal("HasSeat must see the adopted seat")
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatal("the legacy file must stay for the old binary to keep using")
	}
}

// After enrolment, a seat's commands find their hub from the pin alone: a
// harness whose shell forgets exported env between commands still reaches the
// right server with a bare `comms room --as <seat>` — no COMMS_SERVER needed.
func TestBareCommandUsesPinnedServer(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)

	var c capture
	e := &Env{
		Out:       &Out{Stdout: &c.out, Stderr: &c.err},
		Stdin:     strings.NewReader(""),
		Server:    DefaultServer, // nothing chose a server
		Host:      "test-host",
		LookupEnv: func(string) (string, bool) { return "", false },
	}
	if code := Run(e, []string{"room", "--as", seat}); code != ExitOK {
		t.Fatalf("bare room via pin failed: %d %s", code, c.out.String())
	}
	if !strings.Contains(c.out.String(), `"core"`) {
		t.Fatalf("expected the pinned hub's rooms, got: %s", c.out.String())
	}
}
