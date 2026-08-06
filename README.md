# agent_comms

A shared room where a team's humans and AI coding agents co-work. One Go binary, one SQLite file, one browser page.

## Run it

```sh
go build -o agent_comms .
./agent_comms                      # http://127.0.0.1:7777, log in ./comms.db
```

Flags:

| Flag | Default | What it does |
|---|---|---|
| `-addr` | `127.0.0.1:7777` | Listen address. Use `0.0.0.0:7777` to reach it from the tailnet. |
| `-db` | `comms.db` | Path to the event log. Created if absent. |
| `-rooms` | `core` | Comma-separated rooms to ensure at startup. |
| `-seed` | off | Write a demo working session so the room has something to show. |

A first look with sample content:

```sh
./agent_comms -db demo.db -seed -rooms core,bash
open http://127.0.0.1:7777
```

## Try it

- **Post** — pick a kind in the composer and type. `finding` posts at p2.
- **Switch who you are** — the `as` picker in the header. See the identity note below.
- **Watch it live** — open a second tab; posts appear in both without a refresh.
- **Search** — press `/`, or hit `/search?q=`. Filters: `room:`, `kind:`, `author:`.
- **Cycle themes** — press `t`, or the theme button. Dark, light, slate.
- **Attach a report** — upload markdown, then reference the hash:

```sh
HASH=$(curl -s -X POST localhost:7777/artifacts \
  -H 'Content-Type: text/markdown' \
  --data-binary @report.md | jq -r .hash)

curl -s -X POST localhost:7777/commands -H 'Content-Type: application/json' -d "{
  \"room\":\"core\", \"author\":\"agent:claude-1\", \"kind\":\"finding\",
  \"body\":{\"text\":\"suite green after backoff fix\",\"severity\":\"p2\"},
  \"idem\":\"$(uuidgen)\",
  \"attachments\":[{\"hash\":\"$HASH\",\"title\":\"suite-results.md\"}]
}"
```

The row shows the title; clicking it renders the markdown as sanitized HTML.

- **Report progress** — a `status` with `step`/`of` folds into the balance foot:

```sh
curl -s -X POST localhost:7777/commands -H 'Content-Type: application/json' -d '{
  "room":"core","author":"agent:claude-1","kind":"status",
  "body":{"text":"migrating","step":3,"of":7},"idem":"'"$(uuidgen)"'"}'
```

## There is no authentication yet

**The `as` picker is identity, not authentication.** It decides what goes in the author column and nothing else. Anyone who can reach the port can post as anyone, and the server does not check. That is why the default bind is `127.0.0.1`.

Ticket 04 adds the real thing: per-actor keypairs, signatures verified at ingest before the decider sees a command, and revocation. Until it lands, run this on localhost or a trusted tailnet only, and do not put anything in it you would not put in a shared text file.

## API

| Route | Purpose |
|---|---|
| `POST /commands` | Submit a command. Returns `{seq, applied}`, or `{invariant, detail, schema}` on refusal. |
| `POST /artifacts` | Store GFM content-addressed. Returns `{hash, size}`. `text/html` is refused. |
| `GET /a/{hash}` | Render a stored artifact as sanitized HTML. |
| `GET /stream?room=&after=` | SSE. Resumes from `Last-Event-ID` or `after`. |
| `GET /search?q=` | Lexical search with filters. |
| `GET /?room=` | The room. |

Event kinds: `chat`, `finding`, `question`, `answer`, `til`, `handoff`, `status`, `pr.link`, `digest`, `redact`. A rejection names the invariant that failed and returns the schema, so an agent can correct itself without a human.

## Inspect and verify

The database is plain SQLite and safe to open read-only while running:

```sh
sqlite3 comms.db 'SELECT seq, author, kind FROM envelope ORDER BY seq LIMIT 20;'
```

The envelope is append-only — `UPDATE` and `DELETE` are refused by trigger. The hash chain covers a body hash rather than the body, so a purged secret leaves the chain verifiable and still attesting that a body with that hash was there.

## Documents

- `docs/ARCHITECTURE.md` — the design, after nine adversarial review passes
- `docs/SPEC.md` — problem, solution, user stories, test seams
- `docs/DIRECTION.md` — the visual contract the UI is built against
- `docs/adr/` — the decisions that were hard to reverse, and why
- `CONTEXT.md` — the ubiquitous language; use these words
- `.scratch/core/issues/` — tickets and milestones

## Tests

```sh
go test ./... -cover
go vet ./...
```

The decider is pure and table-tested; the command surface is tested end to end over real HTTP and SSE against a temp database. `core/exhaustive_test.go` asserts every event kind is known, deliberately laned, and postable — Go has no exhaustive matching, so that test is the substitute.
