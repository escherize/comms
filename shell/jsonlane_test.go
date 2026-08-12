package shell

import (
	"bufio"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// frames reads SSE frames from the JSON lane within a window.
func frames(t *testing.T, url, lastID string, within time.Duration) []map[string]any {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Accept", "application/json")
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	out := make(chan []map[string]any, 1)
	go func() {
		var got []map[string]any
		sc := bufio.NewScanner(resp.Body)
		var event string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				var m map[string]any
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &m) == nil {
					m["__event"] = event
					got = append(got, m)
				}
			}
		}
		out <- got
	}()

	time.Sleep(within)
	resp.Body.Close()
	select {
	case g := <-out:
		return g
	case <-time.After(2 * time.Second):
		return nil
	}
}

func countOf(fs []map[string]any, event string) int {
	n := 0
	for _, f := range fs {
		if f["__event"] == event {
			n++
		}
	}
	return n
}

// Exactly one caught-up frame per connection, between backlog and live. It is
// the only way a process that must exit can tell "I have read the history"
// from "the room is quiet".
func TestCaughtUpSentinelIsEmittedExactlyOnce(t *testing.T) {
	srv, _ := newServer(t)
	post(t, srv, cmd("chat", "one", "c1"))
	post(t, srv, cmd("chat", "two", "c2"))

	fs := frames(t, srv.URL+"/stream?room=core", "", 500*time.Millisecond)
	if n := countOf(fs, "caught-up"); n != 1 {
		t.Errorf("want exactly one caught-up frame, got %d", n)
	}
	if countOf(fs, "event") != 2 {
		t.Errorf("want the two backlog events, got %d", countOf(fs, "event"))
	}
	// Order: the sentinel comes after the backlog.
	var sawEvent bool
	for _, f := range fs {
		if f["__event"] == "event" {
			sawEvent = true
		}
		if f["__event"] == "caught-up" && !sawEvent {
			t.Error("caught-up must come after the backlog, not before")
		}
	}
}

// An empty room still gets the sentinel — otherwise "quiet" and "not yet read"
// are the same silence.
func TestCaughtUpOnAnEmptyRoom(t *testing.T) {
	srv, _ := newServer(t)
	fs := frames(t, srv.URL+"/stream?room=core", "", 400*time.Millisecond)
	if countOf(fs, "caught-up") != 1 {
		t.Errorf("an empty room must still emit caught-up, got %d frames", len(fs))
	}
}

// A restart is a fact the client is told, never one it infers from a seq delta.
func TestHelloFrameCarriesBootID(t *testing.T) {
	srv, _ := newServer(t)
	fs := frames(t, srv.URL+"/stream?room=core", "", 400*time.Millisecond)
	if len(fs) == 0 || fs[0]["__event"] != "hello" {
		t.Fatalf("the first frame must be hello, got %v", fs)
	}
	if fs[0]["boot"] == "" || fs[0]["boot"] == nil {
		t.Error("hello must carry a boot id")
	}
}

// Resume loses nothing across a reconnect, on this lane.
func TestJSONLaneResumesWithoutGap(t *testing.T) {
	srv, _ := newServer(t)
	_, a := post(t, srv, cmd("chat", "alpha", "r1"))
	post(t, srv, cmd("chat", "bravo", "r2"))
	post(t, srv, cmd("chat", "charlie", "r3"))

	fs := frames(t, srv.URL+"/stream?room=core", itoa(int64(a["seq"].(float64))), 500*time.Millisecond)
	var texts []string
	for _, f := range fs {
		if f["__event"] != "event" {
			continue
		}
		if body, ok := f["body"].(map[string]any); ok {
			texts = append(texts, body["text"].(string))
		}
	}
	joined := strings.Join(texts, ",")
	if strings.Contains(joined, "alpha") {
		t.Error("resume must not replay what the client already had")
	}
	if !strings.Contains(joined, "bravo") || !strings.Contains(joined, "charlie") {
		t.Errorf("resume must deliver everything after the cursor, got %q", joined)
	}
}

// recipient= is the only filter inbox needs.
func TestRecipientAndKindFilterServerSide(t *testing.T) {
	srv, st := newServer(t)
	seedActor(t, st, "human:bcm")
	seedActor(t, st, "human:sarah")
	post(t, srv, cmd("chat", "ambient noise", "f1"))
	post(t, srv, `{"room":"core","author":"agent:c2","kind":"question",`+
		`"body":{"text":"for bcm"},"recipient":"human:bcm","idem":"f2"}`)
	post(t, srv, `{"room":"core","author":"agent:c2","kind":"question",`+
		`"body":{"text":"for sarah"},"recipient":"human:sarah","idem":"f3"}`)

	fs := frames(t, srv.URL+"/stream?room=core&recipient=human:bcm", "", 500*time.Millisecond)
	if countOf(fs, "event") != 1 {
		t.Errorf("recipient= must filter server-side, got %d events", countOf(fs, "event"))
	}

	fs = frames(t, srv.URL+"/stream?room=core&kind=question", "", 500*time.Millisecond)
	if countOf(fs, "event") != 2 {
		t.Errorf("kind= must filter server-side, got %d events", countOf(fs, "event"))
	}
}

