package shell

import (
	"crypto/ed25519"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/escherize/agent-comms/store"
)

// signedServer enforces signatures, the way a real deployment does.
func signedServer(t *testing.T) (*httptest.Server, *store.Store, ed25519.PrivateKey) {
	t.Helper()
	srv, st := newSigningServer(t)
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RegisterKey("human:bcm", pub, time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	return srv, st, priv
}

func postSigned(t *testing.T, srv *httptest.Server, body string, priv ed25519.PrivateKey) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", srv.URL+"/commands", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if priv != nil {
		req.Header.Set("X-Signature", hex.EncodeToString(ed25519.Sign(priv, []byte(body))))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestSignedCommandIsAccepted(t *testing.T) {
	srv, _, priv := signedServer(t)
	resp := postSigned(t, srv, cmd("chat", "signed hello", "s1"), priv)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a correctly signed command must be accepted, got %d", resp.StatusCode)
	}
}

func TestUnsignedCommandIsRejected(t *testing.T) {
	srv, _, _ := signedServer(t)

	resp := postSigned(t, srv, cmd("chat", "no signature", "u1"), nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("an unsigned command must be rejected with 401, got %d", resp.StatusCode)
	}
}

// The signature covers the exact bytes, so an attacker who captures a valid
// signature cannot reuse it on a different payload.
func TestSignatureDoesNotTransferToAnotherPayload(t *testing.T) {
	srv, _, priv := signedServer(t)

	original := cmd("chat", "benign", "t1")
	sig := hex.EncodeToString(ed25519.Sign(priv, []byte(original)))

	forged := `{"room":"core","author":"human:bcm","kind":"redact","body":{"text":"x"},"refs":["10000"],"idem":"t2"}`
	req, _ := http.NewRequest("POST", srv.URL+"/commands", strings.NewReader(forged))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Signature", sig)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a signature must not transfer to a different payload, got %d", resp.StatusCode)
	}
}

// Posting as someone else is the attack the as-picker made trivial. With
// signatures on, it fails: the signature is checked against the claimed
// author's key.
func TestCannotPostAsAnotherActor(t *testing.T) {
	srv, st, priv := signedServer(t)
	otherPub, _, _ := ed25519.GenerateKey(nil)
	if err := st.RegisterKey("human:sarah", otherPub, time.Now()); err != nil {
		t.Fatal(err)
	}

	impersonation := `{"room":"core","author":"human:sarah","kind":"chat","body":{"text":"not me"},"idem":"i1"}`
	req, _ := http.NewRequest("POST", srv.URL+"/commands", strings.NewReader(impersonation))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Signature", hex.EncodeToString(ed25519.Sign(priv, []byte(impersonation))))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("signing as yourself must not let you author as someone else, got %d", resp.StatusCode)
	}
}

func TestMalformedSignatureHeaderIsRejected(t *testing.T) {
	srv, _, _ := signedServer(t)
	body := cmd("chat", "x", "m1")

	for _, bad := range []string{"not-hex", "", "abcd"} {
		req, _ := http.NewRequest("POST", srv.URL+"/commands", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if bad != "" {
			req.Header.Set("X-Signature", bad)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("malformed signature %q must be rejected, got %d", bad, resp.StatusCode)
		}
	}
}

// Revocation takes effect on the next command, and the actor's history stays.
func TestRevokedKeyStopsPosting(t *testing.T) {
	srv, st, priv := signedServer(t)

	resp := postSigned(t, srv, cmd("chat", "before revoke", "r1"), priv)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setup post should succeed, got %d", resp.StatusCode)
	}

	// Before the signing server's fixed clock (12:30), so the revocation is in
	// effect by the time the next command is verified.
	if err := st.RevokeKey("human:bcm", time.Date(2026, 8, 6, 12, 15, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}

	resp = postSigned(t, srv, cmd("chat", "after revoke", "r2"), priv)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a revoked key must stop posting, got %d", resp.StatusCode)
	}

	recs, _ := st.Since("core", 0, 100)
	var found bool
	for _, rec := range recs {
		if rec.Text() == "before revoke" {
			found = true
		}
	}
	if !found {
		t.Error("revocation must not erase what the actor already said")
	}
}

// Enrolment needs a token. Without one, /keys would be trust-on-first-use and
// whoever claimed an actor name first would own it.
func TestEnrolmentRequiresAValidInvite(t *testing.T) {
	srv, st := newSigningServer(t)
	pub, _, _ := ed25519.GenerateKey(nil)
	body := func(tok string) string {
		return `{"actor":"agent:newbie","public_key":"` + hex.EncodeToString(pub) + `","token":"` + tok + `"}`
	}

	// No token, and a made-up token, are both refused.
	for _, tok := range []string{"", "deadbeef"} {
		resp, err := http.Post(srv.URL+"/keys", "application/json", strings.NewReader(body(tok)))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("token %q must be refused, got %d", tok, resp.StatusCode)
		}
	}

	// A real token works exactly once.
	tok, err := st.MintInvite("agent:newbie", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(srv.URL+"/keys", "application/json", strings.NewReader(body(tok)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a valid token must enrol, got %d", resp.StatusCode)
	}

	resp, err = http.Post(srv.URL+"/keys", "application/json", strings.NewReader(body(tok)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a used token must not enrol again, got %d", resp.StatusCode)
	}
}

// A token minted for one actor cannot enrol another.
func TestInviteIsBoundToItsActor(t *testing.T) {
	srv, st := newSigningServer(t)
	pub, _, _ := ed25519.GenerateKey(nil)
	tok, _ := st.MintInvite("human:sarah", time.Now())

	body := `{"actor":"mallory","public_key":"` + hex.EncodeToString(pub) + `","token":"` + tok + `"}`
	resp, err := http.Post(srv.URL+"/keys", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a token issued for sarah must not enrol mallory, got %d", resp.StatusCode)
	}
}

// The client must not demand a key the server is not going to check. With
// -insecure the composer posts unsigned; with signing on it enrols. Getting
// this wrong froze a browser on a modal enrolment prompt.
func TestPageAdvertisesWhetherSigningIsRequired(t *testing.T) {
	insecure, _ := newServer(t) // RequireSignature = false
	if page := getPage(t, insecure.URL+"/?room=core"); !strings.Contains(page, `data-signing="false"`) {
		t.Error("an insecure server must tell the page not to enrol or sign")
	}

	signing, _ := newSigningServer(t)
	if page := getPage(t, signing.URL+"/?room=core"); !strings.Contains(page, `data-signing="true"`) {
		t.Error("a signing server must tell the page to enrol and sign")
	}
}

// No modal dialogs: prompt/alert/confirm block the whole renderer.
func TestNoBlockingDialogsInPageScripts(t *testing.T) {
	srv, _ := newServer(t)
	page := getPage(t, srv.URL+"/?room=core")
	for _, banned := range []string{"prompt(", "alert(", "confirm("} {
		if strings.Contains(page, banned) {
			t.Errorf("page scripts must not call %s — it blocks every subsequent event", banned)
		}
	}
}
