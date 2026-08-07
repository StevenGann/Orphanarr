#!/usr/bin/env bash
# Orphanarr — end-to-end round trip.
#
# WHAT THIS PROVES
# ----------------
# The unit tests prove each package in isolation. This proves the claims that
# only exist once they are wired together, and it asserts them against a real
# filesystem rather than a mock:
#
#   1. The orphan predicate rejects a CATEGORISED torrent and accepts an
#      uncategorised one, from the same client, in the same scan.
#   2. A plan is inert: after scanning, the library is still empty.
#   3. Executing places the file at the Plex/Jellyfin path.
#   4. The SOURCE IS BYTE-IDENTICAL afterwards (I1).
#   5. Undo removes exactly what was created and nothing else, and the source
#      is still intact after it.
#
# Claim 4 is the one worth having a test for at this level. Every package
# refuses to touch a source individually; this asserts that the composition
# of all of them still does.
#
# Run:  bash tests/e2e/roundtrip.sh [path-to-orphanarr-binary]
# Exit: 0 if every assertion holds, 1 on the first that does not.
set -u

BIN="${1:-./orphanarr}"
if [ ! -x "$BIN" ]; then
    echo "SKIP: no orphanarr binary at $BIN (build it first)" >&2
    exit 2
fi

PORT_QB="${PORT_QB:-18899}"
PORT_APP="${PORT_APP:-18793}"
WORK="$(mktemp -d)"
HERE="$(cd "$(dirname "$0")" && pwd)"
fail=0

cleanup() {
    [ -n "${APP_PID:-}" ] && kill "$APP_PID" 2>/dev/null
    [ -n "${QB_PID:-}" ] && kill "$QB_PID" 2>/dev/null
    rm -rf "$WORK"
}
trap cleanup EXIT

ok()  { echo "[VERIFIED] $1"; }
bad() { echo "[REFUTED ] $1"; fail=1; }

DL="$WORK/dl"
MEDIA="$WORK/media/movies"
PAYLOAD='MATRIX-PAYLOAD-BYTES-24!'
mkdir -p "$WORK/cfg" "$DL/The.Matrix.1999.1080p.BluRay.x264-AMIABLE" "$MEDIA"
SRC="$DL/The.Matrix.1999.1080p.BluRay.x264-AMIABLE/the.matrix.1999.mkv"
printf '%s' "$PAYLOAD" > "$SRC"
SRC_SUM_BEFORE="$(sha256sum "$SRC" | cut -d' ' -f1)"

python3 "$HERE/fake_qbittorrent.py" "$PORT_QB" "$DL" & QB_PID=$!
for _ in $(seq 1 30); do
    curl -fsS "http://127.0.0.1:$PORT_QB/api/v2/app/version" >/dev/null 2>&1 && break
    sleep 1
done

start_app() {
    "$BIN" --config "$WORK/cfg" --addr "127.0.0.1:$PORT_APP" >> "$WORK/app.log" 2>&1 & APP_PID=$!
    for _ in $(seq 1 30); do
        curl -fsS "http://127.0.0.1:$PORT_APP/api/v1/health" >/dev/null 2>&1 && return 0
        sleep 1
    done
    echo "app never became healthy; log follows" >&2
    cat "$WORK/app.log" >&2
    exit 1
}
start_app

KEY="$(grep -o 'api_key=[a-f0-9]*' "$WORK/app.log" | head -1 | cut -d= -f2)"
BASE="http://127.0.0.1:$PORT_APP/api/v1"
api() { curl -s -H "X-Api-Key: $KEY" -H 'Content-Type: application/json' "$@"; }
field() { python3 -c "import json,sys;d=json.load(sys.stdin);print($1)"; }

echo "=== A. configuration through the API ==="

