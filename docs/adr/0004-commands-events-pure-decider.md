# Commands in, events out, decided by a pure core

Clients never write events; they submit commands that may be refused. The decider — `decide(state, command) → events | rejection` — is pure: no clock, no IO, no network. The shell parses untrusted JSON into domain types exactly once at the boundary (parse, don't validate), verifies signatures (authentication), and appends what the core returns; every domain refusal (authorization) lives in the core.

## Consequences

An event is a fact and can never be invalid, so validation in event handlers is validation that runs too late — the command/event split is what makes invariants like content-bound approval expressible at all. Anything needing a clock or a window (lease expiry evaluation, rejection aggregation, attention budgets) is shell work by definition.
