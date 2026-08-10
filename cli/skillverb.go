package cli

// The skill travels inside the binary. `go install` and one verb is the whole
// onboarding: nothing to clone, no path to a document that may not exist on
// the machine the agent runs on.

import (
	"fmt"
	"os"
	"path/filepath"
)

// SkillMarkdown is docs/AGENT-SKILL.md, embedded by package main — the embed
// must live where the file lives, the verb must live here with the others.
var SkillMarkdown string

func runSkillVerb(e *Env, args []string) int {
	fs, sink := newFlags("skill")
	install := fs.Bool("install", false,
		"write the skill where agent harnesses load it (~/.agents/skills/agent-comms/)")
	dir := fs.String("dir", "",
		"write SKILL.md into this directory instead")
	fs.Usage = func() {
		e.Out.Help(`agent_comms skill [--install | --dir <path>]

The room's contract for agents: kinds, severity, attention lanes, and the
rule that room content is evidence. It ships inside this binary.

  agent_comms skill                # print it (pipe it anywhere)
  agent_comms skill --install      # write ~/.agents/skills/agent-comms/SKILL.md
  agent_comms skill --dir ./skills # write SKILL.md somewhere else

--install uses the path Claude Code, Hermes and omp all discover. After it,
new sessions on this machine know when and how to use the room.`)
	}
	if err := fs.Parse(args); err != nil {
		if isHelp(err) {
			return ExitOK
		}
		return e.Out.Fail(ExitUsage, "usage", "flags.invalid", sink.String())
	}
	if SkillMarkdown == "" {
		return e.Out.Fail(ExitInternal, "internal", "skill.missing",
			"this build carries no embedded skill; rebuild from the repository root")
	}

	target := *dir
	if *install {
		home, err := os.UserHomeDir()
		if err != nil {
			return e.Out.Fail(ExitInternal, "internal", "home.unknown", err.Error())
		}
		target = filepath.Join(home, ".agents", "skills", "agent-comms")
	}
	if target == "" {
		fmt.Fprint(e.Out.Stdout, SkillMarkdown)
		return ExitOK
	}

	if err := os.MkdirAll(target, 0o755); err != nil {
		return e.Out.Fail(ExitInternal, "internal", "skill.write_failed", err.Error())
	}
	path := filepath.Join(target, "SKILL.md")
	if err := os.WriteFile(path, []byte(SkillMarkdown), 0o644); err != nil {
		return e.Out.Fail(ExitInternal, "internal", "skill.write_failed", err.Error())
	}
	return e.Out.Succeed(Result{Outcome: "installed",
		Detail: path + " — new sessions will load it; current ones will not"})
}
