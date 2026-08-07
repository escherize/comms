package cli

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/bcm/agent_comms/core"
)

// Env is everything a verb needs, injected so tests drive the real code paths
// without touching process globals.
type Env struct {
	Out    *Out
	Stdin  io.Reader
	Server string
	Host   string
	// LookupEnv is os.LookupEnv in production; tests substitute it.
	LookupEnv func(string) (string, bool)
}

func (e *Env) getenv(k string) (string, bool) {
	if e.LookupEnv != nil {
		return e.LookupEnv(k)
	}
	return os.LookupEnv(k)
}

// Verbs the binary answers, in help order.
var Verbs = []string{"enrol", "post", "redact", "ask", "answer", "attach", "read", "inbox", "search", "room", "whoami", "escalate"}

// Run dispatches one verb. It returns the process exit code and never calls
// os.Exit, so a test can assert on it.
func Run(e *Env, args []string) int {
	if len(args) == 0 {
		return usage(e)
	}
	switch args[0] {
	case "enrol":
		return runEnrol(e, args[1:])
	case "post":
		return runPost(e, args[1:])
	case "whoami":
		return runWhoami(e, args[1:])
	case "redact":
		return runRedact(e, args[1:])
	case "escalate":
		return runEscalate(e, args[1:])
	case "ask", "answer", "attach", "read", "inbox", "search", "room":
		return e.Out.Fail(ExitUsage, "usage", "verb.not_built",
			args[0]+" is designed but not built yet; see docs/CLI.md and .scratch/core/issues/")
	case "-h", "--help", "help":
		return usage(e)
	}
	return e.Out.Fail(ExitUsage, "usage", "verb.unknown",
		"no verb "+args[0]+"; known verbs: "+strings.Join(Verbs, ", "))
}

func usage(e *Env) int {
	e.Out.Note("agent_comms <verb> [flags]\n\nverbs: %s\n\nEvery verb answers --help. Start with: agent_comms enrol --help",
		strings.Join(Verbs, ", "))
	e.Out.Line(Result{OK: true, Outcome: "usage"})
	return ExitOK
}

// newFlags builds a flag set that reports errors through our contract rather
// than printing to stderr and exiting.
func newFlags(name string) (*flag.FlagSet, *strings.Builder) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	var sink strings.Builder
	fs.SetOutput(&sink)
	return fs, &sink
}

// ---------------------------------------------------------------- enrol

func runEnrol(e *Env, args []string) int {
	fs, sink := newFlags("enrol")
	actor := fs.String("as", "", "the seat to enrol, e.g. agent:bcm/claude-1")
	token := fs.String("token", "", "REFUSED: pipe the token on stdin instead")
	fs.Usage = func() {
		e.Out.Note(`agent_comms enrol --as <seat>

Enrols one seat. The invite token is read from stdin, never a flag: argv is
visible to every process on the machine and lands in shell history.

  agent_comms -invite agent:bcm/claude-1      # a human runs this, gets a token
  echo "<token>" | agent_comms enrol --as agent:bcm/claude-1

The private key is written 0600 under %s and is never printed.`, KeyDir())
	}
	if err := fs.Parse(args); err != nil {
		return e.Out.Fail(ExitUsage, "usage", "flags.invalid", strings.TrimSpace(sink.String()))
	}

	// A token on argv is visible in `ps` and in shell history. Refusing it is
	// cheaper than explaining why it leaked.
	if *token != "" {
		return e.Out.Fail(ExitUsage, "usage", "token.on_argv",
			"a token passed as a flag is visible in ps and shell history; pipe it on stdin instead")
	}
	if _, set := e.getenv("AGENT_COMMS_KEY"); set {
		return e.Out.Fail(ExitUsage, "usage", "key.on_env",
			"AGENT_COMMS_KEY is set; this client never reads a key from the environment, and one there is already exposed to every child process")
	}
	if *actor == "" {
		return e.Out.Fail(ExitUsage, "usage", "actor.required",
			"name the seat with --as, e.g. --as agent:bcm/claude-1")
	}

	tok, err := readToken(e.Stdin)
	if err != nil {
		return e.Out.Fail(ExitUsage, "usage", "token.required",
			"pipe the invite token on stdin: echo \"<token>\" | agent_comms enrol --as "+*actor)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return e.Out.Fail(ExitInternal, "internal", "keygen.failed", err.Error())
	}

	c := NewClient(e.Server, *actor, priv)
	status, resp, err := c.Enrol(e.Server, *actor, tok, pub)
	if err != nil {
		return e.Out.Fail(ExitRefused, "refused", "server.unreachable", err.Error())
	}
	if status != 200 {
		return e.Out.FailWith(Result{
			Outcome: "refused", Exit: ExitRefused,
			Invariant: orDefault(resp.Invariant, "enrolment.refused"),
			Detail:    orDefault(resp.Detail, "the server refused this enrolment"),
		})
	}

	if err := SaveSeat(*actor, priv); err != nil {
		return e.Out.Fail(ExitInternal, "internal", "key.unwritable", err.Error())
	}

	e.Out.Note("enrolled %s; key written to %s (never printed)", *actor, KeyDir())
	return e.Out.Succeed(Result{
		Outcome: "enrolled", Actor: *actor, Host: e.Host,
		Server: e.Server, PubKey: hex.EncodeToString(pub),
	})
}

