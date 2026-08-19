# Handoff — Ticket 33 (room-scoped invites) + invite UX

**Date:** 2026-08-17
**Branch:** `ticket-33-gui` (5 commits ahead of `main`, unmerged, all green under `./check`)
**Plan:** `~/.claude/plans/playful-inventing-moonbeam.md`
**ADR:** `docs/adr/0015-room-scoped-reads-and-always-on-auth.md`

## Status: functionally complete, one live browser bug open

All 10 plan steps are built, committed, and pass the full gate. The security
core (steps 1–8) is already merged to `main` and pushed. Steps 9–10, three
security fixes found by live testing, and a UX batch are on `ticket-33-gui`,
**not yet merged**. There is **one open bug**: the browser page hangs during the
`#setup=` enrol flow (see below). Resolve that, re-QA the GUI, then merge.

## What shipped

Room scoping is the tenancy unit: an invite carries a room scope, redeeming
binds seat→room membership, the decider refuses posts outside membership, reads
are filtered by membership. A polarity flip fell out of it: **reads are now
always authenticated — there is no open-read mode**, `--read-auth` is a
deprecated no-op. `--superuser` invites grant all-rooms + the invite capability
(distinct from `--rooms all`, which is a member not an admin).

### Commits on `main` (merged, pushed): steps 1–8
- 1 membership table + grandfather migration
- 2 invite.scope column threaded through mint/lookup
- 3 RedeemInvite binds membership in-tx
- 4 decider `room.not_a_member` (no-leak: checked before RoomExists)
- 5 reads always authenticated + membership-filtered (search scoped in SQL)
- 6 artifact_ref index + `/a/{hash}` membership authz
- 7 CLI `--rooms` + scoped-admin subset authz
- 8 human invite prints a `#setup=` URL

### Commits on `ticket-33-gui` (unmerged): steps 9–10 + fixes
- `17d335e` step 9 — GUI invite room picker (progressive disclosure)
- `22e0ece` step 10 — ADR-0015
- `ffefe58` — **3 loopback-trust security fixes** found kicking the tires:
  1. `serve --as` never granted the owner `*` membership → owner locked out.
  2. loopback bypassed read-scoping → `comms read --as scoped-seat` on the box
     saw every room (the six-agents-on-one-loopback hole).
  3. loopback bypassed invite subset-authz → a scoped admin `--as` itself could
     mint into any room.
  Root cause for all three: **loopback trust was too broad and overrode an
  explicitly named seat's scope.** Fix: identity wins over locality — only a
  *seatless* loopback request is the operator; a named `--as` seat is bound by
  its own scope everywhere. Regression tests added.
- `eaf13e2` — superuser invite + setup pre-fills the seat from its token
- `4098b2c` — invite UX batch (see below)

## RESOLVED (2026-08-17, a23b740) — the "hang" was connection-pool starvation

Not a JS bug and not the unlock page (loopback bypasses the read gate, so
on-box browsers get the room page). Browsers cap HTTP/1.1 at ~6 sockets per
host; every room/search tab held its `/stream` EventSource forever, so the
`#setup=` navigation queued behind them. Fix: hidden tabs close the stream and
resume from `lastEventId` on show; `hashchange` -> reload honours a setup link
pasted into an open tab. Playwright cannot verify this (its automation flags
pin visibilityState to "visible") — verify by hand. Original notes below.

## THE OPEN BUG (superseded) — browser page hangs on `#setup=` enrol

**Symptom:** opening `http://127.0.0.1:7799/#setup=<token>` hangs the page.
**Hub is healthy** — `curl /index` returns 200 in <1ms, token is live. So it is
a **client-side JS hang**, introduced in the last UX commit (`4098b2c`) or the
one before. Bisect against `eaf13e2` (which enrolled fine — the user posted "hi"
successfully at that point).

**Where to look — `shell/session.go` `unlockPage` JS** (the always-on read gate
means an unenrolled browser lands on the unlock page, NOT the room page):
- The `#setup=` branch added in `4098b2c` calls `/invites/whose` → `enrolThen`
  → `establish` → `location.reload()`. A hang is most likely an **unresolved
  promise or a reload loop**: if `establish` reloads but the session did not
  actually stick, the reload re-hits the unlock page, re-runs setup, loops.
- Check: does `enrolThen`'s `establish` set the session cookie before
  `location.reload()`? If the reload races the cookie write, or the fresh key
  enrolled but the challenge/session step failed silently, you get a spin.
- Also check `shell/html.go` `keyFor` (composer path) — the token-clear added in
  `4098b2c` (`if(tf) tf.value=''`) and the `chosenSeat()`/prefix-picker changes.
  A thrown error in the submit chain would surface as a red error, not a hang,
  so the hang is more likely the unlock-page reload path.

**Fastest repro/debug:** open the URL with devtools console open; a hang with no
error = stuck promise; repeated identical network calls = reload loop. The
server log is at `/tmp/kick.txt`; the scratch db at `/tmp/kick.db`.

**A running hub is up** on `127.0.0.1:7799` (db `/tmp/kick.db`, owner
`human:owner` enrolled as superuser, rooms `core`+`comms`). Mint a fresh setup
token via loopback:
```
curl -s http://127.0.0.1:7799/invite -X POST -H 'Content-Type: application/json' \
  -d '{"actor":"human:admin","rooms":"superuser"}' | grep -oE '"token":"[0-9a-f]{32}"'
```
then open `http://127.0.0.1:7799/#setup=<token>`.

## The UX batch in `4098b2c` (verify these once the hang is fixed)
- unlock page reads `#setup=` and enrols fresh (overrides stale IndexedDB key)
- composer clears the spent token after enrol (was: 2nd post resent used token)
- invite panel is a vertical stack (was a row-flex that wrapped catastrophically)
- seat namespace is a `human:`/`agent:` picker, not typed
- inline room-create inside the scope disclosure (preserves ticked boxes)
- copy button matches mint, does not resize on click
- **human prompt**: `comms invite human:* --prompt` and the GUI copy button now
  give a person-flavoured blurb (setup link + one enrol command, no harness
  steps); the button label adapts by namespace. CLI-tested & working.
- repo link (`github.com/escherize/comms`) in the settings theme panel

## To finish
1. Fix the `#setup=` page hang (unlock-page reload path, `shell/session.go`).
2. Re-QA the GUI in a browser: enrol, gear → invite (prefix picker, inline room
   create, human vs agent copy button), superuser toggle disables the picker.
3. `./check` green, then `git checkout main && git merge --ff-only ticket-33-gui
   && git push origin main`.
4. Delete the branch; nuke the `/tmp/kick.db` scratch hub.

## Notes / non-goals (already decided, do not build)
- Member removal / scope narrowing after redemption = revoke + re-grant (later
  auditable record type).
- Named room-sets / regex membership: rejected (future-room auto-grant). A glob
  may only fill checkboxes in the UI, never be stored authority.
- The Fly deploy is to be nuked (`flyctl apps destroy
  agent-comms-thrumming-tree-1758`); nothing valuable on it. Needs `fly auth`
  which this environment lacks — run it on a box with flyctl.
