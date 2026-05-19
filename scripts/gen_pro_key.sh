#!/usr/bin/env bash
# Generates a Pro subscriber API key, prints sync.json config + KV registration command.
# Usage:
#   EIDETIC_WORKER_URL=https://eidetic-sync.<account>.workers.dev \
#   EIDETIC_KV_NS_ID=<kv-namespace-id> \
#   ./scripts/gen_pro_key.sh <email> <device_id>

set -euo pipefail

EMAIL="${1:?Usage: $0 <email> <device_id>}"
DEVICE="${2:?Usage: $0 <email> <device_id>}"
WORKER_URL="${EIDETIC_WORKER_URL:?set EIDETIC_WORKER_URL env var}"
KV_NS_ID="${EIDETIC_KV_NS_ID:-}"  # optional — if set, prints wrangler KV command

# Validate device_id: 4-64 chars, lowercase alphanum + dash + underscore
if ! echo "$DEVICE" | grep -qE '^[a-z0-9][a-z0-9_-]{2,62}[a-z0-9]$'; then
    echo "Error: device_id must be 4-64 chars, lowercase alphanum/dash/underscore" >&2
    exit 1
fi

KEY=$(openssl rand -hex 32)
TS=$(date -u +%Y-%m-%dT%H:%M:%SZ)
REGISTRY="${HOME}/.eidetic/pro-customers.csv"

# sha256 of the key for KV storage
KEY_HASH=$(echo -n "$KEY" | shasum -a 256 | cut -d' ' -f1)

# Append to registry
if [ ! -f "$REGISTRY" ]; then
    echo "timestamp,email,device_id,key_hash_prefix" > "$REGISTRY"
    chmod 600 "$REGISTRY"
fi
echo "${TS},${EMAIL},${DEVICE},${KEY_HASH:0:16}..." >> "$REGISTRY"

echo ""
echo "=== eidetic Pro — onboarding for ${EMAIL} ==="
echo ""
echo "1. Register key in Cloudflare KV (run this now):"
if [ -n "$KV_NS_ID" ]; then
    KV_META="{\"email\":\"${EMAIL}\",\"device_id\":\"${DEVICE}\",\"added\":\"${TS}\"}"
    echo "   wrangler kv:key put --namespace-id=${KV_NS_ID} ${KEY_HASH} '${KV_META}'"
else
    echo "   (set EIDETIC_KV_NS_ID env var to get the wrangler command)"
    echo "   KV key (sha256): ${KEY_HASH}"
fi

echo ""
echo "2. Email this sync.json to ${EMAIL}:"
echo ""
echo "   Drop at ~/.eidetic/sync.json:"
echo "   {"
echo "     \"worker_url\":    \"${WORKER_URL}\","
echo "     \"api_key\":       \"${KEY}\","
echo "     \"device_id\":     \"${DEVICE}\","
echo "     \"sync_interval\": 60"
echo "   }"
echo ""
echo "   Then restart:"
echo "     launchctl kickstart -k gui/\$(id -u)/works.eidetic.eideticd"
echo "   Or on Linux:"
echo "     systemctl --user restart eideticd"
echo ""
echo "   Test immediately:"
echo "     eideticd --sync-now"
echo ""
echo "============================================"
echo "Full key (DO NOT log further): ${KEY}"
echo "Key hash (KV entry):           ${KEY_HASH}"
echo "Appended to:                   ${REGISTRY}"
echo "============================================"
