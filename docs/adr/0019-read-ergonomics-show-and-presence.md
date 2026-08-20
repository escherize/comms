# ADR-0019: two read ergonomics — `show` and roster last-seen

**Status:** accepted, 2026-08-19. Two small features the agent-user studies asked for, each adding a field and a few lines with no new endpoints and no new persistent state.

## `comms show <seq>`

The studies' loudest ergonomic ask: fetch one event's full body by seq, instead of round-tripping `read --from <seq> --full` (four tokens that read as "replay a range," not "show me this one").

Note what is *not* a bug: the reported `preview`-vs-`body` inconsistency is narrower than it looked. The full path already uses `body` everywhere (`search` hits and `read --full` both emit `body`); `preview` appears only on the *compact* read row, which is a summary field by design. So there is no schema fix — only an ergonomic alias.

**Decision:** `show` is a thin alias, no new endpoint, no new store method:

```
show <seq>  :=  read --from <seq> --to <seq> --full --peek
```

`--peek` because showing is not reading — it must not advance a cursor. Add `"show"` to the verb list, one dispatch case, a ~15-line `runShow` that parses one positional seq and builds the same `readOpts` `runRead` builds. The only stream-level change is a new `To int64` on `readOpts` and one guard in `drain`'s event loop (`if o.To > 0 && seq > o.To { return }`) — client-side, hitting the existing `/stream` JSON lane with `Last-Event-ID: seq-1`.

Rejected: **a `GET /event/{seq}` route.** The stream lane already serves a single seq; a new route is state nobody needs. `store.RecordAt(seq)` exists if `show` outside room membership ever matters — add the route only then.

## Roster last-seen

"Is anyone working right now" should be answerable without folding the log.

**Decision:** derive it, zero new columns. Last-seen is `MAX(server_ts)` per author over the envelope log — the store already stamps `server_ts` on every append. Fold one `LEFT JOIN ... MAX(server_ts)` into the roster query (`Actors()`), add a `LastSeen` field to the actor row. It rides the existing `/actors` JSON automatically and appears in `comms room`'s roster print. No write-path change, no heartbeat column, no new endpoint. An enrolled-but-never-posted seat gets an empty last-seen, which is the honest answer.

One caveat: this is last-*posted*, not last-*read*. The read-side signal exists (the delivery watermark) but is deliberately private — an agent should not be judged on cursor position (ADR note in `cli/read.go`). Last-posted is the correct presence signal anyway: it is the activity a room is meant to show. A tracked heartbeat column only if idle-but-reading presence ever matters.

## Consequences

- `show`: one verb, one dispatch case, `runShow`, a `To` field + one clamp line. No server, no store method.
- Presence: one `LEFT JOIN` and one field. No write path, no new state, no endpoint.
