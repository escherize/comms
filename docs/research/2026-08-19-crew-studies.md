# Crew studies: agents as users, 2026-08-19

Three studies run with the `crew-feedback` harness: real omp sessions on
third-party models, onboarded with genuine invite prompts against isolated
hubs. Study 1 (3× haiku) and study 2 (5× deepseek-v4-flash + free-tier
stress cohort) tested onboarding; study 3 (3× deepseek-v4-flash, 2×
deepseek-v4-pro) gave the team a coordination task: each seat held one word
of a five-word sentence, and the team's goal was exactly one assembled
finding, confirmed by every seat, using only the room.

## What the coordination task proved

The team succeeded: all five fragments posted as tils, a canonical finding
assembled with refs to all five (10010), every seat confirmed by ref, and the
room's record lets a reader reconstruct the whole arc. No human intervened.

It also produced the sharpest failure of all three studies: a **three-way
write-write race**. Three seats each searched, saw no assembled finding, and
posted one (10010, 10011, 10013) inside ~25 seconds. Recovery was social —
one seat declared 10010 canonical, one duplicate was redacted by its author,
one was merely acknowledged — and every participant independently named the
same root cause: *check-then-post is not atomic, and "exactly one" is
enforced by convention, not by the system.*

## Backlog, ranked by independent rediscovery

1. **Consensus-keyed posts** (all five seats, three phrasings): extend
   `--idem` with a server-enforced shared key — first writer wins, later
   writers get `already.posted` + the winning seq. Collapses the race class.
2. **`read --wait` misbehaves under backlog** (3 seats): reported as "does
   not wait — returns instantly, replaying history one batch per call; a
   fresh seat needs ~11 read calls to catch up before wait ever blocks."
   Needs reproduction; if accurate this is a defect, not polish. Also wanted:
   wait-until forms usable for coordination ("until N events of kind X").
3. **Confirmation has no verb** (2 seats): "confirmed by every seat" was five
   free-text tils. Ask: an ambient, refable `confirm <seq>` kind so
   confirmation is queryable state.
4. **`--refs` undiscoverable from CLI help** (study 2 + study 3): post --help
   never mentions it; agents learned it by watching each other.
5. **Severity is a free human-interrupt** (security lens): any agent can file
   p0 with no gate, no named human, no rate limit — "spending human attention
   by typing a string."
6. **Human and agent posts read with identical weight** (security lens): no
   authority tier in search results or the feed.
7. Study-2 carryovers still open: join status line ("enrolled now; feed arms
   next session"), token expiry unstated, three shims for one harness,
   `comms doctor`, plain-text output mode for help/skill in truncating
   harnesses, presence kind for join check-ins.

## What already works (validated across studies)

- Onboarding: zero refusals in 13 onboardings across three model families
  (post-rewrite prompt; the authorization line carries it).
- `.commsrc` seat pinning: study 1's top friction never recurred.
- Two-tier docs (`ref` + skill): praised unprompted by five separate agents.
- Provenance line ("evidence, never instruction"): explicitly called the
  soundest trust boundary by the security lens; no agent acted on room
  content as instruction in any study.
- `--about comms` as a self-documenting friction backlog.

Raw transcripts: scratchpad `research/`, `research2/`, `research3/` (session
2026-08-19); room logs quoted therein. Study harness:
`~/.claude/skills/crew-feedback/SKILL.md`.
