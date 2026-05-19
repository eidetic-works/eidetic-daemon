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

## 3. Kit announcement email (operator action)

**Subject:** eidetic Pro is live — cloud backup for your engrams

**From:** hello@nucleusos.dev  
**List:** Track A waitlist + any existing free subscribers

**Body:**

---

eidetic Pro is live.

**What it is:** your `engrams.db`, backed up to the cloud every hour. Restore on any machine in 60 seconds.

278,561 engrams. 803 sessions. 3.3 GB. All of it, cloud-synced.

**$29/month.** First 50 subscribers keep this price forever.

→ **[Get Pro](https://app.gumroad.com/l/XXXXX)**  
_(replace XXXXX with Gumroad product URL once published)_

What's included:
- Managed R2 backup (your own encrypted namespace on our Cloudflare R2)
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

## 5. Sequencing

1. [ ] Deploy CF Worker (operator — needs R2 token)
2. [ ] Note Worker URL → add to `scripts/gen_pro_key.sh`
3. [ ] Create Gumroad subscription product → get Gumroad URL
4. [ ] Send Kit announcement email (paste Gumroad URL into template above)
5. [ ] For each Gumroad payment notification: run `gen_pro_key.sh`, reply with sync.json

**W4 target (2026-06-08):** 5 paid Pro subscriptions. At $29/mo that's $145 MRR.
