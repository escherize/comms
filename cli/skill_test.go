package cli

import (
	"os"
	"time"

	"github.com/escherize/agent-comms/core"
	"regexp"
	"strings"
	"testing"
)

// The skill file is the only documentation an agent reads before acting, so a
// command in it that errors on first use is worse than one that is missing: the
// agent has no way to tell a broken example from its own mistake, and the first
// thing it learns about the room is that the room lies.
//
// So every command in SKILL.md is run here, in order, against a live server.

// skillCommand is one runnable line lifted out of the skill file.
type skillCommand struct {
	line  int
	args  []string
	stdin string
}

// skillCommands parses the fenced sh blocks. It handles the three shapes the
// file actually uses: a plain invocation, a pipe into the client, and a heredoc.
func skillCommands(t *testing.T, doc string) []skillCommand {
	t.Helper()
	var out []skillCommand
	lines := strings.Split(doc, "\n")
	inBlock := false

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			// An info string opens a block; a bare fence closes one.
			if !inBlock && len(strings.TrimLeft(trimmed, "`")) > 0 {
				inBlock = true
			} else {
				inBlock = false
			}
			continue
		}
		if !inBlock || trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Join backslash continuations before anything else looks at the line.
		full := trimmed
		for strings.HasSuffix(full, "\\") && i+1 < len(lines) {
			i++
			full = strings.TrimSuffix(full, "\\") + " " + strings.TrimSpace(lines[i])
		}

		// A pipe feeds the client; what is on the left is the agent's own work
		// and not ours to run.
		stdin := ""
		if idx := strings.Index(full, "| agent-comms"); idx != -1 {
			stdin = "=== RUN TestAuth\n--- FAIL: TestAuth (0.02s)\n    auth_test.go:88: cold cache\n"
			full = strings.TrimSpace(full[idx+1:])
		}

		// A heredoc supplies stdin from the following lines.
		if hd := regexp.MustCompile(`<<'?(\w+)'?$`).FindStringSubmatch(full); hd != nil {
			var body []string
			for i+1 < len(lines) && strings.TrimSpace(lines[i+1]) != hd[1] {
				i++
				body = append(body, lines[i])
			}
			i++ // the terminator
			stdin = strings.Join(body, "\n") + "\n"
			full = strings.TrimSpace(full[:strings.Index(full, "<<")])
		}

		if !strings.HasPrefix(full, "agent-comms") {
			continue
		}
		// A trailing comment is prose, not an argument.
		if idx := strings.Index(full, "  # "); idx != -1 {
			full = strings.TrimSpace(full[:idx])
		}

		args := splitArgs(full)
		out = append(out, skillCommand{line: i + 1, args: args[1:], stdin: stdin})
	}
	return out
}

