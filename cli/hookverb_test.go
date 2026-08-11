package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/escherize/comms/core"
)

// The hook's whole contract is "exit 0, and quiet means zero bytes". A hook
// that fails loudly breaks every harness turn at once; a hook that prints on
// a quiet room charges every turn for nothing.
func TestHookRunIsSilentAndZeroOnEveryFailure(t *testing.T) {
	isolateKeys(t)

	// No seat at all.
	var c capture
	if code := Run(c.env(t, "http://127.0.0.1:1", ""), []string{"hook", "run"}); code != ExitOK {
		t.Fatalf("hook run with no seat exited %d", code)
	}
	if c.out.String() != "" {
		t.Errorf("no seat must print nothing to stdout, got %q", c.out.String())
	}

	// A seat, but the hub is unreachable.
	var c2 capture
	if code := Run(c2.env(t, "http://127.0.0.1:1", ""),
		[]string{"hook", "run", "--as", seat}); code != ExitOK {
		t.Fatalf("hook run against a dead hub exited %d", code)
	}
	if c2.out.String() != "" {
		t.Errorf("a dead hub must print nothing to stdout, got %q", c2.out.String())
	}
}

func TestHookRunInjectsCapsAndAdvances(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)

	for i := 0; i < 3; i++ {
		if _, err := st.Append(core.Event{Room: "core", Author: "human:sarah",
			Kind: core.KindFinding,
			Body: map[string]any{"text": "finding number", "severity": "p2"},
			Lane: core.LaneOf(core.KindFinding)}, "hook-f"+itoa(int64(i)), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	q, err := st.Append(core.Event{Room: "core", Author: "human:sarah",
		Kind: core.KindQuestion, Recipient: core.Actor(seat),
		Body: map[string]any{"text": "is this yours?"},
		Lane: core.LaneOf(core.KindQuestion)}, "hook-q", time.Now())
	if err != nil {
		t.Fatal(err)
	}

	// Cap below the backlog: two shown, the rest named and counted.
	var c capture
	if code := Run(c.env(t, srv.URL, ""),
		[]string{"hook", "run", "--as", seat, "--cap", "2"}); code != ExitOK {
		t.Fatalf("hook run exited %d: %s", code, c.out.String())
	}
	out := c.out.String()
	if !strings.Contains(out, "4 new in core") {
		t.Errorf("the header must count everything new, got:\n%s", out)
	}
	if !strings.Contains(out, "2 more not shown") {
		t.Errorf("what the cap held back must be counted and reachable, got:\n%s", out)
	}
	if !strings.Contains(out, "comms search") {
		t.Errorf("the footer must coach search, got:\n%s", out)
	}

	// The next run picks up exactly where the cap stopped: the question,
	// marked as addressed.
	var c2 capture
	if code := Run(c2.env(t, srv.URL, ""),
		[]string{"hook", "run", "--as", seat}); code != ExitOK {
		t.Fatalf("second hook run exited %d", code)
	}
	if !strings.Contains(c2.out.String(), "→ you") {
		t.Errorf("an addressed question must be marked, got:\n%s", c2.out.String())
	}
	if !strings.Contains(c2.out.String(), itoa(q)) {
		t.Errorf("the second run must reach the question at %d, got:\n%s", q, c2.out.String())
	}

	// Caught up: zero bytes.
	var c3 capture
	if code := Run(c3.env(t, srv.URL, ""),
		[]string{"hook", "run", "--as", seat}); code != ExitOK {
		t.Fatalf("third hook run exited %d", code)
	}
	if c3.out.String() != "" {
		t.Errorf("a caught-up hook must print nothing, got %q", c3.out.String())
	}
}

// The Claude shim merges into a settings file the user owns. It must add ours
// without disturbing theirs, update rather than accumulate on re-install, and
// refuse a file it cannot parse rather than repair it.
func TestClaudeShimMergesAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(target, []byte(`{"model":"opus","hooks":{"Stop":[{"hooks":[{"type":"command","command":"say done"}]}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := writeClaudeShim(target, "/usr/local/bin/comms hook run"); err != nil {
		t.Fatal(err)
	}
	if err := writeClaudeShim(target, "/opt/comms hook run"); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatalf("the merged file must stay valid JSON: %v", err)
	}
	if settings["model"] != "opus" {
		t.Error("the merge must not disturb unrelated settings")
	}
	hooks := settings["hooks"].(map[string]any)
	if _, ok := hooks["Stop"]; !ok {
		t.Error("the merge must not disturb the user's other hooks")
	}
	if n := len(hooks["UserPromptSubmit"].([]any)); n != 1 {
		t.Errorf("re-install must update in place, not accumulate; got %d entries", n)
	}
	if !strings.Contains(string(raw), "/opt/comms hook run") {
		t.Error("re-install must point the entry at the new binary")
	}

	// A file that does not parse is the user's problem to fix, not ours to
	// clobber.
	if err := os.WriteFile(target, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeClaudeShim(target, "comms hook run"); err == nil {
		t.Error("a settings file that does not parse must be refused, not overwritten")
	}
}

// Install detects harnesses by their home config dirs, defaults to the
// project scope, and never litters a machine with shims for harnesses that
// are not there.
func TestHookInstallScopesAndDetects(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	// No ~/.pi: that shim must be skipped in both scopes.

	project := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(prev)

	// Default scope is the project: files land in the working directory, and
	// the personal settings file, not the shared one.
	var c capture
	if code := Run(c.env(t, "http://127.0.0.1:1", ""), []string{"hook", "--install"}); code != ExitOK {
		t.Fatalf("hook --install exited %d: %s", code, c.out.String())
	}
	if _, err := os.Stat(filepath.Join(project, ".claude", "settings.local.json")); err != nil {
		t.Error("project install must write .claude/settings.local.json in the working directory")
	}
	if _, err := os.Stat(filepath.Join(project, ".opencode", "plugin", "comms-hook.js")); err != nil {
		t.Error("project install must write the opencode shim into the working directory")
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Error("a project install must not touch the machine-wide settings")
	}
	if _, err := os.Stat(filepath.Join(project, ".pi")); !os.IsNotExist(err) {
		t.Error("install must not create config dirs for harnesses that are not there")
	}
	if !strings.Contains(c.out.String(), `"skipped"`) {
		t.Error("a skipped harness must be reported, not silent")
	}

	// --global writes under home instead.
	var c2 capture
	if code := Run(c2.env(t, "http://127.0.0.1:1", ""), []string{"hook", "--install", "--global"}); code != ExitOK {
		t.Fatalf("hook --install --global exited %d: %s", code, c2.out.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); err != nil {
		t.Error("global install must write ~/.claude/settings.json")
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "plugin", "comms-hook.js")); err != nil {
		t.Error("global install must write the opencode shim under home")
	}
}
