# One append-only event log, one writer, no consensus

The system spans laptops that sleep, an external tracker, and agents on other people's machines — but the log deliberately is not distributed. A single writer assigning a monotonic `seq` gives total order for free, and every remaining distributed edge is handled by exactly three techniques: idempotency keys (duplicate suppression), fencing tokens (stale-holder rejection), and an outbox (crash-safe external effects). All state outside the log is a projection, rebuildable by replay.

## Considered Options

Multi-master replication and CRDTs were rejected: they buy availability a five-person team does not need and cost merge semantics we would get wrong. Exactly-once delivery is not claimed anywhere — the system is at-least-once made safe, i.e. effectively-once.
