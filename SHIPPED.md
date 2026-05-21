# SHIPPED — Compression sprint catalog (2026-05-19 → 2026-05-21)

Index of everything shipped during Lokesh's "finish the 80-day plan in 2 days" compression directive. ~48h of intensive parallel-agent build, 7+ surfaces shipping concurrently. Read this to know what exists; read `CHANGELOG.md` for per-version detail.

**Sprint close 2026-05-21 morning IST:** All Lokesh-keyboard items resolved (5 captured + Reddit deliberately skipped per memory). All 8 workers deployed + verified. Daemon at v0.0.62.

## Quick numbers

- **32 daemon versions tagged** (v0.0.32 → v0.0.62)
- **10 MCP package versions** (eidetic-mcp 0.0.1 → 0.0.10; latest published 0.0.10)
- **15 integration surfaces** (every major dev tool ecosystem)
- **8 Cloudflare Workers** — **all live** (sync, payments, analytics, affiliate, account, Slack/Discord/Telegram chat bots)
- **3 chat-app registrations live** (Slack `eidetic-works`, Discord app `Eidetic`, Telegram `@eideticworks_bot`)
- **12 docs** (compliance, pricing, integrations, ADRs)
- **6 daemon scripts** (operator + customer-facing CLI)
- **~125 new tests** (14 added in v0.0.61 internal/bundle)

## Daemon (Go binary, 4 platforms, MIT)

Stable at v0.0.62. Cross-compiles to darwin-arm64 + linux-amd64 + linux-arm64 + windows-amd64. Pure-Go (modernc.org/sqlite), no CGO. Tagged versions auto-publish via `release.yml`; Homebrew tap auto-updates via `HOMEBREW_TAP_PAT`.

### CLI flags (26 total)

- **Lifecycle:** `-install`, `-uninstall`, `-purge`, `-init`, `-yes`
- **Inspection:** `-version`, `-stats`, `-check`, `-backups`
- **Cloud sync:** `-sync-now`, `-restore`
- **Server:** `-uds`, `-tcp`, `-bridge`, `-auth`
- **CLI access:** `-ask <q>`, `-digest 24h|7d|30d`, `-export` (NDJSON stream)
- **Capture:** `-capture` + `-surface NAME`
- **Bulk ingest:** `-import-bundle <path|->` + `-bundle-format auto|ndjson|markdown|text` (v0.0.61+)
- **Maintenance:** `-vacuum`, `-auto-tag 24h|7d|30d`

### HTTP API (15+ endpoints)

`/engrams` (GET/POST/DELETE), `/engrams/batch`, `/engrams/{id}`, `/engrams/count`, `/surfaces`, `/search`, `/recent`, `/ask`, `/timeline`, `/digest`, `/export`, `/hooks`, `/metrics`, `/healthz`

### Major features by version

| v | Feature |
|---|---|
| 0.0.32-34 | `--restore` + sync-state + `--check` |
| 0.0.35-37 | Hot-reload + `--backups` history + version-check |
| 0.0.38-39 | `/ask` HTTP + shared-team (`X-Team-ID`) |
| 0.0.41-42 | Cursor PathContains + `/export` NDJSON |
| 0.0.44-45 | `--uninstall` + `/ask` LRU cache |
| 0.0.46-48 | `-init` wizard + `/timeline`+`/digest` + shell completions |
| 0.0.49-52 | Cache metrics + `--digest` CLI + `--ask` CLI + `--capture` |
| 0.0.53-56 | Refactor textsearch + `--vacuum` + hooks + regex/status |
| 0.0.58-60 | Meta enrichment + `nucleus_link` + auto-tag classifier + TUI |
| 0.0.61 | `--import-bundle` universal (ndjson + markdown + text auto-detect, stdin pipe) |
| 0.0.62 | Cursor-parser 500 MiB single-pass cap (no boot-time OOM); JSONL launchd plist XML-escape fix; generic 401 + case-insensitive Bearer + sync.json validation |

## MCP package (eidetic-mcp on PyPI)

Stable at 0.0.10. 17 tools registered.

### Recall family (the "wow" tools)

