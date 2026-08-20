# TODO

The backlog lives here — plain markdown, versioned with the code, greppable.
Not a tracker: no assignees, no states beyond checked/unchecked. When an item
grows a real design, it becomes an ADR under `docs/adr/`; when it ships, delete
the line (the git history is the record). Severity in brackets: p2 = worth a
release, p3 = whenever.

Most of these came out of the agent-crew review studies
(`docs/research/2026-08-19-crew-studies.md`); the confirmed p2 bugs from those
studies are already fixed and are not listed here.

## Bugs

- [ ] [p3] `shell/digest.go` — `lastDigestSeq` uses a 2000-record lookback, so
      after a >2000-event gap the digest re-summarizes the *oldest* 2000 events
      ("since 0"), not the newest.

## Doc / comment rot (crew-confirmed, low value each — one sweep)

- [ ] [p3] Dead functions with stale comments: `store.PurgeArtifact`,
      `store.DropVector`, `RecordProgress` (the last has a divergent fold that
      is harmless only because it is unreachable — delete or wire it).
- [ ] [p3] Misattached / stacked doc comments flagged across store/shell/cli
      (e.g. a doc comment sitting above the wrong function). Fix only the ones
      confirmable by reading.
- [ ] [p3] `ftsQuery` doc says AND, code does OR — reconcile.
- [ ] [p3] Several `first_undelivered_seq` / cursor comments describe the field
      as "last delivered" or vice versa — pick one truth and match the code.

## Design gaps (need an ADR before code)

- [ ] Issues / tasks. Today there is no issue store; SPEC §23-27 + ADR-0007
      designed a Linear-mirror path that was never built. Decide: (a) Linear
      stays the store and comms mirrors it via webhook + outbox, or (b) a
      native `task` kind with an open→claimed→done lifecycle folded into a
      projection (the `question`/`handoff` shape), claimed via the addressed
      lane with consensus-`--idem` for exactly-one-claimant. This file is the
      interim answer.
- [ ] Severity is an unenforced human-interrupt claim: any agent can file `p0`
      with no gate, no named human, no rate limit. Policy question — decide
      whether `p0`/`p1` require a named recipient before adding a check.
- [ ] Handoff has no "seen/claimed" signal: "nobody declined and nobody posted"
      is indistinguishable from "nobody noticed." A read-receipt cuts against
      the ambient-lane philosophy — decide deliberately.
- [ ] [shelved] Room scratchpad — mutable structured state (a keyed table) per
      room, written by signed `scratch` events and folded into a projection
      (the fix for "the log can't hold a mutable backlog/checklist/claim
      table"). Product doc: `docs/research/2026-08-19-room-scratchpad.md`.
      Needs an ADR (scratch-as-projection, structured-op-not-raw-SQL for
      determinism, tight keyed vocabulary first). This is where a
      backlog/issues home eventually lives on the hub. Big feature — parked.
- [ ] [shelved, bigger] Agent-authored views/dashboards over the scratchpad —
      declarative template + query, rendered server-side through the existing
      artifact sanitizer, live via SSE. Guardrail: agents contribute data +
      declarative templates, NEVER executable code in another actor's session
      (the XSS class). Depends on the scratchpad; a big jump beyond it.

## Ergonomics (crew-requested, small)

- [ ] `comms show <seq>` — fetch one event's full body by seq. Previews
      truncate; agents currently round-trip `read --from <seq> --full`, and
      `read` says `preview` while `search` says `body` (inconsistent field).
- [ ] Roster presence: a last-seen timestamp per seat so "is anyone working
      right now" is answerable without reconstructing it from the log.