// readToken takes the first non-empty line of stdin.
func readToken(r io.Reader) (string, error) {
	if r == nil {
		return "", fmt.Errorf("no stdin")
	}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		if t := strings.TrimSpace(sc.Text()); t != "" {
			return t, nil
		}
	}
	return "", fmt.Errorf("no token on stdin")
}

// ---------------------------------------------------------------- post

func runPost(e *Env, args []string) int {
	fs, sink := newFlags("post")
	actor := fs.String("as", "", "the seat posting")
	room := fs.String("room", "core", "room to post in")
	text := fs.String("text", "", "the entry")
	severity := fs.String("severity", "", "p0|p1|p2|p3, findings only")
	url := fs.String("url", "", "pr.link only")
	to := fs.String("to", "", "recipient, addressed kinds only")
	refs := fs.String("refs", "", "comma-separated refs")
	step := fs.Int("step", 0, "status only")
	of := fs.Int("of", 0, "status only")
	dryRun := fs.Bool("dry-run", false, "print the exact bytes and signature without sending")
	fs.Usage = func() {
		e.Out.Note(`agent_comms post <kind> --as <seat> [flags]

kinds: %s

  agent_comms post finding --as agent:bcm/claude-1 --severity p2 \
      --text "auth.py:88 flakes under -race"

The client does not validate the domain. A missing --severity is sent and comes
back naming the invariant and the schema, which is how you learn the rule.`,
			strings.Join(knownKindNames(), ", "))
	}

	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
			fs.Usage()
			return e.Out.Succeed(Result{Outcome: "usage"})
		}
		return e.Out.Fail(ExitUsage, "usage", "kind.required",
			"name the kind first: agent_comms post <"+strings.Join(knownKindNames(), "|")+">")
	}
	kind := core.Kind(args[0])
	if err := fs.Parse(args[1:]); err != nil {
		return e.Out.Fail(ExitUsage, "usage", "flags.invalid", strings.TrimSpace(sink.String()))
	}

	// The verb set never outruns the kind set. A verb whose event kind does not
	// exist would write a lie into permanent storage, and the log is
	// append-only, so there is no later repair.
	if !knownKind(kind) {
		return e.Out.Fail(ExitUsage, "usage", "kind.unknown",
			"no kind "+string(kind)+"; known kinds: "+strings.Join(knownKindNames(), ", "))
	}

	seat, code := resolveSeat(e, *actor)
	if code != 0 {
		return code
	}
	priv, err := LoadSeat(seat)
	if err != nil {
		return e.Out.Fail(ExitUsage, "usage", "seat.not_enrolled", err.Error())
	}

	body := map[string]any{}
	if *text != "" {
		body["text"] = *text
	}
	if *severity != "" {
		body["severity"] = *severity
	}
	if *url != "" {
		body["url"] = *url
	}
	if *step > 0 {
		body["step"] = *step
	}
	if *of > 0 {
		body["of"] = *of
	}

	cmd := map[string]any{
		"room": *room, "author": seat, "kind": string(kind),
		"body": body, "idem": newIdem(),
	}
	if *to != "" {
		cmd["recipient"] = *to
	}
	if *refs != "" {
		cmd["refs"] = strings.Split(*refs, ",")
	}

	c := NewClient(e.Server, seat, priv)

	if *dryRun {
		// The escape hatch that would otherwise be a `sign` verb. It signs only
		// a command this client built, so it cannot be turned into a signing
		// oracle for arbitrary bytes.
		payload, sig, err := c.Preview(cmd)
		if err != nil {
			return e.Out.Fail(ExitInternal, "internal", "build.failed", err.Error())
		}
		e.Out.Line(map[string]any{"type": "event", "bytes": string(payload), "signature": sig})
		return e.Out.Succeed(Result{Outcome: "dry-run"})
	}

	sent, err := c.Post(cmd)
	if err != nil {
		return e.Out.Fail(ExitSpooled, "spooled", "transport.failed", err.Error())
	}

	exit, outcome := statusToExit(sent.Status, sent.Body.Invariant)
	if exit != ExitOK {
		return e.Out.FailWith(Result{
			Outcome: outcome, Exit: exit,
			Invariant: sent.Body.Invariant, Detail: sent.Body.Detail,
			Schema: sent.Body.Schema,
			Retry:  retryFor(sent.Body.Invariant, kind, args),
		})
	}

	applied := sent.Body.Applied
	if applied {
		e.Out.Note("posted %s at %d", kind, sent.Body.Seq)
	} else {
		e.Out.Note("replayed %s at %d (already in the log)", kind, sent.Body.Seq)
	}
	outcomeName := "accepted"
	if !applied {
		outcomeName = "replayed"
	}
	return e.Out.Succeed(Result{Outcome: outcomeName, Seq: sent.Body.Seq, Applied: &applied})
}

