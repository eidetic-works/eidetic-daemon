# eidetic-daemon

**Local-first Go daemon** that captures work across IDE / AI surfaces (Cursor, Cowork, Claude Code) into a single SQLite-WAL engram store, and serves it back over a local Unix socket in <100ms P95.

Part of [Nucleus](https://nucleusos.dev). 90-day public probe (started 2026-05-10).

---

## What it does

- **Engram capture** — `fsnotify` watches each surface's session files; new text → engram row in <50ms of file-write.
- **Engram retrieval** — `GET /engrams?surface=X&limit=N` over local Unix socket. P95 <100ms on 10K-row store.
- **Multi-surface mirror** — Cursor / Cowork / Claude Code all feed one canonical store, indexed `(surface, ts DESC)`.

Single static binary. No CGO. Cross-compiles to darwin-arm64 + linux-amd64 + windows-amd64.

---

## Install (Day 7+)

```sh
curl -fsSL https://nucleusos.dev/install.sh | sh
```

Not yet shipped publicly. Latest internal release: [v0.0.3](https://github.com/eidetic-works/eidetic-daemon/releases/tag/v0.0.3) (3 cross-compile assets attached). See `scripts/install.sh` for what the one-line installer runs.

Full demo flow with expected outputs at every step: [`docs/demo.md`](./docs/demo.md). Architecture decisions: [`docs/DECISIONS.md`](./docs/DECISIONS.md). Release notes per version: [`CHANGELOG.md`](./CHANGELOG.md).

### MCP bridge (Cursor / Claude Code / Cline / any MCP client)

Optional Python wrapper exposing the daemon's UDS API as MCP tools. Lives at [`bridge/python/`](./bridge/python). Process-isolated from the daemon (separate install, separate crash surface).

```sh
pip install -e bridge/python
# Then add to your MCP client config:
#   {"eidetic": {"command": "python", "args": ["-m", "eidetic_mcp.server"]}}
```

Tools: `query_engrams(surface, limit, since)` + `daemon_status()`. See [`bridge/python/README.md`](./bridge/python/README.md) for full setup + per-client config snippets.

After install, the daemon spawns at login via launchd / systemd-user. To verify:

```sh
# Confirm the daemon is alive.
curl --unix-socket /tmp/eidetic-daemon.sock http://localhost/healthz
# → {"status":"ok"}

# Open Cursor or Claude Code, write something. Then read it back.
curl --unix-socket /tmp/eidetic-daemon.sock 'http://localhost/engrams?surface=claude_code&limit=5'
```

---

## Latency

P95 retrieval **0.27 ms** on a 10K-row fixture, M4 MacBook, 2026-05-13 (mainline build, full daemon stack). Spec section 3 SLO is ≤100 ms; current headroom **~370×**.

Three gates wired in `bench/`:

| Bench | Spec / ADR gate | Measured P95 | Headroom |
|---|---|---|---|
| Retrieve (10K rows, 1000 reqs × 3 runs) | ≤100 ms (spec section 3) | 0.31 / 0.27 / 0.25 ms | 320–400× |
| Write (100 req/s sustained) | ≤50 ms (ADR-014 gap A) | 0.65 ms | ~75× |
| Concurrent (5 readers + 1 writer) | ≤100 ms (ADR-014 gap C) | 3.5 ms | ~28× |

Reproduce: `make bench`. CI fails the build below threshold.

ADR-016 modernc cold-init 1.75 s is hidden behind launchd/systemd `RunAtLoad=true` so users never see it.

See [SECURITY.md](./SECURITY.md) for the threat model + storage modes before relying on the daemon for anything sensitive.

---

## Architecture

- **Driver:** `modernc.org/sqlite` (pure-Go, no CGO; cross-compile-clean per ADR-016)
- **Store:** SQLite WAL + open-string pragmas (`busy_timeout=5000`, `cache=shared`, `synchronous=NORMAL`)
- **Connection pools:** 1 writer (single conn) + 8 readers (read-only opens)
- **Retrieval:** prepared statement + composite index `(surface, ts DESC)`
- **Capture:** `fsnotify` per-surface watchers + incremental parsers + offset-state in `~/.eidetic/state.json`
- **Lifecycle:** spawn-at-app-startup mandatory (absorbs 1.75s modernc cold-init behind app load)

See [docs/SPEC.md](docs/SPEC.md) for the binding W1 spec, [docs/IMPLEMENTATION_PLAN.md](docs/IMPLEMENTATION_PLAN.md) for the file-by-file build plan.

---

## Status

W1 scaffold (Day 3 of 7). Track via `docs/IMPLEMENTATION_PLAN.md` § 11 phase sequencing.

| Phase | What | State |
|---|---|---|
| 0 | Repo + scaffold | ✅ |
| 1 | `internal/store` complete | ✅ (#1) |
| 2 | `internal/api` complete | ✅ (#2) |
| 3 | `internal/capture` 3 parsers + state + race fix | ✅ (#4) |
| 4 | Integration: mirror + concurrency tests | ✅ (rolled into #4) |
| 5 | Bench gates wired | ✅ (#5) |
| 6 | Cross-compile artifacts + install.sh + service files | ✅ (#5) |
| 7 | GitHub release + demo post | ✅ v0.0.2 + v0.0.3 released; demo doc at `docs/demo.md`; public-flip + DO post pending |

---

## License

MIT. See [LICENSE](LICENSE).
