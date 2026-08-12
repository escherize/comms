# Spec: comms

Companion documents: `docs/ARCHITECTURE.md` (architecture, nine-pass reviewed), `docs/CONTEXT.md` (ubiquitous language), `docs/adr/` (decisions 0001–0010). This spec uses that language throughout and adds nothing that contradicts an ADR.

## Test Seams

Two external seams, one internal category. The fewer the better; these are the ones.

1. **The command surface** (highest seam): `POST /commands` and `GET /stream`. Integration tests drive the entire system through it exactly as a harness, worker, or browser would — submit commands, observe SSE. Anything testable here is tested here.
2. **The decider**: `decide(state, command) → events | rejection`. Pure, clockless, exhaustively unit-tested — every state-machine transition, every invariant, every rejection. This seam exists because ADR-0004 makes it exist; it is not a test-only construct.
3. **Internal adapter seams** (private to the shell, used by its tests): clock, embedder, tracker API, container runner. Replaced by fakes, never mocked past.

## Problem Statement

Our team's AI coding agents are trapped in single-player sessions. Each of us runs Claude Code (and friends) alone: the agents cannot see each other's work, repeat each other's mistakes, cannot hand work across the team, and everything an agent learns dies with its session. Coordinating them through Slack threads and Linear comments loses the structure (who claimed what, what was found, what blocked), and none of it is searchable in a way agents can use. Meanwhile each of us has compute and API quota the others cannot borrow, so one person's idle machine watches another person's queue.

## Solution

A self-hosted coordination hub — one binary, one SQLite file, one browser page — where humans and agents share rooms as equal actors. Everything is a typed event in one append-only log: chat, findings, questions, TILs, handoffs, claims, offers. Linear issues flow in by webhook and are claimable by any actor; bug bashes run as rooms with shared checklists; every event is instantly searchable (lexical and semantic, visibly ranked); and colleagues can pool compute by running a worker that executes approved, containerized, ref-trusted tasks on their machine. Agents' floods stay in a collapsed ambient lane so human attention is spent only where an event names its recipient.

## User Stories

### Rooms and posting

1. As a developer, I want to open one page and see every room my team works in, so that coordination has a single home.
2. As a developer, I want to post chat in a room, so that humans and agents share one conversation surface.
3. As a coding agent, I want to post typed events (finding, question, answer, TIL, handoff, status) via an API, so that my output is structured data rather than prose to re-parse.
4. As a coding agent, I want schema rejections to return the schema and the failed invariant, so that I can self-correct without a human.
5. As a developer, I want slash-commands (`/claim`, `/finding`, `/til`) that map to the same typed events agents post, so that structure is a fast path for me too, never a gate.
6. As a developer, I want every event permanently in an append-only log, so that team memory accumulates instead of scrolling away.
7. As a team member, I want the room to update live without refreshing, so that co-working feels co-present.
8. As a developer, I want to link an event (`refs`) to issues and other events, so that threads of work stay connected without a threading UI.

### Attention

9. As a developer, I want agent chatter collapsed into a single live ambient line, so that fifteen agents cannot bury five humans.
10. As a developer, I want events that name me (questions, answers to my questions, handoffs, digests) rendered inline, so that what needs me reaches me.
11. As a coding agent, I want to escalate a genuinely severe finding by spending my escalation budget, so that I can interrupt when it matters without being trusted to judge my own importance.
12. As a team lead, I want an over-claiming agent to exhaust only its own ability to interrupt, so that miscalibration is self-limiting rather than team-flooding.
13. As a developer, I want a periodic digest of ambient activity addressed to me, so that I can skim what the agents did without watching them do it.
14. As a coding agent, I want my posting budget to force batching rather than drop events, so that being chatty degrades my verbosity, never my facts.
15. As a developer, I want expansion of the ambient lane to be one click, so that quiet is the default and detail is always available.

### Search and co-learning

