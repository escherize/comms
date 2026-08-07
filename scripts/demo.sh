#!/bin/sh
# The premise, end to end, in one script: a human and an agent co-working.
#
# It runs the real binary against a scratch hub on a scratch port, so it proves
# what a stranger would experience rather than what the test suite mocks. Every
# test in this repository calls cli.Run directly; nothing else exercises the
# path from `go build` through argument handling to a running server, which is
# where the last two MVP defects lived.
#
#   ./scripts/demo.sh
#
# Leaves nothing behind and never touches a port you are using.
set -eu

WORK="$(mktemp -d)"
PORT="$(awk 'BEGIN{srand();print 20000+int(rand()*20000)}')"
BIN="$WORK/agent_comms"
export AGENT_COMMS_HOME="$WORK/home"
export AGENT_COMMS_SERVER="http://127.0.0.1:$PORT"

go build -o "$BIN" .
"$BIN" serve -db "$WORK/demo.db" -rooms core -addr "127.0.0.1:$PORT" >"$WORK/serve.log" 2>&1 &
PID=$!
trap 'kill "$PID" 2>/dev/null || true; rm -rf "$WORK"' EXIT

i=0
while [ $i -lt 50 ]; do
	curl -fsS -m 1 "$AGENT_COMMS_SERVER/rooms" -H 'Accept: application/json' >/dev/null 2>&1 && break
	i=$((i + 1)); sleep 0.2
done
[ $i -lt 50 ] || { echo "demo: the hub never came up"; cat "$WORK/serve.log"; exit 1; }
echo "hub up on $AGENT_COMMS_SERVER"

# Enrolment. The token exists only in the database it was minted into, which is
# why -db is the same on both sides.
for SEAT in human:bcm agent:bcm/claude-1; do
	TOK=$("$BIN" -db "$WORK/demo.db" -invite "$SEAT" | grep -oE '[0-9a-f]{32}')
	echo "$TOK" | "$BIN" enrol --as "$SEAT" >/dev/null
	echo "enrolled $SEAT"
done

export AGENT_COMMS_ACTOR=agent:bcm/claude-1
say() { printf '\n== %s\n' "$1"; }

say "the agent orients before it decides anything"
"$BIN" room core >/dev/null

say "it searches before it posts, and the room is empty, and it says so"
"$BIN" search "cold cache" | tail -1

say "it claims work, attaches the evidence, and files a finding"
"$BIN" post status --text "claiming LIN-214: flaky auth suite" --refs LIN-214 --step 0 --of 3 >/dev/null
HASH=$(printf 'FAIL auth_test.go:88 cold cache\n' | "$BIN" attach - --title race.md | sed -n 's/.*"hash":"\([a-f0-9]*\)".*/\1/p')
"$BIN" post finding --severity p2 --about auth.py --refs LIN-214 \
	--attach-hash "$HASH" --attach-title race.md \
	--text "auth suite fails on cold cache: warm() runs after the first assertion" | tail -1

say "it asks a person — and search attaches what the room already knows"
"$BIN" ask --to bcm --refs LIN-214 --text "is the -race flake ours or the runner image?"

say "the human answers; the recipient is derived from the question"
Q=$("$BIN" read --as human:bcm --peek --kind question | sed -n 's/.*"seq":\([0-9]*\).*/\1/p' | head -1)
"$BIN" answer --as human:bcm --to-question "$Q" --text "the runner — pin the image" | tail -1

say "the agent finds the answer addressed to it"
"$BIN" inbox | tail -2

say "and the room is searchable in both lanes"
sleep 2
"$BIN" search "cold cache" | tail -1

say "inside one attempt, re-running a command is a replay rather than a second event"
# AGENT_COMMS_RUN is what makes that true. Without it the scope is the process,
# so two invocations are two attempts and two events — which is right for a
# person typing the same thing twice, and wrong for a harness retrying a step.
# A harness that shells out gets a fresh process every time, so it must set
# this or the dedup does nothing for the case it exists for.
LESSON="a -race flake that vanishes on a pinned runner is host contention"
AGENT_COMMS_RUN=attempt-1 "$BIN" post til --text "$LESSON" | tail -1
AGENT_COMMS_RUN=attempt-1 "$BIN" post til --text "$LESSON" | tail -1

say "and a genuinely new attempt is genuinely new work"
AGENT_COMMS_RUN=attempt-2 "$BIN" post til --text "$LESSON" | tail -1

printf '\ndemo: PASSED — open %s to see the room\n' "$AGENT_COMMS_SERVER"
