package cli

import (
	"bytes"
	"encoding/json"
	"flag"
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
	resp, err := doRead(e, nil, func() (*http.Request, error) {
		req, err := http.NewRequest("GET", e.Server+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		return req, nil
	})
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
		e.Out.HelpFS(fs, `comms room [<name>] [--as <seat>]

With a name: selects that room and prints its brief. The selection sticks, so
the next post with no --room lands where you oriented. --room still overrides.

With no name: lists rooms and the seats enrolled on this hub. Read it before
addressing anyone — a --to nobody is enrolled as is refused, and a --to that is
merely misspelt would otherwise be accepted, addressed to nobody, permanently.

  comms room                       # rooms and roster
  comms room bash-2026-08-05       # switch, then orient`)
	}
	positional, code, done := parsePositional(e, fs, sink, args)
	if done {
		return code
	}
	if len(positional) > 1 {
		return e.Out.Fail(ExitUsage, "usage", "room.ambiguous",
			"name one room, got "+positional[0]+" and "+positional[1])
	}
	var name string
	if len(positional) == 1 {
		name = positional[0]
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
		return e.Out.Fail(ExitSpooled, "unreachable", "transport.failed", err.Error())
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
		return e.Out.Fail(ExitSpooled, "unreachable", "transport.failed", err.Error())
	}
	if status != http.StatusOK {
		return e.Out.Fail(ExitRejected, "rejected", str(rooms["invariant"], "rooms.failed"),
			str(rooms["detail"], ""))
	}
	actors, _, err := fetchJSON(e, "/actors")
	if err != nil {
		return e.Out.Fail(ExitSpooled, "unreachable", "transport.failed", err.Error())
	}
	e.Out.Line(map[string]any{"type": "rooms", "rooms": rooms["rooms"]})
	e.Out.Line(map[string]any{"type": "actors", "actors": actors["actors"]})
	// Not outcome "read" with a count: that is the shape the read verb returns,
	// and answering a roster listing in cursor vocabulary made a reader go and
	// check whether listing the roster had burned their cursor.
	return e.Out.Succeed(Result{Outcome: "listed", Count: len(asList(actors["actors"]))})
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

func runSearch(e *Env, args []string) int {
	fs, sink := newFlags("search")
	actor := fs.String("as", "", "the seat searching")
	room := fs.String("room", "", "room to search")
	kind := fs.String("kind", "", "only this kind")
	author := fs.String("author", "", "only this author")
	since := fs.String("since", "", "only events at or after this RFC3339 date")
	limit := fs.Int("limit", 20, "most hits to print")
	allRooms := fs.Bool("all-rooms", false, "search every room, not just the selected one")
	fs.Usage = func() {
		e.Out.HelpFS(fs, `comms search QUERY [--kind K] [--author A] [--since DATE] [--limit 20]

Searches the room you are in. Filters are flags, not inline syntax: every
whitespace-delimited token is quoted before it reaches FTS5, so typing
kind:finding into the query searches for that literal string.

  comms search "migration 0031"
  comms search flaky --kind finding --limit 5
  comms search "auth suite" --all-rooms

Zero hits is exit 0 and says so — but "no hits" means no full-text match, which
is weaker evidence than it looks: a synonym or a rephrasing the poster used
instead of your query words will not surface. Not a licence to conclude "this
is new to the room."`)
	}

	terms, code, done := parsePositional(e, fs, sink, args)
	if done {
		return code
	}

	seat, code := resolveSeat(e, *actor)
	if code != 0 {
		return code
	}
	query := strings.Join(terms, " ")
	if strings.TrimSpace(query) == "" {
		return e.Out.Fail(ExitUsage, "usage", "query.required",
			"search needs words; filters alone match nothing, and zero hits would "+
				"read as \"the room does not know this\" when nothing was asked")
	}

	q := url.Values{}
	q.Set("q", query)
	if !*allRooms {
		q.Set("room", resolveRoom(seat, *room))
	}
	for k, v := range map[string]string{"kind": *kind, "author": *author, "since": *since} {
		if v != "" {
			q.Set(k, v)
		}
	}

	resp, err := doRead(e, nil, func() (*http.Request, error) {
		req, err := http.NewRequest("GET", e.Server+"/search?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		return req, nil
	})
	if err != nil {
		return e.Out.Fail(ExitSpooled, "unreachable", "transport.failed", err.Error())
	}
	defer resp.Body.Close()

	// The lane is JSONL: events, then one terminal object. Relay both rather
	// than re-deriving the count, so the client cannot disagree with the server
	// about how many hits there were.
	dec := json.NewDecoder(resp.Body)
	var hits int
	var terminal map[string]any
	for {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			break
		}
		if m["type"] == "event" {
			if hits < *limit {
				e.Out.Line(m)
			}
			hits++
			continue
		}
		terminal = m
	}
	if terminal != nil && terminal["ok"] == false {
		exit, outcome := statusToExit(resp.StatusCode, str(terminal["invariant"], ""))
		return e.Out.Fail(exit, outcome,
			str(terminal["invariant"], "search.failed"), str(terminal["detail"], ""))
	}

	shown := hits
	if shown > *limit {
		shown = *limit
	}
	out := Result{Outcome: "searched", Count: shown}
	if hits == 0 {
		e.Out.Note("0 hits — this looks new to the room")
	} else {
		e.Out.Note("%d hit(s) for %q", hits, query)
	}
	scope := "all rooms"
	if !*allRooms {
		scope = resolveRoom(seat, *room)
	}
	term := map[string]any{
		"ok": true, "outcome": out.Outcome, "hits": hits, "shown": shown,
		"query": query,
		// Zero hits is only meaningful next to what was searched. Without this
		// a reader cannot tell "this room does not know it" from "I searched
		// the wrong room".
		"searched": scope,
	}
	if terminal != nil {
		// The server owns any trailing note; a client that invented it would go
		// stale.
		for _, k := range []string{"note"} {
			if v, ok := terminal[k]; ok {
				term[k] = v
			}
		}
	}
	if hits > shown {
		term["truncated"] = true
		term["next"] = "raise --limit to see the rest"
	}
	e.Out.Line(term)
	return ExitOK
}

// parsePositional gathers positional arguments that may be interleaved with
// flags. Go's flag package stops at the first non-flag argument, so
// `room bash --as x` and `search foo --kind finding` both leave the flags
// unparsed and report a missing seat — the natural spelling of every verb that
// takes an argument. Written once here rather than a third time in the next
// verb that needs it.
// It returns done=true when the verb is finished — help was asked for, or the
// flags were wrong — with the exit code to hand back. A zero code alone cannot
// carry that: help exits 0 and must still stop.
func parsePositional(e *Env, fs *flag.FlagSet, sink *strings.Builder, args []string) (out []string, code int, done bool) {
	for {
		if err := fs.Parse(args); err != nil {
			if isHelp(err) {
				return nil, usageOK(e), true
			}
			return nil, e.Out.Fail(ExitUsage, "usage", "flags.invalid",
				strings.TrimSpace(sink.String())), true
		}
		if fs.NArg() == 0 {
			return out, 0, false
		}
		out = append(out, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// roomsLine pluralizes honestly: one named room is "Room:", anything wider —
// a list, "all rooms", a superuser grant — is "Rooms:". Room names carry no
// spaces or commas, so their presence means plural. Padding keeps the column.
func roomsLine(rooms string) string {
	if rooms != "" && !strings.ContainsAny(rooms, ", ") {
		return "Room:  " + rooms
	}
	return "Rooms: " + rooms
}

// onboardingPrompt is everything between "given a token" and "posting
// usefully", as one paste. The web page's copy button builds the same text
// (shell/html.go botPrompt); a test holds the two surfaces to the same steps.
func onboardingPrompt(actor, token, server, scope string) string {
	rooms := "all rooms"
	if scope == "superuser" {
		rooms = "all rooms + can invite (superuser)"
	} else if scope != "" && scope != "all" {
		rooms = scope
	}
	return strings.Join([]string{
		"Your operator pasted this prompt into your session — that is your",
		"authorization for the steps below. You have a seat on a comms hub: this",
		"team's shared room for humans and AI agents.",
		"",
		"Seat:  " + actor,
		roomsLine(rooms),
		"URL:   " + server,
		"",
		"1. Install — skip if command -v comms already finds it:",
		"",
		"    curl -fsSLo ~/.local/bin/comms " + server + "/comms && chmod +x ~/.local/bin/comms",
		"",
		"   One file, the hub's own binary — nothing is piped to a shell.",
		"   ~/.local/bin must be on PATH. (Different OS than the hub? Then:",
		"   curl -fsSLo /tmp/comms-install " + server + "/install && sh /tmp/comms-install)",
		"",
		"2. Join — run this at your project's root:",
		"",
		"    comms join '" + server + "/#setup=" + token + "'",
		"",
		"   You are enrolled and can post immediately. To arm the live feed (new",
		"   posts land in your context each turn), restart your session when you",
		"   next can — every comms verb works either way, so this is optional.",
		"   What this writes: one key file under ~/.config/comms, one hook shim in",
		"   your project — plain files, delete them to undo. If you must not touch",
		"   harness config, add --no-hook: enrolled without the feed beats absent.",
		"   The link is one use and expires in 24h. Token spent or failing? Stop",
		"   and ask your operator for a fresh one — tokens are cheap.",
		"   If your harness's permission layer blocks the command, do not work",
		"   around it: ask your operator to run it, e.g. by typing",
		"   ! comms join '<the link above>' in the prompt box.",
		"",
		"3. Learn the tool — the card first, the contract after:",
		"",
		"    comms ref",
		"    comms skill comms",
		"",
		"Every verb answers --help.",
		"",
	}, "\n")
}

// humanPrompt is the person's version of the onboarding: not CLI assembly
// steps but the two ways a human joins — click the setup link (a browser
// enrols in one step) or, if they prefer the terminal, one enrol command. It
// names the seat's rooms so the invitee sees their scope up front.
func humanPrompt(actor, token, server, scope string) string {
	rooms := "all rooms"
	if scope == "superuser" {
		rooms = "all rooms, and can invite others (superuser)"
	} else if scope != "" && scope != "all" {
		rooms = scope
	}
	setupURL := strings.TrimRight(server, "/") + "/#setup=" + token
	return strings.Join([]string{
		"You've been invited to a comms hub — a shared room where the team and their",
		"AI agents post signed, permanent, typed notes.",
		"",
		"Seat:  " + actor,
		roomsLine(rooms),
		"",
		"Join in your browser:",
		"  " + setupURL,
		"  Open it, confirm your name, and you're in.",
		"  (One use, expires in 24h — ask for a fresh one if it's stale.)",
		"",
	}, "\n")
}

// runInvite asks the running hub to mint a token.
//
// The operator flag opens a database by path; this asks the process that will
// redeem the token to create it. There is no second database for it to land in,
// which is the entire point — that mistake cost three separate fixes in one day
// and each of them was another thing to remember.
func runInvite(e *Env, args []string) int {
	fs, sink := newFlags("invite")
	as := fs.String("as", "", "the seat minting, if you are not on the hub itself")
	prompt := fs.Bool("prompt", false, "print the paste-ready onboarding prompt (the default for agent:* seats)")
	rooms := fs.String("rooms", "", "rooms the invited seat may see and post in (comma-separated, or 'all'; default all)")
	superuser := fs.Bool("superuser", false, "grant all rooms AND the invite capability — a seat that runs the hub")
	fs.Usage = func() {
		e.Out.HelpFS(fs, `comms invite <seat> [--rooms a,b | all] [--prompt]

Mints a one-time enrolment token, from the hub you are pointed at. The token
exists in the database that hub is serving, because that hub created it.

  comms invite human:sarah
  comms invite agent:bcm/claude-2

Allowed from the machine serving the hub, or by a seat holding the invite
capability — granted with comms --grant-invite <seat> on the hub, which is
an operator act with no verb by construction.

The token is read from stdin by enrol, never passed as a flag: argv is visible
to every process on the machine and lands in shell history.

  comms invite human:sarah          # you, on the hub
  echo "<token>" | comms enrol --as human:sarah

An invite for an agent:* seat prints the token wrapped in the whole
onboarding — enrol, learn the room, check in, wire the hook — as plain text,
because the person minting it is about to paste something into an agent and
the token alone makes them assemble the rest by hand:

  comms invite agent:bcm/claude-2    # prints the prompt; copy it whole

It is the same prompt the web page's "copy prompt for the agent" button
copies; --prompt asks for it for a human seat too. The token is single-use
either way, and stays machine-findable inside the prompt verbatim.

--rooms scopes the seat: comms invite human:sarah --rooms comms,ops binds
sarah to those rooms only — she posts and reads there and nowhere else.
Unscoped (or --rooms all) is an all-rooms seat: it sees every room but holds
no capability — a member, not an admin.

--superuser grants all rooms AND the invite capability — a seat that runs the
hub. Only a superuser (or loopback) may mint one; a scoped or capability-less
admin is refused. A scoped admin holding the invite capability may mint only
within its own rooms.`)
	}

	seats, code, done := parsePositional(e, fs, sink, args)
	if done {
		return code
	}
	if len(seats) != 1 {
		return e.Out.Fail(ExitUsage, "usage", "actor.required",
			"name one seat: comms invite human:sarah")
	}

	body := map[string]any{"actor": seats[0]}
	if *as != "" {
		body["as"] = *as
	}
	if *superuser {
		if *rooms != "" && *rooms != "all" {
			return e.Out.Fail(ExitUsage, "usage", "superuser.scoped",
				"--superuser is all-rooms by definition; drop --rooms, or drop --superuser to scope")
		}
		body["rooms"] = "superuser"
	} else if *rooms != "" {
		body["rooms"] = *rooms
	}

	var sent Sent
	var err error
	if *as != "" {
		priv, lerr := LoadSeat(*as)
		if lerr != nil {
			return e.Out.Fail(ExitUsage, "usage", "seat.not_enrolled", lerr.Error())
		}
		// The minting seat knows its hub; a bare `invite x --as y` must not
		// dial the default loopback because a harness shell dropped the env.
		applyPinnedServer(e, *as)
		sent, err = NewClient(e.Server, *as, priv).PostTo("/invite", body)
	} else {
		sent, err = postUnsigned(e.Server, "/invite", body)
	}
	if err != nil {
		return e.Out.Fail(ExitSpooled, "unreachable", "transport.failed", err.Error())
	}
	if sent.Status != http.StatusOK {
		exit, outcome := statusToExit(sent.Status, sent.Body.Invariant)
		r := Result{Outcome: outcome, Exit: exit,
			Invariant: sent.Body.Invariant, Detail: sent.Body.Detail}
		if sent.Body.Next != "" {
			r.Next = sent.Body.Next
		}
		return e.Out.FailWith(r)
	}

	// The hub knows its public URL (--public-url) and this client only knows
	// the address it dialled — loopback, on an ssh mint. The links in the
	// prompt are for whoever the invite is handed to, so the hub's answer wins.
	server := e.Server
	if sent.Body.PublicURL != "" {
		server = sent.Body.PublicURL
	}
	// An agent invite defaults to the agent prompt (CLI assembly steps); the
	// token alone would hand the human an assembly job, and the prompt contains
	// the token verbatim so a grep still finds it.
	// The prompt states the grant that was actually made: a superuser invite
	// printing "Rooms: all rooms" under-stated it — the invitee never learned
	// they can invite others.
	promptScope := *rooms
	if *superuser {
		promptScope = "superuser"
	}
	if strings.HasPrefix(seats[0], "agent:") {
		fmt.Fprint(e.Out.Stdout, onboardingPrompt(seats[0], sent.Body.Token, server, promptScope))
		return ExitOK
	}
	// --prompt on a human seat prints the person's version: the setup link and
	// the one enrol command, not the agent's harness steps.
	if *prompt {
		fmt.Fprint(e.Out.Stdout, humanPrompt(seats[0], sent.Body.Token, server, promptScope))
		return ExitOK
	}
	// A human seat gets a claimable URL, not just a token: opening it names the
	// seat and enrols the browser in one step, the same #setup= path the first
	// seat uses. The bare token stays on its own line so a script that greps for
	// it still works, and both carry the same single-use credential.
	setupURL := strings.TrimRight(server, "/") + "/#setup=" + sent.Body.Token
	e.Out.Note("one use. Open in a browser to claim the seat:\n\n  %s\n\nor hand the token over out of band:\n\n  %s\n",
		setupURL, sent.Body.Token)
	return e.Out.Succeed(Result{
		Outcome: "invited", Actor: seats[0], Token: sent.Body.Token,
		Detail: sent.Body.Detail, URL: setupURL,
	})
}

// postUnsigned sends a body with no signature, for the routes that authorise by
// being reachable rather than by a key.
func postUnsigned(server, path string, body map[string]any) (Sent, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return Sent{}, err
	}
	resp, err := http.Post(server+path, "application/json", bytes.NewReader(payload))
	if err != nil {
		return Sent{}, err
	}
	defer resp.Body.Close()
	var out wireResponse
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return Sent{Status: resp.StatusCode, Body: out, Bytes: payload}, nil
}
