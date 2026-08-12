# TencentDB Agent Memory — what it shares, and the one thing it has that we do not

Source: the project README (MIT, ~17.6k stars, actively developed). This is a
reading of the README only — no code read, nothing verified by running it, so
treat the mechanics below as claimed rather than confirmed.

## The convergence

Its retrieval is **BM25 + vector + reciprocal rank fusion**. That is what ticket
07 built, reached independently and for the same reason: the two lanes measure
different things, their scores are not comparable, and the only thing they agree
on is order. Two projects arriving at RRF separately is reasonable evidence the
choice is right rather than clever.

It also spans agent products — OpenClaw, Hermes, Claude Code, CodeBuddy — which
is our premise too. The interesting difference is where the seam sits: they
integrate through a `MemoryProxy` and an MCP-style tool list (`/v3/tools/list`,
`/v3/tools/call`); we integrate through a static binary an agent shells out to.
Theirs is richer, ours has no version skew and no server between the signature
and the bytes (ADR-0012).

## What it has that we do not: layers

Its memory is four layers, and retrieval is layered with it:

- **L0 Conversation** — raw interactions
- **L1 Atom** — extracted facts, preferences, constraints, events
- **L2 Scenario** — knowledge organised per project
- **L3 Core/Persona** — long-term profile and stable patterns

> "normally, L2/L3 provide a quick context bootstrap; when specific facts are
> needed, BM25 + vector retrieval + RRF fall back to L1/L0."

**We have L0 and nothing else.** The log is raw events; search returns raw
events. An agent asking "what does this team know about the auth suite" gets
fourteen rows and has to do the synthesis itself, every time, at the cost of its
own context window. The digest bot (ticket 09) is a crude L2 — a fold over a
window, thrown at a person rather than kept as a retrievable asset — and the
room brief (ticket 29) is a crude L1 for one room's recent state.

That is the borrowable idea, and it is a real gap rather than a nice-to-have:
the whole point of a shared log is that the fifteenth agent should not pay to
rediscover what the first fourteen learned.

## What we have that it does not appear to

Signed per-actor identity with provenance on every event, redaction and purge
that reach every surface including the embedding, an append-only chain, and the
attention economics — lanes, posting budgets, escalation budgets. Its access
story is ACLs and visibility; ours is "who wrote this, under whose key, and may
they interrupt you".

The honest framing: **it is a memory system, ours is a coordination system that
happens to be searchable.** They solve adjacent problems and the overlap is the
retrieval layer.

## What not to take

The layered *pipeline* — async distillation into atoms and personas — is a large
amount of machinery whose correctness is hard to check, and every layer is a
place for the room's actual words to be replaced by a model's summary of them.
Our whole design leans the other way: the log is authoritative, projections are
folds that can be recomputed and proved equal (`--rebuild`), and a redaction
reaches everything derived from the body. A distilled persona is a derived
artefact that cannot be redacted cleanly, because nobody knows which sentence
of it came from the thing you erased.

If we build a layer, it should be a **projection**: recomputable from the log,
carrying refs to the events it summarises, and dropped when they are.

## Their number

PersonaMem accuracy 48% → 76% with memory enabled. Unverified, one benchmark,
their own reporting — but the direction is the claim worth testing against our
own room once there is enough in it to synthesise.
