package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withSkill(t *testing.T, doc string) {
	t.Helper()
	prev := SkillMarkdown
	SkillMarkdown = doc
	t.Cleanup(func() { SkillMarkdown = prev })
}

func TestSkillVerbPrintsTheEmbeddedDoc(t *testing.T) {
	withSkill(t, "# the room's contract\npost what someone will search for later\n")

	var c capture
	if code := Run(c.env(t, "http://unused", ""), []string{"skill"}); code != ExitOK {
		t.Fatalf("skill should print and exit 0, got %d: %s", code, c.err.String())
	}
	if c.out.String() != SkillMarkdown {
		t.Errorf("the print form must be the document byte for byte, got %q", c.out.String())
	}
}

func TestSkillVerbInstallsWhereClaudeCodeLooks(t *testing.T) {
	withSkill(t, "---\nname: agent-comms\n---\n# contract\n")
	home := t.TempDir()
	t.Setenv("HOME", home)

	var c capture
	if code := Run(c.env(t, "http://unused", ""), []string{"skill", "--install"}); code != ExitOK {
		t.Fatalf("install should succeed, got %d: %s", code, c.out.String())
	}
	path := filepath.Join(home, ".agents", "skills", "agent-comms", "SKILL.md")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the skill should exist at %s: %v", path, err)
	}
	if string(got) != SkillMarkdown {
		t.Error("what lands on disk must be the embedded document")
	}
	if !strings.Contains(c.last(t)["detail"].(string), path) {
		t.Error("the result should say where the skill went")
	}
}

// A build that somehow lacks the embed must say so rather than install an
// empty contract an agent would then follow.
func TestSkillVerbRefusesAnEmptyEmbed(t *testing.T) {
	withSkill(t, "")
	var c capture
	if code := Run(c.env(t, "http://unused", ""), []string{"skill"}); code == ExitOK {
		t.Error("an empty embed must not print as if it were the skill")
	}
}
