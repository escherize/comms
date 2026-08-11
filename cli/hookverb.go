package cli

// The hook closes the half of the protocol agents skip. Posting is a reflex —
// the work produces something, the agent posts it. Reading is a discipline —
// nothing forces it, so it does not happen. A harness hook makes reading
// ambient: every turn, the room's delta lands in the agent's context without
// the agent deciding to look.
//
// One body, N shims. `comms hook run` is the hook: harness-agnostic, text on
// stdout, exit 0 always. `comms hook --install` writes each harness's native
// wiring, and every shim is one line that invokes the body. Hooks have no
// cross-harness standard — skills and MCP travel, hooks were left
// client-specific on purpose — so the portable thing is the binary, and the
// shims are disposable.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// hookCap bounds what one turn injects. A chatty room must not eat an agent's
// context window; what is not shown is named, counted, and reachable.
const hookCap = 10

func runHook(e *Env, args []string) int {
	if len(args) > 0 && args[0] == "run" {
		return runHookRun(e, args[1:])
	}

	fs, sink := newFlags("hook")
	install := fs.Bool("install", false, "write the shims for every harness present on this machine")
	global := fs.Bool("global", false, "with --install: wire every session on this machine, not just this project")
	dryRun := fs.Bool("dry-run", false, "with --install: print what would be written where, write nothing")
	fs.Usage = func() {
		e.Out.HelpFS(fs, `comms hook [run | --install [--global] [--dry-run]]

Wires the room into an agent harness's turn loop, so reading stops being a
discipline and becomes ambient: each turn, anything new in the room lands in
the agent's context.

  comms hook --install             # wire this project (run it in the repo)
  comms hook --install --global    # wire every session on this machine
  comms hook --install --dry-run   # show what would be written where
  comms hook run                   # the hook body itself (what shims invoke)

The body is one command: comms hook run. It prints the room's delta for the
seat in COMMS_ACTOR — capped at %d events, addressed entries marked — then
advances the read cursor and exits 0. On any problem it prints nothing and
still exits 0: a broken hook must not break the harness's turn.

The shim is wiring; the switch is per-session. A session with no COMMS_ACTOR
hits the no-seat path — zero bytes, exit 0 — so only sessions that export a
seat get injections, and everything else a shim reaches stays untouched.

The project scope is the default because the room is a project: a hook armed
machine-wide fires in every unrelated session forever. Per harness found on
this machine, --install writes into the working directory:
  Claude Code   .claude/settings.local.json    (a UserPromptSubmit hook, merged)
  opencode      .opencode/plugin/comms-hook.js
  pi            .pi/extensions/comms-hook.ts
--global writes the machine-wide equivalents under ~ instead.

The opencode and pi shims are templates carrying the one-line contract — run
the command, put its stdout in front of the model — and are safe to edit.
Each shim invokes this binary by absolute path, so PATH does not matter.`, hookCap)
	}
	if err := fs.Parse(args); err != nil {
		if isHelp(err) {
			return usageOK(e)
		}
		return e.Out.Fail(ExitUsage, "usage", "flags.invalid", strings.TrimSpace(sink.String()))
	}
	if !*install {
		fs.Usage()
		return usageOK(e)
	}
	return runHookInstall(e, *global, *dryRun)
}

// ---------------------------------------------------------------- hook run