// A TIL written by a since-compromised key must not read like any other.
func TestEventsCarryAuthorKeyStatus(t *testing.T) {
	srv, st := newServer(t)
	pub, _, _ := ed25519.GenerateKey(nil)
	if err := st.RegisterKey("agent:c1", pub, time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	post(t, srv, `{"room":"core","author":"agent:c1","kind":"til",`+
		`"body":{"text":"chunk before embed"},"idem":"k1"}`)

	fs := frames(t, srv.URL+"/stream?room=core", "", 400*time.Millisecond)
	var found bool
	for _, f := range fs {
		if f["__event"] == "event" {
			if f["author_key_status"] != "active" {
				t.Errorf("want active, got %v", f["author_key_status"])
			}
			found = true
		}
	}
	if !found {
		t.Fatal("no event frame")
	}

	// Now mark it compromised; the same event must read differently.
	if err := st.MarkCompromised("agent:c1", time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	fs = frames(t, srv.URL+"/stream?room=core", "", 400*time.Millisecond)
	for _, f := range fs {
		if f["__event"] == "event" {
			if f["author_key_status"] != "compromised" || f["flagged"] != true {
				t.Errorf("a compromised key's event must say so, got %v / %v",
					f["author_key_status"], f["flagged"])
			}
		}
	}
}

// A redacted event crosses this lane suppressed, same as the render path.
func TestRedactedEventsCrossTheLaneSuppressed(t *testing.T) {
	srv, _ := newServer(t)
	_, out := post(t, srv, cmd("chat", "SECRET-TOKEN-abc123", "s1"))
	target := itoa(int64(out["seq"].(float64)))
	post(t, srv, `{"room":"core","author":"human:bcm","kind":"redact",`+
		`"body":{"text":"pasted a token"},"refs":["`+target+`"],"idem":"s2"}`)

	fs := frames(t, srv.URL+"/stream?room=core", "", 500*time.Millisecond)
	for _, f := range fs {
		raw, _ := json.Marshal(f)
		if strings.Contains(string(raw), "SECRET-TOKEN-abc123") {
			t.Fatal("a redacted body must not cross the read lane")
		}
	}
}

// The four conditions signature.invalid used to hide are four invariants.
func TestAuthFailuresAreFourDistinctInvariants(t *testing.T) {
	srv, st := newSigningServer(t)
	body := cmd("chat", "x", "a1")

	send := func(actor string, priv ed25519.PrivateKey, payload string) map[string]any {
		req, _ := http.NewRequest("POST", srv.URL+"/commands", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Signature", hex.EncodeToString(ed25519.Sign(priv, []byte(payload))))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&m)
		return m
	}

	_, unknownPriv, _ := ed25519.GenerateKey(nil)
	if got := send("human:bcm", unknownPriv, body); got["invariant"] != "key.unknown" {
		t.Errorf("unenrolled seat: want key.unknown, got %v", got["invariant"])
	}

	pub, priv, _ := ed25519.GenerateKey(nil)
	st.RegisterKey("human:bcm", pub, time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC))

	// A signature over different bytes is a client bug, not a key problem.
	forged := `{"room":"core","author":"human:bcm","kind":"chat","body":{"text":"other"},"idem":"a2"}`
	req, _ := http.NewRequest("POST", srv.URL+"/commands", strings.NewReader(forged))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Signature", hex.EncodeToString(ed25519.Sign(priv, []byte(body))))
	resp, _ := http.DefaultClient.Do(req)
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	resp.Body.Close()
	if m["invariant"] != "signature.invalid" {
		t.Errorf("mismatched bytes: want signature.invalid, got %v", m["invariant"])
	}

	st.RevokeKey("human:bcm", time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC))
	if got := send("human:bcm", priv, body); got["invariant"] != "key.revoked" {
		t.Errorf("revoked: want key.revoked, got %v", got["invariant"])
	}

	st.MarkCompromised("human:bcm", time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC))
	if got := send("human:bcm", priv, body); got["invariant"] != "key.compromised" {
		t.Errorf("compromised: want key.compromised, got %v", got["invariant"])
	}
}

func TestRoomsListsJSON(t *testing.T) {
	srv, st := newServer(t)
	st.EnsureRoom("bash")

	req, _ := http.NewRequest("GET", srv.URL+"/rooms", nil)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	rooms, _ := m["rooms"].([]any)
	if len(rooms) != 2 {
		t.Errorf("want core and bash, got %v", m["rooms"])
	}
}

// The datastar lane must be untouched by the JSON lane on the same route.
func TestDatastarLaneIsUnchanged(t *testing.T) {
	srv, _ := newServer(t)
	post(t, srv, cmd("chat", "for the browser", "d1"))

	req, _ := http.NewRequest("GET", srv.URL+"/stream?room=core", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got := readAvailable(t, resp, 400*time.Millisecond)

	if !strings.Contains(got, "event: datastar-patch-elements") {
		t.Error("the browser lane must still speak the datastar patch protocol")
	}
	if strings.Contains(got, `"type":"event"`) {
		t.Error("the browser lane must not have become JSON")
	}
}

// The wire noun is event; record must appear in no key or frame name.
func TestWireNounIsEvent(t *testing.T) {
	srv, _ := newServer(t)
	post(t, srv, cmd("chat", "x", "w1"))
	fs := frames(t, srv.URL+"/stream?room=core", "", 400*time.Millisecond)
	for _, f := range fs {
		raw, _ := json.Marshal(f)
		if strings.Contains(strings.ToLower(string(raw)), `"record"`) {
			t.Errorf("docs/CONTEXT.md forbids record as the wire noun for an event: %s", raw)
		}
	}
}
