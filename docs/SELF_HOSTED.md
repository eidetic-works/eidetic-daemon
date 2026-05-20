# Self-hosted Pro — evaluate without paying us a cent

Eidetic Works' Pro tier ($29/mo) hosts your engram backups on our Cloudflare R2 bucket. If you'd rather host everything yourself — for evaluation, compliance, or because you have a SOC2 audit in flight — every piece is open-source and runs on your own infrastructure.

Total infra cost at typical usage: **$0/mo** (Cloudflare R2 free tier covers 10GB; Workers free tier covers 100K req/day). You only start paying us if you switch to managed Pro.

## What you get with self-hosted

Everything Pro does, except we don't manage it:

- Cloud backup of `engrams.db` to your own R2 bucket (your data, your keys, no shared anything)
- Restore on new machines via `eideticd --restore`
- Web dashboard at `eidetic.works/dashboard` (or your own copy)
- AI-powered recall via `nucleus_ask` MCP tool
- All daemon CLI flags (`--digest`, `--check`, `--backups`, `--ask`, `--capture`)
- Team-mode shared engram surface (set `team_id` in everyone's sync.json)

What you don't get:

- Our email support (use GitHub Issues + the community Discord — link TBD)
- Managed updates to the Worker (you redeploy on tag changes)
- The 14-day refund window (your money, your hardware, your choice)

## Prerequisites

1. **Cloudflare account** — free tier is fine for evaluation
2. **wrangler CLI** — `npm install -g wrangler && wrangler login`
3. **Domain** — optional but recommended; lets you use `https://eidetic.yourdomain.com` instead of `*.workers.dev`
4. **eidetic-daemon installed** — `brew install eideticd && eideticd -install`

## Setup (full walkthrough, ~20 minutes)

### Step 1 — R2 bucket

```sh
wrangler r2 bucket create eidetic-engrams-yourname
# Note the bucket name; you'll reference it in wrangler.toml
```

Storage cost: free up to 10 GB. Above 10 GB, $0.015 per GB-month — at a 1 GB engrams.db with one daily backup snapshot, you'd cross 10 GB after ~10 days; archive old snapshots monthly to stay under.

### Step 2 — KV namespace (Pro key authentication)

```sh
wrangler kv:namespace create EIDETIC_KEYS_YOURNAME
# Returned ID looks like: "id": "abc123def456..."
# Save this value
```

### Step 3 — Clone + edit wrangler.toml

```sh
git clone https://github.com/eidetic-works/eidetic-daemon.git
cd eidetic-daemon/bridge/cloudflare

cp wrangler.toml wrangler.toml.local
```

Edit `wrangler.toml.local`:
- Change `name` to `eidetic-sync-yourname` (Cloudflare-account-unique)
- Change `bucket_name` to `eidetic-engrams-yourname` (from step 1)
- Replace `id = "34d23af4669a40bd907f5c58c56802e8"` with your KV namespace ID from step 2
- Save

### Step 4 — Set the shared API key secret (for solo use)

If you're a single user (not multi-customer), you can skip the per-user KV system and use a single shared bearer token:

```sh
# Generate a random key
SHARED_KEY=$(openssl rand -hex 32)

# Set as Worker secret
wrangler -c wrangler.toml.local secret put EIDETIC_API_KEY
# Paste SHARED_KEY when prompted
```

Save `SHARED_KEY` — you'll put it in your daemon's `sync.json`.

### Step 5 — Deploy the Worker

```sh
wrangler -c wrangler.toml.local deploy
# → outputs Worker URL: https://eidetic-sync-yourname.<your-account>.workers.dev
```

Save the Worker URL.

### Step 6 — Optional: custom domain

In Cloudflare dashboard → Workers → eidetic-sync-yourname → Custom Domains → Add Custom Domain → `eidetic.yourdomain.com`. CNAME is automatic.

### Step 7 — Configure your daemon's sync.json

Create `~/.eidetic/sync.json`:

```json
{
  "worker_url": "https://eidetic-sync-yourname.<your-account>.workers.dev",
  "api_key":    "<SHARED_KEY from step 4>",
  "device_id":  "your-laptop-name",
  "sync_interval": 60
}
```

The daemon hot-reloads sync.json (v0.0.35+) — sync starts within ~1 second of saving the file.

### Step 8 — Verify

```sh
# Health check
eideticd --check
# → worker: ✓ reachable (200 OK)
# → status: ✓ sync healthy

# Force an immediate upload
eideticd --sync-now
# → sync-now: upload complete

# View backup history
eideticd --backups
# → 2026-05-20 21:30  engrams/your-laptop-name/engrams-...  (3.3 MB)
```

Open a new shell on a different machine and try:

```sh
brew install eideticd
# (same sync.json on the new machine)
eideticd --restore
# → ✓ Downloaded 3.3 MB engrams.db from cloud backup
```

That's it. You now have full Pro functionality, end-to-end, on your own infrastructure.

## Multi-user setup (if you're running for a team)

Skip the shared `EIDETIC_API_KEY` and use the per-user KV system:

1. For each user, generate a key + register in KV:
   ```sh
   ./scripts/gen_pro_key.sh user@company.com their-device-id
   # Outputs the wrangler kv:key put command — run it
   # Outputs sync.json — email to the user
   ```
2. Each user drops their `sync.json` and runs `eideticd --check` to verify

For 5+ users, the operator pattern of receiving Gumroad webhooks + Telegram notifications applies even in self-hosted mode — see `docs/PRO_LAUNCH.md` § 4 + `workers/gumroad-kit-sync/` (you'd run your own copy without the Eidetic Works' branding).

## Team-shared engram surface (advanced)

Add `team_id` to every team member's `sync.json`:

```json
{
  "worker_url": "...",
  "api_key":    "...",
  "device_id":  "alice-laptop",
  "team_id":    "acme-engineering",
  "sync_interval": 60
}
```

Daemon sends `X-Team-ID` header on every upload (v0.0.39+). Worker dual-writes to:
- `engrams/<device_id>/engrams-<ts>.db` (per-device, unchanged)
- `engrams/team/<team_id>/<device_id>/engrams-<ts>.db` (NEW: team shared prefix)

Query across all team members' uploads via `GET /team-engrams` with the same `X-Team-ID` header.

This is opt-in. Solo users omit `team_id` and pay no dual-write cost.

## Going from self-hosted → managed Pro

If you want to switch from your own infra to our managed Pro tier:

1. Buy Pro at https://eideticworks.gumroad.com/l/eidetic-pro
2. Email hi@eidetic.works your existing `device_id` — we'll provision a key with the same ID so your existing R2 backups don't conflict in our bucket (or we'll migrate the data, depending on your preference)
3. Replace `worker_url` and `api_key` in your `sync.json`
4. Done — your daemon hot-reloads

You can also keep both side-by-side if you want belt-and-suspenders — drop both sync.json files in different DataDirs (one via `EIDETIC_DATA_DIR=/tmp/eidetic-managed eideticd ...`, the other on the default path). Engrams get backed up to both buckets independently.

## Going from managed Pro → self-hosted

The reverse works the same. Email hi@eidetic.works for an export of your historical R2 objects (we'll provide signed download URLs) and replace `sync.json` with the values from your own Worker. Refund pro-rated for the unused portion of your billing cycle.

## What we recommend

| Your situation | Recommendation |
|---|---|
| Solo dev, want it to "just work" | Managed Pro ($29/mo or $299/yr — 14% off) |
| Solo dev, evaluating before committing | Self-hosted free for 30 days, then decide |
| Privacy-strict / regulated industry | Self-hosted ALWAYS (you keep keys + audit trail) |
| Team of 2-5 | Managed Team ($99/mo, 5 seats, single payment) |
| Team of 6-20 | Self-hosted multi-user; or contact us for custom Team-Plus |
| 20+ users or HIPAA-regulated | Self-hosted + custom MSA — `security@eidetic.works` |

See `docs/enterprise/SOC2_READINESS.md` for the current compliance posture and `docs/enterprise/BAA_TEMPLATE.md` for the HIPAA path.

## Updates

Your Worker is on your account; updates don't roll out automatically. Watch for new tags on https://github.com/eidetic-works/eidetic-daemon/releases — when a new Worker version ships, `wrangler -c wrangler.toml.local deploy` updates yours.

Daemon updates: `brew upgrade eideticd` (Homebrew users — auto-tap-update keeps formula current). Or download new tarball + reinstall.

## Where to ask for help

- **GitHub Issues** — https://github.com/eidetic-works/eidetic-daemon/issues — public, all welcome
- **Security disclosures** — security@eidetic.works (private; see `SECURITY.md`)
- **Custom MSA / enterprise** — hi@eidetic.works
- **General questions** — hi@eidetic.works (24h response weekdays)
