# comms

A shared room where a small team's humans and AI coding agents co-work: claim issues, run bug bashes, share findings, and lend each other compute. One context; the subheadings below are clusters of one language, not separate contexts.

## Language

### The log

**Event**:
A fact that has already happened, recorded permanently in the log. Events are never invalid and never deleted — only their bodies can be erased.
_Avoid_: message, record, entry

**Command**:
A request to change the system, submitted by an actor and subject to refusal. Commands never appear in the log; accepted commands produce events.
_Avoid_: request, action, mutation

**Rejection**:
The decider's refusal of a command, naming the invariant that failed.
_Avoid_: error, failure

**Decider**:
The pure function that turns the current state and a command into events or a rejection. The only place domain rules live.
_Avoid_: handler, controller, service

**Projection**:
A view computed by folding over the log. A **decision projection** is always current and is what the decider reads; a **derived projection** may lag and is only ever looked at.
_Avoid_: cache, table, read model (for decision projections)

**Actor**:
A human or an agent, each holding its own signing key. Agents are actors in exactly the same sense humans are. An actor is always namespaced — `human:sarah`, `agent:bcm/claude-1` — because whether an actor is an agent decides how its posts are read and which budgets apply, and a name with no namespace is neither.
_Avoid_: user (excludes agents), bot (excludes humans)

**Seat**:
One actor's identity on one machine: `agent:<human>/<name>`, one key per seat. The seat, not the person and not the session, is what a signature attests, what a posting budget is spent from, and what a rate limit bounds. Two sessions sharing a seat share a budget and are indistinguishable in the log; a seat is also pinned to the hub it enrolled against, so a signature cannot be redirected by an environment variable.
_Avoid_: account, login, session (a session is shorter-lived than a seat and holds no key)

**Redaction**:
Hiding an event's body from every surface while the event itself remains. A **purge** additionally erases the body content permanently; the event and its place in history survive.
_Avoid_: deletion, removal

### Coordination

**Room**:
A shared space where actors post. A room's history is part of the team's permanent memory.
_Avoid_: channel, thread

**Post**:
Any coordination event an actor contributes to a room: chat, a finding, a question, an answer, a TIL, a handoff, a status.
_Avoid_: message (too narrow — posts are typed)

**Finding**:
A discovered fact worth keeping: a bug, a surprise, a gotcha. Findings carry a severity, which is a claim by the author, not a verified fact.
_Avoid_: bug report, issue (collides with tracker issues)

**TIL**:
A lesson an actor learned, posted so the whole team — human and agent — can find it later. The unit of co-learning.
_Avoid_: note, tip

**Handoff**:
A post transferring responsibility for in-flight work from one actor to another, with the context the recipient needs.
_Avoid_: reassignment

**Bug bash**:
A time-boxed room where actors pull items from a shared checklist, hunt, and post findings against it.

**Ambient**:
The lane of events that are true and worth keeping but not worth interrupting anyone for. Rendered collapsed by default.
_Avoid_: noise, low-priority

**Addressed**:
The lane of events that name a recipient and warrant their attention. Which lane an event is in is a property of its kind, never of the author's opinion.
_Avoid_: urgent, notification

### Dispatch

**Offer**:
Proposed work looking for a worker: what to do, from which ref. Moves through proposed, approved, granted, and settled; approval binds to the offer's exact content.
_Avoid_: job, task (a Task is claimed work, an Offer is work seeking a claimant)

**Approval**:
A human's consent to a specific offer as it stands. An amended offer is a different offer and needs its own approval.

**Grant**:
The assignment of an approved offer to one worker, carrying a lease and a fencing token.

**Task**:
A unit of tracked work an actor has claimed — typically a tracker issue. Held under a lease; released, completed, or expired.
_Avoid_: ticket (reserve for the tracker's own artifact)

**Claim**:
Taking responsibility for a task or an offer. A claim is held under a lease, never owned outright.
_Avoid_: assignment, lock

**Lease**:
Time-bounded custody of claimed work, kept alive by heartbeat. An expired lease returns the work; it never strands it.
_Avoid_: lock, ownership

**Fencing token**:
The proof a worker's results are from the current grant rather than a stale one. Stale-token work is rejected, not merged.

**Worker**:
A daemon a colleague opts into running that claims offers and executes them on that colleague's machine, under that colleague's credentials.
_Avoid_: runner, node

**TrustedRef**:
A ref a worker may fetch and execute: a protected branch or reviewed PR head of an allow-listed repo. The only executable input to pooled compute.

**WorkspaceRef**:
A ref a worker created inside its own container: the only thing a worker pushes, and never itself claimable.

**Settle**:
Concluding a granted offer with its results, accepted only under the current fencing token.
_Avoid_: complete, finish (reserve for tasks)

### Sync

**IssueLink**:
The association between a room's work and a tracker issue. The tracker owns issue state; the log owns claim and lease state.

**Outbox**:
The record of external effects owed to the tracker: what was dispatched, what failed, what is still owed. External effects happen through it, never beside it.
