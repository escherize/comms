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
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/escherize/comms/core"
)

// Env is everything a verb needs, injected so tests drive the real code paths
// without touching process globals.
type Env struct {
	Out    *Out
	Stdin  io.Reader
	Server string
	Host   string
	// Seat is the acting actor once a verb has resolved --as. doRead uses it
	// to carry and, when a hub demands one, establish a read session.
	Seat string
	// LookupEnv is os.LookupEnv in production; tests substitute it.
	LookupEnv func(string) (string, bool)
}

func (e *Env) getenv(k string) (string, bool) {
	look := os.LookupEnv
	if e.LookupEnv != nil {
		look = e.LookupEnv
	}
	return look(k)
}

// Verbs the binary answers, in help order.
var Verbs = []string{"serve", "kinds", "invite", "enrol", "post", "redact", "ask", "answer", "attach", "decline", "read", "inbox", "watch", "search", "room", "whoami", "escalate", "skill", "skills", "hook"}

// Run dispatches one verb. It returns the process exit code and never calls
// os.Exit, so a test can assert on it.
func Run(e *Env, args []string) int {
	if len(args) == 0 {
		return usage(e)
	}
	// --server points a client verb at a non-default hub. It is pulled out here,
	// before the verb dispatch, so every verb honours it without each declaring
	// its own flag — and so `comms invite --server http://host:port` works, which
	// is what a hub served on a non-default --addr needs. COMMS_SERVER still sets
	// the default; the flag wins when both are present. A bare --server with no
	// value is a usage error, not a silent no-op.
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if v, ok := strings.CutPrefix(a, "--server="); ok {
			e.Server = v
			continue
		}
		if v, ok := strings.CutPrefix(a, "-server="); ok {
			e.Server = v
			continue
		}
		if a == "--server" || a == "-server" {
			if i+1 >= len(args) {
				return e.Out.Fail(ExitUsage, "usage", "server.required",
					"--server needs a URL, e.g. --server http://127.0.0.1:7878")
			}
			e.Server = args[i+1]
			i++
			continue
		}
		rest = append(rest, a)
	}
	args = rest
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
	case "decline":
		return runDecline(e, args[1:])
	case "serve":
		// The binary intercepts this before the client sees it. The client
		// still answers --help for it, because a verb in the list that cannot
		// explain itself is a verb that looks broken.
		return runServeHelp(e, args[1:])
	case "kinds":
		return runKinds(e, args[1:])
	case "invite":
		return runInvite(e, args[1:])
	case "watch":
		return runWatch(e, args[1:])
	case "room":
		return runRoom(e, args[1:])
	case "search":
		return runSearch(e, args[1:])
	case "skill":
		return runSkillVerb(e, args[1:])
	case "skills":
		return runSkillsList(e, args[1:])
	case "hook":
		return runHook(e, args[1:])
	case "-h", "--help", "help":
		return usage(e)
	}
	return e.Out.Fail(ExitUsage, "usage", "verb.unknown",
		"no verb "+args[0]+"; known verbs: "+strings.Join(Verbs, ", ")+
			" (this binary: "+buildID()+" — if the docs promise this verb, the installed binary is stale)")
}

// buildID names the binary for skew diagnosis: when a doc or setup banner
// promises a verb this binary lacks, the error must say which build refused,
// or the skew reads as user error.
func buildID() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown build"
	}
	rev, at := "", ""
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.time":
			at = s.Value
		}
	}
	if rev == "" {
		return "unknown build"
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if at != "" {
		return rev + " built " + at
	}
	return rev
}

func usage(e *Env) int {
	e.Out.Help(`comms — a shared room where a team's humans and agents post signed,
typed, permanent entries: findings, questions, handoffs, and what got learned.

usage: comms <verb> [flags]
       comms serve [--db <path>] [--rooms <list>]

join a room
   enrol       register this seat's key against a one-time invite token
   room        select a room and orient; bare form lists rooms and seats
   whoami      which seat you hold, where posts land, how far you have read

say something
   post        one typed entry: finding, til, status, chat, pr.link
   ask         a question, addressed to a person who can answer it
   answer      reply to a question by its seq
   attach      store long content by hash; reference it from a post
   decline     refuse a handoff, out loud
   redact      suppress a body you posted (the record remains)
   escalate    pull an entry into a person's attention — priced, 3/hour

read the room
   read        everything new since you last read, then exit
   inbox       only what is addressed to you, then exit
   watch       hold the addressed lane open, pipe each event to a handler
   search      lexical + semantic, over the room you are in
   kinds       the kind table, printed from the server's own list

run a hub
   serve       the hub itself: http://127.0.0.1:7777, log in ./comms.db
   invite      mint an enrolment token through the running hub

skills
   skill       print or install the skills this binary carries
   skills      list them
   hook        wire the room into an agent harness's turn loop (--install)

'comms <verb> --help' explains any verb; start with enrol.
'comms --h-server' lists the operator flags (verify, rebuild, grants).`)
	return usageOK(e)
}