// retryFor renders a corrected invocation that works when run verbatim. An
// agent that is told what is wrong and not how to fix it spends a turn guessing.
func retryFor(invariant string, kind core.Kind, args []string) string {
	base := "agent_comms post " + string(kind) + " " + strings.Join(args[1:], " ")
	switch invariant {
	case "body.severity.invalid":
		return base + " --severity p2"
	case "body.text.required":
		return base + ` --text "<what you found>"`
	case "recipient.required":
		return base + " --to <someone>"
	case "recipient.forbidden":
		return strings.ReplaceAll(base, "--to ", "")
	case "body.url.required":
		return base + " --url https://…"
	}
	return ""
}

// ---------------------------------------------------------------- redact

func runRedact(e *Env, args []string) int {
	fs, sink := newFlags("redact")
	actor := fs.String("as", "", "the seat redacting")
	room := fs.String("room", "core", "the room the event is in")
	why := fs.String("why", "", "why it is being suppressed")
	fs.Usage = func() {
		e.Out.Note(`agent_comms redact <seq> --as <seat> --why "<reason>"

Suppresses one of your own events: its body leaves the room, search, and any
attached artifact stops being served. The event itself stays, because
corrections are new entries.

  agent_comms redact 20014 --as agent:bcm/claude-1 --why "pasted a token"

The seq is positional, not a --refs string, so the refs value you are carrying
through a piece of work cannot land here by habit. You can only redact your own
event; someone else's is an operator action.`)
	}

	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
			fs.Usage()
			return e.Out.Succeed(Result{Outcome: "usage"})
		}
		return e.Out.Fail(ExitUsage, "usage", "seq.required",
			"name the event to redact: agent_comms redact <seq> --as <seat> --why \"...\"")
	}
	seqArg := args[0]
	if _, err := strconv.ParseInt(seqArg, 10, 64); err != nil {
		return e.Out.Fail(ExitUsage, "usage", "seq.invalid",
			"the first argument is a seq, e.g. 20014; got "+seqArg)
	}
	if err := fs.Parse(args[1:]); err != nil {
		return e.Out.Fail(ExitUsage, "usage", "flags.invalid", strings.TrimSpace(sink.String()))
	}
	if *why == "" {
		return e.Out.Fail(ExitUsage, "usage", "why.required",
			"say why with --why; the room keeps the reason even though the body goes")
	}

	seat, code := resolveSeat(e, *actor)
	if code != 0 {
		return code
	}
	priv, err := LoadSeat(seat)
	if err != nil {
		return e.Out.Fail(ExitUsage, "usage", "seat.not_enrolled", err.Error())
	}

	c := NewClient(e.Server, seat, priv)
	sent, err := c.Post(map[string]any{
		"room": *room, "author": seat, "kind": "redact",
		"body": map[string]any{"text": *why},
		"refs": []string{seqArg}, "idem": newIdem(),
	})
	if err != nil {
		return e.Out.Fail(ExitSpooled, "spooled", "transport.failed", err.Error())
	}

	exit, outcome := statusToExit(sent.Status, sent.Body.Invariant)
	if exit != ExitOK {
		return e.Out.FailWith(Result{
			Outcome: outcome, Exit: exit,
			Invariant: sent.Body.Invariant, Detail: sent.Body.Detail, Schema: sent.Body.Schema,
		})
	}
	e.Out.Note("redacted %s; body and attachments are gone, the event remains", seqArg)
	applied := sent.Body.Applied
	return e.Out.Succeed(Result{Outcome: "accepted", Seq: sent.Body.Seq, Applied: &applied})
}

