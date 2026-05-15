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

# Stage 7: /metrics observability surface (v0.0.7+). Asserts the JSON
# schema fields present + counters reflect the capture we just performed.
# Acts as a regression gate on the v0.0.7 endpoint contract.
metrics_body=$(curl -sf --unix-socket "$SOCKET" "http://localhost/metrics" 2>/dev/null || echo '{}')
echo "$metrics_body" | python3 -c "
import json, sys
m = json.loads(sys.stdin.read())
required = ('version', 'uptime_seconds', 'engram_total', 'engram_by_surface',
            'capture_skipped', 'db_path', 'db_size_bytes')
missing = [f for f in required if f not in m]
if missing:
    print(f'demo-smoke: /metrics missing fields {missing}: {m}', file=sys.stderr); sys.exit(1)
if not isinstance(m['engram_by_surface'], dict):
    print(f'demo-smoke: engram_by_surface not dict: {m[\"engram_by_surface\"]}', file=sys.stderr); sys.exit(1)
# We just captured at least 1 engram in stage 5 — total must be >= 1.
if m['engram_total'] < 1:
    print(f'demo-smoke: engram_total={m[\"engram_total\"]} < 1 after capture (regression?)', file=sys.stderr); sys.exit(1)
# Skip-counter must be 0 — our marker payload is well under MaxPayloadBytes.
if m['capture_skipped'] != 0:
    print(f'demo-smoke: capture_skipped={m[\"capture_skipped\"]} > 0 (oversized payload regression?)', file=sys.stderr); sys.exit(1)
print('demo-smoke: /metrics schema + counters OK')
"

# Stage 8: caller authentication contract (v0.0.9+). Spawns a SECOND
# daemon instance with EIDETIC_AUTH=1, verifies the 4 contract cases:
# (a) /healthz open even with auth, (b) /metrics 401 without token,
# (c) /metrics 401 with wrong token, (d) /metrics 200 with valid Bearer.
# Acts as a regression gate on the v0.0.9 auth contract.
AUTH_SOCKET="${SOCKET}.auth"
AUTH_DATADIR="${DATADIR}-auth"
AUTH_WATCH_BASE="${WATCH_BASE}-auth"
mkdir -p "$AUTH_DATADIR" "$AUTH_WATCH_BASE/.claude/projects"

EIDETIC_DATA_DIR="$AUTH_DATADIR" EIDETIC_AUTH=1 HOME="$AUTH_WATCH_BASE" \
    "$BINARY" -uds "$AUTH_SOCKET" >/dev/null 2>&1 &
AUTH_PID=$!
trap 'cleanup; [ -n "${AUTH_PID:-}" ] && kill "$AUTH_PID" 2>/dev/null || true; rm -f "$AUTH_SOCKET"; rm -rf "$AUTH_DATADIR" "$AUTH_WATCH_BASE"' EXIT

deadline=$(( $(date +%s) + MAX_READY_SEC ))
ready=0
while [ "$(date +%s)" -lt "$deadline" ]; do
    if curl -sf --unix-socket "$AUTH_SOCKET" "http://localhost/healthz" >/dev/null 2>&1; then
        ready=1; break
    fi
    sleep 0.1
done
if [ "$ready" = "0" ]; then
    echo "demo-smoke: auth daemon /healthz did not respond within ${MAX_READY_SEC}s" >&2
    exit 1
fi

[ -f "$AUTH_DATADIR/auth-token" ] || { echo "demo-smoke: auth-token file not written when EIDETIC_AUTH=1" >&2; exit 1; }
TOKEN_PERMS=$(stat -f '%Lp' "$AUTH_DATADIR/auth-token" 2>/dev/null || stat -c '%a' "$AUTH_DATADIR/auth-token" 2>/dev/null)
if [ "$TOKEN_PERMS" != "600" ]; then
    echo "demo-smoke: auth-token perms=$TOKEN_PERMS, want 600" >&2
    exit 1
fi
TOKEN=$(cat "$AUTH_DATADIR/auth-token")
[ "${#TOKEN}" = "64" ] || { echo "demo-smoke: token len=${#TOKEN}, want 64" >&2; exit 1; }

# Case (a): /healthz open even with auth on
hz_code=$(curl -s -o /dev/null -w '%{http_code}' --unix-socket "$AUTH_SOCKET" "http://localhost/healthz")
[ "$hz_code" = "200" ] || { echo "demo-smoke: /healthz with auth: got $hz_code, want 200 (open path contract violation)" >&2; exit 1; }

# Case (b): /metrics without token = 401
m_no_token=$(curl -s -o /dev/null -w '%{http_code}' --unix-socket "$AUTH_SOCKET" "http://localhost/metrics")
[ "$m_no_token" = "401" ] || { echo "demo-smoke: /metrics without token: got $m_no_token, want 401" >&2; exit 1; }

# Case (c): /metrics with wrong token = 401
m_wrong=$(curl -s -o /dev/null -w '%{http_code}' -H 'Authorization: Bearer wrong-token' --unix-socket "$AUTH_SOCKET" "http://localhost/metrics")
[ "$m_wrong" = "401" ] || { echo "demo-smoke: /metrics with wrong token: got $m_wrong, want 401" >&2; exit 1; }

# Case (d): /metrics with valid Bearer = 200
m_ok=$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $TOKEN" --unix-socket "$AUTH_SOCKET" "http://localhost/metrics")
[ "$m_ok" = "200" ] || { echo "demo-smoke: /metrics with valid token: got $m_ok, want 200" >&2; exit 1; }

echo "demo-smoke: auth contract OK (token 64ch perm 600; healthz open; metrics 401/401/200)"

echo "demo-smoke: PASS — spec § 8 acceptance #3 (write→capture→read end-to-end) + /metrics schema gate + v0.0.9 auth contract hold"
