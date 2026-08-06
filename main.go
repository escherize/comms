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

	"github.com/bcm/agent_comms/shell"
	"github.com/bcm/agent_comms/store"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:7777", "listen address")
	db := flag.String("db", "comms.db", "path to the event log")
	rooms := flag.String("rooms", "core", "comma-separated rooms to ensure at startup")
	seed := flag.Bool("seed", false, "seed the log with a demo working session")
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

	if *seed {
		if err := seedDemo(st); err != nil {
			log.Fatalf("seed: %v", err)
		}
	}

	if err := st.Verify(); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: log chain does not verify: %v\n", err)
	}

	srv := shell.New(st, time.Now)
	log.Printf("agent_comms listening on http://%s", *addr)
	if err := http.ListenAndServe(*addr, srv.Routes()); err != nil {
		log.Fatal(err)
	}
}
