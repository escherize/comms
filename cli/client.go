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
}

// Sent is the outcome of one post, including the exact bytes that were signed
// and sent. Tests assert these are the same bytes; nothing else reads Bytes.
type Sent struct {
	Status int
	Body   wireResponse
	Bytes  []byte
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

	sig := ed25519.Sign(c.priv, payload)

	req, err := http.NewRequest("POST", c.Server+"/commands", bytes.NewReader(payload))
	if err != nil {
		return Sent{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Signature", hex.EncodeToString(sig))

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Sent{Bytes: payload}, err
	}
	defer resp.Body.Close()

	var out wireResponse
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)

	return Sent{Status: resp.StatusCode, Body: out, Bytes: payload}, nil
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
