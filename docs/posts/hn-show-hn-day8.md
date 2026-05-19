# Hacker News — Show HN

**Title:** Show HN: eidetic-daemon – Go binary that captures every Claude Code/Cursor session to local SQLite

**Body (comment):**

I kept losing context between Claude Code sessions and got tired of it. Built this over 2 weeks as my own daily tool.

**What it does:**
- Runs at login, tails Claude Code / Cursor session files with fsnotify
- INSERTs each message as an engram in `~/.eidetic/engrams.db` (SQLite-WAL) within <50ms of file write
- Exposes a Unix socket API: `GET /engrams`, `GET /search`, `GET /metrics`
- MCP bridge (`pip install eidetic-mcp`) so AI assistants can query your history directly

**Numbers from my machine after 2 weeks:**
- 278,561 engrams captured
- P95 retrieval 0.27ms on that live dataset (SLO was 100ms)
- <50ms capture latency on all measured writes

**Architecture decisions worth noting:**
- Pure-Go SQLite via modernc.org/sqlite (not CGO mattn/go-sqlite3) — cross-compile-clean single binary for darwin-arm64, linux-amd64, windows-amd64. CGO + cross-compile silently strips SQLite — learned this the hard way.
- WAL mode mandatory (`PRAGMA journal_mode=WAL; synchronous=NORMAL`) — WAL is the only mode that gives concurrent readers without blocking the writer
- FTS5 full-text index with AFTER INSERT/DELETE triggers — phrase queries, boolean operators, relevance ranking
- Single writer pool (SetMaxOpenConns=1) + separate read pool (8 conns) — eliminates "database is locked" under the write-append-only shape

**Try it:**
```
curl -fsSL https://eidetic.works/install.sh | sh
```

Source: https://github.com/eidetic-works/eidetic-daemon (MIT)

Questions welcome on the SQLite design, the fsnotify watcher, or the MCP bridge architecture.

**Pro tier** (just launched, $29/mo): if you want your engrams to follow you across machines — managed Cloudflare R2 sync, personal API key within 24h, `eideticd --restore` on a new machine. First 50 subscribers keep that price. https://eidetic.works
