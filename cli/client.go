package cli

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client talks to one server as one seat.
type Client struct {
	Server string
	Actor  string
	priv   ed25519.PrivateKey
	HTTP   *http.Client
}

func NewClient(server, actor string, priv ed25519.PrivateKey) *Client {
	return &Client{
		Server: server, Actor: actor, priv: priv,
		HTTP: &http.Client{Timeout: 30 * time.Second},
	}
}

// wireResponse is what the command surface returns, either shape.
type wireResponse struct {
	Seq       int64  `json:"seq"`
	Applied   bool   `json:"applied"`
	Invariant string `json:"invariant"`
	Detail    string `json:"detail"`
	Schema    string `json:"schema"`

	// Guidance the server computed and the client must not re-derive. The
	// server knows things the client cannot: how long to wait, and how many
	// times this seat has already failed this way.
	Exit         int    `json:"exit"`
	Next         string `json:"next"`
	RetryAfterMS int64  `json:"retry_after_ms"`
	Attempts     int    `json:"attempts"`
	Remaining    int    `json:"remaining"`
	Token        string `json:"token"`
	PublicURL    string `json:"public_url"`
}

// Sent is the outcome of one post, including the exact bytes that were signed
// and sent. Tests assert these are the same bytes; nothing else reads Bytes.
type Sent struct {
	Status    int
	Body      wireResponse
	Bytes     []byte
	Signature string
}

// Post builds, signs, and sends one command without the bytes leaving this
// function. Marshalling once and signing that exact slice is the whole point:
// a re-serialization between signing and sending is where a map ordering or a
// trailing newline turns into signature.invalid.
func (c *Client) Post(cmd map[string]any) (Sent, error) {
	payload, err := json.Marshal(cmd)
	if err != nil {
		return Sent{}, fmt.Errorf("building command: %w", err)
	}

	// Signed once, retried as a pair. The retry unit is (bytes, signature), so
	// a re-serialize between attempts is impossible by construction and the
	// idem key inside the payload cannot drift across attempts either.
	sig := hex.EncodeToString(ed25519.Sign(c.priv, payload))

	var lastErr error
	for attempt := 0; attempt < transportAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(retryBackoff * time.Duration(attempt))
		}
		status, out, err := postExactWith(c.HTTP, c.Server, payload, sig)
		if err == nil {
			return Sent{Status: status, Body: out, Bytes: payload, Signature: sig}, nil
		}
		lastErr = err
	}
	return Sent{Bytes: payload, Signature: sig}, lastErr
}

// transportAttempts is three tries over the identical pair. More would delay
// the spool, which is the thing that actually makes the loss recoverable.
const (
	transportAttempts = 3
	retryBackoff      = 200 * time.Millisecond
)

// postExactWith sends bytes that are already signed. Nothing here may rebuild
// the payload: these are the bytes the signature covers.
func postExactWith(hc *http.Client, server string, payload []byte, sig string) (int, wireResponse, error) {
	req, err := http.NewRequest("POST", server+"/commands", bytes.NewReader(payload))
	if err != nil {
		return 0, wireResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Signature", sig)

	resp, err := hc.Do(req)
	if err != nil {
		return 0, wireResponse{}, err
	}
	defer resp.Body.Close()

	var out wireResponse
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out, nil
}

// postExact is the drain path: one attempt, because the drain is already a
// retry.
func postExact(server string, payload []byte, sig string) (int, wireResponse, error) {
	return postExactWith(&http.Client{Timeout: 30 * time.Second}, server, payload, sig)
}

// PostTo signs and sends to a route other than /commands. The signing rule is
// the same everywhere: marshal once, sign that slice, send that slice.
func (c *Client) PostTo(path string, body map[string]any) (Sent, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return Sent{}, fmt.Errorf("building request: %w", err)
	}
	sig := hex.EncodeToString(ed25519.Sign(c.priv, payload))

	req, err := http.NewRequest("POST", c.Server+path, bytes.NewReader(payload))
	if err != nil {
		return Sent{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Signature", sig)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Sent{Bytes: payload, Signature: sig}, err
	}
	defer resp.Body.Close()

	var out wireResponse
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return Sent{Status: resp.StatusCode, Body: out, Bytes: payload, Signature: sig}, nil
}

// Preview returns the exact bytes and signature a Post would send, without
// sending. It builds the command the same way Post does, so what it shows is
// what would go on the wire.
func (c *Client) Preview(cmd map[string]any) ([]byte, string, error) {
	payload, err := json.Marshal(cmd)
	if err != nil {
		return nil, "", err
	}
	return payload, hex.EncodeToString(ed25519.Sign(c.priv, payload)), nil
}

// Enrol registers a public key against a one-time invite token. The private
// half never leaves the caller.
func (c *Client) Enrol(server, actor, token string, pub ed25519.PublicKey) (int, wireResponse, error) {
	body, _ := json.Marshal(map[string]any{
		"actor": actor, "public_key": hex.EncodeToString(pub), "token": token,
	})
	resp, err := (&http.Client{Timeout: 30 * time.Second}).
		Post(server+"/keys", "application/json", bytes.NewReader(body))
	if err != nil {
		return 0, wireResponse{}, err
	}
	defer resp.Body.Close()

	var out wireResponse
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out, nil
}

// statusToExit maps an HTTP status onto the retry contract.
func statusToExit(status int, invariant string) (int, string) {
	switch status {
	case http.StatusOK:
		return ExitOK, "accepted"
	case http.StatusUnauthorized, http.StatusForbidden:
		return ExitRefused, "refused"
	case http.StatusTooManyRequests:
		return ExitThrottled, "throttled"
	case http.StatusConflict, http.StatusUnprocessableEntity, http.StatusBadRequest:
		// A refusal we cannot describe is not a refusal an agent can correct.
		if _, known := verdicts[invariant]; !known {
			return ExitRefused, "refused"
		}
		return ExitRejected, "rejected"
	}
	return ExitRefused, "refused"
}
