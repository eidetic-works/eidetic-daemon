# Op-Assistant Queue — Pro Launch (2026-05-19)

Tasks cc-main cannot run (need Cloudflare token + browser for Gumroad).
Run these in order. Each is independent once its prerequisite completes.

---

## Task 1 — Deploy CF Worker (prerequisite for all Pro subscribers)

**Status:** BLOCKED — current CLOUDFLARE_API_TOKEN is Pages-only (auth error on R2+KV+Workers APIs)

**operator keyboard required first:**
1. Go to https://dash.cloudflare.com/profile/api-tokens
2. Create a new token (or edit the existing one) with these permissions:
   - `Workers Scripts:Edit`
   - `Workers KV Storage:Edit`
   - `Cloudflare R2:Edit`
   - `Account Settings:Read` (optional, removes the email warning)
3. Set in terminal: `export CLOUDFLARE_API_TOKEN=<new-token>`
4. Then the commands below will run

**Requires:** Cloudflare API token with Workers:Edit + R2:Write + KV:Write

```sh
# From repo root
cd /Users/example/work-eidetic-daemon/work/bridge/cloudflare

# Step 1: Create R2 bucket (safe to run even if bucket exists)
wrangler r2 bucket create eidetic-pro-sync

# Step 2: Create KV namespace — copy the 'id' from the output
wrangler kv:namespace create EIDETIC_KEYS
# → outputs something like: id = "abc123def456..."

# Step 3: Paste that id into wrangler.toml line 14 (currently REPLACE_WITH_KV_NAMESPACE_ID)
# File: /Users/example/work-eidetic-daemon/work/bridge/cloudflare/wrangler.toml

# Step 4: Set fallback API key secret
wrangler secret put EIDETIC_API_KEY
# (enter output of: openssl rand -hex 32)

# Step 5: Deploy
wrangler deploy
# → outputs Worker URL: https://eidetic-sync.<account>.workers.dev
```

**After deploy:** write the Worker URL to a note — it goes in every Pro user's sync.json.
Set EIDETIC_WORKER_URL env var for gen_pro_key.sh calls.

**Commit wrangler.toml** with the real KV namespace ID (the placeholder is checked in):
```sh
cd /Users/example/work-eidetic-daemon/work
git add bridge/cloudflare/wrangler.toml
git commit -m "chore(worker): set KV namespace ID for Pro tier"
git push origin main
```

---

## Task 2 — Create Gumroad Pro subscription product

Full copy is in `docs/PRO_LAUNCH.md § 1`. Key fields:

| Field | Value |
|---|---|
| Type | Subscription |
| Name | eidetic-daemon Pro |
| Price | $29/month |
| Thumbnail | `og-eidetic-works.png` (same as landing) |
| Files | None (sync.json delivered by email) |
| Refund policy | 7 days no-questions-asked |

After publish, copy the Gumroad product URL (e.g. `https://app.gumroad.com/l/XXXXX`).

---

## Task 3 — Update landing Pro CTA with real Gumroad URL

The landing currently has a `mailto:` placeholder. Once Gumroad is live:

Edit `/Users/example/ai-mvp-backend/landing/src/pages/index.astro` line ~100:

```html
<!-- change href from the mailto: to the Gumroad URL -->
<a href="https://app.gumroad.com/l/XXXXX" class="pro-cta-btn">
  Get Pro — $29/mo →
</a>
```

Then rebuild + deploy:
```sh
cd /Users/example/ai-mvp-backend/landing
npx astro build
npx wrangler pages deploy dist --project-name=eideticworks-landing --branch=main
```

---

## Task 4 — Send Kit Pro announcement email

Template in `docs/PRO_LAUNCH.md § 3`.

- **From:** hello@nucleusos.dev
- **Subject:** eidetic Pro is live — cloud backup for your engrams
- **List:** Track A waitlist + existing free subscribers
- **Replace:** `XXXXX` in the CTA link with the Gumroad product URL from Task 2

---

## Done marker

When all 4 tasks complete, edit this file and add:

```
## Completed: <date>
- Worker URL: <url>
- Gumroad URL: <url>
- Kit email sent: yes
```

And relay back to cc-main so the landing CTA is verified live.
