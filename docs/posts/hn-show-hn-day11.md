# Show HN: eidetic-daemon — local-first Claude Code memory + AI recall + 14 integrations

## The post

**Title (80 char max — HN truncates anything longer):**
> Show HN: Local daemon that remembers every Claude Code / Cursor session

**URL:** https://eidetic.works

**Body (HN allows ~2000 chars; this is ~1950):**

I built a Go daemon that captures every JSONL written by Claude Code, Cursor, and Cowork sessions into a local SQLite+FTS5 store, then makes the content recallable from anywhere I work.

Two weeks ago this was a 200-line script. Today it ships at v0.0.55, 300K+ engrams from my own use (P95 retrieval 0.27ms), and surfaces into 14 places:

**IDEs:** VS Code extension, JetBrains plugin, Raycast extension, Chrome MV3 extension. All scaffolded; each adds a sidebar/popup that calls the daemon's local UDS HTTP API.

**Mac:** SwiftBar menubar plugin (lives in the bar showing engram count + last sync). AppKit native scaffold for the App Store route.

**Chat:** Slack /eidetic, Discord /eidetic, Telegram /eidetic — each is a Cloudflare Worker that proxies through your own Cloudflare Tunnel to your local daemon.

**AI hosts:** eidetic-mcp on PyPI (0.0.7) ships three MCP tools — `nucleus_ask` (RAG over your engrams), `nucleus_digest` (weekly recap), `nucleus_timeline` (cross-tool chronology). Your engrams never leave your machine; the "AI" is the Claude/Cursor LLM in your session.

**Web:** eidetic.works/dashboard is a PWA installable on iPhone — paste your bridge URL + token, browse + search + recall.

**CLI:** `eideticd --ask "what was that postgres trick"`, `--digest 7d`, `--capture` (any stdin → engram), `--export` (NDJSON of everything you've captured).

**Outbound:** `~/.eidetic/hooks.json` declares webhook URLs to fire when matching engrams arrive — Slack you when "deploy failed" hits, PagerDuty you on CrashLoopBackOff, mirror everything to your own backend. Per ADR-020 (privacy posture committed to source), default payload sends NO engram content; opt-in for forwarding.

Pro tier ($29/mo): we host the Cloudflare R2 bucket so cross-machine sync + restore in 60 seconds is one command (`eideticd --restore`). Self-hosted is fully documented and free.

Stack: pure-Go (modernc.org/sqlite — no CGO, cross-compiles clean to 4 platforms via `make build-all`). Homebrew tap auto-updates on every tag push. Privacy contract enumerates every outbound network call.

Source: https://github.com/eidetic-works/eidetic-daemon (MIT). Pro: https://eideticworks.gumroad.com/l/eidetic-pro.

Happy to answer questions about the architecture, the SQLite-FTS5 vs vector tradeoff, the privacy posture, or how the integration scaffold ecosystem cross-pollinates.

---

## Fire playbook

### When to post

**Best:** Tuesday 8:00-8:30am PT (11am ET / 16:00 UTC) — HN's morning surge starts here. Show HN posts that hit front page do so within 30 min; the surge window is the determining factor.

**Second-best:** Wednesday 8:00-8:30am PT.

**Avoid:**
- Monday morning (people clearing weekend backlog; lower engagement on Show HN)
- Friday or weekend (Show HN dies fast there)
- Anytime after 4pm PT (Europe asleep; daytime Show HN consistently outperforms evening)

### Pre-post checklist (5 min before submitting)

1. Confirm landing page loads in incognito: https://eidetic.works
2. Confirm GitHub repo isn't gated: `gh repo view eidetic-works/eidetic-daemon`
3. Confirm `brew install eideticd` works (someone WILL try, immediately)
4. Confirm the Gumroad URL works: https://eideticworks.gumroad.com/l/eidetic-pro
5. Have https://news.ycombinator.com open in another tab so you can refresh + reply immediately
6. Free ~3 hours for the comment window (HN ranking is engagement-driven; first 90 min is everything)

### Post-submission playbook (first 30 min)

- Refresh your submission every 60s for the first 10 min — vote velocity matters
- Reply to EVERY comment within 5 min while you're in the window. HN rewards author engagement.
- DON'T self-vote (HN auto-detects + removes posts)
- DON'T ask friends to vote (same)
- DON'T post about it on Twitter immediately — HN flags submissions with sudden external traffic as gamed

### Likely questions + 1-line replies (have these ready)

**Q: How is this different from LangChain memory / mem.ai / Pieces?**
A: LangChain memory is per-agent runtime state. Pieces is closed-source SaaS. eidetic is OS-level cross-tool capture — Claude Code session 1 → Cursor session 2 sees the same engrams. Local-first, MIT, single static binary.

**Q: Why SQLite+FTS5 instead of vectors?**
A: P95 0.27ms on 10K rows, zero embedding cost, works offline, no GPU. Vector search is on the roadmap for v0.1; FTS5 stays the default for sub-millisecond response.

**Q: Why a Cloudflare Worker for sync, not S3?**
A: Workers free tier + R2 free tier = $0/mo at our typical user storage. S3 would cost $5/mo minimum for the same compute path. Decision documented in ADR-019.

**Q: What about privacy? You're a single dev.**
A: ADR-020 (in repo) enumerates every network call the daemon makes. Each one is opt-in or HEAD-only-with-no-data. There's a tcpdump-audit recipe at the bottom you can run to verify.

**Q: SOC2 / HIPAA?**
A: Not certified. We're pre-audit. Honest posture in docs/enterprise/SOC2_READINESS.md + BAA_TEMPLATE.md. Free tier means data never leaves your machine, which is the safest path for compliance-sensitive use.

**Q: How do you make money?**
A: Pro tier ($29/mo) hosts the sync. First 50 subscribers keep that price forever; annual at $299/yr is 14% off; founder lifetime at $499 (cap 50). Team is $99/mo for 5 seats.

**Q: Cursor session JSONL changes shape often — does eidetic break?**
A: v0.0.41 added a path-filter so we only capture chatSessions/*.json — Cursor's workspace.json metadata stub no longer pollutes. Parser is whole-file replace with content-hash de-dupe; survives Cursor format churn.

**Q: Why not just write to a Notion / Obsidian vault?**
A: That's a different product. eidetic is for AI-tool context-aggregation. You can do both — the daemon captures, you separately curate to Notion the small fraction worth keeping in long-form.

### What NOT to say in comments

- Don't dunk on competitors by name (mem.ai, Pieces) — HN dislikes negativity
- Don't promise features not yet shipped ("vector search coming soon" — say "on the roadmap", not "soon")
- Don't reveal MRR / customer count (looks insecure either way)
- Don't argue with downvoters — let the comment lie

### If the post takes off (>30 upvotes in 30 min)

- Keep replying. The first 90 min compounds. Sustained engagement is what HN rewards.
- DO NOT bump the eidetic-works X account about it during that window — Show HN sucess + sudden external traffic often gets flagged as gamed and softly penalized.
- If you get to front page, traffic to eidetic.works will spike — confirm Cloudflare Pages is serving (it should; we have auto-scale).

### If it dies (which is likely first try)

- Show HN's hit rate is brutal. ~1 in 20 posts gets traction.
- Don't repost the same content. HN remembers.
- Wait 2 weeks, re-shape the post around what you've shipped since then, try again.

### After (24h later)

- Whether or not it ranked, post a brief retro to docs/posts/hn-show-hn-day11-retro.md
- Note: votes, comments, traffic to eidetic.works, install-script hits, Pro signups during the window
- Use the data to calibrate the next attempt