- **`nucleus_ask(question)`** — RAG via FTS5 (0.0.5)
- **`nucleus_digest(window)`** — RAG-shaped weekly recap (0.0.6)
- **`nucleus_timeline(window, surfaces?)`** — cross-tool chronology (0.0.7)
- **`nucleus_link(engram_id)`** — temporally adjacent engrams across surfaces (0.0.8)
- **`nucleus_curate(query, limit?)`** — de-noised top-K engrams for downstream LLM context (0.0.9+)

### Operational tools

`query_engrams`, `search_engrams`, `recent_engrams`, `count_engrams`, `list_surfaces`, `daemon_status`, `daemon_metrics`, `insert_engram`, `insert_engrams_batch`, `get_engram_by_id`, `delete_engram_by_id`, `purge_engrams`

## Integration surfaces (15)

| # | Surface | Path | Status |
|---|---|---|---|
| 1 | Daemon binary | (root) | ✅ Homebrew tap, install.sh, install.ps1 |
| 2 | eidetic-mcp Python | `bridge/python/` | ✅ PyPI 0.0.10 |
| 3 | VS Code extension | `integrations/vscode/` | ✅ TS+esbuild, 11/11 tests |
| 4 | JetBrains plugin | `integrations/jetbrains/` | ✅ Kotlin+Gradle v2 |
| 5 | Raycast extension | `integrations/raycast/` | ✅ 4 commands, build clean |
| 6 | Chrome MV3 extension | `integrations/chrome-extension/` | ✅ 12 files |
| 7 | Mac SwiftBar plugin | `integrations/macos-menubar/` | ✅ Live-tested ("🧠 300K") |
| 8 | Mac native menubar | `integrations/macos-menubar/EideticMenubar/` | ✅ Swift scaffold |
| 9 | Slack `/eidetic` | `integrations/slack-app/` | ✅ HMAC Worker |
| 10 | Discord `/eidetic` | `integrations/discord-bot/` | ✅ Ed25519 Web Crypto |
| 11 | Telegram `/eidetic` | `integrations/telegram-bot/` | ✅ Constant-time secret Worker |
| 12 | WordPress plugin | (in landing repo) `integrations/wordpress/` | ✅ PHP plugin |
| 13 | Notion sync | (in landing repo) `integrations/notion-sync/` | ✅ Worker + poller |
| 14 | Obsidian plugin | `integrations/obsidian/` | ✅ TS, 3 features |
| 15 | Daemon TUI | `cmd/eideticd-browse/` | ✅ Bubble Tea binary |

Plus web dashboard PWA at eidetic.works/dashboard (installable on iPhone/Android) and the docs site **LIVE at https://docs.eidetic.works/** (CF Pages + custom domain, SSL via Cloudflare).

## Cloudflare Workers (8 live + 1 deployment-pending)

| Worker | Purpose | Status |
|---|---|---|
| eidetic-sync | Pro R2 backup + Team dual-write | ✅ Live va4e1c516 |
| gumroad-kit-sync | 4-tier Gumroad routing (Pro/Annual/Founder/Team) | ✅ Live ve1287839 |
| eidetic-affiliate | `/ref/<code>` attribution tracking | ✅ Live |
| eidetic-analytics | Privacy-safe conversion funnel | ✅ **LIVE** — AE binding live (dataset `eidetic_funnel`, binding `ANALYTICS`), `POST /event` 204 verified |
| eidetic-account | Customer Pro dashboard (paste api_key → see backups) | ✅ Live |
| eidetic-slack | `/eidetic` Slack slash command | ✅ **LIVE** — app `eidetic-works`, workspace `eidetic-works.slack.com`, /healthz 200 |
| eidetic-discord | `/eidetic` Discord bot | ✅ **LIVE** — app `Eidetic` (1506865174773108836), interactions endpoint verified by Discord PING |
| eidetic-telegram | `/eidetic` Telegram bot | ✅ **LIVE** — `@eideticworks_bot`, webhook registered + secret-validated |

## Docs (12)

