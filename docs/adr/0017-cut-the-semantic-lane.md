# ADR-0017: cut the semantic lane — it is FTS5 in a fun-house mirror

**Status:** accepted, 2026-08-19. Supersedes ADR-0013, which chose *how* to build the vector lane; this decides *whether*, and the answer is no.

## Why

ADR-0013 reasons as if a real 384-dimension embedding model exists. It does not. `shell/shell.go` wires `HashEmbedder{}` — a deterministic stand-in whose own doc says "not a model" — as the production embedder, unconditionally, with no config seam or flag. There is no other embedder anywhere.

`HashEmbedder` hashes lowercased `[a-z0-9]` tokens into 256 SHA-256 buckets and L2-normalizes; the "semantic" score is cosine over those token-count vectors. Cosine over normalized token-count vectors **is token-overlap similarity** — the same signal FTS5/BM25 already computes, minus BM25's IDF weighting and phrase awareness, plus a hash-collision floor FTS5 does not have. So the vector lane is not a second kind of recall beside the lexical one. It is a worse reimplementation of the lexical one, running beside it.

Worse: because `s.embed` is never nil in production, the honest "unbuilt — lexical only" path never fires. Users see a `vector: searched` lane-status foot that **asserts a semantic lane ran when none did**. The feature's only production behavior is to mislead.

Two bugs this session (the `--since` boundary divergence, fixed twice) lived in the vector lane's Go-side filter — it had to re-apply the SQL filters by hand precisely because it was the sloppy parallel path. Deleting the lane deletes the bug class.

## The decision

Cut the semantic lane. Search becomes lexical-only via FTS5 (`store.Search`), which already honors every documented filter (kind/author/since/room/allow) in SQL.

Delete whole: `store/vector.go`, `shell/embed.go`, `shell/embed_test.go`. Delete the `vector`, `embed_failure`, `embed_state` tables; the `--reembed` flag and its handler; `StartEmbedder`/`Reembed`/`indexStatus` and the `GET /index` route; the fusion/rank plumbing in `render.go` (one rank column remains). Move `Head`/`ServerTSOf`/`NextSeq` out of `vector.go` into `store.go` — they are used by drills and status, not the lane.

`Purge` loses two statements (`DELETE FROM vector`, the dead-letter tombstone insert) and one failure-tracking subsystem.

## Rejected alternatives

- **Keep it, wire a real model.** The opposite of simplifying: adds an external API dependency, keys, per-post cost, a network call in the embed loop, breaking ADR-0002's one-binary-no-external-service thesis — to make real a feature no study used.
- **Keep the clean seam (ADR-0013's argument).** A clean seam is worth keeping when it is load-bearing; this one guards a stub. Building the harness (watermark, retries, dead-letter, RRF fusion, brute-force cosine) before the model is the speculative work YAGNI forbids. ADR-0013's rebuild recipe survives in prose; rebuild the seam the day a real embedder is wired, tested against that model, not against a hash.

## Consequences

- ~1200 lines deleted; the binary stays pure-Go.
- Search quality is unchanged — the second lane never added real recall.
- `Purge` gets simpler **and safer**: the cleanest way to guarantee an embedding drops on suppression is to never derive one. One fewer body-derived artifact a future bug could forget to erase.
- The search UI stops claiming a semantic lane ran.
