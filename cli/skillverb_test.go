package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testAgentSkill = "---\nname: agent-comms\ndescription: post to the room\n---\n# the room's contract\n"
const testHubSkill = "---\nname: agent-comms-hub\ndescription: run a hub\n---\n# running a hub\n"

func withSkills(t *testing.T, docs ...string) {
	t.Helper()
	prev := Skills
	Skills = nil
	for _, d := range docs {
		Skills = append(Skills, SkillDoc{Doc: d})
	}
	t.Cleanup(func() { Skills = prev })
}

func TestSkillVerbPrintsThePrimaryByDefault(t *testing.T) {
	withSkills(t, testAgentSkill, testHubSkill)

	var c capture
	if code := Run(c.env(t, "http://unused", ""), []string{"skill"}); code != ExitOK {
		t.Fatalf("skill should print and exit 0, got %d: %s", code, c.err.String())
	}
	if c.out.String() != testAgentSkill {
		t.Errorf("the bare form must print the primary skill byte for byte, got %q", c.out.String())
	}
}

func TestSkillVerbPrintsANamedSkill(t *testing.T) {
	withSkills(t, testAgentSkill, testHubSkill)

	var c capture
	if code := Run(c.env(t, "http://unused", ""), []string{"skill", "agent-comms-hub"}); code != ExitOK {
		t.Fatalf("named skill should print, got %d", code)
	}
	if c.out.String() != testHubSkill {
		t.Errorf("naming a skill must print that one, got %q", c.out.String())
	}

	var miss capture
	if code := Run(miss.env(t, "http://unused", ""), []string{"skill", "nope"}); code != ExitUsage {
		t.Errorf("an unknown name must be a usage error naming the known set, got %d", code)
	}
	if !strings.Contains(miss.out.String(), "agent-comms-hub") {
		t.Errorf("the refusal should list what exists, got %s", miss.out.String())
	}
}

func TestSkillVerbInstallsEverySkillWhereHarnessesLook(t *testing.T) {
	withSkills(t, testAgentSkill, testHubSkill)
	home := t.TempDir()
	t.Setenv("HOME", home)

	var c capture
	if code := Run(c.env(t, "http://unused", ""), []string{"skill", "--install"}); code != ExitOK {
		t.Fatalf("install should succeed, got %d: %s", code, c.out.String())
	}
	for name, doc := range map[string]string{
		"agent-comms": testAgentSkill, "agent-comms-hub": testHubSkill,
	} {
		path := filepath.Join(home, ".agents", "skills", name, "SKILL.md")
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s should exist: %v", path, err)
		}
		if string(got) != doc {
			t.Errorf("%s must hold its embedded document", name)
		}
	}
}

func TestSkillsVerbListsWhatTheBinaryCarries(t *testing.T) {
	withSkills(t, testAgentSkill, testHubSkill)

	var c capture
	if code := Run(c.env(t, "http://unused", ""), []string{"skills"}); code != ExitOK {
		t.Fatalf("skills should list, got %d", code)
	}
	out := c.out.String()
	for _, want := range []string{`"name":"agent-comms"`, `"name":"agent-comms-hub"`, "post to the room"} {
		if !strings.Contains(out, want) {
			t.Errorf("the listing should carry %q, got %s", want, out)
		}
	}
}

// A build that somehow lacks the embeds must say so rather than install an
// empty contract an agent would then follow.
func TestSkillVerbRefusesAnEmptyEmbed(t *testing.T) {
	withSkills(t)
	var c capture
	if code := Run(c.env(t, "http://unused", ""), []string{"skill"}); code == ExitOK {
		t.Error("an empty embed must not print as if it were the skill")
	}
}
