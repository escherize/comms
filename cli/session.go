package cli

// Read sessions (ADR-0014). A hub running -read-auth answers reads with 401
// session.required until the seat proves itself by signing a challenge. The
// client does that here, invisibly: on the first refusal it signs, caches the
// token, and retries — so "the hub requires read auth now" is a change nobody
// has to relearn a workflow for.

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// SessionHeader must match the shell's. One constant name, two packages, and a
// test in each direction pinning them together.
const SessionHeader = "X-Session"

func sessionDir() string {
	return filepath.Join(filepath.Dir(KeyDir()), "sessions")
}

// sessionFile is per seat and per server: a token is minted by one hub and
// means nothing to another.
func sessionFile(server, actor string) string {
	clean := func(s string) string {
		return strings.NewReplacer("/", "_", ":", "_", ".", "_", string(filepath.Separator), "_").Replace(s)
	}
	host := server
	if u, err := url.Parse(server); err == nil && u.Host != "" {
		host = u.Host
	}
	return filepath.Join(sessionDir(), clean(host)+"__"+clean(actor)+".session")
}

func loadSession(server, actor string) string {
	if actor == "" {
		return ""
	}
	raw, err := os.ReadFile(sessionFile(server, actor))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func saveSession(server, actor, token string) {
	if err := os.MkdirAll(sessionDir(), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(sessionFile(server, actor), []byte(token+"\n"), 0o600)
}

// establishSession signs a fresh challenge with the seat key and trades it for
// a token. The bytes are marshalled once and signed as-is — the same rule
// every signed request in this client follows.
func establishSession(e *Env, actor string) (string, error) {
	priv, err := LoadSeat(actor)
	if err != nil {
		return "", err
	}

	resp, err := http.Get(e.Server + "/session/challenge")
	if err != nil {
		return "", err
	}
	var ch struct {
		Challenge string `json:"challenge"`
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err := json.Unmarshal(raw, &ch); err != nil || ch.Challenge == "" {
		return "", fmt.Errorf("no challenge from %s: %s", e.Server, strings.TrimSpace(string(raw)))
	}

	payload, err := json.Marshal(map[string]any{"actor": actor, "challenge": ch.Challenge})
	if err != nil {
		return "", err
	}
	sig := hex.EncodeToString(ed25519.Sign(priv, payload))

	req, err := http.NewRequest("POST", e.Server+"/session", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Signature", sig)
	sresp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer sresp.Body.Close()

	var out struct {
		Token     string `json:"token"`
		Invariant string `json:"invariant"`
		Detail    string `json:"detail"`
	}
	sraw, _ := io.ReadAll(sresp.Body)
	_ = json.Unmarshal(sraw, &out)
	if sresp.StatusCode != http.StatusOK || out.Token == "" {
		return "", fmt.Errorf("session refused (%s): %s", out.Invariant, out.Detail)
	}
	saveSession(e.Server, actor, out.Token)
	return out.Token, nil
}

// doRead sends a request built by build, carrying the seat's read session if
// one is cached. On a 401 it establishes a session and retries once; a hub
// without read auth never returns 401 to a read, so the common path costs one
// file stat.
//
// build is called per attempt: a request's Body is consumed by sending it, so
// a retry needs a fresh one.
func doRead(e *Env, hc *http.Client, build func() (*http.Request, error)) (*http.Response, error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	req, err := build()
	if err != nil {
		return nil, err
	}
	// Attach a session so the read is attributed to this seat and filtered by
	// its room membership — even against a loopback hub, which would otherwise
	// serve the full operator view to any local read regardless of --as. If none
	// is cached and this seat holds a key, establish one up front rather than
	// waiting for a 401 that loopback never sends.
	tok := loadSession(e.Server, e.Seat)
	if tok == "" && e.Seat != "" && HasSeat(e.Seat) {
		if fresh, err := establishSession(e, e.Seat); err == nil {
			tok = fresh
		}
	}
	if tok != "" {
		req.Header.Set(SessionHeader, tok)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}
	resp.Body.Close()

	// A 401 that cannot be answered must be an error, not a response: handed
	// back, its JSON body parses as an empty room and reads as "nothing here",
	// which is a locked door reported as an empty one.
	if e.Seat == "" {
		return nil, fmt.Errorf(
			"this hub requires a read session; name the seat with --as or COMMS_ACTOR")
	}
	if !HasSeat(e.Seat) {
		return nil, fmt.Errorf(
			"this hub requires a read session and %s holds no key here; run: comms enrol --as %s",
			e.Seat, e.Seat)
	}

	tok, err = establishSession(e, e.Seat)
	if err != nil {
		return nil, fmt.Errorf("this hub requires a read session and establishing one as %s failed: %w",
			e.Seat, err)
	}
	req, err = build()
	if err != nil {
		return nil, err
	}
	req.Header.Set(SessionHeader, tok)
	return hc.Do(req)
}
