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

// The human invite prompt gives the two ways a person joins — the setup link
// and one enrol command — not the agent's harness steps, and names the scope.
func TestHumanPromptOffersLinkAndEnrol(t *testing.T) {
	p := humanPrompt("human:sarah", "tok123", "http://h:7799", "comms,ops")
	for _, want := range []string{
		"http://h:7799/#setup=tok123",  // the browser join
		"comms enrol --as human:sarah", // the terminal join
		"Rooms: comms,ops",             // the scope
	} {
		if !strings.Contains(p, want) {
			t.Errorf("human prompt missing %q:\n%s", want, p)
		}
	}
	// It must NOT drag in the agent-only harness step.
	if strings.Contains(p, "hook --install") {
		t.Error("the human prompt must not include agent harness steps")
	}
}
