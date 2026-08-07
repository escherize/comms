package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// SelectedRoom is the room this seat is working in. Every chat tool in
// existence treats naming a room as switching to it, and an agent that orients
// into bash-2026-08-05 and then posts into core has written a wrong-room event
// into a log that cannot take it back.
func SelectedRoom(actor string) string {
	raw, err := os.ReadFile(roomPath(actor))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// SelectRoom persists the selection for this seat.
func SelectRoom(actor, room string) error {
	if err := os.MkdirAll(stateDir(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(roomPath(actor), []byte(room+"\n"), 0o600)
}

func roomPath(actor string) string {
	safe := strings.NewReplacer("/", "_", ":", "_", string(filepath.Separator), "_").Replace(actor)
	return filepath.Join(stateDir(), safe+".room")
}

// resolveRoom is the precedence: an explicit --room, then the selection, then
// core. The flag wins so a one-off post into another room never needs a switch
// and a switch back.
func resolveRoom(actor, flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if sel := SelectedRoom(actor); sel != "" {
		return sel
	}
	return "core"
}

// fetchJSON reads one JSON object from a lane.
func fetchJSON(e *Env, path string) (map[string]any, int, error) {
	req, err := http.NewRequest("GET", e.Server+path, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("unreadable response: %s", strings.TrimSpace(string(raw)))
	}
	return out, resp.StatusCode, nil
}

func runRoom(e *Env, args []string) int {
	fs, sink := newFlags("room")
	actor := fs.String("as", "", "the seat orienting")
	brief := fs.Bool("brief", true, "print the room brief after selecting")
	fs.Usage = func() {
		e.Out.Help(`agent_comms room [<name>] [--as <seat>]

With a name: selects that room and prints its brief. The selection sticks, so
the next post with no --room lands where you oriented. --room still overrides.

With no name: lists rooms and the seats enrolled on this hub. Read it before
addressing anyone — a --to nobody is enrolled as is refused, and a --to that is
merely misspelt would otherwise be accepted, addressed to nobody, permanently.

  agent_comms room                       # rooms and roster
  agent_comms room bash-2026-08-05       # switch, then orient`)
	}
	// Go's flag package stops at the first non-flag argument, so `room bash
	// --as x` would leave --as unparsed and report actor.required. The room
	// name is a positional in the natural spelling; take it, then parse on.
	var name string
	for {
		if err := fs.Parse(args); err != nil {
			if isHelp(err) {
				return e.Out.Succeed(Result{Outcome: "usage"})
			}
			return e.Out.Fail(ExitUsage, "usage", "flags.invalid", strings.TrimSpace(sink.String()))
		}
		if fs.NArg() == 0 {
			break
		}
		if name != "" {
			return e.Out.Fail(ExitUsage, "usage", "room.ambiguous",
				"name one room, got "+name+" and "+fs.Arg(0))
		}
		name = fs.Arg(0)
		args = fs.Args()[1:]
	}

	seat, code := resolveSeat(e, *actor)
	if code != 0 {
		return code
	}

	if name == "" {
		return listRoomsAndActors(e)
	}

	body, status, err := fetchJSON(e, "/rooms/"+url.PathEscape(name))
	if err != nil {
		return e.Out.Fail(ExitSpooled, "spooled", "transport.failed", err.Error())
	}
	if status != http.StatusOK {
		return e.Out.Fail(ExitRejected, "rejected",
			str(body["invariant"], "room.unknown"), str(body["detail"], ""))
	}
	if err := SelectRoom(seat, name); err != nil {
		return e.Out.Fail(ExitInternal, "internal", "room.unwritable", err.Error())
	}
	if *brief {
		if b, ok := body["brief"].(map[string]any); ok {
			e.Out.Line(map[string]any{"type": "brief", "brief": b})
			noteBrief(e, name, b)
		}
	}
	return e.Out.Succeed(Result{Outcome: "selected", Room: name})
}

func listRoomsAndActors(e *Env) int {
	rooms, status, err := fetchJSON(e, "/rooms")
	if err != nil {
		return e.Out.Fail(ExitSpooled, "spooled", "transport.failed", err.Error())
	}
	if status != http.StatusOK {
		return e.Out.Fail(ExitRejected, "rejected", str(rooms["invariant"], "rooms.failed"),
			str(rooms["detail"], ""))
	}
	actors, _, err := fetchJSON(e, "/actors")
	if err != nil {
		return e.Out.Fail(ExitSpooled, "spooled", "transport.failed", err.Error())
	}
	e.Out.Line(map[string]any{"type": "rooms", "rooms": rooms["rooms"]})
	e.Out.Line(map[string]any{"type": "actors", "actors": actors["actors"]})
	return e.Out.Succeed(Result{Outcome: "read", Count: len(asList(actors["actors"]))})
}

// noteBrief is the human's version: three lines, not a JSON object.
func noteBrief(e *Env, room string, b map[string]any) {
	var open, stalled int
	for _, q := range asList(b["questions"]) {
		if m, ok := q.(map[string]any); ok && m["answered"] != true {
			open++
		}
	}
	var working []string
	for _, w := range asList(b["working"]) {
		m, ok := w.(map[string]any)
		if !ok {
			continue
		}
		mark := ""
		if m["stalled"] == true {
			stalled++
			mark = " (stalled)"
		}
		working = append(working, fmt.Sprintf("%v %v/%v%s", m["author"], m["step"], m["of"], mark))
	}
	e.Out.Note("%s: %d open question(s), %d stalled", room, open, stalled)
	if len(working) > 0 {
		e.Out.Note("in flight: %s", strings.Join(working, ", "))
	}
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func str(v any, fallback string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return fallback
}
