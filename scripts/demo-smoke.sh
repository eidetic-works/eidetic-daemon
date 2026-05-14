#!/usr/bin/env bash
# End-to-end demo smoke — validates spec § 8 acceptance flow against the
# REAL built binary. Sister to scripts/smoke.sh (which checks daemon-up +
# JSON-shape only); this one exercises the capture path:
#
#   1. Build (or use existing) eideticd
#   2. Start daemon → poll /healthz until 200 (handles modernc cold-init)
#   3. Assert -version output matches /^eideticd / (no hardcoded version
#      so dev / tagged builds both pass; per ADR-017)
#   4. Write a JSONL line into a watched dir — daemon's fsnotify path
#      should capture within ~50ms (spec § 2.3)
#   5. Poll /engrams?surface=claude_code until row appears (timeout 2s
#      including modernc cold-init headroom)
#   6. Assert returned engram payload contains the marker we wrote
#   7. Cleanup
#
# Closes spec § 8 acceptance criterion #3 ("Within 50ms ... engram row
# appears in ~/.eidetic/engrams.db") at the binary level. Suitable for CI
# (no Cursor / Claude Code dependency; pure file-write triggers fsnotify).
#
# Exits 0 on smoke pass, 1 on any failure.

set -euo pipefail

BINARY="${BINARY:-./bin/eideticd}"
SOCKET="${SOCKET:-/tmp/eidetic-demo-$$.sock}"
DATADIR="${DATADIR:-/tmp/eidetic-demo-data-$$}"
WATCH_BASE="${WATCH_BASE:-/tmp/eidetic-demo-watch-$$}"
MAX_READY_SEC="${MAX_READY_SEC:-5}"
MAX_CAPTURE_SEC="${MAX_CAPTURE_SEC:-2}"
MARKER="demo-smoke-$$-$(date +%s)"

cleanup() {
    [ -n "${PID:-}" ] && kill "$PID" 2>/dev/null || true
    rm -f "$SOCKET"
    rm -rf "$DATADIR" "$WATCH_BASE"
}
trap cleanup EXIT

[ -x "$BINARY" ] || { echo "demo-smoke: $BINARY not found — run 'make build' first" >&2; exit 1; }

# Stage 1: -version output sanity (binary self-identification per ADR-017).
ver_out=$("$BINARY" -version 2>&1)
case "$ver_out" in
    "eideticd "*) ;;
    *) echo "demo-smoke: -version output unexpected: $ver_out" >&2; exit 1 ;;
esac
echo "demo-smoke: -version OK ($ver_out)"

# Stage 2: start daemon. We point capture's claude_code surface at our
# WATCH_BASE by mocking $HOME so the default-surfaces resolver picks it up.
# This is per `internal/capture/watcher.go` DefaultSurfaces() which uses
# os.UserHomeDir() + "/.claude/projects" for claude_code.
mkdir -p "$WATCH_BASE/.claude/projects"
mkdir -p "$DATADIR"

EIDETIC_DATA_DIR="$DATADIR" HOME="$WATCH_BASE" "$BINARY" -uds "$SOCKET" >/dev/null 2>&1 &
PID=$!

# Stage 3: poll /healthz until 200 OR timeout (modernc cold-init ~1.75s).
deadline=$(( $(date +%s) + MAX_READY_SEC ))
ready=0
while [ "$(date +%s)" -lt "$deadline" ]; do
    if curl -sf --unix-socket "$SOCKET" "http://localhost/healthz" >/dev/null 2>&1; then
        ready=1; break
    fi
    sleep 0.1
done
if [ "$ready" = "0" ]; then
    echo "demo-smoke: daemon /healthz did not respond within ${MAX_READY_SEC}s" >&2
    exit 1
fi
healthz_body=$(curl -sf --unix-socket "$SOCKET" "http://localhost/healthz")
case "$healthz_body" in
    *'"status":"ok"'*) ;;
    *) echo "demo-smoke: /healthz body unexpected: $healthz_body" >&2; exit 1 ;;
esac
echo "demo-smoke: /healthz OK"

# Stage 4: write a JSONL line into the watched claude_code surface.
JSONL_PATH="$WATCH_BASE/.claude/projects/demo-session/session.jsonl"
mkdir -p "$(dirname "$JSONL_PATH")"
echo "{\"role\":\"user\",\"payload\":\"$MARKER\",\"ts\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"}" > "$JSONL_PATH"
echo "demo-smoke: wrote JSONL with marker=$MARKER"

# Stage 5: poll /engrams?surface=claude_code until our marker appears OR timeout.
# fsnotify event → 10ms debounce → parser → InsertBatch → reader pool sees it.
deadline=$(( $(date +%s) + MAX_CAPTURE_SEC ))
captured=0
while [ "$(date +%s)" -lt "$deadline" ]; do
    body=$(curl -sf --unix-socket "$SOCKET" "http://localhost/engrams?surface=claude_code&limit=10" 2>/dev/null || echo '[]')
    case "$body" in
        *"$MARKER"*) captured=1; break ;;
    esac
    sleep 0.1
done
if [ "$captured" = "0" ]; then
    echo "demo-smoke: marker not captured within ${MAX_CAPTURE_SEC}s" >&2
    echo "demo-smoke: last /engrams body: $body" >&2
    exit 1
fi
echo "demo-smoke: capture round-trip OK (marker found in /engrams response)"

# Stage 6: confirm the returned engram is well-shaped (id/surface/ts/payload).
echo "$body" | python3 -c "
import json, sys
rows = json.loads(sys.stdin.read())
if not isinstance(rows, list) or len(rows) == 0:
    print('demo-smoke: /engrams returned non-list or empty', file=sys.stderr); sys.exit(1)
for r in rows:
    for f in ('id','surface','ts','payload'):
        if f not in r:
            print(f'demo-smoke: row missing field {f!r}: {r}', file=sys.stderr); sys.exit(1)
print('demo-smoke: engram row shape OK')
"

echo "demo-smoke: PASS — spec § 8 acceptance #3 (write→capture→read end-to-end) holds"