// usageOK ends a help request. The terminal object is for programs; Help
// already answered the person, and a JSON line after the prose on a terminal
// reads as a bug.
func usageOK(e *Env) int {
	if e.Out.Quiet {
		e.Out.Line(Result{OK: true, Outcome: "usage"})
	}
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
	via := fs.String("via", "", "mint the invite through this seat's local key (it must hold the invite capability)")
	fs.Usage = func() {
		e.Out.HelpFS(fs, `comms enrol --as <seat> [--via <seat>]

Enrols one seat. The invite token is read from stdin, never a flag: argv is
visible to every process on the machine and lands in shell history.

  comms invite agent:bcm/claude-1       # a human runs this, gets a token
  echo "<token>" | comms enrol --as agent:bcm/claude-1

--via mints and redeems in one process, for a session giving itself its own
seat: the via seat's key signs the invite (it must be enrolled here and hold
the invite capability), and no token ever touches a pipe.

  comms enrol --as agent:bcm/claude-s7 --via agent:bcm/claude-1

The private key is written 0600 under %s and is never printed.`, KeyDir())
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return usageOK(e)
		}
		return e.Out.Fail(ExitUsage, "usage", "flags.invalid", strings.TrimSpace(sink.String()))
	}

	// A token on argv is visible in `ps` and in shell history. Refusing it is
	// cheaper than explaining why it leaked.
	if *token != "" {
		return e.Out.Fail(ExitUsage, "usage", "token.on_argv",
			"a token passed as a flag is visible in ps and shell history; pipe it on stdin instead")
	}
	if _, set := e.getenv("COMMS_KEY"); set {
		return e.Out.Fail(ExitUsage, "usage", "key.on_env",
			"COMMS_KEY is set; this client never reads a key from the environment, and one there is already exposed to every child process")
	}
	if *actor == "" {
		return e.Out.Fail(ExitUsage, "usage", "actor.required",
			"name the seat with --as, e.g. --as agent:bcm/claude-1")
	}

	var tok string
	if *via != "" {
		viaPriv, err := LoadSeat(*via)
		if err != nil {
			return e.Out.Fail(ExitRefused, "refused", "via.no_key",
				*via+" holds no key on this machine; --via signs with a local seat")
		}
		// The via seat knows its hub: without this, the documented bare
		// `enrol --as x --via y` dialled the default loopback and dead-ended
		// in every harness whose shell forgot COMMS_SERVER.
		applyPinnedServer(e, *via)
		sent, err := NewClient(e.Server, *via, viaPriv).
			PostTo("/invite", map[string]any{"actor": *actor, "as": *via})
		if err != nil {
			return e.Out.Fail(ExitSpooled, "unreachable", "transport.failed", err.Error())
		}
		if sent.Status != 200 || sent.Body.Token == "" {
			return e.Out.FailWith(Result{
				Outcome: "refused", Exit: ExitRefused,
				Invariant: orDefault(sent.Body.Invariant, "invite.refused"),
				Detail: orDefault(sent.Body.Detail,
					*via+" may not mint invites; grant it with: comms --grant-invite "+*via),
			})
		}
		tok = sent.Body.Token
	} else {
		var err error
		tok, err = readToken(e.Stdin)
		if err != nil {
			return e.Out.Fail(ExitUsage, "usage", "token.required",
				"pipe the invite token on stdin: echo \"<token>\" | comms enrol --as "+*actor)
		}
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return e.Out.Fail(ExitInternal, "internal", "keygen.failed", err.Error())
	}

	c := NewClient(e.Server, *actor, priv)
	status, resp, err := c.Enrol(e.Server, *actor, tok, pub)
	if err != nil {
		// Unreachable is transient: exit 5 says wait and rerun, and the token
		// is still unspent. Exit 4's "never retry" burned tokens' worth of
		// human attention over network blips.
		return e.Out.Fail(ExitSpooled, "unreachable", "transport.failed", err.Error())
	}
	if status != 200 {
		return e.Out.FailWith(Result{
			Outcome: "refused", Exit: ExitRefused,
			Invariant: orDefault(resp.Invariant, "enrolment.refused"),
			Detail:    orDefault(resp.Detail, "the server refused this enrolment"),
		})
	}

	if err := PinServer(*actor, e.Server); err != nil {
		return e.Out.Fail(ExitInternal, "internal", "pin.unwritable", err.Error())
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
	about := fs.String("about", "", "what this concerns: a ticket, a file, a ref")
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
	idem := fs.String("idem", "", "reuse a natural key you already have (see --help)")
	dryRun := fs.Bool("dry-run", false, "print the exact bytes and signature without sending")
	fs.Usage = func() {
		e.Out.HelpFS(fs, `comms post <kind> --as <seat> [flags]

kinds: %s

  comms post finding --as agent:bcm/claude-1 --severity p2 \
      --text "auth.py:88 flakes under -race"

--about names what the entry concerns (a ticket, a file, a ref). It is indexed,
so: search --kind finding "24" finds every finding about ticket 24 rather than
every finding whose prose happens to contain the digits.

--idem reuses a key you already have — a Linear issue id, a CI run id, a task
number. Reach for it when the natural key is better than the content: two
findings with identical text about two different runs are two events, and only
you know that. With no --idem the key is derived from what you are posting, so
re-running the identical command inside one attempt is a replay rather than a
second event.

The entry can come from anywhere quoting is easier: --text "…", --text-file
PATH, or --text - to read stdin. Long content belongs in an artifact instead:
--attach PATH uploads and references in one command, --attach-hash takes what
comms attach printed, and --attach-title names them in the same order.

The client does not validate the domain. A missing --severity is sent and comes
back naming the invariant and the schema, which is how you learn the rule.`,
			strings.Join(knownKindNames(), ", "))
	}

	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
			fs.Usage()
			return usageOK(e)
		}
		return e.Out.Fail(ExitUsage, "usage", "kind.required",
			"name the kind first: comms post <"+strings.Join(knownKindNames(), "|")+">")
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
	if code := CheckServer(e, seat); code != 0 {
		return code
	}
	drainFirst(e, seat)
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
		if err := WithinTree(path); err != nil {
			return e.Out.Fail(ExitUsage, "usage", "attach.outside_tree", err.Error())
		}
		content, err := readContent(e, path)
		if err != nil {
			return e.Out.Fail(ExitUsage, "usage", "attachment.unreadable", err.Error())
		}
		h, _, err := uploadArtifact(e, content)
		if err != nil {
			if errors.Is(err, errUnreachable) {
				return e.Out.Fail(ExitSpooled, "unreachable", "transport.failed", err.Error())
			}
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
	if *about != "" {
		body["about"] = *about
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
		"body": body,
	}
	if len(atts) > 0 {
		cmd["attachments"] = atts
	}
	applyIdem(e, cmd, *idem)
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
		// The signature is never printed. It is a portable, replayable
		// capability over these exact bytes: anything that reads the transcript
		// could post as this seat. The bytes go to a file, and what is printed
		// is a digest, which is enough to compare two runs and useless to
		// replay.
		path := filepath.Join(stateDir(), "dry-run-"+safeName(seat)+".json")
		if err := os.MkdirAll(stateDir(), 0o700); err != nil {
			return e.Out.Fail(ExitInternal, "internal", "dryrun.unwritable", err.Error())
		}
		if err := os.WriteFile(path, payload, 0o600); err != nil {
			return e.Out.Fail(ExitInternal, "internal", "dryrun.unwritable", err.Error())
		}
		e.Out.Line(map[string]any{
			"type": "dry-run", "bytes_path": path, "bytes_len": len(payload),
			"signature_sha256": SignatureDigest(sig),
			"detail": "the signature itself is not printed: it is a replayable " +
				"capability over exactly these bytes",
		})
		return e.Out.Succeed(Result{Outcome: "dry-run"})
	}

	sent, err := c.Post(cmd)
	if err != nil {
		return spoolOrFail(e, c, cmd, sent, err)
	}

	exit, outcome := statusToExit(sent.Status, sent.Body.Invariant)
	if exit != ExitOK {
		if sent.Body.Invariant == "key.revoked" || sent.Body.Invariant == "key.compromised" {
			// A dead seat must not keep a queue of signed bytes that lands the
			// moment somebody re-enrols it.
			DropSpool(seat)
		}
		// The server may tighten the verdict, never loosen it. The local table
		// exists so an unknown future invariant cannot become a retry storm in
		// an unattended run, and that property survives only if a server can
		// turn "retry with a correction" into "stop", but not the reverse.
		if sent.Body.Exit != 0 && stricter(sent.Body.Exit, exit) {
			exit = sent.Body.Exit
			outcome = "refused"
		}
		r := Result{
			Outcome: outcome, Exit: exit,
			Invariant: sent.Body.Invariant, Detail: sent.Body.Detail,
			Schema:       sent.Body.Schema,
			RetryAfterMS: sent.Body.RetryAfterMS,
			Attempts:     sent.Body.Attempts,
			Retry:        retryFor(sent.Body.Invariant, kind, args),
		}
		if sent.Body.Next != "" {
			r.Next = sent.Body.Next
		}
		return e.Out.FailWith(r)
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
// retryFor builds the corrected command. Every argument is re-quoted, because
// this string is meant to be run: an unquoted `--text probe: no severity`
// posts the word "probe:" and leaves the rest as flags, so the retry that was
// supposed to fix the post silently posts a different one.
func retryFor(invariant string, kind core.Kind, args []string) string {
	rest := args[1:]
	base := func(extra ...string) string {
		return "comms post " + string(kind) + " " + shellJoin(append(append([]string{}, rest...), extra...))
	}
	switch invariant {
	case "body.severity.invalid":
		return base("--severity", "p2")
	case "body.text.required":
		return base("--text", "<what you found>")
	case "recipient.required":
		return base("--to", "<someone>")
	case "recipient.forbidden":
		// Drop the flag and its value together. Removing only the flag leaves
		// the recipient behind as a bare positional, so the corrected command
		// fails a second time in a new way.
		var kept []string
		for i := 0; i < len(rest); i++ {
			if rest[i] == "--to" || rest[i] == "-to" {
				i++
				continue
			}
			kept = append(kept, rest[i])
		}
		return "comms post " + string(kind) + " " + shellJoin(kept)
	case "body.url.required":
		return base("--url", "https://…")
	}
	return ""
}

// shellJoin quotes each argument so the result can be pasted and run.
func shellJoin(args []string) string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		out = append(out, shellQuote(a))
	}
	return strings.Join(out, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if strings.IndexFunc(s, func(r rune) bool {
		return r == ' ' || r == '\'' || r == '"' || r == '$' || r == '`' ||
			r == '\\' || r == ';' || r == '&' || r == '|' || r == '<' || r == '>' ||
			r == '(' || r == ')' || r == '*' || r == '?' || r == '!' || r == '#'
	}) == -1 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// ---------------------------------------------------------------- redact

func runRedact(e *Env, args []string) int {
	fs, sink := newFlags("redact")
	idem := fs.String("idem", "", "reuse a natural key you already have")
	actor := fs.String("as", "", "the seat redacting")
	room := fs.String("room", "", "the room the event is in")
	why := fs.String("why", "", "why it is being suppressed")
	fs.Usage = func() {
		e.Out.HelpFS(fs, `comms redact <seq> --as <seat> --why "<reason>"

Suppresses one of your own events: its body leaves the room, search, and any
attached artifact stops being served. The event itself stays, because
corrections are new entries.

  comms redact 20014 --as agent:bcm/claude-1 --why "pasted a token"

The seq is positional, not a --refs string, so the refs value you are carrying
through a piece of work cannot land here by habit. You can only redact your own
event; someone else's is an operator action.`)
	}

	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
			fs.Usage()
			return usageOK(e)
		}
		return e.Out.Fail(ExitUsage, "usage", "seq.required",
			"name the event to redact: comms redact <seq> --as <seat> --why \"...\"")
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
	if code := CheckServer(e, seat); code != 0 {
		return code
	}
	drainFirst(e, seat)
	inRoom := resolveRoom(seat, *room)
	priv, err := LoadSeat(seat)
	if err != nil {
		return e.Out.Fail(ExitUsage, "usage", "seat.not_enrolled", err.Error())
	}

	c := NewClient(e.Server, seat, priv)
	cmd := map[string]any{
		"room": inRoom, "author": seat, "kind": "redact",
		"body": map[string]any{"text": *why},
		"refs": []string{seqArg},
	}
	applyIdem(e, cmd, *idem)
	sent, err := c.Post(cmd)
	if err != nil {
		// Spool it, like every other write. This said "spooled" and dropped the
		// bytes: exit 5, outcome spooled, nothing held. Worse here than
		// anywhere else — redact is the command you run the moment you realise
		// you pasted a credential, and the reply told you it was safely queued.
		return spoolOrFail(e, c, cmd, sent, err)
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
	idem := fs.String("idem", "", "reuse a natural key you already have")
	extraRefs := fs.String("refs", "", "comma-separated refs to carry, alongside what search attaches")
	actor := fs.String("as", "", "the seat asking")
	room := fs.String("room", "", "room to ask in")
	to := fs.String("to", "", "who to ask")
	text := fs.String("text", "", "the question, or - to read stdin")
	textFile := fs.String("text-file", "", "read the question from a file")
	noSearch := fs.Bool("no-search", false, "skip the search for prior answers")
	fs.Usage = func() {
		e.Out.HelpFS(fs, `comms ask --as <seat> --to <who> --text "<question>"

Searches the room for what it already knows, attaches up to three hits to the
question's refs, prints what it attached, and posts either way.

  comms ask --as agent:bcm/claude-1 --to bcm \
      --text "is migration 0031 safe to reorder ahead of 0029?"

It attaches, it never gates: a human seeing the prior hits alongside the
question can tell in a glance whether it is new.`)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return usageOK(e)
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
	if code := CheckServer(e, seat); code != 0 {
		return code
	}
	drainFirst(e, seat)
	inRoom := resolveRoom(seat, *room)
	priv, err := LoadSeat(seat)
	if err != nil {
		return e.Out.Fail(ExitUsage, "usage", "seat.not_enrolled", err.Error())
	}

	var refs []string
	// Refs the caller carries through a piece of work come first: search adds
	// context, it does not replace the thread the agent is already in.
	for _, r := range strings.Split(*extraRefs, ",") {
		if r = strings.TrimSpace(r); r != "" {
			refs = append(refs, r)
		}
	}
	if !*noSearch {
		terms := distinctiveTerms(question)
		if len(terms) > 0 {
			for _, h := range searchFor(e, inRoom, strings.Join(terms, " "), 3) {
				refs = append(refs, fmt.Sprint(h.Seq))
				preview, _ := h.Body["text"].(string)
				e.Out.Line(map[string]any{
					"type": "attached", "seq": h.Seq, "kind": h.Kind,
					"preview": first(truncateText(preview, 100)),
					"why":     "the room already contains this; it is attached to your question",
				})
			}
			e.Out.Note("searched %q, attached %d prior event(s)", strings.Join(terms, " "), len(refs))
		}
	}

	cmd := map[string]any{
		"room": inRoom, "author": seat, "kind": "question",
		"body":      map[string]any{"text": question},
		"recipient": *to,
	}
	if len(refs) > 0 {
		cmd["refs"] = refs
	}
	applyIdem(e, cmd, *idem)
	return send(e, NewClient(e.Server, seat, priv), cmd, "question", nil)
}

// ---------------------------------------------------------------- answer

func runAnswer(e *Env, args []string) int {
	fs, sink := newFlags("answer")
	idem := fs.String("idem", "", "reuse a natural key you already have")
	actor := fs.String("as", "", "the seat answering")
	room := fs.String("room", "", "room the question is in")
	toQuestion := fs.String("to-question", "", "the seq of the question you are answering")
	text := fs.String("text", "", "the answer, or - to read stdin")
	textFile := fs.String("text-file", "", "read the answer from a file")
	fs.Usage = func() {
		e.Out.HelpFS(fs, `comms answer --as <seat> --to-question <seq> --text "<answer>"

No recipient: the server derives it from the question's author, so an answer
always reaches whoever asked.

  comms answer --as bcm --to-question 20014 --text "yes, 0029 is idempotent"`)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return usageOK(e)
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
	if code := CheckServer(e, seat); code != 0 {
		return code
	}
	drainFirst(e, seat)
	inRoom := resolveRoom(seat, *room)
	priv, err := LoadSeat(seat)
	if err != nil {
		return e.Out.Fail(ExitUsage, "usage", "seat.not_enrolled", err.Error())
	}

	// No recipient is sent: the core derives it from the question's author.
	cmd := map[string]any{
		"room": inRoom, "author": seat, "kind": "answer",
		"body": map[string]any{"text": body},
		"refs": []string{*toQuestion},
	}
	applyIdem(e, cmd, *idem)
	return send(e, NewClient(e.Server, seat, priv), cmd, "answer", nil)
}

// ---------------------------------------------------------------- attach

func runAttach(e *Env, args []string) int {
	// --get is the read pair of attach: the same hash, back as the stored
	// markdown, through the seat's read session. Without it an agent can see
	// an attachment exists in a read and have no way to open it — the /a/
	// route wants a session a bare curl does not carry.
	if len(args) > 0 && (strings.HasPrefix(args[0], "--get") || strings.HasPrefix(args[0], "-get")) {
		return runAttachGet(e, args)
	}
	fs, sink := newFlags("attach")
	title := fs.String("title", "", "what to call it where it is referenced")
	actor := fs.String("as", "", "the seat uploading — a session hub requires one")
	fs.Usage = func() {
		e.Out.HelpFS(fs, `comms attach <path|-> [--as <seat>]

Uploads markdown and prints its hash. post --attach-hash accepts the hash, so a
rejected post does not mean re-running a three-minute test to reproduce stdin
you already consumed.

  HASH=$(go test ./... 2>&1 | comms attach - | jq -r .hash)
  comms post finding --as <seat> --severity p2 \
      --text "suite red" --attach-hash "$HASH" --attach-title suite.md

--title travels with the upload and comes back in the reply, so the pair to
paste into the post is printed for you rather than reassembled by hand.

The pair verb: comms attach --get <hash> [--as <seat>] fetches a stored
artifact back as markdown, through your read session. On a terminal it prints
the text; piped, one JSON object with a content field.`)
	}
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fs.Usage()
		if len(args) == 0 {
			return e.Out.Fail(ExitUsage, "usage", "path.required",
				"name a file to upload, or - to read stdin")
		}
		return usageOK(e)
	}
	if err := fs.Parse(args[1:]); err != nil {
		return e.Out.Fail(ExitUsage, "usage", "flags.invalid", strings.TrimSpace(sink.String()))
	}
	// The seat is optional on open hubs but load-bearing on session hubs: it
	// names the read session the upload rides and lets the seat's pinned
	// server apply, so an env-less harness reaches the right hub. Resolved
	// leniently — no seat is not an error here, the server decides.
	if *actor != "" {
		e.Seat = *actor
		applyPinnedServer(e, *actor)
	} else if v, ok := e.getenv("COMMS_ACTOR"); ok && v != "" {
		e.Seat = v
		applyPinnedServer(e, v)
	}

	if err := WithinTree(args[0]); err != nil {
		return e.Out.Fail(ExitUsage, "usage", "attach.outside_tree", err.Error())
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
		// Transport is not refusal: an unreachable hub answers "wait and run
		// this again", never "stop".
		if errors.Is(err, errUnreachable) {
			return e.Out.Fail(ExitSpooled, "unreachable", "transport.failed", err.Error())
		}
		return e.Out.Fail(ExitRefused, "refused", "artifact.rejected", err.Error())
	}
	name := *title
	if name == "" {
		name = defaultTitle(args[0])
	}
	e.Out.Note("stored %d bytes as %s", size, hash[:12])
	return e.Out.Succeed(Result{
		Outcome: "stored", Hash: hash, Size: size, Title: name,
		// The pair to paste, rather than one the agent reassembles by hand from
		// two fields and gets subtly wrong under a shell.
		Next: "reference it: --attach-hash " + hash + " --attach-title " + shellQuote(name),
	})
}

// runAttachGet fetches a stored artifact by hash, as the markdown that was
// uploaded. It reads through doRead, so a session hub gets the seat's session
// and the membership gate on /a/ applies — this is the sanctioned door a bare
// curl lacks.
func runAttachGet(e *Env, args []string) int {
	fs, sink := newFlags("attach")
	hash := fs.String("get", "", "the artifact hash to fetch")
	actor := fs.String("as", "", "the seat reading")
	fs.Usage = func() {
		e.Out.HelpFS(fs, `comms attach --get <hash> [--as <seat>]

Fetches a stored artifact back as markdown, through your read session. On a
terminal it prints the text; piped, one JSON object with a content field:

  comms attach --get <hash> --as <seat> | jq -r .content > report.md`)
	}
	if err := fs.Parse(args); err != nil {
		if isHelp(err) {
			fs.Usage()
			return usageOK(e)
		}
		return e.Out.Fail(ExitUsage, "usage", "flags.invalid", strings.TrimSpace(sink.String()))
	}
	if len(*hash) != 64 {
		return e.Out.Fail(ExitUsage, "usage", "hash.invalid",
			"--get wants the 64-hex artifact hash a read printed")
	}
	if _, code := resolveSeat(e, *actor); code != 0 {
		return code
	}
	resp, err := doRead(e, nil, func() (*http.Request, error) {
		req, err := http.NewRequest("GET", strings.TrimRight(e.Server, "/")+"/a/"+*hash, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "text/markdown")
		return req, nil
	})
	if err != nil {
		return e.Out.Fail(ExitSpooled, "unreachable", "transport.failed", err.Error())
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 500 {
		// A hub failure is not "no such artifact": that lie sent readers
		// chasing hashes that were fine.
		return e.Out.Fail(ExitSpooled, "unreachable", "transport.failed",
			fmt.Sprintf("server error %d fetching the artifact", resp.StatusCode))
	}
	if resp.StatusCode != http.StatusOK {
		// The server 404s identically for unknown, unreferenced, and
		// not-yours-to-see; say all three so the reader does not chase the wrong one.
		return e.Out.Fail(ExitRejected, "rejected", "artifact.unknown",
			"no artifact "+*hash+" visible to "+e.Seat+
				" — unknown hash, or referenced only in rooms this seat is not a member of")
	}
	if e.Out.Quiet {
		return e.Out.Succeed(Result{Outcome: "fetched", Hash: *hash, Size: len(body), Content: string(body)})
	}
	fmt.Fprint(e.Out.Stdout, string(body))
	e.Out.Note("%d bytes, artifact %s", len(body), (*hash)[:12])
	return ExitOK
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
	from := fs.Int64("from", 0, "replay from this seq, inclusive; does not move the cursor")
	since := fs.Duration("since", 0, "replay the last <duration>; does not move the cursor")
	wait := fs.Duration("wait", 0, "block until something arrives, or this elapses (max 30m)")
	untilKind := fs.String("until-kind", "", "with --wait, stop when this kind arrives")
	untilRefs := fs.String("refs", "", "with --wait, stop when an event references this seq")
	fs.Usage = func() {
		e.Out.HelpFS(fs, `comms read --as <seat> [--room core]

Prints what is new since you last read, then exits. It does not hang: a quiet
room returns count 0 in one round trip, and says whether that means you are
caught up or the room is empty.

  comms read --as agent:bcm/claude-1
  comms read --as agent:bcm/claude-1 --full            # whole bodies
  comms read --as agent:bcm/claude-1 --kind finding    # filtered, does not advance
  comms read --as agent:bcm/claude-1 --from 50014 --full   # re-read one event
  comms read --as agent:bcm/claude-1 --since 1h        # replay the last hour
  comms read --as agent:bcm/claude-1 --wait 5m         # block on the ambient lane

--from and --since replay: they print what you have already seen and leave your
cursor where it was. Re-reading is not reading.

Findings and status land ambient, so --wait belongs here as well as on inbox:
waiting on your crew is the ambient case.

read and inbox keep separate cursors, so draining one never hides the other.`)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return usageOK(e)
		}
		return e.Out.Fail(ExitUsage, "usage", "flags.invalid", strings.TrimSpace(sink.String()))
	}
	seat, code := resolveSeat(e, *actor)
	if code != 0 {
		return code
	}
	if code := CheckServer(e, seat); code != 0 {
		return code
	}
	drainFirst(e, seat)
	inRoom := resolveRoom(seat, *room)
	if *reset {
		if err := ResetCursor(seat, inRoom, LaneAll); err != nil {
			return e.Out.Fail(ExitInternal, "internal", "cursor.unwritable", err.Error())
		}
	}

	if *wait > maxWait {
		return e.Out.Fail(ExitUsage, "usage", "wait.too_long",
			fmt.Sprintf("--wait is capped at %s so a stuck agent surfaces; got %s", maxWait, *wait))
	}
	if *from > 0 && *since > 0 {
		return e.Out.Fail(ExitUsage, "usage", "replay.contested",
			"--from and --since both choose a start; use one")
	}

	o := readOpts{
		Actor: seat, Room: inRoom, Lane: LaneAll,
		Kind: *kind, Author: *author, Full: *full,
		From: *from, Since: *since,
		Wait: *wait, UntilKind: *untilKind, UntilRefs: *untilRefs,
		// A filter means the read did not see everything, so it must not claim
		// the cursor did. A replay is not a read at all.
		Peek: *peek || *kind != "" || *author != "" || *from > 0 || *since > 0,
	}
	events, meta, err := drain(e, o)
	if err != nil {
		// A transport failure is not unretryable. Exit 4 here while post on the
		// same unreachable server returns spooled/exit 0 told an agent to stop
		// over a condition that fixes itself.
		return e.Out.Fail(ExitSpooled, "unreachable", "transport.failed", err.Error())
	}
	return emit(e, o, events, meta)
}

func runInbox(e *Env, args []string) int {
	fs, sink := newFlags("inbox")
	actor := fs.String("as", "", "the seat reading")
	room := fs.String("room", "", "room to read")
	compactOut := fs.Bool("compact", false, "one line per event instead of whole bodies")
	peek := fs.Bool("peek", false, "do not advance the cursor")
	from := fs.Int64("from", 0, "replay from this seq, inclusive; does not move the cursor")
	wait := fs.Duration("wait", 0, "block until something arrives, or this elapses (max 30m)")
	untilKind := fs.String("until-kind", "", "with --wait, stop when this kind arrives")
	untilRefs := fs.String("refs", "", "with --wait, stop when an event references this seq")
	fs.Usage = func() {
		e.Out.HelpFS(fs, `comms inbox --as <seat> [--wait 15m --until-kind answer --refs <seq>]

Prints only what is addressed to you, in full, then exits. A handoff is not
ambient chatter: the one message you must act on is the one you must not have
to reconstruct. Use --compact for one line per event.

  comms inbox --as agent:bcm/claude-1
  comms inbox --as agent:bcm/claude-1 --compact
  comms inbox --as agent:bcm/claude-1 --from 50027       # re-read an assignment
  comms inbox --as agent:bcm/claude-1 --wait 15m --until-kind answer --refs 20014

--wait blocks against a deadline and exits 0 either way. Waiting out the clock
is the flag doing its job, not a failure — you get a handoff suggestion.`)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return usageOK(e)
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
	if code := CheckServer(e, seat); code != 0 {
		return code
	}
	drainFirst(e, seat)
	inRoom := resolveRoom(seat, *room)

	o := readOpts{
		Actor: seat, Room: inRoom, Lane: LaneAddressed,
		Recipient: seat, Full: !*compactOut, Peek: *peek || *from > 0,
		From: *from,
		Wait: *wait, UntilKind: *untilKind, UntilRefs: *untilRefs,
	}
	events, meta, err := drain(e, o)
	if err != nil {
		// A drop mid-wait must leave the cursor where it was, so nothing is
		// skipped on the next read.
		return e.Out.Fail(ExitSpooled, "unreachable", "transport.failed", err.Error())
	}

	if *wait > 0 && len(events) == 0 {
		// Waiting out the clock is the flag working. Exit 0, and say what to
		// do instead of waiting again.
		e.Out.Line(map[string]any{
			"ok": true, "outcome": "waited", "count": 0, "room": inRoom,
			"waited": wait.String(),
			"next": "nobody answered; hand off with: comms post handoff --to <human> " +
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
		e.Out.HelpFS(fs, `comms whoami [--as <seat>]

Reports the seat, host, server and public key. It never reports the private
key, and no verb, flag or environment variable does.

  comms whoami --as agent:bcm/claude-1`)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return usageOK(e)
		}
		return e.Out.Fail(ExitUsage, "usage", "flags.invalid", err.Error())
	}
	seat, code := resolveSeat(e, *actor)
	if code != 0 {
		return code
	}
	if !HasSeat(seat) {
		return e.Out.Fail(ExitUsage, "usage", "seat.not_enrolled",
			"no key for "+seat+"; run: comms enrol --as "+seat)
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
	fs, sink := newFlags("escalate")
	idem := fs.String("idem", "", "reuse a natural key you already have")
	actor := fs.String("as", "", "the seat escalating")
	room := fs.String("room", "", "the room the entry is in")
	to := fs.String("to", "", "the person who should look")
	text := fs.String("text", "", "why this needs them now")
	fs.Usage = func() {
		e.Out.HelpFS(fs, `comms escalate <seq> --as <seat> --to <human> --text "<why now>"

Pulls one entry already in the room into a person's attention. It states no new
fact — the finding already says what it says — so what lands in the log is an
ordinary addressed question referencing it, signed by you like anything else.

  comms escalate 20014 --as agent:bcm/claude-1 --to human:sarah \
      --text "this blocks Thursday's migration; postpone or batch the rebuild?"

You get %d of these an hour. That is the whole point: severity routes nothing, a
p0 finding sits in the same ambient lane as a p3, and this is the one lever that
spends someone's afternoon. When it is gone the finding is still in the room and
still searchable — what you have run out of is the right to interrupt, not the
right to record.`, 3)
	}

	seqs, code, done := parsePositional(e, fs, sink, args)
	if done {
		return code
	}
	if len(seqs) != 1 {
		return e.Out.Fail(ExitUsage, "usage", "refs.exactly_one",
			"name the one entry a person should look at: comms escalate <seq>")
	}
	seat, code := resolveSeat(e, *actor)
	if code != 0 {
		return code
	}
	if code := CheckServer(e, seat); code != 0 {
		return code
	}
	if *to == "" {
		return e.Out.Fail(ExitUsage, "usage", "recipient.required",
			"escalate names the person who should look: --to <human>")
	}
	if *text == "" {
		return e.Out.Fail(ExitUsage, "usage", "body.text.required",
			"say why it needs them now; the entry itself already says what it is")
	}

	priv, err := LoadSeat(seat)
	if err != nil {
		return e.Out.Fail(ExitUsage, "usage", "seat.not_enrolled", err.Error())
	}
	c := NewClient(e.Server, seat, priv)
	body := map[string]any{
		"room": resolveRoom(seat, *room), "author": seat,
		"refs": seqs[0], "to": *to, "text": *text,
	}
	applyIdem(e, body, *idem)
	sent, err := c.PostTo("/escalate", body)
	if err != nil {
		return e.Out.Fail(ExitSpooled, "unreachable", "transport.failed", err.Error())
	}
	exit, outcome := statusToExit(sent.Status, sent.Body.Invariant)
	if exit != ExitOK {
		if sent.Body.Exit != 0 && stricter(sent.Body.Exit, exit) {
			exit, outcome = sent.Body.Exit, "refused"
		}
		r := Result{
			Outcome: outcome, Exit: exit,
			Invariant: sent.Body.Invariant, Detail: sent.Body.Detail,
			RetryAfterMS: sent.Body.RetryAfterMS,
		}
		if sent.Body.Next != "" {
			r.Next = sent.Body.Next
		}
		return e.Out.FailWith(r)
	}
	e.Out.Note("escalated at %d; %d left this hour", sent.Body.Seq, sent.Body.Remaining)
	applied := sent.Body.Applied
	return e.Out.Succeed(Result{
		Outcome: "escalated", Seq: sent.Body.Seq, Applied: &applied,
		Remaining: sent.Body.Remaining, Detail: sent.Body.Detail,
	})
}

// ---------------------------------------------------------------- helpers

func resolveSeat(e *Env, flagValue string) (string, int) {
	if flagValue != "" {
		e.Seat = flagValue
		applyPinnedServer(e, flagValue)
		return flagValue, 0
	}
	if v, ok := e.getenv("COMMS_ACTOR"); ok && v != "" {
		e.Seat = v
		applyPinnedServer(e, v)
		return v, 0
	}
	return "", e.Out.Fail(ExitUsage, "usage", "actor.required",
		"name the seat with --as, or set COMMS_ACTOR")
}

// DefaultServer is the built-in hub address a bare client talks to.
const DefaultServer = "http://127.0.0.1:7777"

// applyPinnedServer defaults the server to the hub this seat enrolled
// against, when nothing chose one: no --server flag (e.Server still the
// built-in default) and no COMMS_SERVER. Enrolment pins the hub per seat, so
// a harness whose shell forgets exported env between commands still reaches
// the right hub with a bare `comms read --as <seat>`. An explicit choice
// always wins; CheckServer still refuses a mismatched write.
func applyPinnedServer(e *Env, seat string) {
	if e.Server != DefaultServer {
		return
	}
	if v, ok := e.getenv("COMMS_SERVER"); ok && v != "" {
		return
	}
	if pinned := PinnedServer(seat); pinned != "" {
		e.Server = pinned
	}
}

// newIdem is gone. A random key made every re-run a new event, so the fix an
// agent reaches for by reflex — run it again — was exactly the thing that
// turned one finding into two. See cli/idem.go.

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

// ---------------------------------------------------------------- decline

func runDecline(e *Env, args []string) int {
	fs, sink := newFlags("decline")
	actor := fs.String("as", "", "the seat declining")
	room := fs.String("room", "", "room the handoff is in")
	why := fs.String("why", "", "why you are not taking it")
	idem := fs.String("idem", "", "reuse a natural key you already have")
	fs.Usage = func() {
		e.Out.HelpFS(fs, `comms decline <seq> --as <seat> --why "<why not>"

Refuses a handoff, out loud. It goes back to whoever handed the work over, for
the same reason an answer goes back to whoever asked: the person who needs to
know is the one who thought the work was covered.

  comms decline 50002 --as agent:bcm/claude-1 \
      --why "already three deep in the auth suite; this needs someone free"

Declining is not a failure and costs you nothing. Saying nothing does: a
handoff nobody took and nobody refused looks exactly like a handoff being
worked on, and the difference is discovered when the work is due.`)
	}

	seqs, code, done := parsePositional(e, fs, sink, args)
	if done {
		return code
	}
	if len(seqs) != 1 {
		return e.Out.Fail(ExitUsage, "usage", "refs.exactly_one",
			"name the handoff you are refusing: comms decline <seq>")
	}
	seat, code := resolveSeat(e, *actor)
	if code != 0 {
		return code
	}
	if code := CheckServer(e, seat); code != 0 {
		return code
	}
	if *why == "" {
		return e.Out.Fail(ExitUsage, "usage", "body.text.required",
			"say why: a refusal without a reason makes the sender ask, which is the "+
				"round trip declining exists to save")
	}
	drainFirst(e, seat)

	priv, err := LoadSeat(seat)
	if err != nil {
		return e.Out.Fail(ExitUsage, "usage", "seat.not_enrolled", err.Error())
	}
	cmd := map[string]any{
		"room": resolveRoom(seat, *room), "author": seat, "kind": "decline",
		"body": map[string]any{"text": *why},
		"refs": []string{seqs[0]},
	}
	applyIdem(e, cmd, *idem)
	return send(e, NewClient(e.Server, seat, priv), cmd, "decline", nil)
}

// runServeHelp explains the one verb the client does not run. The binary
// handles `serve` before the client is reached; this exists so `serve --help`
// answers like every other verb rather than looking like a hole in the list.
func runServeHelp(e *Env, args []string) int {
	e.Out.Help(`comms serve [--addr ADDR] [--db PATH] [--rooms A,B] [--seed] [--insecure]

Starts the hub: the room, the command API, the SSE stream, and the background
embedder that fills the semantic lane.

  comms serve                                  # 127.0.0.1:7777, ./comms.db
  comms serve --db demo.db --seed --rooms core,bash
  comms serve --addr 0.0.0.0:7777              # reachable from the tailnet

This is the one verb the client does not send anywhere: it is the thing the
other verbs talk to. Every operator flag is listed by comms --h-server.`)
	return usageOK(e)
}

// ---------------------------------------------------------------- kinds

func runKinds(e *Env, args []string) int {
	fs, sink := newFlags("kinds")
	fs.Usage = func() {
		e.Out.HelpFS(fs, `comms kinds

What you can post, what each one means, and which lane it lands in. Read from
the core's own list, so it cannot drift from what the server will accept —
three documents once listed 8, 8 and 26 kinds while the binary knew the answer
and had no way to say it.

Ambient is true, worth keeping, and not worth interrupting anyone for; those
collapse into a single carried-forward line. Addressed names a recipient and
renders inline in front of that person.`)
	}
	if err := fs.Parse(args); err != nil {
		if isHelp(err) {
			return usageOK(e)
		}
		return e.Out.Fail(ExitUsage, "usage", "flags.invalid", strings.TrimSpace(sink.String()))
	}

	for _, k := range core.Kinds() {
		lane := "ambient"
		if k.Lane == core.Addressed {
			lane = "addressed"
		}
		row := map[string]any{
			"type": "kind", "kind": string(k.Kind), "lane": lane,
			"means": k.Means, "requires": k.Requires,
		}
		if !k.Agent {
			row["agent_postable"] = false
			row["detail"] = "operator capability required"
		}
		e.Out.Line(row)
		mark := " "
		if !k.Agent {
			mark = "*"
		}
		e.Out.Note("%s %-9s %-9s %s  (%s)", mark, k.Kind, lane, k.Means, k.Requires)
	}
	e.Out.Note("* needs a capability an ordinary seat does not have")
	return e.Out.Succeed(Result{Outcome: "listed", Count: len(core.Kinds())})
}
