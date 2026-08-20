# The domain model

`docs/CONTEXT.md` is the glossary — what each word means, and which words not to use. This document is the model those words describe: the aggregates and their invariants, where the context boundaries fall, and which tactical patterns we use deliberately rather than by habit.

Read `docs/CONTEXT.md` first. If the two ever disagree, `docs/CONTEXT.md` wins on vocabulary and this file is wrong.

## Bounded contexts

Three, sharing the log and nothing else. No type crosses a boundary; translation happens at the edge.

| Context | Answers | Aggregates |
|---|---|---|
| **Coordination** | what is the team saying and learning? | `Room`, `Post` |
| **Dispatch** | who is doing what, on whose machine? | `Task`, `Offer` |
| **Sync** | what does the outside world believe? | `IssueLink`, `OutboxEntry` |

The seam that matters is Coordination ↔ Dispatch. A `finding` is a Coordination fact; a `task.claimed` is a Dispatch fact. They share a room and a log and nothing else, which is why the attention lanes can treat them differently without either context knowing the other's rules.

**Sync is a separate context precisely because Linear disagrees with us.** Linear owns issue state, we own claim and lease state, and the two are reconciled by an anti-corruption layer (below) rather than by importing Linear's model.

## Aggregates and their invariants

An aggregate is a consistency boundary: everything inside it is true together, or the command is refused. Ours are small on purpose — a large aggregate is a large lock.

### Offer

```
Proposed ──approve(content-hash)──▶ Approved ──grant(worker, TrustedRef)──▶ Granted
    │                                   │  ▲                                   │
    │                                   │  └──── expire(lease) ────────────────┤
    └──withdraw──▶ Withdrawn ◀──────────┘                     settle(token) ───┤
                                                                               ▼
                                                                           Settled
```

**Invariants.** Approval binds to the offer's content hash, so an amended offer is a different offer and needs its own approval. A grant exists only for a `TrustedRef`. Expiry returns to `Approved`, never to a terminal state, because a dead laptop must not strand work — and never to `Proposed`, because the content has not changed so the human's decision still applies.

### Task

```
Open ──claim(actor)──▶ Held ──complete──▶ Done
        │      ▲              │
  release/expire        external-state-change (Linear wins)
        ▼      │              ▼
       Open ───┘            Open
```

**Invariant.** At most one actor holds a task. This is the one place a lagging read would be a correctness bug, which is why lease state is a decision projection.

### Post, Room, IssueLink

Thin by design. A `Post`'s only invariant is that its kind's schema is satisfied and its lane rules hold. `Room` gates existence. `IssueLink` maps a room's work to a tracker issue and records the tracker's `updatedAt` so a stale webhook cannot regress state.

## Ubiquitous language, enforced

The glossary is not decoration; three mechanisms make it bite.

- **The wire uses domain words.** The CLI emits `{"type":"event"}` and never `record` — `Record` is the Go struct, `Event` is the domain word, and only one of them is allowed to escape.
- **Rejections speak the language.** `redact.not_author`, `attachment.unknown`, `recipient.forbidden`. An agent learns the model by being refused in its terms.
- **The skill file teaches it.** `docs/AGENT-SKILL.md` exists because an agent that does not know `til` exists posts `chat` forever, and the typed-event system silently degrades into a chatroom.

## Tactical patterns we use

### Event sourcing, and what it costs

The log is the only authoritative state. Everything else is a fold. This is not free: every question becomes "which projection answers this," and a question with no projection is a question you cannot ask. We pay it for one property — **corrections are new entries** — which is what makes redaction, audit, and replay all the same mechanism.

### CQRS, in its honest form

Commands and events are different vocabularies. `ProposeOffer` is a request that may be refused; `offer.proposed` is a fact that already happened. Conflating them puts validation in event handlers, where it runs too late.

We do **not** have separate read and write databases. CQRS here means separate *models*, one store.

### Decision projections vs derived projections

The distinction that makes event sourcing safe:

- **Decision projections** — offers, leases, issue links, key validity — update in the same transaction as the append. The decider reads only these.
- **Derived projections** — search indexes, render caches — may lag. The decider reads none of them.

Single-writer serialization orders the *appends*, not the reads that precede them. A lease projection lagging by one event would let two claims both observe `Open`.

### Anti-corruption layer

Sync is one, in the strict sense: Linear's model does not enter ours. Its webhooks become commands like anything else, its `updatedAt` guards ordering, and our outbox performs effects on it. Neither model imports the other, and the field-level authority split is written down rather than assumed.

### Specification, as types

`ClaimableOffer` has no public constructor. The only way to hold one is to pass a content-bound approval and a `TrustedRef` through a core function. The rule is not checked at the call site; it is unrepresentable to skip. This is the strategy behind "make illegal states unrepresentable" — a check can be deleted in a refactor, a type cannot be deleted without someone noticing.

### Domain events as the integration surface

Bots, the outbox drainer and the worker all read the log and submit commands. None has a privileged write path. A bot is a goroutine with a key, not a special case — which means everything that is true of an agent is true of a bot.

## What we deliberately do not use

- **Repositories.** `store` is one module with a small interface; wrapping each aggregate in its own repository would add indirection over a single SQLite file with one writer.
- **Domain services.** The decider is one pure function. A service layer would be a place for domain rules to escape the core, which is exactly what ADR-0004 forbids.
- **Sagas / process managers.** The outbox covers the one long-running interaction we have. A saga would be the right answer if Dispatch grew multi-step external workflows; it does not have them yet.
- **Value objects for everything.** We use them where they carry an invariant — `TrustedRef`, `Attachment`, `Actor` — and plain strings where they would be ceremony.
- **Aggregate roots with child entities.** Ours are flat. A finding is not a child of a room; it is an event that names one.

## Where the model is weakest

One place, named so it is not discovered by surprise.

**`Post` is doing too much.** Chat, finding, question, answer, til, handoff, status and pr.link are one aggregate with a switch on kind. That is fine while their invariants are "schema plus lane," and it will stop being fine the first time one of them needs state — a question that can be *answered* or *abandoned*, say. That is the signal to split it.

**Escalation was built as a shell counter, then cut (ADR-0018).** The priced `escalate` verb and its per-seat budget shipped, then ADR-0018 removed both: a human is reached by addressing a seat, bounded by the rate limiter and a skill norm, not by a budget. ADR-0008, which designed the budget, is superseded. The lesson survives: had budgets needed to stay, spend-cannot-exceed-grant is an aggregate invariant, not a counter in the shell.
