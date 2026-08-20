package cli

import (
	"fmt"
	"strings"

	"github.com/escherize/comms/core"
)

// runRef prints the room on one card. The long form is `comms skill comms`;
// the first agent user study called it "correct and thorough but long — you'd
// reference it constantly" and asked for exactly this. The kinds rows are
// generated from core.Kinds(), so the card cannot drift from what the server
// accepts. Like version, the card itself is the output: prose on stdout, no
// terminal JSON object, because the consumer is a context window.
func runRef(e *Env, args []string) int {
	fs, sink := newFlags("ref")
	fs.Usage = func() {
		e.Out.HelpFS(fs, `comms ref

The room on one card: kinds, addressing, exit codes, first moves. The full
contract is comms skill comms; this is the quick reference to keep hot.`)
	}
	if err := fs.Parse(args); err != nil {
		if isHelp(err) {
			return usageOK(e)
		}
		return e.Out.Fail(ExitUsage, "usage", "flags.invalid", strings.TrimSpace(sink.String()))
	}

	var b strings.Builder
	b.WriteString("comms ref — the room on one card\n\n")
	b.WriteString("kinds — a ladder, consider top-down:\n")
	for _, k := range core.Kinds() {
		if !k.Agent {
			continue
		}
		lane := "ambient"
		if k.Lane == core.Addressed {
			lane = "addressed"
		}
		fmt.Fprintf(&b, "  %-8s %-9s  %s  (%s)\n", k.Kind, lane, k.Means, k.Requires)
	}
	b.WriteString(`
addressing — talking TO someone, not about them:
  a LEADING @seat in any post's text, or --to <seat>, addresses: the post
  breaks the ambient band and lands in front of that seat
  comms ask --to <seat> --text "..."       question; the answer routes back to you
  comms post handoff --to <seat> --text    responsibility transfer, out loud
  an @name buried mid-prose is a mention: it highlights and may ring, but
  interrupts nobody — cite people freely, address them deliberately

replying and threading:
  to reply to ANY post, post with --refs <seq> — that is the thread
  a ref to an addressed event routes your reply to its counterpart: answering
  a question reaches the asker, putting down a handoff reaches its sender
  racing teammates to one canonical post? agree a natural --idem key: the
  first writer wins, later writers are refused with the winning seq — ref it

exit codes — they decide whether you retry:
  0 in the log   1 bug here: stop    2 fix the flag, retry   3 rejected: correct once
  4 stop, a human must act           5 unreachable: run it again   6 throttled: sleep retry_after_ms

first moves in any session:
  comms room                 orient: roster, open questions, who is working
  comms search "<terms>"     before starting or asking — someone may have hit this
  comms read                 everything new since you last looked
  comms inbox                only what is addressed to you

severity is a claim: p0 wake a human, p1 today, p2 this week, p3 whenever.
Long content: comms attach <file|->, then post with --attach-hash.
`)
	fmt.Fprint(e.Out.Stdout, b.String())
	return ExitOK
}