probe="$(api -X POST -d "{\"name\":\"qb\",\"base_url\":\"http://127.0.0.1:$PORT_QB\",\"username\":\"admin\",\"password\":\"x\"}" "$BASE/clients/test")"
if [ "$(echo "$probe" | field "d.get('uncategorised_complete')")" = "1" ]; then
    ok "testing a client reports the uncategorised count BEFORE saving it"
else
    bad "client test did not report the uncategorised count: $probe"
fi

api -X POST -d "{\"name\":\"qb\",\"base_url\":\"http://127.0.0.1:$PORT_QB\",\"username\":\"admin\",\"password\":\"x\",\"enabled\":true}" "$BASE/clients" >/dev/null
api -X POST -d "{\"media_type\":\"movie\",\"root\":\"$MEDIA\",\"enabled\":true}" "$BASE/libraries" >/dev/null
# Settle window to zero so the scan does not defer; dry-run off so the
# execute step is reachable at all.
api -X PUT -d '{"scan__settle_seconds":"0","ops__dry_run":"false"}' "$BASE/settings" >/dev/null

kill "$APP_PID"; wait "$APP_PID" 2>/dev/null
start_app

echo "=== B. scan ==="

scan="$(api -X POST "$BASE/scan")"
orphans="$(echo "$scan" | field "d['orphans']")"
skipped="$(echo "$scan" | field "d['skipped'].get('SKIP_CATEGORIZED', 0)")"

[ "$orphans" = "1" ] && ok "one uncategorised torrent was accepted as an orphan" \
    || bad "expected 1 orphan, got $orphans ($scan)"
[ "$skipped" = "1" ] && ok "the categorised torrent was rejected by the predicate" \
    || bad "expected 1 SKIP_CATEGORIZED, got $skipped"

if [ -z "$(find "$MEDIA" -type f 2>/dev/null)" ]; then
    ok "a plan is inert: scanning wrote nothing into the library"
else
    bad "scanning created files in the library before anything was approved"
fi

echo "=== C. execute ==="

PLAN="$(api "$BASE/plans" | field "d['plans'][0]['id'] if d['plans'] else 0")"
[ "$PLAN" != "0" ] || { bad "no plan was produced"; exit 1; }

dst="$(api "$BASE/plans/$PLAN" | field "d['steps'][0]['dst_path']")"
case "$dst" in
    */"The Matrix (1999)"/"The Matrix (1999).mkv")
        ok "the plan targets the Plex/Jellyfin path: .../The Matrix (1999)/The Matrix (1999).mkv" ;;
    *) bad "unexpected destination: $dst" ;;
esac

api -X POST "$BASE/plans/$PLAN/execute" >/dev/null
if [ -f "$dst" ] && [ "$(cat "$dst")" = "$PAYLOAD" ]; then
    ok "the file was placed with its content intact"
else
    bad "the file was not placed correctly at $dst"
fi

# The claim this whole script exists for.
SRC_SUM_AFTER="$(sha256sum "$SRC" | cut -d' ' -f1)"
if [ "$SRC_SUM_BEFORE" = "$SRC_SUM_AFTER" ]; then
    ok "I1: the source is byte-identical after a full execute"
else
    bad "I1 VIOLATED: the source changed during execution"
fi

echo "=== D. undo ==="

api -X POST "$BASE/plans/$PLAN/undo" >/dev/null
if [ ! -e "$dst" ]; then
    ok "undo removed the file it created"
else
    bad "undo left the placed file behind"
fi
if [ -d "$MEDIA" ]; then
    ok "undo did NOT remove the library root, which it did not create"
else
    bad "undo removed a directory it did not create"
fi
if [ -f "$SRC" ] && [ "$(sha256sum "$SRC" | cut -d' ' -f1)" = "$SRC_SUM_BEFORE" ]; then
    ok "the source is still intact after undo"
else
    bad "the source was damaged by undo"
fi

echo
if [ "$fail" = "0" ]; then
    echo "round trip: every assertion held"
else
    echo "round trip: FAILURES ABOVE"
fi
exit "$fail"