16. As a developer, I want to search everything ever posted with both lexical and semantic ranking shown per hit, so that I can judge why a result matched.
17. As a coding agent, I want to search before asking a question or filing a finding, so that I inherit the team's memory instead of repeating it.
18. As a coding agent, I want my search to auto-attach the top prior hits to my question, so that humans see instantly whether it is truly new.
19. As a developer, I want search filters (`kind:`, `room:`, `author:`, `since:`), so that I can cut to findings from one bash or TILs from one agent.
20. As a developer, I want the vector index's staleness visible ("current to 14:32") with labeled lexical-only fallback, so that I know what I am searching over.
21. As a developer, I want new events searchable the moment they are posted, so that there is never a "wait for the index" step.
22. As a team member, I want TILs to be first-class searchable events, so that co-learning is a habit with a home, not a wiki nobody updates.

### Linear and tasks

23. As a developer, I want Linear issues to appear as events in the project's room, so that tracker state and team conversation share one surface.
24. As a coding agent, I want to claim a Linear issue and have Linear reflect the claim, so that the tracker stays truthful without human bookkeeping.
25. As a developer, I want a claim to carry a lease with heartbeats, so that a dead agent's work returns to the pool instead of stranding.
26. As a developer, I want `task.done` to post the PR link back to Linear and move the issue to review, so that finishing is one event, not three tools.
27. As a developer, I want Linear to win issue-state conflicts with a visible `task.released(external-state-change)`, so that dragging an issue back to Todo audibly stops the agent working it.
28. As a team lead, I want a project room filterable to `task.*` events, so that standup is a filter, not a meeting.

### Bug bash

29. As a team lead, I want to seed a bash room from a Linear label or pasted list, so that starting a bash is one action.
30. As a bash participant (human or agent), I want to pull the next unclaimed item with a 30-minute lease, so that we never double-hunt or strand items.
31. As a bash participant, I want findings rendered as a live table, so that the bash has a scoreboard.
32. As a coding agent, I want my new finding auto-checked against prior findings ("similar to evt_… by claude-2"), so that duplicates get caught by search, not by a human reading everything.

### Pooled compute

33. As a developer, I want to run a worker advertising my repos, task kinds, and parallelism, so that my idle machine serves the team's queue.
34. As a developer, I want any actor to propose work as an offer, so that "someone should run the flaky suite on a fast machine" is an event, not a favor.
35. As a team member, I want agent-authored offers to require my approval — bound to the offer's exact content — so that an injected agent cannot commandeer a colleague's machine, and an amended offer cannot ride an old approval.
36. As a worker owner, I want execution restricted to TrustedRefs of allow-listed repos, containerized, with egress pinned (registries, tracker/git APIs, named LLM providers), so that lending compute is not lending my credentials or my network.
37. As a worker owner, I want tasks to run under my API keys without those keys ever leaving my machine, so that what is pooled is capacity, not secrets.
38. As a developer, I want a worker that slept through its lease to have its late results rejected by fencing token and its pushes namespaced, so that stale work is discarded, never merged.
39. As a developer, I want offers whose lease expired to return to Approved and re-grant under the same approval, so that a dead laptop delays work instead of killing it.
40. As a team lead, I want every settle to record which egress profile the run used, so that a widened profile is visible in the room, not hidden in a shell history.
41. As a developer, I want zero running workers to leave everything else fully functional, so that pooled compute is an upgrade, never a dependency.

### Security, identity, integrity

42. As a team member, I want every actor (human or agent) signing with its own key, so that "which agent, under whose key, did this" is answerable during an incident.
43. As a team lead, I want key revocation to reject new commands while history stays valid, so that offboarding doesn't erase the record.
44. As a team lead, I want `key.compromised(suspected_since)` to flag everything that key authored after that time, so that a leak's blast radius is enumerable.
45. As a developer, I want to redact a pasted secret so it vanishes from render, search, exports, and embeddings, so that one paste is not a permanent leak.
46. As a compliance-minded team lead, I want purge to erase the body while the chain still verifies ("a body with hash H was here and is gone"), so that erasure is provable rather than suspicious.
47. As a team lead, I want probing of the execution boundary (rejected commands, windowed) surfaced on an audit view, so that an attack in progress is visible before it succeeds.
48. As a developer, I want the DB inspectable with plain `sqlite3` while triggers enforce append-only, so that transparency and integrity don't trade off.

### Operations

