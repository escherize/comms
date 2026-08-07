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
	ExitSpooled   = 5 // transport failed; bytes are held. Keep working.
	ExitThrottled = 6 // sleep retry_after_ms, batch
)

// Out carries the streams so tests can capture both without touching globals.
type Out struct {
	Stdout io.Writer
	Stderr io.Writer
	Quiet  bool
}

func Std() *Out {
	// --quiet defaults on when stdout is not a terminal, so a harness that
	// merges the two streams receives JSON only.
	fi, err := os.Stdout.Stat()
	piped := err == nil && (fi.Mode()&os.ModeCharDevice) == 0
	return &Out{Stdout: os.Stdout, Stderr: os.Stderr, Quiet: piped}
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

// Result is the terminal object. A consumer reading the last line always gets
// the outcome.
type Result struct {
	OK      bool   `json:"ok"`
	Outcome string `json:"outcome"`
	Exit    int    `json:"exit,omitempty"`

	Seq     int64  `json:"seq,omitempty"`
	Applied *bool  `json:"applied,omitempty"`
	Actor   string `json:"actor,omitempty"`
	Host    string `json:"host,omitempty"`
	Server  string `json:"server,omitempty"`
	PubKey  string `json:"public_key,omitempty"`
	Hash    string `json:"hash,omitempty"`
	Size    int    `json:"size,omitempty"`
	Count   int    `json:"count,omitempty"`

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

// verdicts maps an invariant to what the agent should do next. The table is
// rendered here rather than passed through from the server, because a future
// server invariant must not become a retry storm in an unattended run.
var verdicts = map[string]string{
	"body.text.required":        "add --text and post again",
	"body.severity.invalid":     "add --severity p0|p1|p2|p3 and post again",
	"body.url.required":         "add --url and post again",
	"recipient.required":        "name a recipient with --to and post again",
	"recipient.forbidden":       "drop --to; this kind is ambient and interrupts nobody",
	"refs.question_required":    "point --refs at the question you are answering",
	"refs.exactly_one":          "name exactly one event in --refs",
	"refs.unknown":              "that event does not exist; check the seq you read",
	"refs.target_unknown":       "no such event in this room; check the seq and the room you read it from",
	"redact.already_redacted":   "it is already redacted; ask a human to purge it if the body must be erased",
	"redact.not_author":         "you cannot redact someone else's event; ask them, or ask a human to purge it",
	"attachment.unknown":        "upload the artifact first with: agent_comms attach",
	"attachment.title.required": "give the attachment a --title",
	"room.unknown":              "check the room name; list them with: agent_comms room",
	"kind.unknown":              "use a kind the server knows; see: agent_comms post --help",
	"idem.conflict":             "this key already carried different content; do not reuse keys",
	"signature.missing":         "stop. The client did not sign. A human must look at this",
	"signature.invalid":         "stop. Your key is not accepted. A human must re-enrol this seat",
	"enrolment.refused":         "stop. Ask a human for a fresh invite token",
	"parse.failed":              "stop. The client built a malformed command; this is a bug here",
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
		return "sleep, then batch what you were going to post"
	}
	return "stop"
}
