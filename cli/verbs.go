package cli

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
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
	case "read":
		return runRead(e, args[1:])
	case "inbox":
		return runInbox(e, args[1:])
	case "ask":
		return runAsk(e, args[1:])
	case "answer":
		return runAnswer(e, args[1:])
	case "attach":
		return runAttach(e, args[1:])
	case "room":
		return runRoom(e, args[1:])
	case "search":
		return e.Out.Fail(ExitUsage, "usage", "verb.not_built",
			args[0]+" is designed but not built yet; see docs/CLI.md and .scratch/core/issues/")
	case "-h", "--help", "help":
		return usage(e)
	}
	return e.Out.Fail(ExitUsage, "usage", "verb.unknown",
		"no verb "+args[0]+"; known verbs: "+strings.Join(Verbs, ", "))
}

func usage(e *Env) int {
	e.Out.Help("agent_comms <verb> [flags]\n\nverbs: %s\n\nEvery verb answers --help. Start with: agent_comms enrol --help",
		strings.Join(Verbs, ", "))
	e.Out.Line(Result{OK: true, Outcome: "usage"})
	return ExitOK
}

// newFlags builds a flag set that reports errors through our contract rather
// than printing to stderr and exiting.
// isHelp reports whether a parse failed only because help was asked for.
// Asking for help is not a usage error.
func isHelp(err error) bool { return errors.Is(err, flag.ErrHelp) }

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
		e.Out.Help(`agent_comms enrol --as <seat>

Enrols one seat. The invite token is read from stdin, never a flag: argv is
visible to every process on the machine and lands in shell history.

  agent_comms -invite agent:bcm/claude-1      # a human runs this, gets a token
  echo "<token>" | agent_comms enrol --as agent:bcm/claude-1

The private key is written 0600 under %s and is never printed.`, KeyDir())
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return e.Out.Succeed(Result{Outcome: "usage"})
		}
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
	room := fs.String("room", "", "room to post in")
	text := fs.String("text", "", "the entry")
	severity := fs.String("severity", "", "p0|p1|p2|p3, findings only")
	url := fs.String("url", "", "pr.link only")
	to := fs.String("to", "", "recipient, addressed kinds only")
	refs := fs.String("refs", "", "comma-separated refs")
	step := fs.Int("step", 0, "status only")
	of := fs.Int("of", 0, "status only")
	textFile := fs.String("text-file", "", "read the entry from a file")
	attachPath := multiFlag{}
	fs.Var(&attachPath, "attach", "upload a file and attach it (repeatable; - reads stdin)")
	attachHash := multiFlag{}
	fs.Var(&attachHash, "attach-hash", "attach an already-uploaded hash (repeatable)")
	attachTitle := multiFlag{}
	fs.Var(&attachTitle, "attach-title", "title for each attachment, in order (repeatable)")
	dryRun := fs.Bool("dry-run", false, "print the exact bytes and signature without sending")
	fs.Usage = func() {
		e.Out.Help(`agent_comms post <kind> --as <seat> [flags]

kinds: %s

  agent_comms post finding --as agent:bcm/claude-1 --severity p2 \
      --text "auth.py:88 flakes under -race"

The entry can come from anywhere quoting is easier: --text "…", --text-file
PATH, or --text - to read stdin. Long content belongs in an artifact instead:
--attach PATH uploads and references in one command, --attach-hash takes what
agent_comms attach printed, and --attach-title names them in the same order.

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
	inRoom := resolveRoom(seat, *room)
	priv, err := LoadSeat(seat)
	if err != nil {
		return e.Out.Fail(ExitUsage, "usage", "seat.not_enrolled", err.Error())
	}

	entry, code := resolveText(e, *text, *textFile, "")
	if code != 0 {
		return code
	}

	// Attachments: uploaded paths first, then hashes already stored. Titles
	// pair by position, and a mismatched count is refused rather than zipped
	// silently — a wrong title on a report is worse than no title.
	var atts []map[string]any
	for _, path := range attachPath {
		if path == "-" {
			if c := claimStdinForAttach(e); c != 0 {
				return c
			}
		}
		content, err := readContent(e, path)
		if err != nil {
			return e.Out.Fail(ExitUsage, "usage", "attachment.unreadable", err.Error())
		}
		h, _, err := uploadArtifact(e, content)
		if err != nil {
			return e.Out.Fail(ExitRefused, "refused", "artifact.rejected", err.Error())
		}
		atts = append(atts, map[string]any{"hash": h, "title": defaultTitle(path)})
	}
	for _, h := range attachHash {
		atts = append(atts, map[string]any{"hash": h, "title": ""})
	}
	if len(attachTitle) > 0 {
		if len(attachTitle) != len(atts) {
			return e.Out.Fail(ExitUsage, "usage", "attachment.title_count",
				fmt.Sprintf("%d --attach-title for %d attachment(s); they pair by position, "+
					"and zipping a mismatch silently would mislabel a report",
					len(attachTitle), len(atts)))
		}
		for i := range atts {
			atts[i]["title"] = attachTitle[i]
		}
	}
	for i, a := range atts {
		if a["title"] == "" {
			atts[i]["title"] = fmt.Sprintf("attachment-%d.md", i+1)
		}
	}

	// A nudge, never a refusal: domain policy lives in the core, and a client
	// that refused long text would be inventing a rule the server does not have.
	if isLongForm(entry) {
		e.Out.Advise("long-entry", "that entry is long; consider --attach so the row stays "+
			"a row and the content stays searchable as an artifact")
	}

	body := map[string]any{}
	if entry != "" {
		body["text"] = entry
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
		"room": inRoom, "author": seat, "kind": string(kind),
		"body": body, "idem": newIdem(),
	}
	if len(atts) > 0 {
		cmd["attachments"] = atts
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
	room := fs.String("room", "", "the room the event is in")
	why := fs.String("why", "", "why it is being suppressed")
	fs.Usage = func() {
		e.Out.Help(`agent_comms redact <seq> --as <seat> --why "<reason>"

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
	inRoom := resolveRoom(seat, *room)
	priv, err := LoadSeat(seat)
	if err != nil {
		return e.Out.Fail(ExitUsage, "usage", "seat.not_enrolled", err.Error())
	}

	c := NewClient(e.Server, seat, priv)
	sent, err := c.Post(map[string]any{
		"room": inRoom, "author": seat, "kind": "redact",
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

// ---------------------------------------------------------------- ask

func runAsk(e *Env, args []string) int {
	fs, sink := newFlags("ask")
	actor := fs.String("as", "", "the seat asking")
	room := fs.String("room", "", "room to ask in")
	to := fs.String("to", "", "who to ask")
	text := fs.String("text", "", "the question, or - to read stdin")
	textFile := fs.String("text-file", "", "read the question from a file")
	noSearch := fs.Bool("no-search", false, "skip the search for prior answers")
	fs.Usage = func() {
		e.Out.Help(`agent_comms ask --as <seat> --to <who> --text "<question>"

Searches the room for what it already knows, attaches up to three hits to the
question's refs, prints what it attached, and posts either way.

  agent_comms ask --as agent:bcm/claude-1 --to bcm \
      --text "is migration 0031 safe to reorder ahead of 0029?"

It attaches, it never gates: a human seeing the prior hits alongside the
question can tell in a glance whether it is new.`)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return e.Out.Succeed(Result{Outcome: "usage"})
		}
		return e.Out.Fail(ExitUsage, "usage", "flags.invalid", strings.TrimSpace(sink.String()))
	}

	question, code := resolveText(e, *text, *textFile, "")
	if code != 0 {
		return code
	}
	if question == "" {
		return e.Out.Fail(ExitUsage, "usage", "text.required",
			"ask what you want to know with --text, --text-file, or --text -")
	}
	if *to == "" {
		return e.Out.Fail(ExitUsage, "usage", "recipient.required",
			"name who you are asking with --to")
	}
	seat, code := resolveSeat(e, *actor)
	if code != 0 {
		return code
	}
	inRoom := resolveRoom(seat, *room)
	priv, err := LoadSeat(seat)
	if err != nil {
		return e.Out.Fail(ExitUsage, "usage", "seat.not_enrolled", err.Error())
	}

	var refs []string
	if !*noSearch {
		terms := distinctiveTerms(question)
		if len(terms) > 0 {
			for _, h := range searchFor(e, inRoom, strings.Join(terms, " "), 3) {
				refs = append(refs, fmt.Sprint(h.Seq))
				preview, _ := h.Body["text"].(string)
				e.Out.Line(map[string]any{
					"type": "attached", "seq": h.Seq, "kind": h.Kind,
					"preview": truncateText(preview, 100),
					"why":     "the room already contains this; it is attached to your question",
				})
			}
			e.Out.Note("searched %q, attached %d prior event(s)", strings.Join(terms, " "), len(refs))
		}
	}

	cmd := map[string]any{
		"room": inRoom, "author": seat, "kind": "question",
		"body":      map[string]any{"text": question},
		"recipient": *to, "idem": newIdem(),
	}
	if len(refs) > 0 {
		cmd["refs"] = refs
	}
	return send(e, NewClient(e.Server, seat, priv), cmd, "question", nil)
}

// ---------------------------------------------------------------- answer

func runAnswer(e *Env, args []string) int {
	fs, sink := newFlags("answer")
	actor := fs.String("as", "", "the seat answering")
	room := fs.String("room", "", "room the question is in")
	toQuestion := fs.String("to-question", "", "the seq of the question you are answering")
	text := fs.String("text", "", "the answer, or - to read stdin")
	textFile := fs.String("text-file", "", "read the answer from a file")
	fs.Usage = func() {
		e.Out.Help(`agent_comms answer --as <seat> --to-question <seq> --text "<answer>"

No recipient: the server derives it from the question's author, so an answer
always reaches whoever asked.

  agent_comms answer --as bcm --to-question 20014 --text "yes, 0029 is idempotent"`)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return e.Out.Succeed(Result{Outcome: "usage"})
		}
		return e.Out.Fail(ExitUsage, "usage", "flags.invalid", strings.TrimSpace(sink.String()))
	}
	if *toQuestion == "" {
		return e.Out.Fail(ExitUsage, "usage", "to_question.required",
			"name the question you are answering with --to-question <seq>")
	}
	body, code := resolveText(e, *text, *textFile, "")
	if code != 0 {
		return code
	}
	if body == "" {
		return e.Out.Fail(ExitUsage, "usage", "text.required",
			"say the answer with --text, --text-file, or --text -")
	}
	seat, code := resolveSeat(e, *actor)
	if code != 0 {
		return code
	}
	inRoom := resolveRoom(seat, *room)
	priv, err := LoadSeat(seat)
	if err != nil {
		return e.Out.Fail(ExitUsage, "usage", "seat.not_enrolled", err.Error())
	}

	// No recipient is sent: the core derives it from the question's author.
	return send(e, NewClient(e.Server, seat, priv), map[string]any{
		"room": inRoom, "author": seat, "kind": "answer",
		"body": map[string]any{"text": body},
		"refs": []string{*toQuestion}, "idem": newIdem(),
	}, "answer", nil)
}

// ---------------------------------------------------------------- attach

func runAttach(e *Env, args []string) int {
	fs, sink := newFlags("attach")
	fs.Usage = func() {
		e.Out.Help(`agent_comms attach <path|->

Uploads markdown and prints its hash. post --attach-hash accepts the hash, so a
rejected post does not mean re-running a three-minute test to reproduce stdin
you already consumed.

  HASH=$(go test ./... 2>&1 | agent_comms attach - | jq -r .hash)
  agent_comms post finding --as <seat> --severity p2 \
      --text "suite red" --attach-hash "$HASH" --attach-title suite.md`)
	}
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fs.Usage()
		if len(args) == 0 {
			return e.Out.Fail(ExitUsage, "usage", "path.required",
				"name a file to upload, or - to read stdin")
		}
		return e.Out.Succeed(Result{Outcome: "usage"})
	}
	if err := fs.Parse(args[1:]); err != nil {
		return e.Out.Fail(ExitUsage, "usage", "flags.invalid", strings.TrimSpace(sink.String()))
	}

	content, err := readContent(e, args[0])
	if err != nil {
		return e.Out.Fail(ExitUsage, "usage", "content.unreadable", err.Error())
	}
	if len(content) == 0 {
		return e.Out.Fail(ExitUsage, "usage", "content.empty", "there is nothing to upload")
	}

	hash, size, err := uploadArtifact(e, content)
	if err != nil {
		return e.Out.Fail(ExitRefused, "refused", "artifact.rejected", err.Error())
	}
	e.Out.Note("stored %d bytes as %s", size, hash[:12])
	return e.Out.Succeed(Result{Outcome: "stored", Hash: hash, Size: size})
}

// ---------------------------------------------------------------- read / inbox

func runRead(e *Env, args []string) int {
	fs, sink := newFlags("read")
	actor := fs.String("as", "", "the seat reading")
	room := fs.String("room", "", "room to read")
	full := fs.Bool("full", false, "print whole bodies rather than one line per event")
	peek := fs.Bool("peek", false, "do not advance the cursor")
	kind := fs.String("kind", "", "only this kind (implies --peek)")
	author := fs.String("author", "", "only this author (implies --peek)")
	reset := fs.Bool("reset", false, "rewind this lane's cursor and read from the start")
	fs.Usage = func() {
		e.Out.Help(`agent_comms read --as <seat> [--room core]

Prints what is new since you last read, then exits. It does not hang: a quiet
room returns count 0 in one round trip.

  agent_comms read --as agent:bcm/claude-1
  agent_comms read --as agent:bcm/claude-1 --full        # whole bodies
  agent_comms read --as agent:bcm/claude-1 --kind finding  # filtered, does not advance

read and inbox keep separate cursors, so draining one never hides the other.`)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return e.Out.Succeed(Result{Outcome: "usage"})
		}
		return e.Out.Fail(ExitUsage, "usage", "flags.invalid", strings.TrimSpace(sink.String()))
	}
	seat, code := resolveSeat(e, *actor)
	if code != 0 {
		return code
	}
	inRoom := resolveRoom(seat, *room)
	if *reset {
		if err := ResetCursor(seat, inRoom, LaneAll); err != nil {
			return e.Out.Fail(ExitInternal, "internal", "cursor.unwritable", err.Error())
		}
	}

	o := readOpts{
		Actor: seat, Room: inRoom, Lane: LaneAll,
		Kind: *kind, Author: *author, Full: *full,
		// A filter means the read did not see everything, so it must not claim
		// the cursor did.
		Peek: *peek || *kind != "" || *author != "",
	}
	events, meta, err := drain(e, o)
	if err != nil {
		return e.Out.Fail(ExitRefused, "refused", "read.failed", err.Error())
	}
	return emit(e, o, events, meta)
}

