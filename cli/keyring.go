// Package cli is the agent client: the verbs an agent uses to join a room,
// post, and read. It holds one seat key, signs, and sends inside one process.
//
// The one-process rule is a correctness constraint, not ergonomics. A signature
// covers the exact posted bytes, so any boundary between computing a signature
// and emitting bytes is a place where a stray newline becomes
// signature.invalid — an error that names the crypto and not the cause.
package cli

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// KeyDir is where seat keys live. Deliberately outside any repository: an
// agent is never told to look here, and no verb prints what is in it.
func KeyDir() string {
	if d := os.Getenv("COMMS_HOME"); d != "" {
		return filepath.Join(d, "keys")
	}
	if d := os.Getenv("AGENT_COMMS_HOME"); d != "" { // the pre-rename spelling
		return filepath.Join(d, "keys")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "agent_comms", "keys")
}

// seatFile maps an actor to its key path. Slashes in a seat name would
// otherwise create directories.
func seatFile(actor string) string {
	safe := strings.NewReplacer("/", "_", ":", "_", string(filepath.Separator), "_").Replace(actor)
	return filepath.Join(KeyDir(), safe+".key")
}

// SaveSeat writes a private key readable only by its owner.
func SaveSeat(actor string, priv ed25519.PrivateKey) error {
	if err := os.MkdirAll(KeyDir(), 0o700); err != nil {
		return err
	}
	// Re-assert the mode: MkdirAll respects umask, so a permissive umask would
	// otherwise leave the directory group- or world-readable.
	if err := os.Chmod(KeyDir(), 0o700); err != nil {
		return err
	}
	path := seatFile(actor)
	if err := os.WriteFile(path, []byte(hex.EncodeToString(priv)+"\n"), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// LoadSeat reads the seat key. It is the only reader; no verb, flag, or
// environment variable exposes what it returns.
func LoadSeat(actor string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(seatFile(actor))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no key for %s; run: comms enrol --as %s", actor, actor)
		}
		return nil, err
	}
	b, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(b) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("key for %s is corrupt; re-enrol", actor)
	}
	return ed25519.PrivateKey(b), nil
}

// HasSeat reports whether a seat is enrolled, without loading the key.
func HasSeat(actor string) bool {
	_, err := os.Stat(seatFile(actor))
	return err == nil
}
