# Tracker effects go through an outbox; authority is split by field

A command that should affect Linear appends only an event; a drainer performs the API call and appends the outcome. Linear owns issue state (status, assignee, labels); the log owns claim and lease state — on conflict Linear wins the field and the lease is voided with a visible reason.

## Considered Options

The naive bot that mutates Linear and appends to the log was rejected as a dual write: a crash between the two diverges the systems silently. Retry safety is classified per operation because idempotency is the callee's property — field writes retry freely; comment creation embeds the originating `seq` as a marker and checks for it before retrying, which is also what makes restore safe when a dispatch receipt was in the lost tail.
