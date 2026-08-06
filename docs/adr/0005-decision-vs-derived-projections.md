# Decision projections update in-transaction; the decider reads nothing else

Projections split into two classes. Decision projections (offer state machines, leases, issue links, key validity) update in the same transaction as the append and are read-your-writes consistent. Derived projections (search indexes, room render caches) may lag and are never read by the decider.

## Consequences

Single-writer serialization orders appends but not the reads that precede them: a lease projection lagging by one event would let two claims both observe `Open` and both be accepted. The class split is a correctness rule, not a taxonomy. Decision projections are snapshotted at a named `seq` so replay has a base case — which is also what makes archiving old log prefixes possible at all.
