package cli

import "testing"

// One check per window: the whole point is that a verb costs at most one HEAD
// every few minutes, not one per invocation.
func TestSelfUpdateDebounces(t *testing.T) {
	t.Setenv("COMMS_HOME", t.TempDir())
	if !selfUpdateDue() {
		t.Fatal("the first check of a fresh home must be due")
	}
	if selfUpdateDue() {
		t.Error("a second check inside the window must not be due")
	}
}

// Executable bytes only travel a channel an on-path attacker cannot write:
// loopback, or TLS. Plain http across a network must never self-update.
func TestSelfUpdateRefusesUntrustedChannels(t *testing.T) {
	for server, want := range map[string]bool{
		"http://127.0.0.1:7777":   true,
		"http://localhost:7777":   true,
		"http://[::1]:7777":       true,
		"https://hub.example.com": true,
		"http://10.0.0.5:7777":    false,
		"http://hub.example.com":  false,
		"ftp://127.0.0.1":         false,
	} {
		if got := updateChannelTrusted(server); got != want {
			t.Errorf("updateChannelTrusted(%q) = %v, want %v", server, got, want)
		}
	}
}
