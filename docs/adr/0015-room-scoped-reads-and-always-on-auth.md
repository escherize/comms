# ADR-0015: room-scoped membership, and reads are always authenticated

**Status:** accepted, 2026-08-17. Narrows ADR-0014 twice: "any enrolled seat reads everything, which is what a room is" becomes "an enrolled seat reads the rooms it is a member of", and "the flag defaults to off" becomes "there is no flag — reads are always authenticated".

## Why

One hub, many topics. ADR-0014 made reads authenticated so a public hostname was not world-readable, but it left one visibility level: any enrolled seat read every room. That is fine for a single team on one tailnet and wrong the moment you want to hand a collaborator one project without handing them the rest, or run six agents where a seat scoped to `mb-work` must not read your private room — and those agents share one loopback, so the network perimeter does not draw that line. Room scoping is the missing boundary: which rooms a seat may see and post in.

## Membership is a record, not a projection

A seat's rooms live in a `membership(actor, room, granted_at, granted_by)` table, mirroring `capability`: a grant with a grantor, so "who let this seat into this room" is an incident question with an answer. `room = '*'` is the all-rooms wildcard — an unscoped invite and every seat grandfathered on upgrade holds it, so nothing that worked before scoping stops. Membership is written in the same transaction that registers the key, from the invite's scope; it is not a fold over the log, so `-rebuild` leaves it alone, the same property that protects keys and capabilities.

Authorization is the decider's, as it already is for capabilities: `State.IsMember` sits beside `HasCapability`, and `Decide` refuses a post outside membership with `room.not_a_member` — checked *before* room existence, so the error names only the author's own rooms and never reveals whether the target room exists. A seat cannot probe for a room it was scoped away from.

## The polarity flip: reads are always authenticated

ADR-0014 added read auth as an opt-in flag, off by default, on the theory that a private network is its own perimeter. Room scoping retires that theory. A scoped seat's promise of privacy is only real if every read is attributed to a seat — and it cannot be attributed if reads are anonymous. The flag would have made the safe state opt-in again, and ADR-0014's own lesson is that opt-in safety loses to how people behave.

So there is no flag. Reads are always authenticated, full stop. `--read-auth` is a deprecated no-op kept only so an existing start script does not fail. The friction this removes-by-flag in 0014 is gone anyway: the CLI mints a session transparently on the first 401 and the browser signs with its WebCrypto key, so onboarding a real client is unchanged; a no-session browser read gets the unlock page, never content, and a `#setup=` token in the URL is what lets an unenrolled browser enrol and come back authenticated. A permanent, secret-bearing log is not served unauthenticated in any mode.

The read gate now stashes the session's seat on the request, and every read path filters by that seat's membership: the room page, brief, and stream 404 a non-member room (indistinguishable from nonexistent — no existence leak); `/rooms` and the nav list only the reader's rooms; search is scoped at the source, the reader's rooms becoming an `IN (...)` allow-list in both the lexical and semantic lanes, so a non-member room's content never enters the result set rather than being fetched and dropped. Loopback and the `*` wildcard keep the full operator view — the same trust boundaries 0014 kept.

## Artifacts are not a bypass

Artifacts are content-addressed and cross-room, so a raw `/a/<hash>` was an authorization hole: any authenticated seat that knew a hash could read a report attached only in a room it was scoped away from. An `artifact_ref(hash, seq, room)` index — written on append, backfilled once from existing attachments, dropped on redaction — makes access an indexed membership check: the blob is served only when the reader is a member of some room whose live event references the hash, else 404.

## Scoped admins cannot escalate

A scoped seat may hold the invite capability, but only to mint within its own rooms. A mint naming a room the granter is not a member of is refused `invite.scope_exceeds_grant`, and a scoped admin minting `all` is refused outright — otherwise it could invite a seat into a room it cannot see and enrol as that seat, granting itself reach it does not hold. Loopback and an all-rooms admin mint anything.

## What this deliberately is not

Not per-room removal: narrowing a live seat's scope is revoke-and-re-grant today, and a seat has already read what it read — a later record type makes membership changes auditable events. Not named room-sets or glob membership: a pattern grants rooms that do not exist yet, which is the standing pre-authorization scoping exists to avoid; a glob may fill checkboxes in the picker, never be stored authority. Not private events within a room: the room stays the unit of visibility. Not cross-hub anything.
