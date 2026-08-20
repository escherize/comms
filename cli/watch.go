package cli

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// watch turns the addressed lane into a wake-up.
//
// The transport was already push: /stream is SSE and `inbox --wait` is a push
// consumer. What was missing is a process that stays connected and does
// something when an event lands, so an agent that is not already running has no
// way to be reached. This is that process, and it is deliberately the smallest
// one that works — a loop around the same read path every other verb uses.
//
// The event is handed to the command as JSON **on stdin**, never in argv and
// never through a shell. That is not tidiness. The room is untrusted input by
// design — anyone with a seat can post, and a post is evidence rather than
// instruction — so interpolating an event's text into a command line is the
// exact injection the whole system is built to refuse. A handoff reading
// `; rm -rf ~` is a handoff, not a command, and stdin is what keeps it one.
func runWatch(e *Env, args []string) int {
	fs, sink := newFlags("watch")
	actor := fs.String("as", "", "the seat to watch")
	room := fs.String("room", "", "room to watch")
	every := fs.Duration("every", 15*time.Minute, "how long each wait blocks before reconnecting")
	once := fs.Bool("once", false, "handle one batch and exit, rather than looping")
	fs.Usage = func() {
		e.Out.HelpFS(fs, `comms watch --as <seat> -- <command> [args...]

Holds the addressed lane open and runs a command each time something arrives.
The event is handed to the command as JSON on stdin — never in argv, never
through a shell — because the room is untrusted input and a handoff that reads
like a command is still a handoff.

  comms watch --as agent:bcm/opencode-1 -- opencode run --stdin
  comms watch --as agent:bcm/pi-1 -- ./on-message.sh

The cursor advances only when the command exits 0, so a crashed handler is
retried on the next wake rather than silently dropped. That makes delivery
at-least-once: write the handler so a repeat is harmless.

With no command it prints the events and advances — inbox --wait in a loop,
useful for watching what a seat is being sent.`)
	}

	// Everything after -- is the command; the flag package stops there anyway.
	var command []string
	for i, a := range args {
		if a == "--" {
			command = args[i+1:]
			args = args[:i]
			break
		}
	}
	if err := fs.Parse(args); err != nil {
		if isHelp(err) {
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
	if *every > maxWait {
		return e.Out.Fail(ExitUsage, "usage", "wait.too_long",
			"--every is capped at "+maxWait.String())
	}
	inRoom := resolveRoom(seat, *room)

	e.Out.Note("watching %s for %s; waking %s", inRoom, seat, describeHandler(command))

	var backoff time.Duration
	for {
		o := readOpts{
			Actor: seat, Room: inRoom, Lane: LaneAddressed,
			Recipient: seat, Full: true,
			// Peek while a handler is configured: the cursor moves only after
			// the handler succeeds, so a crash re-delivers rather than drops.
			Peek: len(command) > 0,
			Wait: *every,
		}
		events, meta, err := drain(e, o)
		if err != nil {
			// A refused read is not a drop: reconnecting cannot make this seat
			// a member, so looping on it is the hot loop this verb must avoid.
			var rr *readRefused
			if errors.As(err, &rr) {
				return e.Out.Fail(ExitUsage, "usage", "room.unknown", rr.Error())
			}
			e.Out.Note("stream ended (%v); reconnecting", err)
			time.Sleep(2 * time.Second)
			if *once {
				return e.Out.Fail(ExitSpooled, "unreachable", "transport.failed", err.Error())
			}
			continue
		}

		var highest, failedSeq int64
		delivered := 0
		for _, f := range events {
			if len(command) == 0 {
				// Print-only still advances: without this, every interval
				// re-delivered the same events forever, and the help's
				// "prints the events and advances" was a lie.
				e.Out.Line(f.Data)
				delivered++
				if f.Seq > highest {
					highest = f.Seq
				}
				continue
			}
			if err := handOff(command, f.Data); err != nil {
				// The failure is a JSONL fact, not just a stderr note: a piped
				// supervisor runs with notes suppressed, which is exactly when
				// it needs to see this.
				e.Out.Line(map[string]any{
					"type": "handler_failed", "seq": f.Seq, "detail": err.Error(),
				})
				e.Out.Note("handler failed on %d (%v); it will be delivered again", f.Seq, err)
				// Stop advancing here: everything after this stays undelivered
				// too, so order is preserved on the retry.
				highest = 0
				failedSeq = f.Seq
				break
			}
			delivered++
			e.Out.Line(map[string]any{
				"type": "woke", "seq": f.Seq, "kind": f.Data["kind"],
				"author": f.Data["author"],
			})
			if f.Seq > highest {
				highest = f.Seq
			}
		}

		if highest > 0 {
			if err := SaveCursor(seat, inRoom, LaneAddressed, highest); err != nil {
				return e.Out.Fail(ExitInternal, "internal", "cursor.unwritable", err.Error())
			}
			reportDelivered(e, seat, inRoom, highest)
		}
		_ = meta

		if *once {
			// A handler crash is the handler's problem, not watch's: exit 0
			// (the event re-delivers next run) but count only what was actually
			// delivered, and say what was not.
			r := Result{Outcome: "watched", Count: delivered}
			if failedSeq > 0 {
				r.Detail = "handler failed on seq " + strconv.FormatInt(failedSeq, 10) +
					"; that event and everything after it will be delivered again"
			}
			return e.Out.Succeed(r)
		}
		// A failed handler re-delivers immediately on the next drain, which
		// without a pause is a hot loop: hundreds of spawns and stream dials a
		// second against an event that will fail the same way. Back off toward
		// --every, and reset the moment a batch succeeds.
		if failedSeq > 0 {
			if backoff == 0 {
				backoff = 2 * time.Second
			} else {
				backoff *= 2
			}
			if backoff > *every {
				backoff = *every
			}
			time.Sleep(backoff)
		} else {
			backoff = 0
		}
	}
}

// handOff runs the command with the event on stdin.
func handOff(command []string, event map[string]any) error {
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdin = strings.NewReader(string(body) + "\n")
	cmd.Stdout = os.Stderr // the handler's output is a note, not our JSONL
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func describeHandler(command []string) string {
	if len(command) == 0 {
		return "nothing (printing only)"
	}
	return strings.Join(command, " ")
}
