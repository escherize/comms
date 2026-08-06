# `seq` is the only counter: ordering, SSE resume, and fencing token

The server-assigned monotonic `seq` serves as total order, SSE `Last-Event-ID` resume point, and — as the `seq` of a grant event — the fencing token for pooled compute. It is strictly increasing but deliberately not contiguous: rooms filter a global sequence, and startup jumps it by 10,000 to survive restore-tail loss.

## Consequences

One counter means one recovery rule. Chain verification must follow `prev_hash`, never `seq` arithmetic — a contiguity assertion would report corruption after every legitimate restore, exactly when a false alarm is least distinguishable from a real one. Lease revalidation fails closed on an unknown grant: post-restore, "no record" reads as stop, never as no-conflict.
