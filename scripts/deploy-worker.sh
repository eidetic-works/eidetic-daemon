#!/usr/bin/env bash
# Deploy the eidetic-sync Cloudflare Worker (one-time operator setup for Pro tier).
#
# Prerequisites:
#   - wrangler installed: npm install -g wrangler
#   - Cloudflare token with Workers:Edit + R2:Write + KV:Write
#     (set CLOUDFLARE_API_TOKEN env var OR run `wrangler login` first)
#
# Usage:
#   cd bridge/cloudflare
#   ../../scripts/deploy-worker.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
WORKER_DIR="$SCRIPT_DIR/../bridge/cloudflare"

cd "$WORKER_DIR"

echo ""
echo "=== eidetic-sync Worker deploy ==="
echo ""

# 1. Create R2 bucket (idempotent — errors if already exists, that's fine)
echo "Step 1: Creating R2 bucket 'eidetic-pro-sync' (skips if already exists)..."
wrangler r2 bucket create eidetic-pro-sync 2>/dev/null || echo "  (bucket may already exist — continuing)"

# 2. Create KV namespace for per-user keys
echo ""
echo "Step 2: Creating KV namespace 'EIDETIC_KEYS'..."
echo "  (copy the 'id' from the output below and paste into wrangler.toml [[kv_namespaces]])"
wrangler kv:namespace create EIDETIC_KEYS

# 3. Remind operator to update wrangler.toml + set secret
echo ""
echo "=== ACTION REQUIRED ==="
echo ""
echo "  a) Copy the 'id' printed above into wrangler.toml:"
echo "       [[kv_namespaces]]"
echo "       binding = \"EIDETIC_KEYS_KV\""
echo "       id = \"<paste id here>\""
echo ""
echo "  b) Set the fallback API key secret (used for self-hosted/legacy):"
echo "       wrangler secret put EIDETIC_API_KEY"
echo "       # enter a random hex key: openssl rand -hex 32"
echo ""
echo "  Then run this script again with --deploy to finish deployment:"
echo "       ../../scripts/deploy-worker.sh --deploy"
echo ""

if [[ "${1:-}" == "--deploy" ]]; then
    echo "Step 3: Deploying Worker..."
    wrangler deploy
    echo ""
    echo "=== Deploy complete ==="
    echo ""
    echo "Note the Worker URL above — it goes in every Pro user's sync.json."
    echo "Run: EIDETIC_WORKER_URL=<url> EIDETIC_KV_NS_ID=<kv-id> ./scripts/gen_pro_key.sh <email> <device_id>"
fi