func runInbox(e *Env, args []string) int {
	fs, sink := newFlags("inbox")
	actor := fs.String("as", "", "the seat reading")
	room := fs.String("room", "", "room to read")
	full := fs.Bool("full", false, "print whole bodies")
	peek := fs.Bool("peek", false, "do not advance the cursor")
	wait := fs.Duration("wait", 0, "block until something arrives, or this elapses (max 30m)")
	untilKind := fs.String("until-kind", "", "with --wait, stop when this kind arrives")
	untilRefs := fs.String("refs", "", "with --wait, stop when an event references this seq")
	fs.Usage = func() {
		e.Out.Help(`agent_comms inbox --as <seat> [--wait 15m --until-kind answer --refs <seq>]

Prints only what is addressed to you, then exits.

  agent_comms inbox --as agent:bcm/claude-1
  agent_comms inbox --as agent:bcm/claude-1 --wait 15m --until-kind answer --refs 20014

--wait blocks against a deadline and exits 0 either way. Waiting out the clock
is the flag doing its job, not a failure — you get a handoff suggestion.`)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return e.Out.Succeed(Result{Outcome: "usage"})
		}
		return e.Out.Fail(ExitUsage, "usage", "flags.invalid", strings.TrimSpace(sink.String()))
	}
	if *wait > maxWait {
		return e.Out.Fail(ExitUsage, "usage", "wait.too_long",
			fmt.Sprintf("--wait is capped at %s so a stuck agent surfaces; got %s", maxWait, *wait))
	}
	seat, code := resolveSeat(e, *actor)
	if code != 0 {
		return code
	}
	inRoom := resolveRoom(seat, *room)

	o := readOpts{
		Actor: seat, Room: inRoom, Lane: LaneAddressed,
		Recipient: seat, Full: *full, Peek: *peek,
		Wait: *wait, UntilKind: *untilKind, UntilRefs: *untilRefs,
	}
	events, meta, err := drain(e, o)
	if err != nil {
		// A drop mid-wait must leave the cursor where it was, so nothing is
		// skipped on the next read.
		return e.Out.Fail(ExitRefused, "refused", "read.failed", err.Error())
	}

	if *wait > 0 && len(events) == 0 {
		// Waiting out the clock is the flag working. Exit 0, and say what to
		// do instead of waiting again.
		e.Out.Line(map[string]any{
			"ok": true, "outcome": "waited", "count": 0, "room": inRoom,
			"waited": wait.String(),
			"next": "nobody answered; hand off with: agent_comms post handoff --to <human> " +
				"--text \"blocked on " + orDefault(*untilRefs, "an unanswered question") + "\"",
		})
		e.Out.Note("waited %s, nothing arrived", *wait)
		return ExitOK
	}
	return emit(e, o, events, meta)
}

