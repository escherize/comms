package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// Exit codes decide whether the agent retries, which is the whole point of
// having them. The load-bearing split is 3 versus 4: an agent that retries a
// signature failure loops forever and burns budget, and an agent that gives up
// on a schema failure abandons the self-correction ADR-0004 exists to provide.
const (
	ExitOK        = 0 // in the log, or a clean empty result
	ExitInternal  = 1 // a bug here — malformed command, unwritable spool
	ExitUsage     = 2 // bad flags, unknown kind, missing key
	ExitRejected  = 3 // the decider refused; read invariant + schema, correct once
	ExitRefused   = 4 // unauthorized, or an invariant we do not recognise. Stop.
	ExitSpooled   = 5 // transport failed on a read; nothing held, run it again. (A failed write spools and exits 0.)
	ExitThrottled = 6 // sleep retry_after_ms, batch
)

// Out carries the streams so tests can capture both without touching globals.
type Out struct {
	Stdout io.Writer
	Stderr io.Writer
	Quiet  bool
	// Color styles help for a terminal. Tests construct Out directly and get
	// plain text; only Std() turns it on, and only when stderr is a tty.
	Color bool
}

func Std() *Out {
	// --quiet defaults on when stdout is not a terminal, so a harness that
	// merges the two streams receives JSON only.
	fi, err := os.Stdout.Stat()
	piped := err == nil && (fi.Mode()&os.ModeCharDevice) == 0
	return &Out{Stdout: os.Stdout, Stderr: os.Stderr, Quiet: piped, Color: colorEnabled()}
}

// Line emits one JSONL object on stdout. stdout is JSONL and nothing else, on
// success and on failure alike — the consumer is a program or a model, neither
// of which parses columns reliably.
func (o *Out) Line(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintf(o.Stdout, `{"type":"event","ok":false,"outcome":"internal"}`+"\n")
		return
	}
	fmt.Fprintln(o.Stdout, string(b))
}

// Note writes one terse human line to stderr. It costs the machine contract
// nothing and makes a transcript readable.
func (o *Out) Note(format string, args ...any) {
	if o.Quiet {
		return
	}
	fmt.Fprintf(o.Stderr, format+"\n", args...)
}

// Help answers --help, once, in the caller's own language. Piped stdout means
// a program is reading: one JSONL line. A terminal means a person is reading:
// the text, once. Writing both to both — the old behaviour — showed a person
// the same help twice with a JSON blob on top, which reads as a bug and was
// reported as one.
func (o *Out) Help(format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	if o.Quiet {
		o.Line(map[string]any{"type": "help", "text": text})
		return
	}
	if o.Color {
		text = colorize(text)
	}
	fmt.Fprintln(o.Stderr, text)
}

// Advise is guidance about using the tool better, never a refusal. It goes out
// as a JSONL line on stdout because that is the stream an agent reads: --quiet
// defaults on whenever stdout is piped, so a note only on stderr would be
// suppressed for exactly the caller the advice is for.
func (o *Out) Advise(topic, detail string) {
	o.Line(map[string]any{"type": "advice", "topic": topic, "detail": detail})
	o.Note("%s", detail)
}

// Result is the terminal object. A consumer reading the last line always gets
// the outcome.
type Result struct {
	OK      bool   `json:"ok"`
	Outcome string `json:"outcome"`
	Exit    int    `json:"exit,omitempty"`

	Seq          int64            `json:"seq,omitempty"`
	Applied      *bool            `json:"applied,omitempty"`
	Actor        string           `json:"actor,omitempty"`
	Host         string           `json:"host,omitempty"`
	Server       string           `json:"server,omitempty"`
	Room         string           `json:"room,omitempty"`
	KeyStatus    string           `json:"key_status,omitempty"`
	Cursors      map[string]int64 `json:"cursors,omitempty"`
	RetryAfterMS int64            `json:"retry_after_ms,omitempty"`
	Attempts     int              `json:"attempts,omitempty"`
	PubKey       string           `json:"public_key,omitempty"`
	Hash         string           `json:"hash,omitempty"`
	Title        string           `json:"title,omitempty"`
	Remaining    int              `json:"remaining,omitempty"`
	Token        string           `json:"token,omitempty"`
	URL          string           `json:"url,omitempty"`
	Size         int              `json:"size,omitempty"`
	Count        int              `json:"count,omitempty"`
	Content      string           `json:"content,omitempty"`

	Invariant string `json:"invariant,omitempty"`
	Detail    string `json:"detail,omitempty"`
	Schema    string `json:"schema,omitempty"`
	Next      string `json:"next,omitempty"`
	Retry     string `json:"retry,omitempty"`
}

// Succeed emits the terminal object for an accepted outcome.
func (o *Out) Succeed(r Result) int {
	r.OK = true
	o.Line(r)
	return ExitOK
}

// Fail emits the terminal object for a refusal and returns its exit code.
func (o *Out) Fail(code int, outcome, invariant, detail string) int {
	return o.FailWith(Result{
		Outcome: outcome, Exit: code,
		Invariant: invariant, Detail: detail,
		Next: verdictFor(invariant, code),
	})
}

