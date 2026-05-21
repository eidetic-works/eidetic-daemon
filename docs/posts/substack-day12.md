# Substack Post — eidetic-daemon Day 12

**Substack:** the eidetic-works newsletter on nucleusos.dev (or whichever Substack the brand owns; if none exists yet, this is the first issue).

**Title:**
> What a 48-hour compression sprint actually looks like (and what came out of it)

**Subhead:**
> 31 daemon versions, 15 integration surfaces, 8 workers, $0 infra. Plus the 5 things that almost derailed it.

**Body (Markdown, no length cap):**

I gave myself an arbitrary constraint four days ago: finish the next 80 days of roadmap for eidetic-daemon in 48 hours. As I write this, the timer just hit zero. Here's the long-form version of what happened, because I think the lessons are reusable.

---

## What got built

**The daemon** went from v0.0.32 to v0.0.61 — 29 versions in 48 hours. Pure-Go binary, no CGO, cross-compiles to four platforms (`darwin-arm64`, `linux-amd64`, `linux-arm64`, `windows-amd64`). Each tagged release auto-publishes through GitHub Actions and the Homebrew tap updates itself within a minute. Zero manual release work.

**The integration surfaces** went from 5 to 15:

- IDE: VS Code, JetBrains, Cursor (via session-file capture, no plugin needed), Raycast
- Browser: Chrome MV3 extension
- Desktop: Mac menubar (Swift + SwiftBar fallback), Linux/Windows via daemon CLI
- Chat: Slack `/eidetic`, Discord `/eidetic`, Telegram `/eidetic` (all three went live this morning)
- Notes: Obsidian plugin, Notion bidirectional sync, WordPress plugin
- AI: MCP for Claude / ChatGPT / Cursor / any MCP-capable host

Each one is a real working scaffold — not "we'll get to it later." VS Code packages to a 18KB `.vsix`. Chrome extension is an 11KB `.zip`. Both ready to upload to their respective marketplaces.

**The infrastructure** is 8 Cloudflare Workers:

- `eidetic-sync` — R2 backup for Pro subscribers ($29/mo)
- `gumroad-kit-sync` — 4-tier payment routing (Pro/Annual/Founder/Team)
- `eidetic-affiliate` — `/ref/<code>` attribution
- `eidetic-analytics` — privacy-safe conversion funnel (Analytics Engine binding live)
- `eidetic-account` — customer dashboard at eidetic.works/me
- `eidetic-slack` / `eidetic-discord` / `eidetic-telegram` — the three chat bots

Total infra cost: **$0/mo** on Cloudflare's free tier. Marginal cost per paid customer: ~$0.001/mo (R2 storage). The whole stack runs forever-free up to the Cloudflare ceiling, which is somewhere around 5K paid subscribers.

## What I learned

### 1. The bottleneck wasn't code

Out of 48 hours, maybe 15 went to writing Go and TypeScript. The other 33 were:

- Registering accounts on Slack, Discord, Telegram (each their own form)
- Configuring DNS records in Cloudflare (CNAMEs for `docs.eidetic.works`, etc.)
- Enabling the Analytics Engine binding via the Cloudflare dashboard
- Coordinating webhook URLs across Gumroad / ConvertKit / R2 / KV
- Debugging the "wrangler picks up the wrong API token" problem (fix: `unset CLOUDFLARE_API_TOKEN` before deploys)

If I had to do it again, I'd spend Day 1 entirely on account creation + DNS setup. Then Day 2 on code. Inverting that ordering would have saved 4-6 hours.

### 2. Pure-Go SQLite is underrated

`modernc.org/sqlite` is a pure-Go transliteration of SQLite. Slower than `mattn/go-sqlite3` (which uses CGO) by maybe 2x in benchmarks. **The distribution win dwarfs the perf cost.**

CGO means: you can't easily cross-compile, you need a C toolchain on every build machine, your binaries become OS-specific, and `go install` stops being a one-command install. None of that matters for a server; all of it matters for a daemon that needs to run on every developer's laptop.

Switched the daemon from `mattn` to `modernc` two weeks before this sprint. Distribution became one `goreleaser` away from "ships everywhere."

### 3. Parallel agents change calendar math

The "80 days" in my roadmap was estimated for serial work. What this sprint compressed wasn't the work itself — it was the gap between work. I had ~7 parallel build streams running concurrently:

- cc-main (this conversation) — driving the strategic ships
- cc-tb — Third Brother model training in the background
- cc-peer — code review + cross-checking
- op-assistant — deploys and DNS work
- cc-gq — separate stream for GentleQuest (a sibling product)
- Two CI runners — release.yml + Homebrew tap updates

When all 7 are saturated, calendar time shrinks dramatically. When one of them stalls (waiting on me to paste a Slack token, say), the others queue up work in the meantime.

The constraint moves from "how fast can I build" to "how fast can I supply directives + unblock token-paste tasks." Different bottleneck, same total throughput.

### 4. Distribution >> features

The product was technically shippable on Day 8 of this 12-day arc. The Slack/Discord/Telegram surfaces, the customer dashboard, the marketplace artifacts — none of those are features in the strict sense. They're friction-removers.

Each one removes one reason a potential customer says "this looks cool but I'll try it later" and turns it into "I'll try it right now." That's where the real win is. Features are easy; friction is hard.

### 5. Pseudonymous + open-source is a valid distribution shape

I ship under a brand (Eidetic Works) for reasons unrelated to the product. It means I can't use a personal Reddit account (auto-mod takes them out), can't reuse my personal LinkedIn following, can't put my face on the landing page.

What I CAN do: open-source the whole thing under MIT, point to the code, let the work speak. ~50 free installs in the first 10 days, zero paid Pro yet. The conversion question (more distribution, more features, or trial?) is still open. But the loop is closed: a stranger can install in 30 seconds, evaluate for free forever, and choose to pay if it sticks.

That's enough of a foundation. Everything else is iteration.

---

## What's next

I'm taking the rest of the day off. Then back to:

- First paid Pro subscriber (target: end of week 4)
- VS Code Marketplace submission (4-day review SLA)
- Chrome Web Store submission (1-3 day SLA)
- Vector search in the daemon (v0.1.x, optional + opt-in)
- A v2 landing page that leads with the chat-bot integrations

If you want to follow along, subscribe to this Substack — I'll post a "what shipped this week" digest each Friday.

If you AI-code daily and want to try it: **eidetic.works** (free tier, no signup).

---

## Posting notes
- Substack posts get email-blasted to all subscribers immediately. Don't publish until proofread end-to-end.
- The headline is what shows in the email subject line; the subhead becomes preview text.
- Internal links should use Substack's link button (not raw URLs) — better tracking.
- Tag categories: "Build in Public", "Indie Maker", "Developer Tools".
- Cross-post to X with a quote of paragraph 1 + a link.