// runHookRun is the hook body. Its contract is unconditional: exit 0, and on
// any failure print nothing to stdout. It runs on every turn of every agent,
// so a hub outage surfacing as a hook error would break every harness at once.
// Diagnostics go to stderr, which harnesses do not inject.
func runHookRun(e *Env, args []string) int {
	fs, _ := newFlags("hook run")
	actor := fs.String("as", "", "the seat reading (default COMMS_ACTOR)")
	room := fs.String("room", "", "room to read")
	cap := fs.Int("cap", hookCap, "most events to inject per turn; 0 is unlimited")
	if err := fs.Parse(args); err != nil {
		if isHelp(err) {
			return usageOK(e)
		}
		return ExitOK
	}

	seat := *actor
	if seat == "" {
		seat, _ = e.getenv("COMMS_ACTOR")
	}
	if seat == "" {
		e.Out.Note("comms hook run: no seat; set COMMS_ACTOR or pass --as")
		return ExitOK
	}
	// The pinned server is authoritative for a headless hook: there is no
	// human to notice COMMS_SERVER pointing somewhere else.
	if pinned := PinnedServer(seat); pinned != "" {
		e.Server = pinned
	}
	inRoom := resolveRoom(seat, *room)

	events, meta, err := drain(e, readOpts{Actor: seat, Room: inRoom, Lane: LaneAll})
	if err != nil {
		e.Out.Note("comms hook run: %v", err)
		return ExitOK
	}
	if len(events) == 0 {
		// Quiet room, zero bytes: the hook must cost nothing when there is
		// nothing.
		return ExitOK
	}

	shown := events
	if *cap > 0 && len(shown) > *cap {
		shown = shown[:*cap]
	}
	fmt.Fprint(e.Out.Stdout, hookRender(seat, inRoom, events, shown))

	// The cursor advances only over what was injected. When the cap held
	// events back, the next turn picks up exactly there; when everything was
	// shown, catch up to the head like an ordinary read.
	highest := shown[len(shown)-1].Seq
	if len(shown) == len(events) {
		if head, ok := meta["caught_up_seq"].(float64); ok && int64(head) > highest {
			highest = int64(head)
		}
	}
	if err := SaveCursor(seat, inRoom, LaneAll, highest); err != nil {
		e.Out.Note("comms hook run: cursor: %v", err)
	}
	return ExitOK
}

// hookRender is the injected text: terse, one line per event, and a coaching
// footer that names the verbs a good room citizen reaches for. The footer
// rides only on turns that already inject events, so quiet turns stay free.
func hookRender(seat, room string, events, shown []frame) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[comms] %d new in %s for %s:\n", len(events), room, seat)
	for _, f := range shown {
		author, _ := f.Data["author"].(string)
		kind, _ := f.Data["kind"].(string)
		line := fmt.Sprintf("  %d %s %s", f.Seq, kind, author)
		if rec, _ := f.Data["recipient"].(string); rec == seat {
			line += " → you"
		}
		if body, ok := f.Data["body"].(map[string]any); ok {
			if sev, ok := body["severity"].(string); ok && sev != "" {
				line += " " + sev
			}
			if txt, ok := body["text"].(string); ok {
				preview, clipped := truncateText(txt, 100)
				line += ": " + preview
				if clipped {
					line += fmt.Sprintf(" (full: comms read --from %d --full --as %s)", f.Seq, seat)
				}
			}
		}
		b.WriteString(line + "\n")
	}
	if rest := len(events) - len(shown); rest > 0 {
		fmt.Fprintf(&b, "[comms] %d more not shown: comms read --as %s --full\n", rest, seat)
	}
	fmt.Fprintf(&b, "[comms] use the room like a pro: comms search \"<terms>\" before starting or asking"+
		" — someone may have hit this already; post finding/til the moment you learn something"+
		" branch-independent; answer what names you: comms answer --to-question <seq> --as %s;"+
		" your addressed lane: comms inbox --as %s.\n", seat, seat)
	return b.String()
}

// ---------------------------------------------------------------- hook --install

// hookShim is one harness's wiring. Detect is the home directory whose
// presence means the harness lives on this machine; a harness that is not
// there gets no file anywhere, because a shim in a config dir the user never
// made is litter. Project and Global are the two targets one shim can land
// at — the file is only wiring either way; the per-session switch is
// COMMS_ACTOR.
type hookShim struct {
	Name    string
	Detect  string
	Project string
	Global  string
	Write   func(target, cmd string) error
}

func hookShims() []hookShim {
	return []hookShim{
		{"claude-code", ".claude",
			filepath.Join(".claude", "settings.local.json"),
			filepath.Join(".claude", "settings.json"), writeClaudeShim},
		{"opencode", filepath.Join(".config", "opencode"),
			filepath.Join(".opencode", "plugin", "comms-hook.js"),
			filepath.Join(".config", "opencode", "plugin", "comms-hook.js"), writeOpencodeShim},
		{"pi", ".pi",
			filepath.Join(".pi", "extensions", "comms-hook.ts"),
			filepath.Join(".pi", "extensions", "comms-hook.ts"), writePiShim},
	}
}

