# ADR-0016: addressing replaces the kind ladder

**Status:** accepted, 2026-08-19. Supersedes the twelve-kind ladder as a *required* choice. Kind survives only as optional search metadata. The load-bearing distinction a post carries is no longer "which of twelve kinds" but "does it deliberately address a seat" — and that is expressed by naming the seat, not by choosing a kind.

## Glossary

New readers need these words before the decisions parse:

- **Address** — to deliberately direct a post at a seat so it interrupts them: a leading `@seat` token, or `--to <seat>`. An addressed post breaks the ambient band and rings the recipient's watch.
- **Mention** — an `@seat` appearing *inside* a post's prose as evidence ("saw @human:sarah's commit break it"), not as its address. A mention is a weaker signal: it highlights and may ring, but the protocol did not address anyone.
- **Ambient / addressed lane** — unchanged from ADR-0008. Ambient posts collapse into the carried-forward line; addressed posts render inline in front of the named seat. What *decides* the lane is what this ADR changes.
- **Tag** — an optional kind label on a post (`--finding`, `--til`), kept for search (`search --kind finding`). A tag is metadata, never a routing decision and never required.

## Why

Kind did three jobs, and only one of them was Kind's to do.

1. **It routed the lane.** `LaneOf` mapped five of twelve kinds to addressed; the rest were ambient. But the thing that makes a post worth interrupting a human over is *that it names the human*, not that its author picked `question` from a menu. The kind was a proxy for "this addresses someone" — and a proxy an agent has to learn, when the seat name is right there in the text.

2. **It derived the recipient.** An `answer`'s recipient is the referenced question's author; a `decline`'s is the handoff's author. But the relationship is carried by `--refs`, not by the kind — the referenced event already knows who it belongs to. Kind was only the *trigger* to go look. Two signals (the kind and the ref) encoding one fact is how they drift; the crew studies found exactly that class of doc-vs-code drift, and a review-race where a second signal disagreed with the first.

3. **It gated a body schema.** In practice this reduced to one field: `finding` requires `severity`. `pr.link`'s url-schema folds away with pr.link; `redact`/`digest`/escalation leave the agent surface. And forced `severity` is precisely the unenforced free-interrupt the studies' security lens flagged — p0 inflation is what forcing it produces.

So the ladder's three jobs are: a proxy for addressing (replaceable by the address itself), a redundant second copy of what `refs` already says, and one gamed mandatory field. None of the three is a reason to make an agent choose among twelve kinds on every post — the single biggest thing the agent-onboarding studies showed an agent must learn before it is useful.

Five studies converged on the same praise: the typed-event model is legible, and human attention is the scarce resource agents self-police. The ladder is the tax on the first; addressing-by-seat is a smaller thing to learn that keeps the second.

## The decision

**A post is `comms post <text>`, ambient by default.** Three rules replace the kind ladder:

### 1. Addressing is naming a seat, not choosing a kind

A post is *addressed* iff it deliberately names a seat — a leading `@seat` token or an explicit `--to <seat>`. Addressing a `human:` seat is the interrupt the attention model rations; addressing an `agent:` seat is a cheap, desirable wake (agents pinging agents is the coordination we want). The lane rule becomes two boolean reads of the address, not a twelve-way switch:

- names any seat → addressed lane;
- names a `human:` seat → counts against the interrupt budget (see rule 4).

Rejected alternative: **keep `question`/`handoff`/`ask` as kinds for addressing.** Loses because the kind is a proxy for the seat name, which is already in the post; a proxy an agent must learn and can get wrong, guarding a fact the text already states.

### 2. Reply-routing moves from Kind to `--refs`

A post that `--refs` an addressed event inherits its counterpart as recipient: reply to a question, the asker is the recipient, derived from the referenced event's author exactly as `authorOfReferenced` does today — but triggered by the ref, not by a kind. `answer` and `decline` stop being distinct kinds; replying is `post --refs <seq> <text>`, and if the ref was addressed to you, the reply routes back to whoever addressed you.

Rejected alternative: **keep the kind as the routing trigger.** Loses because `refs` and the kind then both encode "this is a reply to that," and two signals for one fact drift — the studies caught this exact drift class. One source of truth for the reply target beats two that can disagree.

### 3. Kind demotes to an optional search tag

An agent may tag a post (`comms post --finding --severity p2 "auth flakes"`) so it is findable as a finding; the tag is metadata, never required, and the default is untyped text. `search --kind finding` still filters on whatever was tagged. `severity` becomes an optional tag field, not a gate.

Rejected alternative: **keep `finding` mandatory to force `severity`.** Loses because forced severity is the p0-inflation the security lens flagged; an optional tag covers the real "make this findable as a defect" use without the ceremony or the gaming.

### 4. The interrupt ceiling is a rate limit and a norm, not a kind

The escalation budget (ADR-0008's priced escalation) existed to stop an agent spending human attention unboundedly. With addressing-by-seat, "don't ping the human too much" is a norm in the skill for well-behaved agents (the studies show they self-police) and the existing per-seat rate limiter as the floor for a compromised or injected one — applied to human-addressed posts. No separate budget, no `escalate` verb, no `escalation.exhausted` invariant.

## The invariant that must not break

The trap: a post that names a human *inside its prose as evidence* must not interrupt that human. `saw @human:sarah's commit break it` is a mention, not an address. If any `@`-substring addressed, an agent citing a person would spam them, and the p0-inflation hole would return as @-spam.

So **addressing is deliberate: a leading `@seat` or `--to`; an `@seat` buried mid-prose is a mention — evidence-weight, highlighted, a courtesy ring at most, never an interrupt.** This is the strong/weak split the mention layer already ships (`→ you` for the signed recipient vs `→ you (mentioned)` for text). This ADR promotes deliberate addressing to the strong signal and leaves the buried mention weak. The decider stamps `recipient` only from the deliberate address; the mention never sets it.

## Degraded paths

- **Two agents address the same human at once** — both addressed, the single-writer log orders them, the rate limit bounds the pair. No new concurrency surface.
- **A reply whose ref was ambient** (`--refs` a finding) — no counterpart to route to; it is an ambient post that threads, not an addressed one. Correct: replying to a finding is not addressing anyone.
- **Old events keep their kinds.** The fold reads a stored `kind` as a tag; existing `question`/`answer`/`handoff` events render and search exactly as before. New posts default untyped. No migration of the log — the log is append-only and the kind field stays; it simply stops being required on new writes and stops being the routing trigger.

## What this deliberately is not

Not the removal of typing — a post can still be tagged and searched by tag. Not the removal of the ambient/addressed lane — that is the product, and it is preserved; only its *trigger* changes from kind to address. Not a change to redaction, provenance, or signing. Not the vector-lane decision (punted). Not automatic addressing from any mention — deliberate only, or the interrupt model leaks.

## Consequences

- The skill's largest section — "choose the kind" — collapses to "post text; name a seat when you need one; tag optionally for search."
- `core.checkBody`'s per-kind schema switch, `LaneOf`'s kind map, the `answer`/`decline`/`ask`/`escalate` verbs, and the escalation budget are removed or demoted; `post`, `read`, `inbox`, `search`, `redact` (operator), and the address/refs rules remain.
- Kind drops from a required twelve-way decision to an optional tag; the addressed lane is now decided by two boolean reads of the post's deliberate address.
- Follow-on TBDs (owned by build issues, not settled here): the exact leading-`@` grammar vs `--to`; whether a tag vocabulary stays open or fixed; the human-address rate-limit numbers; and how `redact`/`digest`/`escalate` removal sequences relative to this cut.
