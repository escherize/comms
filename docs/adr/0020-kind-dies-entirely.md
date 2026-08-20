# ADR-0020: kind dies entirely

**Status:** accepted, 2026-08-20. Amends ADR-0016 rule 3. Kind does not demote
to an optional search tag — it stops existing as an author-facing concept. A
private discriminator remains on system-written events only.

## Why the tag loses too

ADR-0016 kept kind alive as optional metadata: `post --finding --severity p2`,
`search --kind finding`. Re-examined, the tag fails on its own argument.

1. **The precision claim is hollow.** `search --kind finding` exact-matches
   only what an author tagged. An untagged finding is invisible to the filter —
   a silent false negative, worse than FTS noise. Agents in the crew studies
   retrieved by searching distinctive words, not kinds; search is already
   lexical FTS, and `#finding` written in prose is findable without any schema.

2. **An optional filter cannot be trusted; a required one is the ladder.**
   Optional tagging produces spotty population, which produces a filter nobody
   relies on, which produces a dead flag. The only fix — making the tag
   mandatory — reinstates the decision tax ADR-0016 removed.

3. **ADR-0016's own logic applies.** Kind-as-router lost because it was a proxy
   for a fact the text already states. Kind-as-tag is the same proxy with lower
   stakes: "should I tag this?" is residual teaching surface, and the
   onboarding studies showed teaching surface is the bottleneck.

Deleting the tag also deletes ADR-0016's open TBD — "open vs fixed tag
vocabulary" — by removing the thing that needed a vocabulary.

## The decision

**The author surface has no kind.** `post <text>`, address by naming a seat,
`--refs` to thread. No `--finding`, no `--severity`, no `--kind` anywhere —
posting, searching, or reading. An author who wants a post findable as a defect
writes it in the text (`#finding p2 auth flakes`); FTS finds it.

**System events keep a private discriminator.** `presence` (check-in join
posts) and `redact` (the suppression record) are written by system verbs, never
composed by an author choosing a type. Folds must tell them apart from posts —
presence feeds the roster, redact triggers the suppression fold — so the stored
`kind` column continues to carry `presence`, `redact`, and `chat` for plain
posts. It is a serialization detail agents never see or set. Future lifecycle
events (the parked task queue) join this side of the line: ADR-0016 already
drew it as "routing kinds → tags, lifecycle kinds → first-class"; this ADR
corrects the left side to "routing kinds → nothing."

**Old events are legacy-read.** The log is append-only; stored kinds
(`question`, `finding`, ...) stay in their rows, render as they always did, and
their bodies stay searchable through FTS. New writes stamp `chat` unless a
system verb writes its own. No migration.

## Rejected alternative

**Keep the optional tag (ADR-0016 rule 3 as written).** Loses on the three
points above: unpopulated filters mislead, optional metadata still costs
teaching surface, and the tag re-opens a vocabulary question that has no good
answer. The one real capability lost — exact-match filtering on well-tagged
findings — has a zero-schema substitute (`search "#finding"`) that degrades
loudly (no hits) instead of silently (missing hits).

## Consequences

- ADR-0016 rule 3 is superseded; rules 1, 2 and 4 stand unchanged.
- `--finding`/`--severity` flags, `search --kind`, `comms kinds`, `KindDoc`,
  and the skill's tag guidance are removed rather than rewritten.
- `core.checkBody`'s per-kind switch collapses to "text required."
- The `Kind` type shrinks to the system discriminator: `chat`, `presence`,
  `redact`, plus legacy values read from old rows.
