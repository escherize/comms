// Command comms serves the coordination hub: one binary, one SQLite file,
// one browser page.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	_ "embed"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/escherize/comms/cli"
	"github.com/escherize/comms/shell"
	"github.com/escherize/comms/store"
)

// The skills travel inside the binary (docs/*-SKILL.md are the sources), so
// onboarding an agent or an operator is the binary plus one verb — no
// repository checkout on the machine that only runs the client. First entry
// is the primary: what a bare `skill` prints.
//
//go:embed docs/AGENT-SKILL.md
var agentSkill string

//go:embed docs/HUB-SKILL.md
var hubSkill string

func main() {
	addr := flag.String("addr", "127.0.0.1:7777", "listen address")
	// COMMS_DB is the default when set, so a shell that exports it once
	// stops every "wrong database" mistake at the source. The flag still wins:
	// an explicit -db is a deliberate act and must not be overridden by an
	// environment variable somebody forgot they set.
	db := flag.String("db", envOr("COMMS_DB", "comms.db"),
		"path to the event log (default $COMMS_DB, else ./comms.db)")
	rooms := flag.String("rooms", "core", "comma-separated rooms to ensure at startup")
	publicURL := flag.String("public-url", envOr("COMMS_PUBLIC_URL", ""),
		"public base URL of this hub (e.g. https://comms.example.com); used in printed invite links, which otherwise name the loopback address")
	asSeat := flag.String("as", "", "enrol this seat as the hub owner at startup (grants invite; the key is written locally)")
	seed := flag.Bool("seed", false, "seed the log with a demo working session")
	insecure := flag.Bool("insecure", false, "accept unsigned commands (localhost demos only)")
	readAuth := flag.Bool("read-auth", false,
		"deprecated no-op: reads are always authenticated now")
	invite := flag.String("invite", "", "mint a one-time enrolment token for this actor and exit")
	purge := flag.Int64("purge", 0, "erase one event's body and attachments permanently, then exit")
	flagged := flag.String("flagged", "", "list events authored by a compromised key, then exit")
	revoke := flag.String("revoke", "", "revoke a seat's key — future commands are refused, history stays valid — then exit")
	compromise := flag.String("compromised-key", "", "mark a seat's key compromised (seat, or seat=RFC3339 suspected-since), then exit")
	grantInvite := flag.String("grant-invite", "", "let a seat mint enrolment tokens remotely, then exit")
	rebuild := flag.Bool("rebuild", false, "recompute every log-derived projection from the log, then exit")
	verify := flag.Bool("verify", false, "check the log chain end to end, then exit")
	seqReport := flag.Bool("seq-report", false, "print the head and the next seq, then exit")
	// Verb form: comms <verb> ... is the agent client (ADR-0012). Flag
	// form is the operator surface. A verb is never also a flag.
	//
	// This runs before flag.Parse because the client owns its own flags: parsing
	// the operator set first makes `comms post --text x` die on an unknown
	// flag instead of reaching the client.
	args := os.Args[1:]
	// `serve` is a verb because starting the hub is the first thing anyone does,
	// and it has to be in the list they get when they type the binary's name.
	// Ticket 19 made the bare binary print the verbs, which is right; the README
	// still said the bare binary serves, which then was not true. Naming it
	// makes both true rather than picking one. It is a prefix, not a second
	// implementation: it drops itself and the operator path runs as it always
	// did, so there is nothing to keep in sync.
	serveVerb := len(args) > 0 && args[0] == "serve"
	if serveVerb {
		// A fresh slice. args aliases os.Args[1:], so appending into
		// os.Args[:1] overwrites the elements being copied out of it — the
		// second flag lands where the first was read from, and `serve -db
		// demo.db` becomes `-db` followed by nothing, then `demo.db` as a verb.
		rest := make([]string, 0, len(args)-1)
		rest = append(rest, args[1:]...)
		os.Args = append([]string{os.Args[0]}, rest...)
		args = rest
	}
	// The operator flag set prints double-dash: Go's flag package accepts one
	// dash and two interchangeably, but the help must show the standard form.
	flag.Usage = func() {
		cli.Std().Help(`comms serve — the hub, and the operator actions on its database

usage: comms serve [--db <path>] [--rooms <list>] [flags]

  comms serve                                  # 127.0.0.1:7777, ./comms.db
  comms serve --db demo.db --seed --rooms core,bash
  comms serve --addr 0.0.0.0:7777              # reachable from the tailnet

Most flags below are operator actions that touch the database and exit
(--invite, --verify, --rebuild, --purge) rather than serve.

%s`, cli.FlagsHelp(flag.CommandLine))
	}
	// -h-server is the escape hatch: the operator flag set, which the client
	// form now shadows.
	if len(args) == 1 && (args[0] == "-h-server" || args[0] == "--h-server") {
		flag.Usage()
		return
	}
	// A bare `comms serve` leaves zero args, which must mean "serve with
	// defaults", not the no-verb client help.
	clientForm := !serveVerb && (len(args) == 0 ||
		!strings.HasPrefix(args[0], "-") ||
		args[0] == "-h" || args[0] == "--help" || args[0] == "help" ||
		args[0] == "--version" || args[0] == "-version")
	if clientForm {
		cli.Skills = []cli.SkillDoc{{Doc: agentSkill}, {Doc: hubSkill}}
		os.Exit(cli.Run(&cli.Env{
			Out:    cli.Std(),
			Stdin:  os.Stdin,
			Server: envOr("COMMS_SERVER", cli.DefaultServer),
			Host:   hostname(),
		}, args))
	}

	flag.Parse()

	st, err := store.Open(*db)
	if err != nil {
		log.Fatalf("open %s: %v", *db, err)
	}
	defer st.Close()

	// An operator action on a database nothing has ever served is almost always
	// the wrong database. -db defaults to comms.db relative to the working
	// directory, and every operator flag will happily create one — so running
	// -invite from the wrong directory mints a real token into a file the hub
	// has never opened, and the only symptom is "unknown enrolment token" much
	// later, pointing at the token. That has cost two sessions in one day.
	if *invite != "" || *purge > 0 || *flagged != "" || *revoke != "" || *compromise != "" {
		if rooms, err := st.Rooms(); err == nil && len(rooms) == 0 {
			abs, _ := filepath.Abs(*db)
			log.Fatalf("refusing to act on %s: it has no rooms, so no hub has ever "+
				"served it — this is almost certainly not the database you meant.\n\n"+
				"A running hub prints the file it serves at startup. Pass that path "+
				"with --db.\n\n"+
				"If you really are setting up a new hub, start it once first:\n"+
				"  comms serve --db %s --rooms core", abs, *db)
		}
	}

	if *invite != "" {
		tok, err := st.MintInvite(*invite, store.ScopeAll, time.Now())
		if err != nil {
			log.Fatalf("invite: %v", err)
		}
		abs, _ := filepath.Abs(*db)
		fmt.Printf("enrolment token for %s:\n\n  %s\n\n"+
			"One use. Hand it over out of band.\n\n"+
			"Minted into %s — the token only exists in this database.\n"+
			"The server redeeming it must be running with the same --db, or the\n"+
			"token will come back as unknown.\n", *invite, tok, abs)
		return
	}

	// Operator capabilities. Flags on the server binary rather than verbs an
	// agent seat can reach, because both act on other actors' events and the
	// only credential they need is holding the database.
	if *verify {
		// The question a restore actually raises: did we get the whole file, or
		// a torn one? A non-zero exit is what lets a drill script fail.
		if err := st.Verify(); err != nil {
			fmt.Fprintf(os.Stderr, "the log chain does not verify: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("the log chain verifies end to end")
		return
	}

	if *seqReport {
		fmt.Printf("head %d, next %d (every start jumps %d, so a seq issued before a "+
			"restore can never be issued again)\n", st.Head(), st.NextSeq(), store.SeqJump)
		return
	}

	if *rebuild {
		if err := st.Rebuild(); err != nil {
			log.Fatalf("rebuild: %v", err)
		}
		fmt.Println("rebuilt every log-derived projection from the log. " +
			"Keys, invites and capabilities were not touched: they are records, not projections")
		return
	}

	if *grantInvite != "" {
		if err := st.Grant(*grantInvite, shell.CapInvite, "operator", time.Now()); err != nil {
			log.Fatalf("grant-invite: %v", err)
		}
		fmt.Printf("granted %q the right to mint enrolment tokens without being on the hub\n",
			*grantInvite)
		return
	}

	if *purge > 0 {
		if err := st.Purge(*purge); err != nil {
			log.Fatalf("purge: %v", err)
		}
		fmt.Printf("purged the body and attachments of %d; the event and the chain remain\n", *purge)
		return
	}

	if *revoke != "" {
		if err := st.RevokeKey(*revoke, time.Now()); err != nil {
			log.Fatalf("revoke: %v", err)
		}
		fmt.Printf("revoked %s: future commands are refused; history stays valid.\n"+
			"Re-enrol with a fresh invite to rotate the key.\n", *revoke)
		return
	}

	if *compromise != "" {
		actor, sinceStr, _ := strings.Cut(*compromise, "=")
		var since time.Time
		if sinceStr != "" {
			t, err := time.Parse(time.RFC3339, sinceStr)
			if err != nil {
				log.Fatalf("compromised-key: %q is not RFC3339 (e.g. 2026-08-18T00:00:00Z)", sinceStr)
			}
			since = t
		} else if k, ok := st.KeyFor(actor); ok {
			// Unknown compromise time: flag from the key's activation.
			// Over-flagging is recoverable, under-flagging is not.
			since = k.ActiveFrom
		}
		if err := st.MarkCompromised(actor, since); err != nil {
			log.Fatalf("compromised-key: %v", err)
		}
		fmt.Printf("marked %s compromised as of %s; review its events with --flagged %s\n",
			actor, since.UTC().Format(time.RFC3339), actor)
		return
	}

	if *flagged != "" {
		recs, err := st.FlaggedEvents(*flagged)
		if err != nil {
			log.Fatalf("flagged: %v", err)
		}
		if len(recs) == 0 {
			fmt.Printf("nothing flagged for %s; mark the key compromised first\n", *flagged)
			return
		}
		fmt.Printf("%d events authored by %s at or after its suspected compromise:\n", len(recs), *flagged)
		for _, r := range recs {
			fmt.Printf("  %d  %s  %s  %s\n", r.Seq,
				r.ServerTS.Format(time.RFC3339), r.Kind, r.Text())
		}
		return
	}

	// Ensuring rooms is what turns a file into a hub, so it belongs to serving.
	// Doing it for every operator action erased the only evidence that a
	// database had never been served, which is exactly the check below needs.
	for _, r := range strings.Split(*rooms, ",") {
		if r = strings.TrimSpace(r); r != "" {
			if !shell.ValidRoomName(r) {
				log.Fatalf("--rooms %q: a room name is 1-32 of a-z 0-9 - _, starting with a letter or digit", r)
			}
			if err := st.EnsureRoom(r); err != nil {
				log.Fatalf("ensure room %s: %v", r, err)
			}
		}
	}

	if *seed {
		if err := seedDemo(st); err != nil {
			log.Fatalf("seed: %v", err)
		}
	}

	if err := st.Verify(); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: log chain does not verify: %v\n", err)
	}

	srv := shell.New(st, time.Now)
	srv.PublicURL = strings.TrimRight(*publicURL, "/")
	// The hub-served installer pins clients to this build's version, so a
	// client installed through the hub can never be skewed against it.
	shell.InstallVersion = cli.Version
	if *insecure {
		srv.RequireSignature = false
		log.Printf("WARNING: --insecure is set. Unsigned commands are accepted, so anyone " +
			"who can reach this port can post as anyone. Localhost demos only.")
	}
	if *readAuth {
		// Reads are always authenticated now — there is no open-read mode to
		// turn on. The flag is kept as a no-op so an existing fly.toml or start
		// script that passes it does not fail; it just no longer does anything.
		log.Printf("note: --read-auth is deprecated and now a no-op — reads are always " +
			"authenticated. You can drop the flag.")
	}
	if abs, err := filepath.Abs(*db); err == nil {
		log.Printf("serving %s", abs)
	}
	// Bind before announcing. ListenAndServe does both, so logging first prints
	// "listening on :8799" and then, on the next line, that the port was already
	// taken — and anything reading the log for the success line believes it.
	// That cost an operator ten minutes today: a stale server held the port, the
	// new one never started, and the client's refusal pointed at the token.
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("cannot listen on %s: %v", *addr, err)
	}
	log.Printf("comms listening on http://%s", ln.Addr())

	// The printed commands must work as pasted against THIS hub, not a default
	// one. A hub served on a non-default --addr would otherwise print
	// `comms enrol ...` that quietly targets 127.0.0.1:7777, so the token minted
	// here is redeemed nowhere and the only symptom is a much later "unknown
	// token" pointing at the innocent token. These are all client verbs that
	// reach the hub over HTTP, so they carry --server; the hint is empty on a
	// default-address serve, so the common case stays clean.
	srvHint := ""
	if h, p, err := net.SplitHostPort(ln.Addr().String()); err == nil {
		host := h
		if host == "" || host == "0.0.0.0" || host == "::" {
			host = "127.0.0.1"
		}
		if !(host == "127.0.0.1" && p == "7777") {
			srvHint = " --server http://" + net.JoinHostPort(host, p)
		}
	}

	// serve --as enrols the owner in one step: no token to copy out of this
	// output and paste into a second command. serve runs on the box, so it has
	// the same standing as any operator flag — it registers the public key
	// directly and grants invite, and writes the private key locally the same
	// way `comms enrol` would, so the seat can post from this machine
	// immediately. Idempotent: re-serving with the same --as when the seat is
	// already enrolled here is a no-op, so it is safe in a restart command.
	serverURL := "http://" + ln.Addr().String()
	if *asSeat != "" {
		if err := ownerEnrol(st, *asSeat, serverURL, time.Now()); err != nil {
			log.Fatalf("serve --as %s: %v", *asSeat, err)
		}
	}

	// The banner. One thing pops — the claim link, on a first run — and
	// everything else is dim or gone: the verb tour lives in --help, and the
	// two log lines above stay because the deploy docs grep for them. Color
	// only on a terminal and never under NO_COLOR, so pipes and fly logs get
	// plain text.
	color := false
	if fi, err := os.Stdout.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		_, noColor := os.LookupEnv("NO_COLOR")
		color = !noColor
	}
	dim := func(s string) string {
		if !color {
			return s
		}
		return "\033[2m" + s + "\033[0m"
	}
	pop := func(s string) string {
		if !color {
			return s
		}
		return "\033[1;36m" + s + "\033[0m"
	}
	fmt.Println()
	fmt.Println(dim("  ╭──────────────╮"))
	fmt.Println(dim("  │") + "   ·))  comms " + dim("│"))
	fmt.Println(dim("  ╰──────────────╯"))
	fmt.Println()
	if *asSeat == "" && st.EnrolledSeats() == 0 {
		if tok, err := st.MintBootstrapInvite(time.Now()); err == nil {
			base := "http://" + ln.Addr().String()
			if srv.PublicURL != "" {
				base = srv.PublicURL
			}
			link := base + "/#setup=" + tok
			fmt.Println("  claim the first seat — open:")
			fmt.Println()
			fmt.Println("      " + pop(link))
			fmt.Println()
			fmt.Println(dim("  one use · expires in 24h · the first seat owns the hub"))
			fmt.Println(dim("  terminal instead:  comms join '" + link + "' --as human:<you>"))
			fmt.Println()
		}
	}
	fmt.Println(dim("  invite:  comms invite human:<name>|agent:<name>" + srvHint + "     help:  comms --help"))
	fmt.Println()
	if err := http.Serve(ln, srv.Routes()); err != nil {
		log.Fatal(err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ownerEnrol enrols one seat as the hub owner at startup, for `serve --as`.
// It is the token dance collapsed: serve is the operator on the box, so it
// registers the public key directly, grants invite (the same capability the
// first seat gets by claiming an empty hub), and writes the private key
// locally exactly as `comms enrol` would, pinned to this hub. Idempotent so it
// is safe in a restart command: a seat already enrolled here with a local key
// is left alone; a seat enrolled elsewhere with no local key is refused rather
// than silently minting a second key that cannot sign for the first.
func ownerEnrol(st *store.Store, actor, serverURL string, now time.Time) error {
	if err := store.ValidActor(actor); err != nil {
		return err
	}
	haveKey := cli.HasSeat(actor)
	enrolled := st.ActorEnrolled(actor)
	if enrolled && haveKey {
		return nil // already set up on this machine; nothing to do
	}
	if enrolled && !haveKey {
		return fmt.Errorf("%s is already enrolled on this hub but has no key on this "+
			"machine; its key lives where it was enrolled — do not re-key it here", actor)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := st.RegisterKey(actor, pub, now); err != nil {
		return err
	}
	if err := st.Grant(actor, "invite", "serve", now); err != nil {
		return err
	}
	// The owner is an all-rooms member: claiming the hub means seeing and
	// posting in every room. Without this the owner is a member of nothing —
	// enrolled on a db that already has the membership table, so the one-time
	// grandfather backfill (which only fires for seats predating scoping) does
	// not cover it — and every post is refused room.not_a_member. This is the
	// same '*' the bootstrap browser seat and every grandfathered seat hold.
	if err := st.AddMembership(actor, "*", "serve", now); err != nil {
		return err
	}
	// Local key last: if this failed after the store writes, a re-serve with
	// the same --as would find the seat enrolled-without-a-local-key and refuse
	// clearly, rather than leaving a half state that signs for nothing.
	if err := cli.SaveSeat(actor, priv); err != nil {
		return err
	}
	if err := cli.PinServer(actor, serverURL); err != nil {
		return err
	}
	log.Printf("enrolled %s as owner (invite granted); key written locally", actor)
	return nil
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}