func runHookInstall(e *Env, global, dry bool) int {
	home, err := os.UserHomeDir()
	if err != nil {
		return e.Out.Fail(ExitInternal, "internal", "home.unknown", err.Error())
	}
	root := home
	scope := "global"
	if !global {
		scope = "project"
		if root, err = os.Getwd(); err != nil {
			return e.Out.Fail(ExitInternal, "internal", "cwd.unknown", err.Error())
		}
	}
	bin, err := os.Executable()
	if err != nil {
		return e.Out.Fail(ExitInternal, "internal", "binary.unknown", err.Error())
	}
	cmd := shellQuote(bin) + " hook run"

	installed := 0
	for _, s := range hookShims() {
		rel := s.Project
		if global {
			rel = s.Global
		}
		target := filepath.Join(root, rel)
		if _, err := os.Stat(filepath.Join(home, s.Detect)); err != nil {
			e.Out.Line(map[string]any{"type": "shim", "harness": s.Name,
				"outcome": "skipped", "detail": "~/" + s.Detect + " does not exist"})
			continue
		}
		if dry {
			e.Out.Line(map[string]any{"type": "shim", "harness": s.Name,
				"outcome": "would-write", "scope": scope, "path": target, "command": cmd})
			continue
		}
		if err := s.Write(target, cmd); err != nil {
			e.Out.Line(map[string]any{"type": "shim", "harness": s.Name,
				"outcome": "failed", "path": target, "detail": err.Error()})
			continue
		}
		installed++
		e.Out.Line(map[string]any{"type": "shim", "harness": s.Name,
			"outcome": "installed", "scope": scope, "path": target})
		e.Out.Note("%s: %s", s.Name, target)
	}
	if dry {
		return e.Out.Succeed(Result{Outcome: "dry-run"})
	}
	return e.Out.Succeed(Result{Outcome: "installed", Count: installed,
		Detail: "new sessions pick the hook up; current ones do not. The hook fires " +
			"only in sessions that export COMMS_ACTOR — that is the per-session switch"})
}

// writeClaudeShim merges one UserPromptSubmit entry into settings.json. Merge,
// never overwrite: this file is the user's, and the hook is a guest in it. A
// file that does not parse is refused rather than repaired — clobbering a
// hand-edited settings file to install a convenience would be theft.
func writeClaudeShim(target, cmd string) error {
	settings := map[string]any{}
	if raw, err := os.ReadFile(target); err == nil {
		if err := json.Unmarshal(raw, &settings); err != nil {
			return fmt.Errorf("%s does not parse as JSON; fix it first: %v", target, err)
		}
	}

	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	entries, _ := hooks["UserPromptSubmit"].([]any)

	// Idempotent by marker: any command ending in " hook run" is ours from an
	// earlier install (possibly of a binary that has since moved), so it is
	// updated in place rather than accumulated.
	found := false
	for _, entry := range entries {
		m, _ := entry.(map[string]any)
		inner, _ := m["hooks"].([]any)
		for _, h := range inner {
			hm, _ := h.(map[string]any)
			if c, _ := hm["command"].(string); strings.HasSuffix(c, " hook run") {
				hm["command"] = cmd
				found = true
			}
		}
	}
	if !found {
		entries = append(entries, map[string]any{
			"hooks": []any{map[string]any{"type": "command", "command": cmd}},
		})
	}
	hooks["UserPromptSubmit"] = entries
	settings["hooks"] = hooks

	raw, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, append(raw, '\n'), 0o644)
}

// The opencode and pi shims are whole files this verb owns: rewriting them on
// every install is what makes re-running it safe. Their event names are a
// best effort against a moving plugin API; the header states the actual
// contract so a human or agent can re-aim them in one edit.

const opencodeShimTmpl = `// written by: comms hook --install — safe to edit or delete.
// contract: run the command below and put its stdout in front of the model.
// if the event name has drifted from opencode's plugin API, fix the event,
// keep the command.
export const CommsHook = async ({ $ }) => ({
	event: async ({ event }) => {
		if (event.type !== "session.idle") return
		const out = await $%s.text().catch(() => "")
		if (out.trim()) console.log(out)
	},
})
`

func writeOpencodeShim(target, cmd string) error {
	body := fmt.Sprintf(opencodeShimTmpl, "`"+cmd+"`")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, []byte(body), 0o644)
}

const piShimTmpl = `// written by: comms hook --install — safe to edit or delete.
// contract: run the command below and put its stdout in front of the model.
// if the hook name has drifted from pi's extension API, fix the hook,
// keep the command.
export default function (pi: any) {
	pi.on("turn_start", async (ctx: any) => {
		const out = await pi.exec(%s)
		if (out?.stdout?.trim()) ctx.addContext(out.stdout)
	})
}
`

func writePiShim(target, cmd string) error {
	args, _ := json.Marshal(strings.Fields(cmd))
	body := fmt.Sprintf(piShimTmpl, string(args))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, []byte(body), 0o644)
}
