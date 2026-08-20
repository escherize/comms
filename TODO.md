# Build queue

What we implement next, in order. Not a feature tracker — this is *our* backlog.
Ordered top-to-bottom by what to do first; dependencies noted inline. When an
item ships, delete the line (git history is the record). When a design item
grows a real decision it gets an ADR under `docs/adr/` and the line points at it.

The order is deliberate: **simplify before adding, and cut leaves before the
trunk.** Every "add a feature" idea from this session sits below the
simplification that must land first, or it just gets un-built.

---

## Now — the simplification (ADR-0016), in dependency order

Done since this queue was written: ADR-0017 cut the semantic lane, ADR-0018 cut
`escalate` + the escalation budget and the `digest` kind/bot. What remains of the
simplification is ADR-0016 (`docs/adr/0016-addressing-replaces-the-kind-ladder.md`):
a post is text, addressed by naming a seat, kind demoted to an optional search
tag. Land it in this order — each step is its own commit, skill/docs trimmed to
match, `./check` green.

1. [ ] **Fold `pr.link` into a plain post.** One kind gone, and the url-scheme
       validation surface (the XSS class) shrinks to "a url in body text is
       linkified through the sanitizer." [core, render.go]
2. [ ] **Reply-routing from `--refs`, not Kind** (ADR-0016 rule 2). A post that
       `--refs` an addressed event inherits its counterpart as recipient. Retire
       `answer`/`decline` as distinct kinds; replying is `post --refs <seq>`.
       Keep `authorOfReferenced`, trigger it on the ref not the kind.
3. [ ] **Lane from deliberate address, not Kind** (ADR-0016 rules 1 + the
       invariant). A leading `@seat` / `--to` addresses; a mid-prose `@seat` is a
       mention (evidence-weight, never an interrupt). Rewrite `LaneOf` +
       `Decide`'s recipient stamping. THE load-bearing invariant — get the
       deliberate-vs-mention line right or p0-inflation returns as @-spam.
4. [ ] **Delete author-facing kind entirely** (ADR-0020, supersedes 0016 rule
       3). No `--finding`/`--severity`, no `search --kind`, no `comms kinds`.
       `checkBody` collapses to "text required." `Kind` shrinks to the system
       discriminator (`chat`, `presence`, `redact`); old events legacy-read
       their stored kind. Rewrite the skill's "choose the kind" section to
       "post text; name a seat when you need one."

TBDs to settle while building step 3/4 (don't guess): leading-`@` grammar vs
`--to`; the human-address rate-limit numbers.

Also remaining: **ADR-0019** (`docs/adr/0019-read-ergonomics-show-and-presence.md`)
— `comms show <seq>` and roster last-seen. Both are in the Ergonomics section
below; the ADR is accepted, the code is not landed yet.

## Bugs (fix in passing, low value each)

- [ ] [p3] Doc/comment rot sweep — the study-6 tail: `ftsQuery` doc says AND,
      code does OR; `first_undelivered_seq` / cursor comments describe the field
      backwards; a few misattached doc comments. One pass, fix only what reading
      confirms. (Dead functions `PurgeArtifact`/`DropVector`/`RecordProgress`
      already removed.)

## Ergonomics (crew-requested, small, do anytime)

- [ ] `comms show <seq>` — fetch one event's full body by seq. Previews
      truncate; agents round-trip `read --from <seq> --full`, and `read` says
      `preview` while `search` says `body` (inconsistent field). Cheap win, the
      study-5 crew's loudest ask.
- [ ] Roster presence — a last-seen timestamp per seat, so "is anyone working
      right now" is answerable without folding the log.

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
