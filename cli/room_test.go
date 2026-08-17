package cli

import (
	"strings"
	"testing"
)

// The onboarding prompt names the rooms the seat is scoped to, so whoever pastes
// it can see the blast radius; an unscoped invite says "all rooms".
func TestOnboardingPromptEchoesScope(t *testing.T) {
	scoped := onboardingPrompt("human:sarah", "tok123", "http://h:7777", "comms,ops")
	if !strings.Contains(scoped, "Rooms: comms,ops") {
		t.Errorf("a scoped prompt must name its rooms, got:\n%s", scoped)
	}
	for _, all := range []string{"", "all"} {
		p := onboardingPrompt("human:owner", "tok", "http://h:7777", all)
		if !strings.Contains(p, "Rooms: all rooms") {
			t.Errorf("scope %q must render as all rooms, got:\n%s", all, p)
		}
	}
}
