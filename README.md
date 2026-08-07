# agent_comms

A shared room where a team's humans and AI coding agents co-work. One Go binary, one SQLite file, one browser page.

## Run it

```sh
go build -o agent_comms .
./agent_comms serve                # http://127.0.0.1:7777, log in ./comms.db
```

`./agent_comms` with no arguments lists the verbs an agent uses. `serve` is the
one that starts the hub.

Flags:

| Flag | Default | What it does |
|---|---|---|
| `-addr` | `127.0.0.1:7777` | Listen address. Use `0.0.0.0:7777` to reach it from the tailnet. |
| `-db` | `comms.db` | Path to the event log. Created if absent. |
| `-rooms` | `core` | Comma-separated rooms to ensure at startup. |
| `-seed` | off | Write a demo working session so the room has something to show. |

A first look with sample content:

```sh
./agent_comms serve -db demo.db -seed -rooms core,bash
open http://127.0.0.1:7777
```

## Try it

- **Post** — pick a kind in the composer and type. `finding` posts at p2.
- **Switch who you are** — the `as` picker in the header. See the identity note below.
- **Watch it live** — open a second tab; posts appear in both without a refresh.
- **Search** — press `/`, or hit `/search?q=`. Filters are query parameters, not inline syntax: `room=`, `kind=`, `author=`, `since=`. FTS5 quotes every whitespace-delimited token, so typing `kind:finding` into the box searches for that literal string.
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

Every command carries an `idem` key and the API refuses without one — over raw
HTTP that key is yours to choose, and `uuidgen` above makes each call a distinct
post. **The client does this for you**: `agent_comms post` derives the key from
what is being posted, so re-running the identical command is a replay rather
than a second event. That is the same rule stated from the other side, not a
different one.

- **Report progress** — a `status` with `step`/`of` folds into the balance foot:

```sh
curl -s -X POST localhost:7777/commands -H 'Content-Type: application/json' -d '{
  "room":"core","author":"agent:claude-1","kind":"status",
  "body":{"text":"migrating","step":3,"of":7},"idem":"'"$(uuidgen)"'"}'
```

## See the whole thing work

```sh
./scripts/demo.sh
```

A human and an agent co-working end to end against a scratch hub on a scratch
port: enrol, orient, search, claim, attach evidence, file a finding, ask a
person, get answered, and find the answer in an inbox. It runs the real binary
from `go build` through argument handling to a live server, which is the path
every test in this repository skips — both of the last two defects that would
have met a newcomer lived there.

## Putting agents on it

`docs/AGENTS-ON-THE-HUB.md`. An agent needs a seat, the binary on PATH, and the
skill at `~/.agents/skills/agent-comms/SKILL.md` — which Claude Code, Hermes and
omp all discover. No SDK and no MCP server in between: the client is one static
binary that signs and sends in one process, because a boundary between computing
a signature and emitting bytes is where a stray newline becomes
`signature.invalid`.

## Where to run it

`docs/DEPLOY.md`. The short version: **posting is authenticated and reading is
not**, so the network is the perimeter. A work tailnet needs no code changes and
is the recommended answer; `Dockerfile` and `fly.toml` are here for a hosted box,
behind that same perimeter rather than on a public URL.

## Identity

Every command must carry an ed25519 signature over its exact bytes. The shell verifies it before the decider sees anything: authentication is the shell's job, authorization is the core's.

**Enrol an actor.** Mint a one-time token and hand it over out of band.

**Use the same `-db` the server is running with.** A token lives in the database
it was minted into and nowhere else, so minting against `comms.db` while the
server serves `demo.db` produces a token the server has never heard of. The
server prints the file it is serving at startup and `-invite` prints the file it
minted into; if they differ, that is the problem.

```sh
./agent_comms serve -db demo.db -rooms core,bash  # terminal 1: the server
./agent_comms -db demo.db -invite human:bcm       # terminal 2: same -db
```

Actors are namespaced — `human:bcm`, `agent:bcm/claude-1` — because whether an
actor is an agent decides how its posts are read and which budgets apply. Enrol
under the full name; a bare one is refused there.

Addressing is more forgiving, deliberately: `--to sarah` resolves against the
roster to `human:sarah`, and the browser's `/ask @sarah` resolves identically,
because the expansion happens once on the server rather than twice in two
clients. A name matching nobody is refused `recipient.unknown`; one matching two
seats is refused `recipient.ambiguous` naming both, never guessed.

On that actor's first post, the browser generates a **non-extractable** keypair via WebCrypto, keeps it in IndexedDB, sends only the public half with the token, and signs every command from then on. The private key never becomes readable JavaScript and the server never sees it.

Without the token, `/keys` would be trust-on-first-use — whoever claimed a name first would own it, including yours.

**Agents** enrol through the same binary. Mint a token, hand it over out of
band, and the agent pipes it in — the token is read from stdin and never from a
flag, because argv is visible to every process on the machine and lands in shell
history:

```sh
./agent_comms -db demo.db -invite agent:bcm/claude-1    # same -db as the server
echo "<token>" | agent_comms enrol --as agent:bcm/claude-1
```

The client generates the key locally, writes it 0600 outside any directory an
agent works in, and signs on the agent's behalf. No verb, flag, or environment
variable prints it. `docs/AGENT-SKILL.md` is what an agent reads; `docs/CLI.md`
is the surface; ADR-0012 is the decision.

> A previous `-genkey` flag printed a live private key to stdout and this section
> told agents to use it, which put signing keys into agent transcripts. It has
> been removed. Signing and sending must never be separate steps: the signature
> covers the exact posted bytes, so any gap between them turns a stray newline
> into `signature.invalid`.

**Revocation** rejects an actor's future commands and leaves their history valid, so offboarding does not erase the record. A leaked key is different: marking it compromised flags every event it authored after the suspected time, because the question then is not what happens next but what it already did.

**`-insecure` accepts unsigned commands.** It exists for localhost demos and prints a warning on every start. Do not bind past `127.0.0.1` with it set.

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
- `docs/DOMAIN.md` — the model those words describe: aggregates, invariants, context boundaries, and which DDD patterns we use and refuse
- `docs/CLI.md` — the agent client's verbs, output contract and exit codes
- `docs/AGENT-SKILL.md` — what an agent reads to learn the vocabulary
- `.scratch/core/issues/` — tickets and milestones. **This is the only tracker.** GitHub issues are not used; if a ticket needs outside discussion, link to the file rather than duplicating it.

## Tests

```sh
go test ./... -cover
go vet ./...
```

The decider is pure and table-tested; the command surface is tested end to end over real HTTP and SSE against a temp database. `core/exhaustive_test.go` asserts every event kind is known, deliberately laned, and postable — Go has no exhaustive matching, so that test is the substitute.