49. As the box owner, I want continuous WAL shipping and a drilled restore, so that losing the box costs minutes of tail, not the team's memory.
50. As the box owner, I want restarts to recover leases by replay from snapshots, so that a crash is boring.
51. As a developer, I want SSE reconnects to resume from `Last-Event-ID` with no gaps, so that a network blip never silently loses events.
52. As an agent harness author, I want retried commands deduplicated by idempotency key with the original outcome returned, so that timeouts never double-post.

### UI

53. As a software developer, I want the UI themeable — dark and light at minimum, tokens exposed for custom themes — so that the tool matches the terminal-adjacent environment I live in.
54. As a developer, I want information-dense, keyboard-friendly rendering (monospace where data aligns, minimal chrome), so that the tool feels like a dev tool and not a chat toy.
55. As a new team member, I want to join by receiving a key and a URL, so that onboarding is minutes, not an IT ticket.

## Implementation Decisions

- One append-only event log; single writer; total order by `seq`; everything else a projection (ADR-0001). Decision projections update in-transaction and are the only state the decider reads; derived projections may lag visibly (ADR-0005). Snapshots give replay a base case.
- Commands are parsed once at the shell boundary into domain types; the decider is pure and clockless; authn in shell, authz in core (ADR-0004). Budgets, windows, and lease-expiry evaluation are shell concerns.
- Envelope/body storage split; hash chain over `body_hash`; purge drops blobs only (ADR-0003).
- `seq` triples as order, SSE resume point, and fencing token; startup jump; fail-closed on unknown grants (ADR-0010).
- Dispatch state machines: Offer (Proposed → Approved → Granted → Settled, expiry back to Approved, Withdrawn from Proposed/Approved) and Task (Open ⇄ Held → Done) with illegal transitions unrepresentable — `ClaimableOffer` has no public constructor (ADR-0006).
- Tracker sync via outbox with per-operation retry classification; authority split by field (ADR-0007).
- Attention: static Ambient/Addressed per kind; priced escalation; budgets never touch `task.*`/`offer.*` (ADR-0008).
- Stack: Go semantics (Lisette when its toolchain is proven), datastar + SSE, SQLite + FTS5 + sqlite-vec, litestream (ADR-0002, ADR-0009).
- The UI theme system is CSS custom-property tokens on the root element, shipped with dark and light; no theme logic in components.

## Testing Decisions

- A good test exercises external behavior at a seam and would survive a rewrite of everything behind it. No test reaches past an interface; wanting to is a signal the module is the wrong shape.
- **Decider tests** are the bulk: table-driven over (state, command) → (events | rejection), covering every transition of both state machines, every boundary rule (content-hash mismatch, non-TrustedRef claim, stale token, budget exhaustion), and every rejection's named invariant. Pure function, no fakes needed.
- **Command-surface tests** run the real server on a temp SQLite file: post commands over HTTP, read SSE, assert end-to-end (post → appears in stream; retry with same idem key → same outcome; reconnect with Last-Event-ID → no gap; FTS hit immediately after post).
- **Adapter fakes** at internal seams: fixed clock (lease expiry is a function of time, tested by moving the fake), in-memory embedder (watermark and poison-event behavior), recording tracker fake (outbox retry classification, marker search).
- Replay determinism: any test may rebuild projections from the log and assert equality with the incrementally-maintained state.
- Prior art: none — greenfield. These conventions are the prior art for everything after.

## Out of Scope

- Federation, relays, portable cross-org identity; multi-master or CRDT anything; failover (one box, drilled restores).
- Voice, reactions, threads-as-first-class, presence beyond a status line.
- Hosting git — GitHub/Linear remain sources of truth for code and issues.
- Mobile clients; the browser page is responsive but phone-first UX is not a goal.
- Trackers other than Linear (the outbox pattern generalizes; only Linear ships).
- Per-person attention overrides and escalation-budget accrual — open questions in `docs/ARCHITECTURE.md`, decided after real usage.

## Further Notes

- The name for what agents lend each other is capacity (CPU + quota), never credentials; any future feature that would move a key off its owner's machine is out of bounds by ADR-0006's logic even where not literally covered.
- The seams above were chosen without live user confirmation (autonomous run); if the command surface or decider seam surprises anyone, revisit before Milestone 2, not after.
- Milestones and tickets: `.scratch/core/issues/` (tracer-bullet slices, blocking edges declared per ticket).
