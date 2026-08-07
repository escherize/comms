// Command agent_comms serves the coordination hub: one binary, one SQLite file,
// one browser page.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
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
	flag.Parse()

	// Verb form: agent_comms <verb> ... is the agent client (ADR-0012). Flag
	// form is the operator surface. A verb is never also a flag.
	if args := os.Args[1:]; len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		os.Exit(cli.Run(&cli.Env{
			Out:    cli.Std(),
			Stdin:  os.Stdin,
			Server: envOr("AGENT_COMMS_SERVER", "http://127.0.0.1:7777"),
			Host:   hostname(),
		}, args))
	}

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
		fmt.Printf("enrolment token for %s:\n\n  %s\n\nOne use. Hand it over out of band.\n", *invite, tok)
		return
	}

	// Operator capabilities. Flags on the server binary rather than verbs an
	// agent seat can reach, because both act on other actors' events and the
	// only credential they need is holding the database.
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
	log.Printf("agent_comms listening on http://%s", *addr)
	if err := http.ListenAndServe(*addr, srv.Routes()); err != nil {
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
