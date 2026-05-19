# Reddit /r/SaaS — Day 10: shipped pricing in 36 hours

**Subreddit:** r/SaaS (also viable: r/SideProject, r/microsaas, r/EntrepreneurRideAlong)

**Title (under 100 chars):**
> Shipped Free + $29 Pro + $99 Team in 36 hours — here's the full revenue stack ($1.4K MRR target)

**Body:**

10 days in on a tiny SaaS. Local-first dev tool (eidetic-daemon — captures every Claude Code / Cursor session JSONL, stores in SQLite, exposes via MCP for AI recall). MIT-licensed free tier, $29/mo Pro, $99/mo Team. 277K engrams from my own daily usage validates the dogfood story.

Here's the revenue stack I shipped in the last 36 hours, with cost per piece:

**Free tier (already shipped, drives top-of-funnel):**
- Daemon binary, install.sh, Homebrew tap, Windows .ps1
- Landing at eidetic.works (Astro on Cloudflare Pages)
- ConvertKit free-tier waitlist form
- Cost: 0 — pure OSS distribution

**$29/mo Pro tier:**
- Gumroad subscription product (eidetic-pro permalink)
- Cloudflare Worker for sync (R2 bucket per user, KV-keyed API auth)
- Telegram bot ping when a customer pays (so I provision keys within 24h, manually)
- `gen_pro_key.sh` operator script: hashes the API key, stores in KV, prints sync.json to email back
- gumroad-kit-sync Worker: webhook → ConvertKit tag → Telegram ping
- Cost: ~6 hours of focused build, $0 infra under R2 free tier (10GB)

**$99/mo Team tier:**
- Same Gumroad subscription pattern (eidetic-team permalink)
- Worker routing branches on permalink (cost-playbook | pro | team)
- Telegram ping shows "TEAM" badge + 5-seat provisioning hint
- Per-seat keys via the same gen_pro_key.sh, just run 5 times
- Cost: 90 minutes of worker.js routing + docs

**The whole revenue stack runs on $0/mo infra** (Cloudflare Free + Gumroad's per-sale fee). Marginal cost per Pro subscriber is ~$0.001/mo (R2 storage for ~3MB engrams.db × 1 upload/hour).

**Conversion lubricants shipped today** (each removes one friction point):
- `eideticd --check` — Pro customers self-diagnose sync config
- `eideticd --restore` — 1-command machine migration
- `sync.json` hot-reload — no daemon restart on first config drop
- `eideticd --backups` — visible cloud backup history (trust but verify)
- `nucleus_ask` MCP tool — the "AI-powered recall" the landing promised
- Web dashboard at /dashboard — non-MCP browsers can still ask

**Target:** 5 paid Pro by end of week 4 ($145 MRR) + 1 Team ($99 MRR) = $244 MRR runway start.

**The thing I keep getting wrong:** writing copy that promises features that don't exist yet. Every landing-page bullet now has a curl behind it. If you can't `eideticd --check` it, it doesn't go on the landing.

**Honest question for the sub:** for a tool with 50 free installs but 0 paid Pro subs (so far), is the right move (a) more distribution, (b) more Pro features, or (c) a 7-day free trial of Pro? I keep going back and forth.

Site: eidetic.works
Source: github.com/eidetic-works/eidetic-daemon (MIT)

---

## Notes for posting

- r/SaaS allows ONE self-promo per 7 days. Don't burn it on a dud day.
- Best time: Monday or Tuesday morning EST
- Don't link-spam in comments; people will check the post URL anyway
- The "honest question" at the bottom is a comment-driver — engagement boosts rank
