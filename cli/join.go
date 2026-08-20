package cli

import (
	"net/url"
	"regexp"
	"strings"
)

var setupFragment = regexp.MustCompile(`^setup=([0-9a-f]{32})$`)

// runJoin is onboarding as one act: hand it the same #setup= link a human
// clicks and it enrols the seat the token names, checks in, and wires the
// harness hook. The whole agent prompt collapses to install + join.
func runJoin(e *Env, args []string) int {
	fs, sink := newFlags("join")
	actorFlag := fs.String("as", "", "the seat to claim — needed only for a bootstrap link that names nobody")
	noHook := fs.Bool("no-hook", false, "enrol and check in, but do not wire the harness hook")
	fs.Usage = func() {
		e.Out.HelpFS(fs, `comms join '<hub>/#setup=<token>' [--as <seat>] [--no-hook]

One command from invited to working: parses the setup link (the same one a
human clicks), enrols the seat the token names, posts a check-in, and wires
the harness hook for that seat (run it at your project root, then restart
your session). The token is single-use; the hub is pinned to the seat, so
every later command needs only --as.

  comms join 'https://hub.example.com/#setup=0123abcd…'`)
	}

	var link string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		link = args[0]
		args = args[1:]
	}
	if err := fs.Parse(args); err != nil {
		if isHelp(err) {
			fs.Usage()
			return usageOK(e)
		}
		return e.Out.Fail(ExitUsage, "usage", "flags.invalid", strings.TrimSpace(sink.String()))
	}
	if link == "" {
		link = fs.Arg(0)
	}
	if link == "" {
		return e.Out.Fail(ExitUsage, "usage", "link.required",
			"paste the setup link: comms join '<hub>/#setup=<token>'")
	}

	u, err := url.Parse(link)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return e.Out.Fail(ExitUsage, "usage", "link.invalid",
			"that does not parse as a hub URL; the link looks like https://hub/#setup=<32 hex>")
	}
	m := setupFragment.FindStringSubmatch(u.Fragment)
	if m == nil {
		return e.Out.Fail(ExitUsage, "usage", "link.invalid",
			"the link carries no #setup=<token> fragment — copy it whole; the part after # matters")
	}
	token := m[1]
	e.Server = u.Scheme + "://" + u.Host

	// The token names its seat; ask the hub rather than making the caller
	// repeat what the link already carries.
	sent, err := postUnsigned(e.Server, "/invites/whose", map[string]any{"token": token})
	if err != nil {
		return e.Out.Fail(ExitSpooled, "unreachable", "transport.failed", err.Error())
	}
	if sent.Status != 200 {
		return e.Out.FailWith(Result{
			Outcome: "refused", Exit: ExitRefused,
			Invariant: orDefault(sent.Body.Invariant, "token.unknown"),
			Detail:    orDefault(sent.Body.Detail, "the hub does not recognise this token"),
		})
	}
	actor := sent.Body.Actor
	switch {
	case actor == "*" && *actorFlag == "":
		return e.Out.Fail(ExitUsage, "usage", "actor.required",
			"a bootstrap token names nobody; name yourself: comms join '<link>' --as human:you")
	case actor == "*":
		actor = *actorFlag
	case *actorFlag != "" && *actorFlag != actor:
		return e.Out.Fail(ExitUsage, "usage", "actor.mismatch",
			"this token enrols "+actor+", not "+*actorFlag+" — drop --as, or get a token for that seat")
	}

	if code := enrolKey(e, actor, token); code != ExitOK {
		return code
	}

	// Check in, signed with the key that just landed. A failure here is
	// reported but does not unwind the enrolment — the seat exists.
	priv, err := LoadSeat(actor)
	if err == nil {
		cmd := map[string]any{
			"room": resolveRoom(actor, ""), "author": actor, "kind": "presence",
			"body": map[string]any{"text": actor + " online"},
		}
		applyIdem(e, cmd, "")
		if posted, perr := NewClient(e.Server, actor, priv).Post(cmd); perr == nil && posted.Status == 200 {
			e.Out.Line(map[string]any{"type": "join", "step": "check-in", "seq": posted.Body.Seq})
		} else {
			e.Out.Note("check-in did not land; run comms join again, or post: comms post --as %s \"%s online\"", actor, actor)
		}
	}

	// Pin the seat to this project: the first agent user study found that a
	// join which still needs --as on every verb reads as unfinished, and the
	// participant only discovered the gap by failing.
	if err := writeRC(actor); err != nil {
		e.Out.Note("could not write .commsrc (%v); pass --as %s to verbs here", err, actor)
	}

	// The hook step must not fail the join: by now the single-use token is
	// spent and the seat exists, so a wiring refusal (wrong directory, no
	// harness detected) is reported as the remaining step, not as failure.
	// Restart comes first in the copy — a study participant flagged that it
	// was buried.
	next := "you are enrolled and can post now; your seat is pinned here in .commsrc, so verbs " +
		"in this project need no --as. Restart your session when you next can — that arms the " +
		"live feed; every verb works either way. Learn the room: comms ref, then comms skill comms"
	if !*noHook {
		if code := runHookInstall(e, actor, false, false); code != ExitOK {
			next = "you are enrolled, but the harness hook is not wired — run at your project root: " +
				"comms hook --install --seat " + actor + " — then restart your session"
		}
	}

	return e.Out.Succeed(Result{
		Outcome: "joined", Actor: actor, Server: e.Server,
		Next: next,
	})
}
