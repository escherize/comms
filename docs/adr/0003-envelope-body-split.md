# The hash chain covers a body hash, not the body

Each event is stored as a chained, immutable envelope (`seq`, author, kind, `body_hash`, `prev_hash`, signature) plus a separate body blob. Purging a leaked secret drops only the blob: no rewrite, no disabled trigger, and the chain still verifies end to end — now attesting "a body with hash H was here and is gone."

## Considered Options

Chaining over full event content was rejected because erasure would then require re-chaining history — an UPDATE the append-only triggers forbid, forcing the 2am hand-edit that destroys the integrity story. Tamper-evident erasure is strictly stronger than erasure that destroys the evidence of itself.
