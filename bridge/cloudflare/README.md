# eidetic-sync Cloudflare Worker

Receives SQLite backup uploads from `eideticd` and stores them in Cloudflare R2.
This is the cloud sync component for `eidetic-daemon` (ADR-019).

**Cost:** ~$0.38/mo on a 25 MB engrams.db. Stays in R2 free tier at current dataset size.

---

## Prerequisites

- [Node.js](https://nodejs.org/) 18+
- [Wrangler CLI](https://developers.cloudflare.com/workers/wrangler/): `npm install -g wrangler`
- Cloudflare account (free tier sufficient)

---

## Deploy the Worker

**1. Authenticate Wrangler**
```sh
wrangler login
```

**2. Create the R2 bucket**
```sh
wrangler r2 bucket create eidetic-engrams
```

**3. Set the API key secret**

Generate a secure random key (keep this — you'll put it in `sync.json` on your machine):
```sh
openssl rand -hex 32
# → e.g. a3f8c1d2e4b5...
```

Store it as a Wrangler secret:
```sh
wrangler secret put EIDETIC_API_KEY
# Paste the key when prompted
```

**4. Deploy**
```sh
cd bridge/cloudflare
wrangler deploy
```

Output: `Deployed to https://eidetic-sync.<your-account>.workers.dev`

---

## Configure the daemon

Create `~/.eidetic/sync.json`:
```json
{
  "worker_url": "https://eidetic-sync.<your-account>.workers.dev",
  "api_key":    "<the key from step 3>",
  "device_id":  "macbook-m2",
  "sync_interval": 60
}
```

- `device_id`: 4–64 chars, lowercase alphanum + `-` + `_`. Used as the R2 path prefix. Pick something that identifies the machine.
- `sync_interval`: minutes between automatic uploads. Default 60 if omitted.

Restart the daemon to pick up the config:
```sh
launchctl kickstart -k gui/$(id -u)/works.eidetic.eideticd
```

Or test immediately (one-shot upload + exit):
```sh
eideticd --sync-now
```

---

## Verify

Check that a backup landed in R2:
```sh
wrangler r2 object list eidetic-engrams --prefix "engrams/<your-device-id>/"
```

Or call the Worker's `/latest` endpoint:
```sh
curl -s \
  -H "Authorization: Bearer <your-api-key>" \
  -H "X-Device-ID: <your-device-id>" \
  https://eidetic-sync.<your-account>.workers.dev/latest | jq .
```

---

## Restore from backup

Download the most recent backup:
```sh
LATEST=$(wrangler r2 object list eidetic-engrams \
  --prefix "engrams/<your-device-id>/" \
  --json | jq -r '.objects | sort_by(.key) | last | .key')

wrangler r2 object get eidetic-engrams "$LATEST" --file engrams-restored.db
```

Then copy to the new machine:
```sh
mkdir -p ~/.eidetic
cp engrams-restored.db ~/.eidetic/engrams.db
```

---

## Security

- The Worker validates `Authorization: Bearer <EIDETIC_API_KEY>` on every request. Without a matching token the response is 401.
- The R2 bucket is private — not publicly accessible. Only the Worker (which holds the binding) can write or read objects.
- The API key is stored as a Wrangler secret (encrypted at rest in Cloudflare's vault, not in `wrangler.toml`).
- R2 encrypts stored objects at rest by default (Cloudflare-managed keys). Client-side encryption is a W3+ enhancement.
- The daemon reads the key from `~/.eidetic/sync.json` (owned by your user, 0600 recommended: `chmod 600 ~/.eidetic/sync.json`).

---

## Endpoints

| Method | Path       | Auth | Description |
|--------|------------|------|-------------|
| POST   | `/sync`    | ✓    | Upload `engrams.db` body. Returns `{key, byteLength, uploadedAt}` on 201. |
| GET    | `/latest`  | ✓    | Returns metadata for the most recent backup for this device. |
| GET    | `/healthz` | —    | Returns `{"status":"ok"}`. No auth required. |

---

## Limits

| Limit | Value | Notes |
|---|---|---|
| Max upload size | 500 MB | Worker guard + R2 single-object limit is 5 GB; 500 MB is the practical daemon ceiling |
| Backups retained per device | 5 | Older backups auto-pruned on each upload |
| Worker free tier | 100K req/day | Sufficient for hourly syncs across ~4000 devices |
