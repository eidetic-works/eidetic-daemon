# Reddit r/ClaudeAI — eidetic-daemon launch

## CURRENT COPY (casual, use this)

**Title:** Built a Go daemon that tails Claude Code session files into local SQLite — 278K engrams in 2 weeks of dogfood

**Body:**

Every time I closed a Claude Code session I'd lose everything. Not backed up, not searchable, just gone. I'd start fresh and spend the first 10 minutes re-explaining context I'd typed 2 hours ago.

So two weeks ago I wrote `eideticd` — a Go binary that runs at login and uses fsnotify to watch the Claude Code session directory. Every message gets written to a local SQLite-WAL database in under 50ms. Nothing leaves your machine, no cloud, no API key.

278,561 engrams on my machine now. P95 retrieval is 0.27ms on that live dataset.

There's also an MCP bridge (pip install eidetic-mcp) so you can ask Claude Code "what was I debugging last Tuesday?" and have it pull from your local history. Sub-millisecond.

    curl -fsSL https://eidetic.works/install.sh | sh

or Homebrew: brew tap eidetic-works/nucleus && brew install eideticd

Source: github.com/eidetic-works/eidetic-daemon (MIT)

Happy to answer anything about the SQLite WAL setup or the fsnotify watcher — both had some non-obvious design decisions.

---

## ORIGINAL DRAFT (v1, more formatted)

**Title:** I built a background daemon that captures every Claude Code session to local SQLite — 278K engrams, P95 retrieval 0.27ms

Every time I closed a Claude Code session, whatever I'd typed was gone. Not archived, not searchable — just gone.

Two weeks ago I started building a fix. Today it's v0.0.25 and I'm using it daily.

`eideticd` is a Go binary that runs at login (via launchd / systemd). It uses fsnotify to tail Claude Code session files in real time. Every message you type becomes an engram row in a local SQLite-WAL database within <50ms of the file write. Nothing leaves your machine.

- 278,561 engrams captured across sessions (two weeks of real dogfood)
- P95 retrieval: 0.27ms on that live dataset
- The SLO I set was 100ms — we cleared it by 370×

GitHub: https://github.com/eidetic-works/eidetic-daemon
