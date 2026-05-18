# I got tired of losing Claude Code context between sessions, so I built a daemon

Every time I closed a Claude Code session, the conversation was gone.

Not archived. Not searchable. Just gone.

I'd start a new session and spend the first 10 minutes re-explaining context I'd *just* worked through. The "Summarize our last conversation" prompt only goes so far when you've had 300 sessions across 8 weeks.

So I built a fix.

---

## What `eideticd` does

It's a Go binary that runs at login (launchd on macOS, systemd on Linux). It uses [fsnotify](https://github.com/fsnotify/fsnotify) to tail Claude Code session files in real time. Every message you type becomes an **engram** — a row in a local SQLite-WAL database — within <50ms of the file write.

Nothing leaves your machine.

```sh
# One-liner install
curl -fsSL https://eidetic.works/install.sh | sh

# Or Homebrew
brew tap eidetic-works/nucleus
brew install eideticd
```

---

## The numbers that matter

After 2 weeks of dogfooding on my own machine:

- **141,502 engrams** captured across sessions
- **P95 retrieval: 0.27ms** on that live dataset
- SLO was 100ms — we cleared it by **370×**
- Capture latency: <50ms on all measured writes

---

## MCP bridge — let your AI query its own history

The companion Python package exposes the daemon's Unix socket API as MCP tools:

```sh
pip install eidetic-mcp
claude mcp add eidetic -- python -m eidetic_mcp.server
```

After that, you can ask Claude Code:

> "What was I debugging last Tuesday?"

And it pulls from your engrams in <1ms. No cloud, no API key, no subscription.

---

## Architecture decisions worth knowing

**Pure-Go SQLite via `modernc.org/sqlite`** — not the CGO `mattn/go-sqlite3`. This matters for distribution: CGO + cross-compile silently strips SQLite and produces a binary that crashes at runtime. The pure-Go driver cross-compiles cleanly to darwin-arm64, linux-amd64, and windows-amd64 from a single host. I learned this the hard way.

**WAL mode mandatory** — `PRAGMA journal_mode=WAL; synchronous=NORMAL` is the only mode that gives concurrent readers without blocking the writer. Write-append-only shape + single-writer pool (SetMaxOpenConns=1) + separate read pool (8 conns) = zero "database is locked" errors under load.

**FTS5 full-text index** — AFTER INSERT/DELETE triggers maintain a full-text index on the content column. Boolean operators, phrase queries, relevance ranking — all the SQLite FTS5 goodness, sub-millisecond.

**Single binary, no daemon manager required** — `eideticd -install` writes the launchd plist and bootstraps it. Uninstall is `eideticd -uninstall`. No Docker, no Python runtime, no config files to manage.

---

## What shipped in v0.0.25 (this week)

- **Compliance daemon** (`eideticd-compliance`): reads a `retention-policy.json`, purges engrams older than the configured TTL per surface, writes an audit log, exits. Designed for cron/launchd/systemd timer.
- **PyPI package**: `pip install eidetic-mcp` — the MCP bridge is now a proper PyPI package, not a `pip install -e` from source.
- **Homebrew formula**: `brew tap eidetic-works/nucleus && brew install eideticd`

---

## Try it

```sh
curl -fsSL https://eidetic.works/install.sh | sh
```

Source: [github.com/eidetic-works/eidetic-daemon](https://github.com/eidetic-works/eidetic-daemon) (MIT)

Happy to answer questions about the SQLite design, the fsnotify watcher, or the retention system.

---

*Tags: `go` `sqlite` `ai` `claudecode` `opensource`*
