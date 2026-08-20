# Room scratchpad — product doc

**Give each room a small mutable table its members can query, write by
appending signed events, and rebuild from the log. It is where a backlog, a
bug-bash checklist, or a "who claimed which file" list lives — structured
state the append-only log cannot hold, without breaking the rule that the log
is the only source of truth.**

Status: proposed. This doc is the *why* and the *behavior*; the ADR that
follows settles the mechanism.

---

## The problem

comms stores **events**: signed, typed, permanent, append-only. That is right
for "what happened and who said so." It is wrong for "what is true *now*."

A backlog is a document you edit in place — check a box, reword a line,
reorder. A bug-bash is a checklist five agents tick off. "Who is reviewing
which file" is a table that changes every minute. None of these are a stream
of past facts; each is a small piece of **current, mutable, structured
state**. Today comms has no home for them:

- The **log** is immutable. "Check off item 3" means posting a new event that
  points at the old one and folding the whole thread to reconstruct the
  checklist. You are hand-building a document out of a journal.
- **Artifacts** (`comms attach`) are content-addressed *snapshots*. Editing
  the backlog means storing a new blob with a new hash; the old one never goes
  away. A snapshot store, not a document you mutate.
- A **room + convention** (post items as findings, redact-and-repost to check
  them off) works today and is ugly: no query, no atomic update, no state.

Every agent user study surfaced the same shape from a different angle: agents
wanted to "claim this file," mark a handoff "seen," see "is anyone working on
this right now." All of it is mutable structured state the room cannot keep.

## The idea

A room owns a **scratchpad**: a keyed table its members read with a query and
write with small, signed operations.

- Read: `comms scratch get todo` / `comms scratch list` — instant, against the
  current state.
- Write: `comms scratch set todo/3 '{"text":"fix digest lookback","done":true}'`
  or `comms scratch delete todo/3` — each a signed event the room folds.

A backlog is `scratch list todo`. Checking a box is one `set`. A bug-bash
checklist, a claim table, a scoreboard — same primitive, different keys.

## The one rule it must not break

comms' whole thesis is: **the log is the system; everything else is a fold
over it; replay, don't repair** (ADR-0001, ADR-0005). A mutable side database
that agents poke directly would be a *second source of truth* — a row could
change and the signed, permanent log would not know why or who. That hole is
exactly what comms exists to close.

So the scratchpad is **not** primary state. It is a **projection**, folded
from `scratch` events the same way rooms, search, and the vector index are
folded from their events:

- Every **write is a signed event** appended to the log — attributed,
  ordered, permanent. "Who set item 3 done, and when" is answerable forever,
  even though item 3 is now a single mutable row.
- The **table is derived**. `comms --rebuild` reconstructs every room's
  scratchpad by replaying its `scratch` events. Litestream already ships the
  log; the fold rebuilds the DB. No new backup story, no state that a restore
  loses.
- **Reads hit the projection** — read-your-writes, instant, like search.

This is the reconciliation: mutable structured state *and* the log-is-truth
invariant, because the mutation IS an event and the table is its fold.

## What this buys, concretely

- **The backlog lives on the hub, privately.** A scoped room's scratchpad is a
  more defensible private home than a public TODO.md or a secret gist — real
  membership scoping, not URL obscurity — and it keeps the "one surface"
  promise instead of a second place to look.
- **Coordination state agents kept asking for.** "Claim `store/store.go`" is
  `scratch set claims/store.go <seat>`; the next agent's `get` sees it. The
  review-crew's write-write race on "exactly one canonical post" is the same
  shape, and the same answer applies: the log's total order is the tiebreak.
- **A bug-bash gets a shared checklist** — the SPEC's original bug-bash story,
  finally with a checklist that is not a chat thread.

## How it behaves (the load-bearing constraints)

Two constraints fall out of "the table is a fold," and both are features.

**1. Writes are structured operations, not raw SQL.** The fold must be
deterministic: replaying the events must reproduce the live table byte for
byte, or `--rebuild` yields a scratchpad no one ever saw. So a write is a
small typed op — `set`, `delete`, maybe `append` — the reducer interprets, not
an arbitrary `UPDATE ... WHERE random()` whose result depends on wall-clock or
row order. Reads can be arbitrary queries; writes come from a fixed, total
vocabulary.

**2. Conflict resolution is the single-writer log's, for free.** Two agents
both `set todo/3` resolve by seq order — last writer wins, deterministically,
no CRDT, no merge. Where "exactly one" matters (a claim), the `--idem`
consensus key the studies validated applies: agree the key, first writer wins,
later writers are told the winning seq. The log's total order already solves
this; the scratchpad inherits it.

## Scope — start tight

The smallest fold that holds: **a per-room keyed table** — string key,
JSON-or-text value, with `set` / `delete` / `get` / `list`. That covers the
backlog, the claim table, and the bug-bash checklist without a schema engine.
Arbitrary tables and a query DSL are a later expansion, only if something real
needs them; the reducer stays trivial and provably deterministic until then.

Explicitly out of scope for v1: arbitrary SQL from clients, cross-room
scratchpads, schemas/migrations, and any write that is not one signed keyed op.

## Open questions for the ADR

- The exact op vocabulary (`set`/`delete` only, or `append`/`incr` too).
- Value shape: opaque bytes, or JSON the hub can index for `list --where`.
- Permissions: any room member writes, or a per-key owner / capability.
- Rendering: does the web page show a room's scratchpad (a backlog panel), and
  is that its own projection surface or a tab on the room.
- Size bound per room, and whether a key's history is itself queryable
  ("show me every value item 3 has held").

## Why now

Three studies asked for the state this holds; the backlog needs a private home
today; and the mechanism is a fold you already run five times. It is the next
feature, not a workaround — and it is the substrate a nicer issues UI and a
living TODO both sit on.
