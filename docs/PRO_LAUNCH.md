# Pro Tier Launch — W4 Prep Package

Everything Lokesh needs to flip the Pro switch. Three operator actions: Gumroad product, CF Worker deploy, Kit email.

---

## 1. Gumroad product listing

**Product type:** Subscription  
**Price:** $29/month  
**Name:** eidetic-daemon Pro

**Summary (shown in Gumroad card):**
> Cross-machine sync + email support for eidetic-daemon. Your 278K engrams, backed up to the cloud and restorable on any machine.

**Full description (paste into Gumroad editor):**

---

eidetic-daemon is already free and open-source. You don't need to pay for the daemon.

**Pro is for people who want their engrams to follow them.**

### What Pro includes

**Managed cloud backup**
Your `engrams.db` syncs to Eidetic Works' Cloudflare R2 bucket automatically — every hour while the daemon runs. Restore to any machine in 60 seconds.

```sh
# Restore to a new machine:
eideticd --restore
# ✓ Downloaded 3.3 GB engrams.db from cloud backup
#   key: engrams/macbook-m2/engrams-1748300000000.db
#   previous db saved to ~/.eidetic/engrams.db.bak
#   restart eideticd to use the restored database
```

We manage the infrastructure. You get a personal API key by email within 24 hours of payment.

**Retention policy setup**
We'll configure `retention-policy.json` for your setup — how long to keep Claude Code sessions, Cursor history, Cowork logs. One email, we handle it.

**Email support**
hi@eidetic.works. Response within 24 hours on weekdays.

**Early-access features**
Pro subscribers get new features before free users. Next up: AI-powered natural language recall (ask Claude to query your own engrams).

### What Pro does NOT include

- Any data leaving your machine *without your sync.json configured* — the daemon is still local-first
- Hosted cloud AI — we don't process your engrams on our servers
- Anything we'd need to see your data for — the sync is encrypted with your key

### Setup after payment

1. Pay → you get a confirmation email within minutes
2. Reply with your machine's `device_id` (pick any short identifier: `macbook-m2`, `work-laptop`, etc.)
3. We reply with your `sync.json` within 24 hours
4. Drop it at `~/.eidetic/sync.json`, restart daemon — sync starts immediately

### Pricing

$29/month — early-bird rate for the first 50 Pro subscribers. Price may increase after 50.

No annual discount yet. Month-to-month, cancel anytime via Gumroad.

---

**Thumbnail:** Use `og-eidetic-works.png` (same as landing page)  
**Files to attach:** None (sync.json sent via email, not Gumroad file download)  
**Refund policy:** 7 days no-questions-asked  

---

## 2. Cloudflare Worker deploy (operator action)

Before accepting Pro subscribers, deploy the shared sync Worker.

**Prerequisites:** Cloudflare token with `R2:Read`, `R2:Write`, `Workers:Edit` permissions.

```sh
cd /Users/lokeshgarg/work-eidetic-daemon/work/bridge/cloudflare

# 1. Create the R2 bucket (once)
wrangler r2 bucket create eidetic-pro-sync

# 2. Generate the shared API key pool seed (save this)
openssl rand -hex 32
# → copy this, it's the EIDETIC_MASTER_KEY for the Worker

# 3. Set Worker secret
wrangler secret put EIDETIC_API_KEY
# Paste the seed key when prompted

# 4. Deploy
wrangler deploy
# → Worker URL: https://eidetic-sync.<account>.workers.dev
```

Note the Worker URL — it goes into every Pro user's `sync.json`.

**Per Pro subscriber onboarding** (run `scripts/gen_pro_key.sh <email> <device_id>`)  
See § 4 below.

---

## 3. Kit announcement email — READY TO SEND (Lokesh keyboard)

**Subject:** eidetic Pro is live — cloud backup for your engrams

**From:** hello@nucleusos.dev  
**List:** Track A waitlist + any existing free subscribers  
**CTA URL:** https://eideticworks.gumroad.com/l/eidetic-pro ✅

**Body (copy-paste ready):**

---

eidetic Pro is live.

**What it is:** your `engrams.db`, backed up to the cloud every hour. Restore on any machine in 60 seconds.

278,561 engrams. 803 sessions. 3.3 GB. All of it, cloud-synced.

**$29/month.** First 50 subscribers keep this price forever.