| Doc | Purpose |
|---|---|
| `INTEGRATIONS.md` (repo root) | 15-surface quickstart matrix |
| `SECURITY.md` (root) | Threat model + disclosure policy |
| `docs/SELF_HOSTED.md` | Enterprise BYO-everything walkthrough |
| `docs/PRO_LAUNCH.md` | Pro tier: Gumroad copy + Kit email + drip emails |
| `docs/TEAM_LAUNCH.md` | Team tier ($99/mo, 5-seat) spec |
| `docs/PRICING.md` | Pro variants: Monthly/Annual/Founder |
| `docs/PROMPT.md` | nucleus_ask integration recipes (5 hosts) |
| `docs/HOOKS.md` | Webhook hooks reference + 4 recipes |
| `docs/enterprise/SOC2_READINESS.md` | TSC mapping + 12-month plan to certify |
| `docs/enterprise/BAA_TEMPLATE.md` | HIPAA path + verbatim BAA template |
| `docs/DECISIONS.md` | All ADRs (ADR-020: privacy posture; ADR-021: TB carve-out) |
| `docs/posts/hn-show-hn-day11.md` | Show HN draft + posting playbook |

## Scripts (6)

| Script | Purpose |
|---|---|
| `scripts/gen_pro_key.sh` | Provision a Pro subscriber API key + KV registration |
| `scripts/weekly-digest.sh` | Cron-friendly /digest renderer (email or tee) |
| `scripts/import-chatgpt.sh` | Seed engram store from ChatGPT export |
| `scripts/import-claude.sh` | Seed engram store from Claude.ai export |
| `scripts/notion-poll.sh` (in landing repo) | Local Notion DB poller |
| `scripts/deploy-worker.sh` | One-time operator setup helper |

## Lokesh-keyboard — ALL CLEARED (2026-05-21)

All 6 items resolved in one driven session:

1. **docs.eidetic.works CNAME** — ✅ Live (200, CF cert provisioned)
2. **CF Analytics Engine enable** — ✅ Live (dataset `eidetic_funnel`, binding `ANALYTICS`)
3. **Slack app registration** — ✅ Live (workspace `eidetic-works.slack.com`, app `Eidetic`)
4. **Discord app registration** — ✅ Live (app `Eidetic`, Interactions Endpoint PING-verified)
5. **Telegram bot** — ✅ Live (`@eideticworks_bot`, webhook registered)
6. **Reddit r/SaaS** — ❌ **Permanently skipped** per `feedback_reddit_low_yield.md` (pseudonymous accounts auto-mod-removed; not retrying)

(HN Show HN skipped per Lokesh.)

## ADRs added this sprint

- **ADR-020** (2026-05-20) — Local-first privacy posture as a HARD CONTRACT. Enumerates every outbound network call; tcpdump-audit recipe.
- **ADR-021** (2026-05-20) — ADR-011 amendment for scoped TB-unpause carve-out (TB resumes on eidetic-compounding work only).

## What's NOT shipped (honest list)

- Vector search (FTS5 stays the default; v0.1 candidate)
- Real-time dashboard updates (refresh-on-poll today)
- Multi-user per-host (single-user UDS trust model per SECURITY.md)
- Customer-managed encryption keys (Cloudflare-managed default; needs CF Enterprise)
- App Store / Marketplace publish: VS Code, JetBrains, Chrome, Raycast, Mac AppKit — all scaffolds ready, Lokesh-keyboard for cert/publish
- HIPAA BAA signing (template documented; conditions for activation in BAA_TEMPLATE.md)

## How to navigate from here

- **New customer:** install daemon (`brew install eideticd && eideticd -install`) → install MCP (`pip install eidetic-mcp`) → in Claude Code, ask "what was that thing I worked on yesterday?"
- **Pro evaluator:** read `docs/PRO_LAUNCH.md` + `docs/PRICING.md` → buy at `eideticworks.gumroad.com/l/eidetic-pro`
- **Enterprise evaluator:** read `docs/SELF_HOSTED.md` + `docs/enterprise/SOC2_READINESS.md` + `docs/enterprise/BAA_TEMPLATE.md`
- **Contributor:** read `INTEGRATIONS.md` for surface inventory; new integration goes under `integrations/<name>/`
- **Future cc-main session:** read this doc + `.brain/session_mirror/cc_main_last.md` for state; nothing major missing on infra side

---

Generated 2026-05-21 by cc-main, mid-compression-sprint. Cross-reference: CHANGELOG.md for per-version detail, .brain/session_mirror/cc_main_last.md for live state.