// ---------------------------------------------------------------- whoami

func runWhoami(e *Env, args []string) int {
	fs, _ := newFlags("whoami")
	actor := fs.String("as", "", "the seat to report on")
	fs.Usage = func() {
		e.Out.Note(`agent_comms whoami [--as <seat>]

Reports the seat, host, server and public key. It never reports the private
key, and no verb, flag or environment variable does.

  agent_comms whoami --as agent:bcm/claude-1`)
	}
	if err := fs.Parse(args); err != nil {
		return e.Out.Fail(ExitUsage, "usage", "flags.invalid", err.Error())
	}
	seat, code := resolveSeat(e, *actor)
	if code != 0 {
		return code
	}
	if !HasSeat(seat) {
		return e.Out.Fail(ExitUsage, "usage", "seat.not_enrolled",
			"no key for "+seat+"; run: agent_comms enrol --as "+seat)
	}
	priv, err := LoadSeat(seat)
	if err != nil {
		return e.Out.Fail(ExitUsage, "usage", "seat.not_enrolled", err.Error())
	}
	pub := priv.Public().(ed25519.PublicKey)

	e.Out.Note("%s on %s → %s", seat, e.Host, e.Server)
	return e.Out.Succeed(Result{
		Outcome: "whoami", Actor: seat, Host: e.Host,
		Server: e.Server, PubKey: hex.EncodeToString(pub),
	})
}

// ---------------------------------------------------------------- escalate

func runEscalate(e *Env, args []string) int {
	// The verb exists so an agent that reaches for it gets a straight answer
	// rather than silence, and posts nothing while doing so.
	return e.Out.FailWith(Result{
		Outcome: "refused", Exit: ExitRefused,
		Invariant: "escalation.not_built",
		Detail: "escalation budgets are designed but not built (ticket 05). " +
			"Nothing was posted. To interrupt a human now, ask them: agent_comms ask --to <human>",
		Next: "stop escalating; ask a human directly instead",
	})
}

// ---------------------------------------------------------------- helpers

func resolveSeat(e *Env, flagValue string) (string, int) {
	if flagValue != "" {
		return flagValue, 0
	}
	if v, ok := e.getenv("AGENT_COMMS_ACTOR"); ok && v != "" {
		return v, 0
	}
	return "", e.Out.Fail(ExitUsage, "usage", "actor.required",
		"name the seat with --as, or set AGENT_COMMS_ACTOR")
}

func newIdem() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func knownKind(k core.Kind) bool {
	for _, n := range knownKindNames() {
		if string(k) == n {
			return true
		}
	}
	return false
}

func knownKindNames() []string {
	return []string{"chat", "finding", "question", "answer", "til",
		"handoff", "status", "pr.link", "digest", "redact"}
}
