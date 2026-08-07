# celld and Durable Objects as an agent substrate

**Filed** 2026-08-06 · source: [@jpschroeder](https://x.com/jpschroeder/status/2085099179110703584) · repo: [denoland/celld](https://github.com/denoland/celld) (Rust, Apache-2.0, 1.8K stars, created 2025-04-25, active)

## What it is

`celld` is Deno's self-hosted, distributed implementation of Cloudflare Durable Objects — the compute-plus-SQLite primitive — runnable in any environment rather than only on Cloudflare. The thread's claim is that Durable Objects are close to an ideal substrate for cloud agents, and that their only real flaw has been single-vendor lock-in, which celld removes.

The argument, as posted:

- **Thread isolation.** Every agent thread needs its own execution environment — compute, memory, and storage unique per agent, even per sub-agent. The post calls the usual answer ("Docker, Kubernetes, VPSs, and a slew of other unholy tooling") *unfit for the job*. A Durable Object is a small primitive with its own compute, memory, HTTP handler, WebSocket handler, and SQLite database; cold start is tens of milliseconds and dozens run in parallel on one thread.
- **Storage.** Agents generate enough data that logging becomes the bottleneck for an agentic platform. Durable Objects distribute writes across threads, so each agent writes its own SQLite without contending for table locks.
- **I/O.** A fetch handler accepts HTTP and WebSocket connections, plus a hibernation API that holds a connection open while the object is shut down. "Tiny little servers."

## Why this matters to us

It is a direct challenge to two of our decisions, and worth engaging rather than filing away.

**Against ADR-0002 (one box, one SQLite, single writer).** Our design deliberately centralises the log: one writer gives total order without consensus, and every hard distributed-systems problem gets pushed to the edges. The thread's point 2 is precisely the objection someone would raise at scale — agent logging contends on a single writer, and per-agent SQLite removes that contention by construction.

The counter, which we should hold onto unless it stops being true: our single writer is what makes `seq` a total order, and total order is what makes the fencing tokens, SSE resume, and replay-equals-incremental guarantees work at all. Distributing writes across per-agent databases buys write throughput and costs the one property the whole design leans on. At five people the contention is hypothetical and the ordering is load-bearing — so this is a re-read trigger, not a redesign, and the trigger is measured write latency showing up as SSE lag (already named in the Durability section).

**Against ADR-0006 (containerised execution).** We chose containers for the pooled-compute execution boundary; this thread calls that tooling unfit for the job. Worth taking seriously *and* worth noting the mismatch: Durable Objects isolate a **V8 isolate**, not an arbitrary process. Our workers run `go test`, builds, and coding agents — arbitrary native processes with filesystem and network needs. A DO cannot run those; the isolation models are not substitutes. The honest reading is that DOs are excellent for *agent orchestration state* and poor for *running someone's test suite*.

## What we might actually take

Two ideas are portable without adopting the runtime:

1. **Per-actor storage for the high-write lane.** If agent status/progress writes ever contend, they are the natural thing to split out — they are a projection, rebuildable, and not part of the ordered log. That is a much smaller change than sharding the log.
2. **The hibernation pattern.** Holding a WebSocket (or SSE) connection while the handler is shut down is directly relevant to workers dialling out over SSE and staying connected across idle periods.

## Open question this raises

If agent isolation ends up wanting a V8-shaped sandbox rather than a container, ticket 15's worker design changes shape. Nothing in our current scope needs that — our tasks are native builds and tests — but a future `agent.task` that only needs JS could run far cheaper this way.

## Status

Research only. No decision, no ADR, no ticket. Re-read when either trigger fires: measured write contention on the single writer, or a pooled-compute task kind that does not need native process isolation.
