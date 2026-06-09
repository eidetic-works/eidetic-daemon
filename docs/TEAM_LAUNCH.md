# Team Tier Launch — operator-keyboard checklist

eidetic-team is the 5-seat multi-user extension of Pro. $99/mo. Targets dev teams who want shared engram visibility across 3-5 engineers.

Worker routing + Telegram-ping flow already scaffolded in `workers/gumroad-kit-sync/worker.js`. Two operator-keyboard actions to flip the switch.

---

## 1. Gumroad product

**Product type:** Subscription
**Price:** $99/month
**Permalink:** `eidetic-team` (EXACT — the worker routes on this)
**Name:** eidetic-daemon Team

**Summary (Gumroad card):**
> 5 seats of eidetic Pro for your dev team. Shared engram visibility, multi-device sync, email support.

**Full description (paste into Gumroad editor):**

---

eidetic Pro for teams. Same daemon, multi-seat license.

### What Team includes

**5 Pro seats**
Each seat gets its own `sync.json`, its own R2 namespace, its own device-id pool. Engrams stay private per seat — no cross-engineer leakage by default.

**Shared-context opt-in (coming next)**
Designate any of the 5 seats as a "team" surface; their engrams sync to a shared bucket so anyone on the team can query "did anyone solve this already?" via `nucleus_ask`.

**Onboarding within 24h**
Pay → reply with the 5 seat emails + a team identifier → you get 5 `sync.json` files within 24 hours of payment.

**Email support**
hi@eidetic.works. Response within 12 hours on weekdays for Team subscribers (vs 24h for Pro).

**Early-access features**
Team subscribers get new features 1 week before Pro: AI-powered recall, web dashboard, retention policy templates.

### Pricing

$99/month flat. No per-seat scaling — your 6th hire pays nothing extra, but tier ceiling is 10 seats per Team subscription. Larger teams: contact for Org tier.

First 10 Team subscribers keep this price forever.

---

**Thumbnail:** Use `og-eidetic-works.png` (same as Pro)
**Files to attach:** None (sync.json files sent via email)
**Refund policy:** 7 days no-questions-asked

---

## 2. Kit `eidetic-team` tag

In Kit UI: Subscribers → Tags → New Tag → `eidetic-team` → copy ID.

Paste into `workers/gumroad-kit-sync/worker.js`:
```javascript
const KIT_TEAM_TAG = "REPLACE_WITH_TEAM_TAG_ID"; // replace this line
```

Then redeploy:
```sh
cd workers/gumroad-kit-sync
wrangler deploy
```

---

## 3. Per-Team onboarding (operator script — TODO)

When a Team subscriber pays:
- Reply asks: "Please send me 5 emails + a team identifier (e.g. `team-acme`)"
- Run `gen_pro_key.sh` 5 times, one per seat email, using device_id format `<team>-seat-N` (e.g. `acme-seat-1`)
- Email each seat their individual `sync.json`

A `gen_team_keys.sh` wrapper could automate this — TODO once first Team customer materializes.

---

## 4. Sequencing

| Step | Status | Who |
|---|---|---|
| Worker routing logic | ✅ Done — handles `eidetic-team` permalink | cc-main |
| `KIT_TEAM_TAG` placeholder | ⏳ operator-keyboard | operator |
| Gumroad Team product | ⏳ operator-keyboard | operator |
| Landing CTA additions | ⏳ After Gumroad | cc-main / op-assistant |
| First Team purchase | ⏳ Waiting | — |
| `gen_team_keys.sh` wrapper | ⏳ After first Team customer | cc-main |

**Target:** 1 Team subscription by 2026-06-22 ($99 MRR contribution — equivalent to 3.4 Pro subs).
