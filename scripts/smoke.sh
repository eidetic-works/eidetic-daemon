#!/usr/bin/env bash
# End-to-end smoke test for the built eideticd binary.
#
# Pattern: start daemon → poll readiness via /engrams probe → on first 200,
# run real assertion. Handles modernc's ~1.75s cold-init per ADR-016
# without a brittle fixed sleep.
#
# Exits 0 on smoke pass, 1 on any failure. Suitable for Makefile target.

set -euo pipefail

BINARY="${BINARY:-./bin/eideticd}"
SOCKET="${SOCKET:-/tmp/eidetic-smoke-$$.sock}"
DATADIR="${DATADIR:-/tmp/eidetic-smoke-data-$$}"
MAX_WAIT_SEC="${MAX_WAIT_SEC:-5}"

cleanup() {
    [ -n "${PID:-}" ] && kill "$PID" 2>/dev/null || true
    rm -f "$SOCKET"
    rm -rf "$DATADIR"
}
trap cleanup EXIT

[ -x "$BINARY" ] || { echo "smoke: $BINARY not found — run 'make build' first"; exit 1; }

mkdir -p "$DATADIR"
EIDETIC_DATA_DIR="$DATADIR" "$BINARY" -uds "$SOCKET" >/dev/null 2>&1 &
PID=$!

# Probe readiness — modernc init can take ~1.75s. Curl-poll until 200 or
# MAX_WAIT_SEC. Empty surface returns 400, which counts as "server is up."
deadline=$(( $(date +%s) + MAX_WAIT_SEC ))
ready=0
while [ $(date +%s) -lt $deadline ]; do
    if curl -sf --unix-socket "$SOCKET" "http://localhost/engrams?surface=ping" >/dev/null 2>&1; then
        ready=1; break
    fi
    # Also treat 400 as ready (server responded, just rejected the empty query).
    code=$(curl -s -o /dev/null -w '%{http_code}' --unix-socket "$SOCKET" "http://localhost/engrams" 2>/dev/null || echo 0)
    if [ "$code" = "400" ]; then ready=1; break; fi
    sleep 0.1
done

if [ "$ready" = "0" ]; then
    echo "smoke: daemon did not become ready within ${MAX_WAIT_SEC}s"
    exit 1
fi

# Real assertion: surface=ping returns 200 + valid JSON (empty array is fine).
body=$(curl -sf --unix-socket "$SOCKET" "http://localhost/engrams?surface=ping&limit=5")
if ! echo "$body" | python3 -c 'import sys, json; json.loads(sys.stdin.read())' 2>/dev/null; then
    echo "smoke: response is not valid JSON"
    echo "body: $body"
    exit 1
fi

echo "smoke: PASS"