// splitArgs is shell word-splitting for the quoting the file actually uses.
func splitArgs(s string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	for _, r := range s {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			cur.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
		case r == ' ':
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func TestEverySkillCommandRuns(t *testing.T) {
	isolateKeys(t)
	srv, st := liveServer(t)
	enrol(t, srv, st)
	seedActor(t, st, "human:sarah")
	seedActor(t, st, "human:bcm")
	claimed = stdinClaim{}
	if err := st.EnsureRoom("core"); err != nil {
		t.Fatal(err)
	}

	// The examples reference files, and attach is confined to the working tree.
	// A temp cwd is both the confinement boundary and a clean place to put them.
	dir := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(prev)
	for _, f := range []string{"repro.md", "row-count-math.md"} {
		if err := os.WriteFile(f, []byte("# "+f+"\n\nreproduction steps\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	raw, err := os.ReadFile(prev + "/../docs/AGENT-SKILL.md")
	if err != nil {
		raw, err = os.ReadFile(prev + "/docs/AGENT-SKILL.md")
		if err != nil {
			t.Fatalf("the skill file must exist: %v", err)
		}
	}
	doc := string(raw)

	cmds := skillCommands(t, doc)
	if len(cmds) < 15 {
		t.Fatalf("only %d commands parsed out of the skill; the parser is wrong", len(cmds))
	}

	// Placeholders stand for values a real session would have captured. They are
	// substituted with real ones so the examples run as written otherwise.
	subs := map[string]string{}
	question := int64(0)
	finding := int64(0)

	// A real agent reaching the answer example has a question to answer and a
	// hash it uploaded. Seed both, so the examples are exercised as written
	// rather than skipped for want of a value the prose assumes.
	seedQ, err := st.Append(core.Event{Room: "core", Author: "human:sarah",
		Kind: core.KindQuestion, Recipient: core.Actor(seat),
		Body: map[string]any{"text": "is the -race flake ours or the runner image?"},
		Lane: core.LaneOf(core.KindQuestion)}, "skill-q", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	subs["20015"] = itoa(seedQ)

	seedF, err := st.Append(core.Event{Room: "core", Author: core.Actor(seat),
		Kind: core.KindFinding, Body: map[string]any{"text": "seed finding", "severity": "p2"},
		Lane: core.LaneOf(core.KindFinding)}, "skill-f", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	subs["20014"] = itoa(seedF)

	// The skill's decline example names a handoff. Seed one addressed to this
	// seat, so the example runs as written rather than being special-cased.
	seedH, err := st.Append(core.Event{Room: "core", Author: "human:sarah",
		Kind: core.KindHandoff, Recipient: core.Actor(seat),
		Body: map[string]any{"text": "the retry path is yours"},
		Lane: core.LaneOf(core.KindHandoff)}, "skill-h", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	subs["50002"] = itoa(seedH)

	var up capture
	if code := Run(up.env(t, srv.URL, "# seeded artifact\n"), []string{"attach", "-"}); code != ExitOK {
		t.Fatalf("seeding an artifact failed: %s", up.out.String())
	}
	claimed = stdinClaim{}
	if h, _ := lines(t, &up)[0]["hash"].(string); h != "" {
		subs["a3f0…9c21"] = h
	} else {
		t.Fatal("seeding an artifact produced no hash")
	}

	for _, c := range cmds {
		args := append([]string{}, c.args...)
		for i, a := range args {
			for from, to := range subs {
				if a == from {
					args[i] = to
				}
			}
		}
		args = withSeat(args)
		// A --wait in the file is a real instruction to a real agent and a
		// fifteen-minute test. The duration is the one thing shortened here;
		// everything else runs as written.
		for i, a := range args {
			if a == "--wait" && i+1 < len(args) {
				args[i+1] = "1s"
			}
		}

		var cap capture
		code := Run(cap.env(t, srv.URL, c.stdin), args)
		claimed = stdinClaim{}

		if code != ExitOK {
			t.Errorf("SKILL.md line %d: `agent-comms %s` exited %d\n%s",
				c.line, strings.Join(args, " "), code, cap.out.String())
			continue
		}

		// Capture what a real session would have carried forward.
		for _, l := range lines(t, &cap) {
			if h, ok := l["hash"].(string); ok && h != "" {
				subs["a3f0…9c21"] = h
			}
			seq, ok := l["seq"].(float64)
			if !ok {
				continue
			}
			switch args[1] {
			case "question":
				question = int64(seq)
				subs["20015"] = itoa(question)
			case "finding":
				finding = int64(seq)
				subs["20014"] = itoa(finding)
			}
			if args[0] == "ask" {
				question = int64(seq)
				subs["20015"] = itoa(question)
			}
		}
	}

	if question == 0 || finding == 0 {
		t.Error("the worked hour should have produced both a question and a finding")
	}
}

// withSeat supplies the seat the examples leave implicit, and only that: an
// example that needed any other flag added to run is an example that does not
// run as written.
func withSeat(args []string) []string {
	needsSeat := map[string]bool{
		"post": true, "ask": true, "answer": true, "decline": true, "escalate": true,
		"read": true, "inbox": true, "redact": true, "whoami": true,
		"room": true, "search": true,
	}
	if len(args) == 0 || !needsSeat[args[0]] {
		return args
	}
	for _, a := range args {
		if a == "--as" {
			return args
		}
	}
	return append(args, "--as", seat)
}

// Every invariant the skill names must exist. An invariant table that lists one
// the room never emits teaches an agent to wait for a signal that never comes.
func TestSkillNamesOnlyRealInvariants(t *testing.T) {
	doc := mustReadSkill(t)
	sources := map[string]bool{}
	for _, f := range []string{"../core/core.go", "../store/keys.go", "../shell/shell.go",
		"../cli/verbs.go", "../cli/ask.go", "../cli/custody.go", "../cli/room.go",
		"../store/store.go", "../shell/ratelimit.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range regexp.MustCompile(`"([a-z_]+\.[a-z_]+)"`).FindAllStringSubmatch(string(b), -1) {
			sources[m[1]] = true
		}
	}

	// The skill names invariants in table cells, as backticked tokens.
	named := regexp.MustCompile("`([a-z_]+\\.[a-z_]+)`").FindAllStringSubmatch(doc, -1)
	var checked int
	for _, m := range named {
		inv := m[1]
		// Not every dotted token is an invariant: kinds and filenames share the
		// shape. Only check the ones that look like one.
		if !strings.Contains(inv, "_") && !knownInvariantPrefix(inv) {
			continue
		}
		checked++
		if !sources[inv] {
			t.Errorf("SKILL.md names the invariant %q and nothing emits it", inv)
		}
	}
	if checked < 5 {
		t.Errorf("only %d invariants checked; the extractor is not finding them", checked)
	}
}

func knownInvariantPrefix(s string) bool {
	for _, p := range []string{"recipient.", "body.", "refs.", "attachment.", "redact.",
		"room.", "key.", "rate.", "digest.", "server.", "attach.", "stdin.", "text."} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// The kind table and the kind set must be the same set. A kind missing from the
// table is a kind no agent will ever use; a kind in the table that the core does
// not know is a rejection on first use.
func TestSkillKindTableMatchesTheKindSet(t *testing.T) {
	doc := mustReadSkill(t)

	// Scope to the kind table. The file has several tables and every one of them
	// begins a row with a backticked token, so an unscoped match reads the
	// invariant table as a list of kinds.
	start := strings.Index(doc, "| Kind | Means |")
	if start == -1 {
		t.Fatal("the skill must have a kind table")
	}
	table := doc[start:]
	if end := strings.Index(table, "\n\n"); end != -1 {
		table = table[:end]
	}

	inTable := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\| `+"`"+`([a-z.]+)`+"`").FindAllStringSubmatch(table, -1) {
		inTable[m[1]] = true
	}
	if len(inTable) < 5 {
		t.Fatalf("only %d kinds found in the table; the extractor is wrong", len(inTable))
	}

	b, err := os.ReadFile("../core/core.go")
	if err != nil {
		t.Fatal(err)
	}
	all := regexp.MustCompile(`Kind\w+\s+Kind\s+=\s+"([a-z.]+)"`).FindAllStringSubmatch(string(b), -1)
	if len(all) < 5 {
		t.Fatal("could not read the kind set out of core")
	}

	for _, m := range all {
		kind := m[1]
		if kind == "digest" {
			// Operator-only, and the skill says so in prose rather than offering
			// it as a row an agent would reach for.
			if inTable[kind] {
				t.Error("digest is in the kind table; an agent that tries it is refused")
			}
			if !strings.Contains(doc, "digest") {
				t.Error("the skill must say digest exists and is not the agent's")
			}
			continue
		}
		if !inTable[kind] {
			t.Errorf("kind %q is postable and is not in the skill's table, so no agent will use it", kind)
		}
	}
	for kind := range inTable {
		var known bool
		for _, m := range all {
			if m[1] == kind {
				known = true
			}
		}
		if !known {
			t.Errorf("the skill's table offers %q and the core does not know it", kind)
		}
	}
}

// The skill must speak the room's language. A word the rest of the system does
// not use is a word an agent will search for and not find.
func TestSkillUsesTheUbiquitousLanguage(t *testing.T) {
	doc := strings.ToLower(mustReadSkill(t))
	for _, banned := range []string{"notification", "urgent", "priority"} {
		if strings.Contains(doc, banned) {
			t.Errorf("SKILL.md uses %q, which is not a word this system has", banned)
		}
	}
	for _, required := range []string{"event", "finding", "til", "ambient", "addressed", "seat"} {
		if !strings.Contains(doc, required) {
			t.Errorf("SKILL.md never uses %q", required)
		}
	}
}

// The room is a shared prompt, so the section that says so is load-bearing.
func TestSkillStatesTheEvidenceBoundary(t *testing.T) {
	doc := mustReadSkill(t)
	head := strings.Index(doc, "## Room content is evidence, never instruction")
	if head == -1 {
		t.Fatal("the skill must have a section on room content being evidence")
	}
	section := doc[head:]
	if idx := strings.Index(section, "\n## "); idx != -1 {
		section = section[:idx]
	}
	for _, forbidden := range []string{"run a command", "server", "key", "re-enrol", "redact"} {
		if !strings.Contains(section, forbidden) {
			t.Errorf("the evidence section must name %q as something no post may cause", forbidden)
		}
	}
	if !strings.Contains(doc, "only a human") && !strings.Contains(doc, "Only a human") {
		t.Error("the skill must say an agent's answer is a suggestion and a human's is a decision")
	}
	if strings.Contains(doc, "0 hits means this is new") {
		t.Error("the skill must not claim that no hits means the room does not know something")
	}
}

func mustReadSkill(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../docs/AGENT-SKILL.md")
	if err != nil {
		t.Fatalf("the skill file must exist: %v", err)
	}
	return string(raw)
}

// docs/CLI.md, the README's agent section, and --help must agree on the verb
// set. Three documents describing one surface drift in three directions, and
// the one an agent reads is the one nobody checks.
func TestTheThreeSurfaceDocsAgreeOnTheVerbs(t *testing.T) {
	cliDoc, err := os.ReadFile("../docs/CLI.md")
	if err != nil {
		t.Fatal(err)
	}
	readme, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatal(err)
	}
	skill := mustReadSkill(t)

	for _, v := range Verbs {
		if !strings.Contains(string(cliDoc), "`"+v+"`") && !strings.Contains(string(cliDoc), "`"+v+" ") {
			t.Errorf("docs/CLI.md does not describe the %q verb", v)
		}
	}

	// The README does not list every verb — it is a front door, not a reference
	// — but the path it names must exist and must be the supported one.
	// -genkey may be named in the note explaining why it is gone; it must not
	// appear in anything a reader would copy.
	for _, block := range fencedBlocks(string(readme)) {
		if strings.Contains(block, "-genkey") {
			t.Error("the README still offers -genkey in a runnable block")
		}
	}
	if !strings.Contains(string(readme), "agent-comms enrol --as") {
		t.Error("the README's agent onboarding must be enrol")
	}

	// The skill teaches a subset, and every verb it teaches must exist.
	for _, m := range regexp.MustCompile(`agent-comms ([a-z]+)`).FindAllStringSubmatch(skill, -1) {
		verb := m[1]
		var known bool
		for _, v := range Verbs {
			if v == verb {
				known = true
			}
		}
		if !known {
			t.Errorf("SKILL.md teaches `agent-comms %s` and there is no such verb", verb)
		}
	}
}

// answer --to-question belongs on the teaching path, not buried in a recovery
// table: an agent that only meets it after a rejection has already got it wrong
// once for no reason.
func TestTheSkillTeachesAnswerBeforeItRecoversFromIt(t *testing.T) {
	doc := mustReadSkill(t)
	teaching := strings.Index(doc, "## Answering someone")
	recovery := strings.Index(doc, "| Invariant | What to do |")
	if teaching == -1 {
		t.Fatal("the skill must teach answering")
	}
	if recovery != -1 && teaching > recovery {
		t.Error("answer is taught only after the recovery table; it belongs on the path")
	}
	section := doc[teaching:]
	if idx := strings.Index(section, "\n## "); idx != -1 {
		section = section[:idx]
	}
	if !strings.Contains(section, "--to-question") {
		t.Error("the teaching section must show --to-question")
	}
}

// fencedBlocks returns the contents of every fenced code block.
func fencedBlocks(doc string) []string {
	var out []string
	var cur strings.Builder
	in := false
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if in {
				out = append(out, cur.String())
				cur.Reset()
			}
			in = !in
			continue
		}
		if in {
			cur.WriteString(line + "\n")
		}
	}
	return out
}

// docs/CLI.md, the README's API section and SKILL.md must tell one story about
// idempotency. Two agents in the 2026-08-07 study reached for an --idem flag,
// found nothing, and drew opposite conclusions about whose job dedup is —
// because the API required a key and the client silently invented one.
func TestTheThreeDocsTellOneIdempotencyStory(t *testing.T) {
	docs := map[string]string{
		"docs/CLI.md":         mustRead(t, "../docs/CLI.md"),
		"README.md":           mustRead(t, "../README.md"),
		"docs/AGENT-SKILL.md": mustRead(t, "../docs/AGENT-SKILL.md"),
	}

	for name, doc := range docs {
		lower := strings.ToLower(doc)
		if !strings.Contains(lower, "idem") {
			t.Errorf("%s never mentions idempotency, so a reader of it alone cannot know "+
				"whether it is their job", name)
			continue
		}
		// The one claim all three must make: the client derives the key, so a
		// re-run of the same command is a replay.
		if !strings.Contains(lower, "replay") && !strings.Contains(lower, "replayed") {
			t.Errorf("%s does not say a re-run is a replay, which is the whole rule", name)
		}
	}

	// And --idem must be real wherever it is described.
	for name, doc := range docs {
		if !strings.Contains(doc, "--idem") {
			continue
		}
		var c capture
		env := c.env(t, "http://127.0.0.1:1", "")
		env.Out.Quiet = true
		Run(env, []string{"post", "--help"})
		if !strings.Contains(c.out.String(), "--idem") {
			t.Errorf("%s documents --idem and post --help does not offer it", name)
		}
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return string(b)
}

// The first command in the README must start the hub. Ticket 19 made the bare
// binary print the verb list, which is right, and left the README saying the
// bare binary serves, which then was not — the single worst place for a
// documentation defect, because it is the first thing anyone types.
func TestTheREADMEStartsTheHubWithACommandThatExists(t *testing.T) {
	readme := mustRead(t, "../README.md")

	var starts bool
	for _, block := range fencedBlocks(readme) {
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			if !strings.Contains(line, "agent-comms") {
				continue
			}
			// Any line that means "run the hub" must say serve, or pass an
			// operator flag. A bare invocation prints the verb list.
			if strings.HasSuffix(line, "agent-comms") ||
				strings.HasPrefix(line, "./agent-comms  ") {
				t.Errorf("README runs the bare binary as though it serves: %q", line)
			}
			if strings.Contains(line, "agent-comms serve") {
				starts = true
			}
		}
	}
	if !starts {
		t.Error("the README never shows how to start the hub")
	}

	// And serve is a verb the binary lists, so somebody who types the binary's
	// name is told about it.
	var listed bool
	for _, v := range Verbs {
		if v == "serve" {
			listed = true
		}
	}
	if !listed {
		t.Error("serve must be in the verb list; it is the first thing anyone runs")
	}
}

// The binary must be able to answer "what can I post". Three documents once
// listed 8, 8 and 26 kinds while core held the answer and had no way to say it,
// so every copy rotted separately and a human had to be asked.
func TestTheKindsVerbMatchesTheCore(t *testing.T) {
	isolateKeys(t)
	var c capture
	env := c.env(t, "http://127.0.0.1:1", "")
	env.Out.Quiet = true
	if code := Run(env, []string{"kinds"}); code != ExitOK {
		t.Fatalf("kinds exited %d", code)
	}

	printed := map[string]string{}
	for _, l := range lines(t, &c) {
		if l["type"] != "kind" {
			continue
		}
		printed[l["kind"].(string)] = l["lane"].(string)
	}

	// Set equality with the core, and the lane it actually assigns.
	for _, k := range core.AllKinds {
		lane, ok := printed[string(k)]
		if !ok {
			t.Errorf("kind %q exists and `agent-comms kinds` does not print it", k)
			continue
		}
		want := "ambient"
		if core.LaneOf(k) == core.Addressed {
			want = "addressed"
		}
		if lane != want {
			t.Errorf("kinds says %q is %s; LaneOf says %s", k, lane, want)
		}
	}
	for k := range printed {
		var known bool
		for _, real := range core.AllKinds {
			if string(real) == k {
				known = true
			}
		}
		if !known {
			t.Errorf("kinds prints %q and the core does not know it", k)
		}
	}
}

// An operator flag that creates a database is almost always the wrong
// database. -db defaults to comms.db relative to the working directory, so
// running -invite from the wrong place mints a real token into a file no hub
// has opened — and the only symptom is "unknown enrolment token" much later,
// pointing at the token. That cost two sessions in one day.
func TestOperatorActionsRefuseANeverServedDatabase(t *testing.T) {
	src := mustRead(t, "../main.go")

	if !strings.Contains(src, "it has no rooms, so no hub has ever") {
		t.Error("operator actions must refuse a database nothing has served")
	}
	// The guard only works if ensuring rooms is a serving concern. Doing it for
	// every invocation is what erased the evidence in the first place.
	guard := strings.Index(src, "refusing to act on")
	ensure := strings.Index(src, "if err := st.EnsureRoom(r)")
	if guard == -1 || ensure == -1 {
		t.Fatal("expected both the guard and the room-ensuring loop")
	}
	if ensure < guard {
		t.Error("rooms are ensured before the guard runs, so every database looks served " +
			"and the guard can never fire")
	}
}