// ---------------------------------------------------------------- whoami

func runWhoami(e *Env, args []string) int {
	fs, _ := newFlags("whoami")
	actor := fs.String("as", "", "the seat to report on")
	fs.Usage = func() {
		e.Out.Help(`agent_comms whoami [--as <seat>]

Reports the seat, host, server and public key. It never reports the private
key, and no verb, flag or environment variable does.

  agent_comms whoami --as agent:bcm/claude-1`)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return e.Out.Succeed(Result{Outcome: "usage"})
		}
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

	room := SelectedRoom(seat)
	if room == "" {
		room = "core"
	}
	cursors := map[string]int64{}
	for _, lane := range []Lane{LaneAll, LaneAddressed} {
		cursors[string(lane)] = Cursor(seat, room, lane)
	}
	status := "unknown"
	if body, code, err := fetchJSON(e, "/actors"); err == nil && code == http.StatusOK {
		for _, a := range asList(body["actors"]) {
			if m, ok := a.(map[string]any); ok && m["actor"] == seat {
				status = str(m["key_status"], "unknown")
			}
		}
	}

	e.Out.Note("%s on %s → %s (room %s, key %s)", seat, e.Host, e.Server, room, status)
	return e.Out.Succeed(Result{
		Outcome: "whoami", Actor: seat, Host: e.Host,
		Server: e.Server, PubKey: hex.EncodeToString(pub),
		Room: room, KeyStatus: status, Cursors: cursors,
	})
}

// ---------------------------------------------------------------- escalate

func runEscalate(e *Env, args []string) int {
	// --help is answered before the refusal: an agent reading the flags has not
	// escalated yet, and refusing the question teaches nothing.
	for _, a := range args {
		if a == "-h" || a == "--help" || a == "-help" {
			e.Out.Help(`agent_comms escalate --as <seat> --to <human> --text <why>

Designed, not built (ticket 05). Escalation spends a budget and moves an entry
into the addressed lane; until the budget exists the verb refuses rather than
posting an interruption nothing accounts for.`)
			return e.Out.Succeed(Result{Outcome: "usage"})
		}
	}
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
