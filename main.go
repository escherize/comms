// Command agent_comms serves the coordination hub: one binary, one SQLite file,
// one browser page.
package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/bcm/agent_comms/core"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bcm/agent_comms/cli"
	"github.com/bcm/agent_comms/shell"
	"github.com/bcm/agent_comms/store"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:7777", "listen address")
	db := flag.String("db", "comms.db", "path to the event log")
	rooms := flag.String("rooms", "core", "comma-separated rooms to ensure at startup")
	seed := flag.Bool("seed", false, "seed the log with a demo working session")
	insecure := flag.Bool("insecure", false, "accept unsigned commands (localhost demos only)")
	invite := flag.String("invite", "", "mint a one-time enrolment token for this actor and exit")
	purge := flag.Int64("purge", 0, "erase one event's body and attachments permanently, then exit")
	flagged := flag.String("flagged", "", "list events authored by a compromised key, then exit")
	reembed := flag.Int64("reembed", -1, "rebuild the semantic lane from this seq, then exit")
	digestAs := flag.String("digest-as", "", "run the digest bot under this seat (must hold the digest capability)")
	digestTo := flag.String("digest-to", "", "who the digest is addressed to")
	digestEvery := flag.Duration("digest-every", time.Hour, "how often the digest bot considers posting")
	grant := flag.String("grant", "", "grant the digest capability to a seat, then exit")
	rebuild := flag.Bool("rebuild", false, "recompute every log-derived projection from the log, then exit")
	verify := flag.Bool("verify", false, "check the log chain end to end, then exit")
	seqReport := flag.Bool("seq-report", false, "print the head and the next seq, then exit")
	// Verb form: agent_comms <verb> ... is the agent client (ADR-0012). Flag
	// form is the operator surface. A verb is never also a flag.
	//
	// This runs before flag.Parse because the client owns its own flags: parsing
	// the operator set first makes `agent_comms post --text x` die on an unknown
	// flag instead of reaching the client.
	args := os.Args[1:]
	// `serve` is a verb because starting the hub is the first thing anyone does,
	// and it has to be in the list they get when they type the binary's name.
	// Ticket 19 made the bare binary print the verbs, which is right; the README
	// still said the bare binary serves, which then was not true. Naming it
	// makes both true rather than picking one. It is a prefix, not a second
	// implementation: it drops itself and the operator path runs as it always
	// did, so there is nothing to keep in sync.
	if len(args) > 0 && args[0] == "serve" {
		os.Args = append(os.Args[:1], args[1:]...)
		args = args[1:]
	}
	// -h-server is the escape hatch: the operator flag set, which the client
	// form now shadows.
	if len(args) == 1 && args[0] == "-h-server" {
		flag.Usage()
		return
	}
	clientForm := len(args) == 0 ||
		!strings.HasPrefix(args[0], "-") ||
		args[0] == "-h" || args[0] == "--help" || args[0] == "help"
	if clientForm {
		os.Exit(cli.Run(&cli.Env{
			Out:    cli.Std(),
			Stdin:  os.Stdin,
			Server: envOr("AGENT_COMMS_SERVER", "http://127.0.0.1:7777"),
			Host:   hostname(),
		}, args))
	}

	flag.Parse()

	st, err := store.Open(*db)
	if err != nil {
		log.Fatalf("open %s: %v", *db, err)
	}
	defer st.Close()

	for _, r := range strings.Split(*rooms, ",") {
		if r = strings.TrimSpace(r); r != "" {
			if err := st.EnsureRoom(r); err != nil {
				log.Fatalf("ensure room %s: %v", r, err)
			}
		}
	}

	if *invite != "" {
		tok, err := st.MintInvite(*invite, time.Now())
		if err != nil {
			log.Fatalf("invite: %v", err)
		}
		abs, _ := filepath.Abs(*db)
		fmt.Printf("enrolment token for %s:\n\n  %s\n\n"+
			"One use. Hand it over out of band.\n\n"+
			"Minted into %s — the token only exists in this database.\n"+
			"The server redeeming it must be running with the same -db, or the\n"+
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

	if *grant != "" {
		// Granting is an operator act with no verb, by construction: a
		// capability an agent could give itself is not a capability.
		if err := st.Grant(*grant, core.CapDigest, "operator", time.Now()); err != nil {
			log.Fatalf("grant: %v", err)
		}
		fmt.Printf("granted %q the digest capability\n", *grant)
		return
	}

	if *reembed >= 0 {
		// The rebuild runs on the operator surface, not as a verb: it rewrites a
		// projection for the whole hub, which is not an agent's to do.
		sv := shell.New(st, time.Now)
		n, err := sv.Reembed(context.Background(), *reembed)
		if err != nil {
			log.Fatalf("reembed: %v", err)
		}
		fmt.Printf("rebuilt the semantic lane from %d: %d event(s) embedded\n", *reembed, n)
		return
	}

	if *purge > 0 {
		if err := st.Purge(*purge); err != nil {
			log.Fatalf("purge: %v", err)
		}
		fmt.Printf("purged the body and attachments of %d; the event and the chain remain\n", *purge)
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

	if *seed {
		if err := seedDemo(st); err != nil {
			log.Fatalf("seed: %v", err)
		}
	}

	if err := st.Verify(); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: log chain does not verify: %v\n", err)
	}

	srv := shell.New(st, time.Now)
	if *insecure {
		srv.RequireSignature = false
		log.Printf("WARNING: -insecure is set. Unsigned commands are accepted, so anyone " +
			"who can reach this port can post as anyone. Localhost demos only.")
	}
	// The semantic lane fills in the background. It is eventually consistent by
	// design; /index and the search foot both publish how far behind it is.
	srv.StartEmbedder(context.Background(), time.Second)

	if *digestAs != "" {
		if *digestTo == "" {
			log.Fatal("-digest-as needs -digest-to: a digest with no recipient is ambient, " +
				"which is a summary that interrupts nobody and is therefore a second copy of the room")
		}
		for _, room := range strings.Split(*rooms, ",") {
			room = strings.TrimSpace(room)
			if room == "" {
				continue
			}
			go srv.RunDigest(context.Background(), shell.DigestBot{
				Actor: core.Actor(*digestAs), Room: room,
				To: core.Actor(*digestTo), Every: *digestEvery,
			})
		}
		log.Printf("digest bot running as %s every %s", *digestAs, *digestEvery)
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
	log.Printf("agent_comms listening on http://%s", ln.Addr())
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

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}
