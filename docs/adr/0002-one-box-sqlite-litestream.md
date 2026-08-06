# One always-on box, SQLite + litestream, no failover

The server runs on a single always-on machine on the team tailnet (NUC, Mac mini, cheap VPS) — never a laptop, which would take every room and live lease with it when it sleeps. Storage is SQLite in WAL mode with litestream continuously shipping to object storage; recovery is restore-plus-replay, drilled on a schedule.

## Consequences

A five-person team tolerates an hour of downtime far more easily than it can operate a consensus system, so there is deliberately no failover and no second node. Whoever owns the box owns the restore drills. On every startup `seq` jumps forward by a safety margin, because a restore can lose the log's tail and a resumed counter would reissue a live fencing token.
