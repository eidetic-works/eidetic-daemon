# Pricing tiers — Gumroad products + Kit tags

Three Pro variants plus Team, all served by the same `gumroad-kit-sync` Worker. Routing happens on `product_permalink`.

## Active products

| Permalink | Price | Tier | Status |
|---|---|---|---|
| `eidetic-pro` | $29/month | Pro (monthly) | ✅ Live |
| `eidetic-pro-annual` | $299/year | Pro (annual, ~14% off) | ⏳ operator-keyboard |
| `eidetic-pro-founder` | $499 one-time | Pro (lifetime, capped at 50) | ⏳ operator-keyboard |
| `eidetic-team` | $99/month | Team (5 seats) | ✅ Live |

## Why these three Pro variants

1. **Monthly ($29)** — default; lowest commitment, captures impulse purchases
2. **Annual ($299)** — committed users; ~14% discount = 10 months for 12; better cash-flow + retention
3. **Founder ($499 lifetime, capped at 50)** — kickstart + community signal. First 50 buyers get every future Pro feature forever. Closes a meaningful narrative ("I was here first") + provides ~$25K upfront if cap fills

The Founder cap is the marketing lever: scarcity. The Annual is the financial lever: lock-in.

## 1. Create the Gumroad products (operator-keyboard, 10 min each)

### eidetic-pro-annual

- **Type:** Subscription (Yearly)
- **Permalink:** `eidetic-pro-annual` (EXACT — worker routes on this)
- **Price:** $299/year
- **Name:** eidetic-daemon Pro — Annual
- **Summary:** "Same Pro features as monthly ($29/mo) — pay yearly, save 14% (10 months pricing for 12)."
- **Full description:** copy from §3 below
- **Thumbnail:** og-eidetic-works.png
- **Refund:** 14 days no-questions-asked (vs 7 for monthly — annual gets longer window)

### eidetic-pro-founder

- **Type:** Single payment (NOT subscription)
- **Permalink:** `eidetic-pro-founder`
- **Price:** $499 one-time
- **Quantity cap:** **50 units** (hard cap — Gumroad UI: "Limit purchase quantity")
- **Name:** eidetic-daemon Pro — Founder (Lifetime)
- **Summary:** "Lifetime access to Pro. First 50 customers only. Every future Pro feature, forever. No renewal."
- **Full description:** copy from §3 below
- **Thumbnail:** og-eidetic-works.png
- **Refund:** 30 days no-questions-asked (lifetime tier gets longest window)

## 2. Create Kit tags (operator-keyboard, 2 min)

In Kit → Subscribers → Tags → New Tag:
- `eidetic-pro-annual` → copy ID
- `eidetic-pro-founder` → copy ID

Paste IDs into `workers/gumroad-kit-sync/worker.js`:

```javascript
const KIT_ANNUAL_TAG  = "REPLACE_WITH_ANNUAL_TAG_ID";  // replace
const KIT_FOUNDER_TAG = "REPLACE_WITH_FOUNDER_TAG_ID"; // replace
```

Then redeploy: `cd workers/gumroad-kit-sync && wrangler deploy`

## 3. Gumroad product copy

### Annual product page

```
eidetic-daemon Pro — Annual

Everything in monthly Pro, paid yearly. You save 14% (10 months' price for 12 months of access).

What's included (same as monthly Pro):
- Managed Cloudflare R2 backup — automatic hourly upload
- Restore on any machine in 60 seconds: eideticd --restore
- nucleus_ask AI-powered recall — RAG over your local engrams via FTS5
- Web dashboard at eidetic.works/dashboard
- Email support (24h response weekdays)
- Early access to new features (1 week before monthly subscribers)

The math:
- Monthly: $29/mo × 12 = $348/year
- Annual:  $299/year (save $49 vs paying monthly)

Cancel anytime — no refund on annual after 14 days, but your access stays live for the remainder of the year.

Already on monthly? Email hi@eidetic.works to switch — we'll pro-rate the conversion.
```

### Founder product page

```
eidetic-daemon Pro — Founder (Lifetime)

ONE-TIME $499. Lifetime access to Pro. First 50 customers only.

You get:
- Every Pro feature, forever (no renewal, ever)
- Every future Pro feature added to the tier, forever
- "Founder" badge in our subscriber graph (so we know who funded the runway)
- Lifetime early-access tier (features 1 week before monthly, 2 weeks before annual)
- Direct email line to operator (12h response, weekdays + weekends — vs 24h for monthly)

Math:
- Pays for itself at month 18 of Pro use (vs monthly)
- Pays for itself at year 2 (vs annual)
- Capped at 50 buyers — once it sells out, the tier closes forever

We use the $25K cap to fund the next 6 months of runway: server costs, my time, and the first hires (when MRR justifies).

This is the "I'm betting on this product" tier. If eidetic-daemon becomes the AI memory layer everyone uses in 5 years, you'll have paid $499 once. If it doesn't, you'll have paid $499 for a working daemon + the satisfaction of having backed the founder.

30-day refund window — try it for a month, decide for sure, no pressure.
```

## 4. Verify routing (cc-main)

After operator creates the Gumroad products + Kit tags + redeploys:

```sh
# Smoke-test the worker routing — each variant should return its product_type
for permalink in eidetic-pro eidetic-pro-annual eidetic-pro-founder eidetic-team; do
  curl -s -X POST -H 'Content-Type: application/json' \
    -d "{\"email\":\"test@invalid\",\"product_permalink\":\"$permalink\"}" \
    https://gumroad-kit-sync.morning-lake-f944.workers.dev/ | jq -r '.product_type'
done
# Expected:
# pro
# pro-annual
# pro-founder
# team
```

Telegram messages now show tier-specific badges:
- 🎉 Pro (monthly)
- 📅 PRO ANNUAL (yearly)
- 💎 FOUNDER (lifetime)
- 🚀 TEAM (multi-seat)

Each routes to the same `gen_pro_key.sh` provisioning flow — no per-tier differentiation needed in key issuance.

## 5. Marketing positioning

When announcing the tiers (Kit broadcast / X / HN):

> Three ways to subscribe:
> - **$29/mo** — pay-as-you-go, cancel anytime
> - **$299/year** — save 14%, lock in price
> - **$499 lifetime** — first 50 only. Founders get everything forever.
> 
> Team tier: $99/mo for 5 seats.

The lifetime cap is the headline. Scarcity drives action; the price ladder reassures the cautious.

## 6. Sequencing

| Step | Status | Who |
|---|---|---|
| Worker routing for 4 tiers | ✅ Done | cc-main |
| Landing CTA shows annual + founder alts | ✅ Done | cc-main |
| Gumroad eidetic-pro-annual product | ⏳ operator-keyboard | operator |
| Gumroad eidetic-pro-founder product (cap 50) | ⏳ operator-keyboard | operator |
| Kit eidetic-pro-annual tag → ID into worker.js | ⏳ operator-keyboard | operator |
| Kit eidetic-pro-founder tag → ID into worker.js | ⏳ operator-keyboard | operator |
| Worker redeploy with real tag IDs | ⏳ op-assistant (after tags exist) | op-assistant |
| Smoke-test 4 permalinks → 4 product_types | ⏳ cc-main (after operator creates) | cc-main |
| Kit broadcast announcing 3 Pro tiers | ⏳ operator-keyboard | operator |
