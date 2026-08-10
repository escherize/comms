package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Idempotency, told one way.
//
// The HTTP API requires `idem` on every command and refuses without it; the
// client had no flag and generated a random one silently. Two agents in the
// 2026-08-07 study reached for `--idempotency-key`, found nothing, and drew
// opposite conclusions about whose job deduplication is — one filed it as a
// missing feature, the other as a deliberate design. Only the raw API told
// them there was a question, and it told them by rejecting them.
//
// Both behaviours are defensible; the pair is not. A random key means a
// re-run is always a new event, so the fix an agent reaches for by reflex —
// run it again — is exactly the thing that turns one finding into two. A
// content-derived key means a re-run of the *same* command is a replay, and
// only a genuinely different command is a new event.

// runKey scopes idempotency to one logical attempt. Without it, posting the
// identical status twice an hour apart would be silently swallowed as a replay,
// which is worse than a duplicate: the second one is true and it vanishes.
//
// It comes from the environment when a harness sets one, so a supervisor can
// make a retry of a whole step be a retry rather than new work.
//
// For an agent seat with no run set, the scope is the seat. One session, one
// seat means the seat is the session, and a pid cannot say that: an agent
// shells out once per command, and the same session resumes under new pids,
// so process scope made every re-run a duplicate — the exact failure the key
// exists to prevent, defaulting to off for the population most likely to
// re-run. The cost is that an agent re-posting byte-identical content later
// in its session gets a replay; COMMS_RUN names new work when that is
// wrong, and content that differs at all is a new event regardless.
//
// A human seat keeps process scope: human seats live for months, and a person
// typing the same command in a fresh shell means it again.
func runKey(e *Env) string {
	if v, ok := e.getenv("COMMS_RUN"); ok && v != "" {
		return v
	}
	if strings.HasPrefix(e.Seat, "agent:") {
		return "seat-" + e.Seat
	}
	return processRun
}

// processRun is stable for the life of this process and different across
// processes.
var processRun = fmt.Sprintf("pid-%d-%d", os.Getpid(), os.Getpid()*2654435761)

// contentIdem derives the key from what is being posted, so the same command is
// the same key. Every field that distinguishes one post from another is in it;
// nothing that varies between two runs of the same intent is.
func contentIdem(e *Env, cmd map[string]any) string {
	h := sha256.New()
	fmt.Fprintf(h, "run=%s\n", runKey(e))

	keys := make([]string, 0, len(cmd))
	for k := range cmd {
		if k == "idem" {
			continue // the thing being derived cannot be an input to itself
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(h, "%s=%s\n", k, stableString(cmd[k]))
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// stableString renders a value the same way every time. Map iteration order is
// randomized in Go, so a body rendered by ranging would produce a different key
// for the same command on every run — which is the bug this whole file exists
// to remove, reintroduced one level down.
func stableString(v any) string {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		for _, k := range keys {
			fmt.Fprintf(&b, "%s:%s;", k, stableString(t[k]))
		}
		return b.String()
	case []any:
		var b strings.Builder
		for _, item := range t {
			fmt.Fprintf(&b, "%s,", stableString(item))
		}
		return b.String()
	case []string:
		var b strings.Builder
		for _, item := range t {
			fmt.Fprintf(&b, "%s,", item)
		}
		return b.String()
	case []map[string]any:
		var b strings.Builder
		for _, item := range t {
			fmt.Fprintf(&b, "%s,", stableString(item))
		}
		return b.String()
	default:
		return fmt.Sprint(v)
	}
}

// applyIdem sets the command's key: the caller's if they have a natural one,
// otherwise one derived from the content.
func applyIdem(e *Env, cmd map[string]any, explicit string) {
	if explicit != "" {
		cmd["idem"] = explicit
		return
	}
	cmd["idem"] = contentIdem(e, cmd)
}
