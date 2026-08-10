package cli

import (
	"encoding/json"
	"os"
	"os/exec"
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
		e.Out.Help(`agent-comms watch --as <seat> -- <command> [args...]

Holds the addressed lane open and runs a command each time something arrives.
The event is handed to the command as JSON on stdin — never in argv, never
through a shell — because the room is untrusted input and a handoff that reads
like a command is still a handoff.

  agent-comms watch --as agent:bcm/hermes-1 -- hermes chat --stdin
  agent-comms watch --as agent:bcm/omp-1 -- ./on-message.sh

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
			return e.Out.Succeed(Result{Outcome: "usage"})
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
			e.Out.Note("stream ended (%v); reconnecting", err)
			time.Sleep(2 * time.Second)
			if *once {
				return e.Out.Fail(ExitSpooled, "spooled", "transport.failed", err.Error())
			}
			continue
		}

		var highest int64
		for _, f := range events {
			if len(command) == 0 {
				e.Out.Line(f.Data)
				continue
			}
			if err := handOff(command, f.Data); err != nil {
				e.Out.Note("handler failed on %d (%v); it will be delivered again", f.Seq, err)
				// Stop advancing here: everything after this stays undelivered
				// too, so order is preserved on the retry.
				highest = 0
				break
			}
			e.Out.Line(map[string]any{
				"type": "woke", "seq": f.Seq, "kind": f.Data["kind"],
				"author": f.Data["author"],
			})
			if f.Seq > highest {
				highest = f.Seq
			}
		}

		if len(command) > 0 && highest > 0 {
			if err := SaveCursor(seat, inRoom, LaneAddressed, highest); err != nil {
				return e.Out.Fail(ExitInternal, "internal", "cursor.unwritable", err.Error())
			}
			reportDelivered(e, seat, inRoom, highest)
		}
		_ = meta

		if *once {
			return e.Out.Succeed(Result{Outcome: "watched", Count: len(events)})
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