func (o *Out) FailWith(r Result) int {
	r.OK = false
	if r.Next == "" {
		r.Next = verdictFor(r.Invariant, r.Exit)
	}
	o.Line(r)
	o.Note("%s: %s", r.Invariant, r.Detail)
	return r.Exit
}

// stricter reports whether the server's exit stops sooner than the local one.
// Only ExitRefused — stop, ask a person — is stricter than a correctable
// rejection; every other direction is the server asking the client to keep
// trying, which is the one thing the local table exists to refuse.
func stricter(fromServer, local int) bool {
	return fromServer == ExitRefused && local == ExitRejected
}

// verdicts maps an invariant to what the agent should do next. The table is
// rendered here rather than passed through from the server, because a future
// server invariant must not become a retry storm in an unattended run.
var verdicts = map[string]string{
	"body.text.required":        "add --text and post again",
	"body.severity.invalid":     "add --severity p0|p1|p2|p3 and post again",
	"body.url.required":         "add --url and post again",
	"recipient.required":        "name a recipient with --to and post again",
	"recipient.forbidden":       "drop --to; this kind is ambient and interrupts nobody",
	"refs.question_required":    "point --to-question at a question; that seq is not one",
	"refs.exactly_one":          "name exactly one event in --refs",
	"refs.unknown":              "that event does not exist; check the seq you read",
	"refs.target_unknown":       "no such event in this room; check the seq and the room you read it from",
	"redact.already_redacted":   "it is already redacted; ask a human to purge it if the body must be erased",
	"redact.not_author":         "you cannot redact someone else's event; ask them, or ask a human to purge it",
	"attachment.unknown":        "upload the artifact first with: comms attach",
	"attachment.title.required": "give the attachment a --title",
	"room.unknown":              "check the room name; list them with: comms room",
	"kind.unknown":              "use a kind the server knows; see: comms post --help",
	"idem.conflict":             "this key already carried different content; do not reuse keys",
	"signature.missing":         "stop. The client did not sign. A human must look at this",
	"signature.invalid":         "stop. The bytes signed and the bytes sent differ; this is a bug in the client, not your key",
	"key.unknown":               "stop. This seat has no key. A human must enrol it",
	"key.revoked":               "stop. This seat's key was revoked. A human must re-enrol it",
	"key.compromised":           "stop immediately and tell a human. This key is marked compromised",
	"enrolment.refused":         "stop. Ask a human for a fresh invite token",
	"escalation.exhausted":      "wait for the window; the entry is recorded either way",
	"budget.exhausted":          "combine what is left into one summarizing finding and post that",
	"parse.failed":              "stop. The client built a malformed command; this is a bug here",

	// Usage failures the caller can fix. `next: "stop"` on any of these tells an
	// agent to abandon work that is one flag away from correct, while `detail`
	// on the same line explains the fix — the two halves of one reply
	// contradicting each other.
	"attachment.title_count":  "give one --attach-title per attachment, in the same order, and post again",
	"attachment.outside_tree": "pipe the file on stdin instead: cat <file> | comms attach -",
	"attach.outside_tree":     "pipe the file on stdin instead: cat <file> | comms attach -",
	"stdin.contested":         "only one flag may read stdin; put the other in a file and use --text-file",
	"text.contested":          "use --text, --text-file or --text -, not two of them",
	"text.unreadable":         "check the path, then post again",
	"content.unreadable":      "check the path, then post again",
	"content.empty":           "there is nothing to upload; check the file or the pipe",
	"query.required":          "give search some words; filters alone match nothing",
	"path.required":           "name a file to upload, or - to read stdin",
	"replay.contested":        "use --from or --since, not both",
	"room.ambiguous":          "name one room",
	"wait.too_long":           "lower --wait; 30m is the cap",
	"actor.required":          "name the seat with --as, or set COMMS_ACTOR",
	"kind.required":           "name a kind: comms post --help lists them",
	"seat.not_enrolled":       "a human must enrol this seat before it can post",
	"server.mismatch":         "unset COMMS_SERVER, or enrol a separate seat for the other hub",
	"rate.exceeded":           "sleep retry_after_ms, then post again; this event was not kept",
	"transport.failed":        "the server is unreachable; wait and run this again",
	"flags.invalid":           "fix the flag named in detail; comms <verb> --help lists them",
}

func verdictFor(invariant string, code int) string {
	if v, ok := verdicts[invariant]; ok {
		return v
	}
	switch code {
	case ExitRejected:
		// An invariant we do not recognise is not a retry. A future server
		// invariant must not loop an unattended agent.
		return "stop. This refusal is not one this client knows how to correct; a human must look"
	case ExitSpooled:
		return "keep working. The bytes are held and will be sent"
	case ExitThrottled:
		// Not "batch": no verb batches, and telling an agent to do something the
		// tool cannot do is worse than telling it nothing.
		return "sleep retry_after_ms, then post again"
	}
	return "stop"
}