→ **[Get Pro](https://eideticworks.gumroad.com/l/eidetic-pro)**

What's included:
- Managed R2 backup (your own encrypted namespace on Cloudflare R2)
- Personal sync.json delivered within 24h
- Retention policy setup
- Email support (24h response weekdays)
- Early access to AI-powered recall (coming next)

Everything else stays free and open-source forever. Pro is for people who want their context to follow them across machines.

Questions? Reply to this email.

— Eidetic Works

---

## 4. Per-user key generation (automated)

Script to run when each Pro subscriber pays. Takes <30 seconds.

**Create at `scripts/gen_pro_key.sh`:**

```bash
#!/usr/bin/env bash
# Usage: ./gen_pro_key.sh <customer_email> <device_id>
# Generates a per-user API key and prints the sync.json to deliver.

set -euo pipefail

EMAIL="${1:?Usage: $0 <email> <device_id>}"
DEVICE="${2:?Usage: $0 <email> <device_id>}"
WORKER_URL="${EIDETIC_WORKER_URL:?set EIDETIC_WORKER_URL}"

KEY=$(openssl rand -hex 32)
TS=$(date -u +%Y%m%dT%H%M%SZ)

# Append to customer registry
echo "${TS},${EMAIL},${DEVICE},${KEY}" >> ~/.eidetic/pro-customers.csv

cat <<EOF
=== Pro Sync Config for ${EMAIL} ===

~/.eidetic/sync.json:
{
  "worker_url": "${WORKER_URL}",
  "api_key":    "${KEY}",
  "device_id":  "${DEVICE}",
  "sync_interval": 60
}

Save this key — we don't store it: ${KEY}
EOF
```

**Usage:**
```sh
EIDETIC_WORKER_URL=https://eidetic-sync.<account>.workers.dev \
  ./scripts/gen_pro_key.sh customer@email.com macbook-m2
```

Copy the output into your reply email to the customer.

> **Note:** The current Worker validates a single `EIDETIC_API_KEY`. To support per-user keys, you need to update the Worker to accept a list of keys or use a KV-backed validation. At <10 users, using a single shared key and trusting customers is fine. At 10+ users, update the Worker before issuing more keys.

---

## 5. Delivery email (send to customer after gen_pro_key.sh)

After running `gen_pro_key.sh`, send this to the subscriber:

**Subject:** Your eidetic Pro sync.json is ready

---

Hi,

Here's your eidetic Pro sync.json. Drop it at `~/.eidetic/sync.json` and restart the daemon:

```json
{
  "worker_url": "https://eidetic-sync.morning-lake-f944.workers.dev",
  "api_key":    "<KEY_FROM_gen_pro_key.sh>",
  "device_id":  "<DEVICE_ID>",
  "sync_interval": 60
}
```

After dropping the file:

```sh
# Test upload immediately:
eideticd --sync-now

# On macOS — restart the daemon:
launchctl kickstart -k gui/$(id -u)/works.eidetic.eideticd

# Check it worked:
eideticd --stats
# Should show a "cloud sync" block with last sync time
```

**To restore on a new machine** — install the daemon, drop the same `sync.json`, then:

```sh
eideticd --restore
# Downloads your engrams.db from the cloud backup
```

Reply to this email if anything doesn't work — I reply same day.

— Lokesh / Eidetic Works

---

## 6. Sequencing (current state as of 2026-05-19)

| Step | Status | Who |
|---|---|---|
| KV namespace created (id: 34d23af4669a40bd907f5c58c56802e8) | ✅ Done | op-assistant |
| wrangler.toml patched with KV namespace ID | ✅ Done | op-assistant |
| EIDETIC_API_KEY secret set on eidetic-sync worker | ✅ Done | op-assistant |
| gumroad-kit-sync deployed (Telegram + Kit wired) | ✅ Done | op-assistant |
| R2 bucket `eidetic-engrams` + eidetic-sync Worker live | ✅ Done 2026-05-20 | Lokesh + op-assistant |
| Gumroad Pro product | ✅ Live — eideticworks.gumroad.com/l/eidetic-pro | Lokesh |
| Landing CTA → Gumroad URL | ✅ Live — eidetic.works | op-assistant |
| gen_pro_key.sh — worker URL + KV namespace ID pre-filled | ✅ Done | cc-main |
| Kit announcement email | ⏳ **LOKESH KEYBOARD** — template ready in § 3 above | Lokesh |
| X thread + HN + Reddit + dev.to posts | ⏳ Lokesh keyboard — files in docs/posts/ | Lokesh |

**W4 target (2026-06-08):** 5 paid Pro subscriptions = $145 MRR.  
**Worker URL:** https://eidetic-sync.morning-lake-f944.workers.dev  
**Gumroad:** https://eideticworks.gumroad.com/l/eidetic-pro
