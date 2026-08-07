# Buzz: what it is, why nostr, and what we should take

Source: a direct read of [block/buzz](https://github.com/block/buzz) (Apache-2.0, Block, launched ~2026-07-21, ~28 crates) and of the nostr NIPs it cites. Claims below are from repo and spec text unless marked **[unconfirmed]**, which flags a delegated survey nobody re-checked.

## Buzz is a relay, not a swarm of agents on relays

`ARCHITECTURE.md`: "The relay is the single source of truth. All reads and writes flow through it." `README.md`: "single-relay workspace… the URL is authoritative for the workspace." There is no peer-to-peer exchange. Buzz is a Rust/Axum server on Postgres, Redis and S3 that speaks nostr as its wire format.

The split is the whole story:

| Concern | Mechanism |
|---|---|
| Actor identity, authorship, non-repudiation | **nostr** — secp256k1, Schnorr signature per event |
| Wire format and feature dispatch | **nostr** — NIP-01 events, `kind` integer registry |
| Group model, connection auth, git objects, delegation | **nostr** — NIP-29, NIP-42/98, NIP-34, house NIP-OA/AA |
| Durable state, ordering, partitioning | Postgres, range-partitioned by `created_at` |
| Search | Postgres GIN tsvector, NIP-50 as façade |
| Fan-out, presence | Redis pub/sub |
| **Mutual exclusion and leases** | **Redis fenced generations; `pg_advisory_lock`** |
| Idempotency, rate limiting, pagination, horizontal scale | Postgres `ON CONFLICT`, Redis Lua, house NIP-CW, QUIC mesh |

Nostr carries identity and schema. Every correctness property — ordering, atomicity, exclusion, durability, search — is conventional server infrastructure. `buzz-relay-mesh`'s own doc comment states it plainly: **"mesh membership is a hint; the Redis fenced generation is the arbiter."**

## Why nostr, honestly assessed

Load-bearing:

1. **Per-actor cryptographic identity.** Agent pubkey ≠ owner pubkey; every action Schnorr-signed and chained into a SHA-256 tamper-evident audit log. This is the pitch and it is genuinely built.
2. **Kind-integer dispatch as schema evolution.** ~46 custom kinds; a new feature is a new number; old clients ignore what they do not know. Arguably the bigger practical win, and it needed nostr only as a versioning convention.
3. **Portable identity across workspaces.** One npub joins several communities without per-workspace accounts.
4. **Git as protocol events.** NIP-34 puts patches, CI and review in the same signed log as chat; NIP-GS signs commits with the same key.

Incidental or unused:

- **"No server to run" is inverted.** Postgres + Redis + S3 + Axum + an optional QUIC mesh. Nostr made deployment heavier, not lighter.
- **Censorship resistance: unused.** NIP-42 is mandatory before any EVENT or REQ, channel membership is "the sole authorization gate", and there are ban/timeout kinds. It is a permissioned relay.
- **Multi-relay redundancy: unused in the core.** NIP-65 relay lists are accepted and never used for routing; no outbox model anywhere. The mesh scales one logical workspace.
- **Cross-org federation: unused.** Multi-tenancy is one operator's infrastructure with host-derived scoping.

The tension: the relay signs its own authoritative projections — membership, ordering, pagination, presence. Exactly where state matters most you are trusting the operator's key, which reintroduces the trust the signatures were sold as removing.

## What nostr costs them

**Ordering.** `buzz-core/src/verification.rs` verifies only the id hash and the Schnorr signature — no temporal check at all. The sole bound is at ingest: `MAX_TIMESTAMP_DRIFT_SECS = 900`. That is a fifteen-minute window of freely chosen ordering, while Postgres partitioning and feed ordering both key off the client-supplied `created_at`. Their own NIP-OA states the consequence: its time conditions constrain "the agent's self-declared timestamp, not wall-clock expiry. The agent could backdate events to bypass expired windows."

**No atomic claim.** Stated outright in NIP-PMA: "three ordinary Nostr `EVENT` writes cannot atomically commit an aggregate," so kind 30179 is reserved and MUST be rejected pending "a future relay contract supporting transactional CAS."

**Replaceable events as the only mutation primitive.** The workarounds are visible: NIP-CW needs a per-request-cursor `d`-tag suffix because a per-channel singleton would make page N overwrite page N+1's bounds; NIP-AE needs `created_at := max(now, T+1)` plus a "clock-poisoned" heuristic; message editing abandoned replaceability for a dedicated kind 40003.

**Domain validation is off-spec.** Being a relay, Buzz does validate — kind→scope map, membership, deletion shape, `kind:9` requires `#h`. None of that is in any NIP, so a generic NIP-29 client's writes are safe only because *Buzz's* relay is on the other end. "Any NIP-29 client works" and "domain invariants hold" pull against each other.

**Read-your-writes was bought out, not solved.** Single relay, `OK` only after the Postgres commit. They escaped the cost by declining to be decentralized. Their one genuinely multi-relay spec, NIP-AE, pays in full: mandatory write-then-recompute-head, and an admission that "disjoint relay sets, partitions, and writes arriving after the recompute window will not be caught."

### Against the three things we care about

**Claiming an issue — not built.** `kind.rs` defines a Job Protocol: 43001 request, 43002 accepted, 43003 progress, 43004 result, 43005 cancel, 43006 error. A code search for `KIND_JOB_ACCEPTED` finds it in the kind registry, a feed query, and four desktop TypeScript files that render it and play a notification sound. **No relay handler, no arbitration.** Worse, issue kinds 1621 and 1630–1633 all map to `Scope::MessagesWrite`, so any channel member can publish "closed" on any issue, and there is no assignee concept at all — `assignee` appears exactly once in the repo, in a roadmap document.

**Bug bash — no arbitration, and lossy.** The only exclusion is per-channel queue serialization inside a single agent harness, and its **default dedup mode is drop**: "while a prompt is in-flight for channel C, new events for channel C are silently dropped." Fan-out does not merely duplicate work, it loses events.

**Co-learning — structurally blocked.** Engrams (kind 30174) are NIP-44-encrypted under the agent↔owner conversation key; even the `d` tag is an HMAC under that key. **Sibling agents cannot read each other's memory at all.** Shared learning has to route through channel messages or a shared persona — through prompt context, not memory.

## The successor landscape

NIP-90 (Data Vending Machines), the closest thing to a standard for agent work dispatch, **is deprecated**. `90.md` opens with `> __Warning__ 'unrecommended': this got totally out of control, prefer use-case-specific microstandards`, and the NIPs README has it struck through. It never had claiming anyway — "service providers compete to fulfill the job requirement in the best way possible," with the customer arbitrating.

So the nostr NIP that Buzz's unimplemented 43001–43006 most resemble has itself been abandoned. Defining those kinds and never wiring a handler reads less like an oversight than the same conclusion reached independently.

**[unconfirmed]** The registry rows were deleted rather than annotated, with no successor spec. Three maintainer-adjacent repair attempts closed unmerged in 2025; zero agent-related NIPs merged in 19 months against ~20 unmerged proposals with colliding kinds. ContextVM is the de facto successor and extends via its own process outside the NIPs repo. NIP-AA PR #2259 (30921 Job Offer / 30922 Work Bid) is the only real two-sided bidding pair, and it is still requester-arbitrated — each bid lives at the bidder's own address, so nothing contends for a slot.

**Every nostr system surveyed resolves work assignment by requester arbitration, race-and-waste, central arbitration, or direct addressing. None implements mutual exclusion, because nostr has no primitive for it.**

## What this means for us

Buzz validates our shape by inversion. It is a single authoritative server with a signed append-only log, per-actor keypairs, decision state in a relational store, and leases with fencing tokens — which is our architecture, reached from the opposite direction. The nostr layer buys identity and a schema-evolution convention and costs a fifteen-minute ordering window, a mutation primitive that fights every use, and a spec surface that cannot express the domain rules the server enforces anyway.

Where we are already ahead:

- **Ordering.** `seq` is server-assigned under a single writer. Their `created_at` is client-supplied and adversarially controllable within ±900s, and it is what their partitioning and feed order key on.
- **Claiming.** Our decision projections are read in the same transaction as the append, which is what makes a claim sound. Theirs is unbuilt, and cannot be built at the protocol layer.
- **Addressing.** `recipient.unknown` against a roster. They have no assignee concept.
- **Redaction.** Hash chain over `body_hash` means a purge is tamper-evident rather than chain-breaking. NIP-09 deletion is advisory across relays.

Where they are ahead, and what to take:

1. **Kind-integer dispatch with forward compatibility.** Old clients ignore kinds they do not know. We reject unknown kinds outright, which is right for a decider but means a new kind requires a server upgrade before any client can name it. Worth an ADR on what an old *reader* does with a new kind.
2. **Delegation and attenuation.** NIP-OA is an owner-signed attestation carrying conditions (`kind=`, `created_at<`, `created_at>`), and NIP-AA turns a valid one into connection-scoped virtual membership without enrolment. Our seats hold full authority forever and revocation is all-or-nothing. Their conditions are unsound — self-declared timestamps — but the shape is right, and a server-assigned expiry fixes exactly what is broken about theirs. This is the one substantial idea to steal.
3. **Dispatch-side queueing.** A per-channel FIFO with cross-channel anti-starvation, exponential retry with jitter, and a dead-letter after N attempts. We have none of this on the dispatch side. Their default drop mode is the lesson in what not to do: silently discarding events is worse than the duplication it prevents.
4. **Formal methods.** TLA+ models and a Tamarin proof in `docs/spec/` and `docs/formal/`. Rare in this space. Our substitute is the exhaustive-kind test.
5. **Portable identity across workspaces.** Relevant to pooling compute with colleagues: one key joining several hubs is the thing our invite-gated, per-hub enrolment does not do. Not urgent, but it is the federation question in its cheapest form.

What not to take: relays, multi-relay redundancy, and censorship resistance — Buzz does not use them either.
