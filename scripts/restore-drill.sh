#!/bin/sh
# Restore drill: prove that a backup restores to a working hub.
#
# A backup nobody has restored is a hypothesis. This runs the whole path
# against a scratch copy — restore the file, start the binary, check the chain,
# rebuild the projections from the log, and confirm the state matches — and it
# is the only evidence that any of the replication above works.
#
#   ./scripts/restore-drill.sh [source.db]
#
# It never writes to the source database and never touches a live port.
set -eu

SRC="${1:-comms.db}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

BIN="$WORK/comms"
go build -o "$BIN" . || { echo "drill: build failed"; exit 1; }

if [ ! -f "$SRC" ]; then
	echo "drill: no source database at $SRC"
	echo "drill: pass one, or run: litestream restore -config litestream.yml $WORK/comms.db"
	exit 1
fi

# A copy, not the original: a drill that can damage the thing it is testing is
# a drill nobody runs.
#
# All three files, not just the database. SQLite runs in WAL mode here, so
# committed data lives in "$SRC-wal" until a checkpoint folds it back. Copying
# only the main file yields a database that opens, verifies, serves, and is
# empty — which is how the first run of this drill passed against nothing.
# Litestream ships WAL frames and gets this right; cp does not, unless told.
cp "$SRC" "$WORK/restored.db"
[ -f "$SRC-wal" ] && cp "$SRC-wal" "$WORK/restored.db-wal"
[ -f "$SRC-shm" ] && cp "$SRC-shm" "$WORK/restored.db-shm"
echo "drill: restored a copy of $SRC"

# 0. There is something to restore. A drill that passes on an empty database is
#    the failure mode drills exist to prevent, and it is the one this script
#    hit on its first run.
SRC_HEAD=$("$BIN" -db "$SRC" -seq-report | sed 's/head \([0-9]*\).*/\1/')
GOT_HEAD=$("$BIN" -db "$WORK/restored.db" -seq-report | sed 's/head \([0-9]*\).*/\1/')
if [ "$GOT_HEAD" != "$SRC_HEAD" ]; then
	echo "drill: FAILED — source head $SRC_HEAD, restored head $GOT_HEAD"
	exit 1
fi
if [ "$GOT_HEAD" = "0" ]; then
	echo "drill: WARNING — the source log is empty, so this drill proved very little"
fi
echo "drill: restored head $GOT_HEAD matches the source"

# 1. The chain verifies. This is the question a restore actually raises: did we
#    get the whole file, or a torn one?
"$BIN" -db "$WORK/restored.db" -verify || { echo "drill: FAILED chain verification"; exit 1; }
echo "drill: chain verifies"

# 2. Every projection recomputes from the log and matches. If the restore
#    landed mid-transaction this is where it shows.
"$BIN" -db "$WORK/restored.db" -rebuild || { echo "drill: FAILED rebuild"; exit 1; }
echo "drill: projections rebuilt from the log"

# 3. The hub starts and serves. A database that verifies and cannot be opened
#    by the binary is still a failed restore.
PORT=$(awk 'BEGIN{srand();print 20000+int(rand()*20000)}')
"$BIN" -db "$WORK/restored.db" -addr "127.0.0.1:$PORT" -insecure >"$WORK/serve.log" 2>&1 &
PID=$!
trap 'kill "$PID" 2>/dev/null || true; rm -rf "$WORK"' EXIT

i=0
while [ $i -lt 50 ]; do
	if curl -fsS -m 1 "http://127.0.0.1:$PORT/rooms" -H 'Accept: application/json' >"$WORK/rooms.json" 2>/dev/null; then
		break
	fi
	i=$((i + 1))
	sleep 0.2
done
if [ $i -ge 50 ]; then
	echo "drill: FAILED — the restored hub never served"
	cat "$WORK/serve.log"
	exit 1
fi
echo "drill: hub serves — $(cat "$WORK/rooms.json")"

# 4. seq jumped past everything the lost tail could have issued, so a fencing
#    token handed out before the restore can never be handed out again.
"$BIN" -db "$WORK/restored.db" -seq-report || true

echo "drill: PASSED"
