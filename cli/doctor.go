package cli

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// runDoctor is one command that answers "why isn't comms working here" —
// asked for by name in three separate agent studies, and the diagnosis for
// every operator incident this tool has had so far: a stale binary shadowing
// a fresh build, a hub serving the wrong database, a hub that is simply down,
// a seat that never enrolled, a hook that never armed. Each check is one
// JSONL line; the terminal object counts the problems. Exit 0 either way —
// a diagnosis is not a failure.
func runDoctor(e *Env, args []string) int {
	fs, sink := newFlags("doctor")
	actor := fs.String("as", "", "the seat to check (default: COMMS_ACTOR, then .commsrc)")
	fs.Usage = func() {
		e.Out.HelpFS(fs, `comms doctor [--as <seat>]

Checks the whole chain, one line per check: binary, seat, hub, version drift,
hook wiring, spool. Run it first when anything seems wrong.`)
	}
	if err := fs.Parse(args); err != nil {
		if isHelp(err) {
			return usageOK(e)
		}
		return e.Out.Fail(ExitUsage, "usage", "flags.invalid", strings.TrimSpace(sink.String()))
	}

	problems := 0
	check := func(name string, ok bool, detail string) {
		if !ok {
			problems++
		}
		e.Out.Line(map[string]any{"type": "check", "name": name, "ok": ok, "detail": detail})
		mark := "ok"
		if !ok {
			mark = "PROBLEM"
		}
		e.Out.Note("%-14s %-7s %s", name, mark, detail)
	}

	// The binary itself.
	exe, _ := os.Executable()
	check("binary", exe != "", buildID()+" at "+exe)

	// The seat: flag, env, project pin — the same order every verb resolves.
	seat := *actor
	if seat == "" {
		seat, _ = e.getenv("COMMS_ACTOR")
	}
	rc := rcSeat()
	if seat == "" {
		seat = rc
	}
	switch {
	case seat == "":
		check("seat", false, "no seat: pass --as, set COMMS_ACTOR, or comms join here (.commsrc pins it)")
	case !HasSeat(seat):
		check("seat", false, seat+" has no key on this machine — comms enrol --as "+seat)
	default:
		src := "explicit"
		if seat == rc {
			src = ".commsrc"
		}
		check("seat", true, seat+" ("+src+"; key present)")
	}

	// The hub: the seat's pin wins, exactly as verbs resolve it.
	if seat != "" {
		e.Seat = seat
		applyPinnedServer(e, seat)
	}
	base := strings.TrimRight(e.Server, "/")
	hc := &http.Client{Timeout: 3 * time.Second}
	resp, err := hc.Head(base + "/comms")
	if err != nil {
		check("hub", false, base+" unreachable: "+err.Error())
	} else {
		resp.Body.Close()
		check("hub", resp.StatusCode == http.StatusOK, base)
		// Version drift, while we hold the hub's build header.
		hub := resp.Header.Get("X-Comms-Build")
		self := ""
		if exe != "" {
			self, _ = fileSHA256(exe)
		}
		switch {
		case hub == "" || self == "":
			check("build-match", true, "not comparable (older hub or unreadable binary)")
		case hub == self:
			check("build-match", true, "this binary is the hub's exact build")
		case Version == "":
			check("build-match", false, "differs from the hub's build (source build — self-update leaves it alone)")
		case resp.Header.Get("X-Comms-Platform") != runtime.GOOS+"/"+runtime.GOARCH:
			check("build-match", false, "differs, and the hub's platform does not match — update by hand")
		default:
			check("build-match", false, "differs from the hub's build; the next verb self-updates")
		}
	}

	// The hook: is this project (or the machine) wired for the seat?
	if only := activeHarness(e); only != "" {
		wired := false
		var looked []string
		for _, s := range hookShims() {
			if s.Name != only {
				continue
			}
			for _, rel := range []string{s.Project, s.Global} {
				root := "."
				if rel == s.Global {
					root, _ = os.UserHomeDir()
				}
				p := filepath.Join(root, rel)
				looked = append(looked, p)
				if _, err := os.Stat(p); err == nil {
					wired = true
				}
			}
		}
		check("hook", wired, only+" — looked at "+strings.Join(looked, ", ")+
			"; wire with: comms hook --install")
	} else {
		check("hook", true, "no harness marker in this shell; skipped")
	}

	// The spool: held writes waiting for the hub to come back.
	if entries, err := os.ReadDir(spoolDir()); err == nil && len(entries) > 0 {
		check("spool", false, strconv.Itoa(len(entries))+" held write(s) — the next write verb sends them in order")
	} else {
		check("spool", true, "empty")
	}

	if problems == 0 {
		return e.Out.Succeed(Result{Outcome: "healthy"})
	}
	return e.Out.Succeed(Result{Outcome: "doctor", Count: problems,
		Detail: "problems found; each PROBLEM line names its fix"})
}
