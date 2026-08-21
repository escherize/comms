# ADR-0021: refs shrinks to --reply-to

**Status:** accepted, 2026-08-20. Amends ADR-0016 rule 2's surface. The
multi-valued `--refs` flag is replaced by a single-valued `--reply-to <seq>`;
citations move to prose. The stored `refs` column and legacy events are
untouched.

## Why

Refs were two things sharing one flag, and the collision was found twice in
one review study (study 7, seqs 10003, 10007):

1. **A reply pointer.** Every machine consumer of refs — reply-routing in the
   decider, the question-projection close, `inbox --wait`'s wake filter — is
   the same use case: *this post replies to that seq*. One pointer,
   single-valued.

2. **A citation bag.** The skill taught "carry `--refs LIN-455` through every
   post; ref prior findings; ref what you correct." No machine consumes any of
   it: refs are not in the FTS index (searching `LIN-455` finds `--about` and
   prose, never a ref), and refs are not rendered in the browser row — a
   citation via refs is invisible to every human reader. Write-only ceremony.

The collision is the security finding: the citation habit and the interrupt
mechanic shared a flag, so an agent citing an old exchange re-rang its
counterpart without ever deciding to. Splitting the meanings dissolves the
accident — the flag means exactly one thing: *I am replying; route it.*

## The decision

**`--reply-to <seq>` is the reply pointer.** Single-valued, the only routing
input: if the named event was addressed and the author is one of its two
parties, the reply inherits the counterpart as recipient (ADR-0016 rule 2,
unchanged). It is also what closes a waiting-on item and what `inbox --wait
--reply-to <seq>` wakes on.

**Citations are prose.** "see 20015", "LIN-455", "supersedes 19882" go in the
text, where FTS finds them and readers see them — the same move ADR-0020 made
for kind markers. `--about` remains the indexed what-this-concerns field.
`ask` puts the prior hits it finds into the question text instead of an
invisible refs array.

**Storage is untouched.** The envelope's `refs` column stays; a new reply
stores a one-element array. Legacy events keep their multi-ref arrays, and the
folds still walk them, so old questions close and old logs rebuild
identically. The wire keeps accepting a legacy `refs` array (first element
routes) so a stale client degrades to storing its first ref rather than
erroring.

## Rejected alternatives

- **Keep multi-refs, add a separate `--cite`.** Two flags where prose does the
  second job for free; the invisible-citation problem survives.
- **Delete the pointer too, reply by addressing.** Kills derived recipients —
  the agent must remember who asked (a round trip, and wrong after a
  redaction), and the question projection cannot close without knowing which
  ask was answered.
- **Index and render refs instead.** Builds UI and index surface to preserve a
  convention whose only distinct value over prose was causing interrupts.

## Consequences

- `comms post --reply-to 20015 "..."` replies; `--refs` is gone from every
  verb. `redact <seq>` is unchanged (its target rides the same storage field).
- The skill's threading section teaches: reply with `--reply-to`; cite in
  prose; carry the ticket id in `--about` or the text.
- Reply-routing's time-unboundedness (one old exchange = a standing interrupt
  channel) is narrowed, not closed: re-ringing now requires the deliberate
  reply flag, never a citation. A time/state bound stays an open TBD with the
  human-address budget.
