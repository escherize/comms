package cli

// Skills travel inside the binary. `go install` and one verb is the whole
// onboarding: nothing to clone, no path to a document that may not exist on
// the machine the agent runs on.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SkillDoc is one embedded skill, set by package main — the embed must live
// where the files live (the repository root); the verbs live here with the
// others. The first entry is the primary: what a bare `skill` prints.
type SkillDoc struct {
	Doc string
}

var Skills []SkillDoc

// skillField reads a frontmatter field. The name field is also the install
// directory, so the two can never disagree.
func skillField(doc, field string) string {
	lines := strings.Split(doc, "\n")
	if len(lines) == 0 || lines[0] != "---" {
		return ""
	}
	for _, line := range lines[1:] {
		if line == "---" {
			break
		}
		if v, ok := strings.CutPrefix(line, field+": "); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func runSkillsList(e *Env, args []string) int {
	fs, sink := newFlags("skills")
	fs.Usage = func() {
		e.Out.HelpFS(fs, `comms skills

List the skills this binary carries. Each is printed by
comms skill <name>, and installed by comms skill --install.`)
	}
	if err := fs.Parse(args); err != nil {
		if isHelp(err) {
			return ExitOK
		}
		return e.Out.Fail(ExitUsage, "usage", "flags.invalid", sink.String())
	}
	for _, s := range Skills {
		name := skillField(s.Doc, "name")
		desc := skillField(s.Doc, "description")
		e.Out.Line(map[string]any{"type": "skill", "name": name, "description": desc})
		e.Out.Note("%s — %s", name, desc)
	}
	return e.Out.Succeed(Result{Outcome: "skills"})
}

func runSkillVerb(e *Env, args []string) int {
	fs, sink := newFlags("skill")
	install := fs.Bool("install", false,
		"write skills where agent harnesses load them (~/.agents/skills/<name>/)")
	dir := fs.String("dir", "",
		"write into <dir>/<name>/SKILL.md instead")
	fs.Usage = func() {
		e.Out.HelpFS(fs, `comms skill [name] [--install | --dir <path>]

The skills this binary carries, embedded at build time. Bare form prints the
primary (the room contract for agents); a name picks one; comms skills
lists them.

  comms skill                      # print the agent skill
  comms skill comms-hub      # print the hub-operating skill
  comms skill --install            # install every skill
  comms skill <name> --install     # install one

--install writes ~/.agents/skills/<name>/SKILL.md, the path Claude Code,
Hermes and omp all discover. New sessions load it; current ones do not.`)
	}
	if err := fs.Parse(args); err != nil {
		if isHelp(err) {
			return ExitOK
		}
		return e.Out.Fail(ExitUsage, "usage", "flags.invalid", sink.String())
	}
	if len(Skills) == 0 {
		return e.Out.Fail(ExitInternal, "internal", "skill.missing",
			"this build carries no embedded skills; rebuild from the repository root")
	}

	chosen := Skills
	if name := fs.Arg(0); name != "" {
		chosen = nil
		for _, s := range Skills {
			if skillField(s.Doc, "name") == name {
				chosen = []SkillDoc{s}
			}
		}
		if chosen == nil {
			var known []string
			for _, s := range Skills {
				known = append(known, skillField(s.Doc, "name"))
			}
			return e.Out.Fail(ExitUsage, "usage", "skill.unknown",
				"no skill named "+name+"; this binary carries: "+strings.Join(known, ", "))
		}
	} else if !*install && *dir == "" {
		chosen = Skills[:1] // bare print form: the primary skill only
	}

	root := *dir
	if *install {
		home, err := os.UserHomeDir()
		if err != nil {
			return e.Out.Fail(ExitInternal, "internal", "home.unknown", err.Error())
		}
		root = filepath.Join(home, ".agents", "skills")
	}
	if root == "" {
		for _, s := range chosen {
			fmt.Fprint(e.Out.Stdout, s.Doc)
		}
		return ExitOK
	}

	var paths []string
	for _, s := range chosen {
		target := filepath.Join(root, skillField(s.Doc, "name"))
		if err := os.MkdirAll(target, 0o755); err != nil {
			return e.Out.Fail(ExitInternal, "internal", "skill.write_failed", err.Error())
		}
		path := filepath.Join(target, "SKILL.md")
		if err := os.WriteFile(path, []byte(s.Doc), 0o644); err != nil {
			return e.Out.Fail(ExitInternal, "internal", "skill.write_failed", err.Error())
		}
		paths = append(paths, path)
	}
	return e.Out.Succeed(Result{Outcome: "installed",
		Detail: strings.Join(paths, ", ") + " — new sessions will load them; current ones will not"})
}
