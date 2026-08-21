# Build queue

What we implement next, in order. Not a feature tracker — this is *our* backlog.
Ordered top-to-bottom by what to do first; dependencies noted inline. When an
item ships, delete the line (git history is the record). When a design item
grows a real decision it gets an ADR under `docs/adr/` and the line points at it.

The order is deliberate: **simplify before adding, and cut leaves before the
trunk.** Every "add a feature" idea from this session sits below the
simplification that must land first, or it just gets un-built.

---

## Now

The simplification arc is landed: ADR-0017 cut the semantic lane, ADR-0018 cut
`escalate` + digest, ADR-0016 replaced the kind ladder with addressing-by-seat
and reply-routing-by-ref, and ADR-0020 deleted the author-facing kind outright
(a post is text; `chat`/`presence`/`redact` survive as the system
discriminator; legacy kinds render from old rows). One TBD deliberately left
open: dedicated human-address rate-limit numbers — the global per-seat limiter
is the floor for now (ADR-0016 rule 4).

## Bugs (fix in passing, low value each)

- [ ] [p3] Doc/comment rot sweep — the study-6 tail is done (ftsQuery doc,
      the backwards truncation field — renamed delivered_through_seq — and the
      misattached doc comments all fixed in the study-7 batches). What remains:
      whatever the next read confirms; fix only that.

## Later / parked (need design, don't start yet)

- [ ] **Task/work queue on the hub** — claimable unaddressed work (the crew's
      "claim this file"). Grilled once: it is NOT a message kind (ADR-0016
      demotes those) but a *lifecycle aggregate* (open→claimed→done) — the line
      ADR-0016 draws is "routing kinds → tags, lifecycle kinds → first-class,"
      and a task is the second. Atomic claim = the single-writer log + consensus
      `--idem` (the review-crew race, already validated). Build AFTER ADR-0016,
      on the simplified model. Needs its own ADR.
- [ ] [parked] Room scratchpad — mutable structured state per room, writes as
      signed `scratch` events folded into a projection. Product doc:
      `docs/research/2026-08-19-room-scratchpad.md`. The general form of "the log
      can't hold a mutable table." Big feature; the task queue is the narrow
      version that probably suffices — prefer that first.
- [ ] [parked, bigger] Agent-authored declarative views over the scratchpad,
      rendered through the artifact sanitizer, live via SSE. Depends on the
      scratchpad. Guardrail: data + declarative templates only, never executable
      code in another actor's session (the XSS class). A jump — shelved.
