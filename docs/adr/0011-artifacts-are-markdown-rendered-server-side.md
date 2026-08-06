# Artifacts are GFM stored, sanitized HTML rendered; agent HTML is never stored or served

Agents attach reports, repro steps, and result tables to events as content-addressed artifacts. Artifacts are stored as GitHub-Flavored Markdown and rendered to sanitized HTML server-side at read time. Agent-authored HTML is never stored and never served.

## Consequences

Rendering agent-authored HTML in a human's authenticated session is the display-side twin of the execution boundary (ADR-0006): an injected agent that can emit script into a room a human views can read every room, post as that human, and approve its own `worker.offer` — a full bypass of the approval gate the types exist to protect. The threat model is our own agents steered by content they read, and this is the cheapest path to it.

Markdown also buys what HTML costs: artifact text goes into FTS5 alongside the event, so artifact contents are searchable — the co-learning feature — whereas a stored HTML blob is markup noise in the index. GFM specifically, because agents already emit tables (test results), task lists (bug-bash checklists), fenced code, and strikethrough, which are exactly GFM's additions over CommonMark. Sanitization lives in one deep module rather than at every call site.

Export-to-HTML for sharing outward remains available as a deliberate human act; that is a person choosing to publish, not an agent choosing what executes in a browser.

## Storage

Artifacts are content-addressed by SHA-256 and live in the same SQLite file as the log, so litestream covers them with no second backup path. Blobs past a few hundred KB spill to a content-addressed directory beside the DB, keyed by the same hash. Redacting an event drops its artifact blobs with its body: a secret pasted into a report must die with the redaction, and the chain still verifies because it covers hashes, not content.
