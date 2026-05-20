# Integrations — every surface eidetic-daemon plugs into

eidetic-daemon ships in 14 places. Most are free + open-source. Pick the one that fits your daily tool.

| # | Surface | Path / Distribution | Status | Quickstart |
|---|---|---|---|---|
| 1 | **Daemon** (the thing itself) | `brew install eideticd` · curl install.sh · install.ps1 | ✅ v0.0.53 live, 4 platforms | `eideticd -init` |
| 2 | **eidetic-mcp** (MCP bridge) | `pip install eidetic-mcp` | ✅ v0.0.7 on PyPI | `claude mcp add eidetic -- python -m eidetic_mcp.server` |
| 3 | **VS Code extension** | `integrations/vscode/` | ✅ scaffolded, 11/11 tests | `cd integrations/vscode && npm install && npm run compile` |
| 4 | **JetBrains plugin** (IntelliJ/PyCharm/GoLand/...) | `integrations/jetbrains/` | ✅ scaffolded (Kotlin+Gradle v2) | `./gradlew buildPlugin` then install from disk |
| 5 | **Raycast extension** | `integrations/raycast/` | ✅ scaffolded, 4 commands | `cd integrations/raycast && npm install && npx ray dev` |
| 6 | **Chrome extension** (MV3) | `integrations/chrome-extension/` | ✅ scaffolded, 12 files | Load unpacked at `chrome://extensions` |
| 7 | **Mac SwiftBar plugin** | `integrations/macos-menubar/eidetic-status.5m.swift` | ✅ live-tested ("🧠 300K") | Copy to `~/Library/Application Support/SwiftBar/Plugins/` |
| 8 | **Mac native menubar app** | `integrations/macos-menubar/EideticMenubar/` | ✅ Swift scaffold (no xcodeproj — Lokesh-keyboard for App Store) | xcodebuild + notarize |
| 9 | **Web dashboard (PWA)** | `eidetic.works/dashboard` | ✅ live, installable on iPhone/Android | `eideticd -bridge :8421` then open dashboard |
| 10 | **Slack `/eidetic` app** | `integrations/slack-app/` | ✅ Worker scaffolded, manifest ready | api.slack.com app + worker deploy |
| 11 | **Discord `/eidetic` bot** | `integrations/discord-bot/` | ✅ Worker + register-commands.js | discord.com app + worker deploy |
| 12 | **Telegram `/eidetic` bot** | `integrations/telegram-bot/` | ✅ Worker + setup form | @BotFather + worker deploy |
| 13 | **Documentation site** | `docs.eidetic.works` | ⏳ awaiting op-assistant Pages deploy | `cd landing-docs && wrangler pages deploy dist` |
| 14 | **HTTP API + curl** | UDS `/tmp/eidetic-daemon.sock` or bridge TCP | ✅ all daemon endpoints | `curl --unix-socket /tmp/eidetic-daemon.sock http://localhost/healthz` |

## Quickstart by use case

### "I want recall in my AI sessions"
1. Install daemon: `brew install eideticd && eideticd -install`
2. Install MCP bridge: `pip install eidetic-mcp`
3. Register with Claude Code: `claude mcp add eidetic -- python -m eidetic_mcp.server`
4. In any Claude Code session: ask "what did I work on yesterday?" — Claude calls `nucleus_ask`, retrieves engrams, answers.

### "I want recall in my IDE without MCP"
- **VS Code:** install from `integrations/vscode/` — sidebar + Cmd+Shift+P "Eidetic: Recall"
- **JetBrains:** install plugin from disk — Tools menu → Eidetic: Recall
- **Cursor:** uses MCP; same as Claude Code above

### "I want recall in my terminal"
- `eideticd --ask "what was that postgres trick"` (v0.0.51+)
- `eideticd browse` for interactive TUI (v0.0.54+ — pending build)

### "I want recall on my phone"
- Run `eideticd -bridge :8421` locally
- Expose via Cloudflare Tunnel: `cloudflared tunnel --url http://localhost:8421`
- Open `eidetic.works/dashboard` on your phone, paste the tunnel URL + bridge token
- Add to home screen (PWA)

### "I want recall from any chat client"
Pick one:
- Slack: `/eidetic <question>` in any channel — see `integrations/slack-app/README.md`
- Discord: `/eidetic <question>` — see `integrations/discord-bot/README.md`
- Telegram: `/eidetic <question>` to your bot — see `integrations/telegram-bot/README.md`

### "I want to capture from anywhere"
- Files on disk that change: daemon's fsnotify watcher handles claude_code/cursor/cowork automatically
- Any other tool's output: pipe to `eideticd --capture --surface NAME` (v0.0.52+)
- Web pages: Chrome extension's "Save this page" button
- Already-existing AI history: `scripts/import-chatgpt.sh` + `scripts/import-claude.sh`

### "I want a recurring digest"
- Cron: `0 9 * * 1 eideticd --digest 7d | mail -s recap you@x.com` (v0.0.50+)
- Or: `scripts/weekly-digest.sh --tee` writes to `/tmp/eidetic-weekly-digest.txt`

## Surface compatibility matrix

| Surface | Daemon endpoint | Auth | Network reach |
|---|---|---|---|
| Daemon CLI flags | direct store open | n/a (read-only) | local |
| MCP tools | UDS `/healthz`, `/search`, `/ask`, etc. | optional Bearer | local |
| VS Code / Raycast / SwiftBar / JetBrains | UDS or TCP bridge | optional Bearer | local |
| Web dashboard (browser) | TCP bridge (`-bridge :8421`) | always Bearer | localhost OR Cloudflare tunnel |
| Slack / Discord / Telegram | bridge via Cloudflare tunnel | always Bearer | internet (HTTPS only) |
| Chrome extension | TCP bridge | always Bearer | localhost |
| Pro cloud sync (R2 upload) | `eidetic-sync` Cloudflare Worker | Bearer + KV-stored hash | outbound to your R2 bucket only |

Per ADR-020: every outbound network call is opt-in and enumerated in `docs/DECISIONS.md`.

## Building from source

All Go integrations: standard `go build`. JS integrations: `npm install && npm run build`. Swift: `swiftc -parse` to verify; `xcodebuild` for the bundle. Kotlin: `./gradlew buildPlugin`.

## Contributing a new integration

Open a PR with a new `integrations/<name>/` directory. Minimum: README + a single working entry point that hits `GET /healthz`. Don't ship token storage logic from scratch — copy the pattern from `integrations/vscode/src/daemonClient.ts` or `integrations/chrome-extension/common.js`.

Reach `hi@eidetic.works` first if you're considering a substantial integration — we'll coordinate so we don't duplicate work.
