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

Not yet shipped. See [docs/IMPLEMENTATION_PLAN.md](docs/IMPLEMENTATION_PLAN.md) for W1 build progress.

---

## Latency

P95 retrieval: _measured number lands here on Day-7 ship per [spec § 3](docs/SPEC.md#3-p95-slo)._

Pre-Day-1 spike (synthetic 10K-row fixture, sequential read, warm cache): **0.397 ms** (modernc.org/sqlite, pure-Go) / 0.369 ms (mattn/go-sqlite3, CGO). 252× under SLO.

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
| 1 | `internal/store` complete | pending |
| 2 | `internal/api` complete | pending |
| 3 | `internal/capture` 3 parsers | pending |
| 4 | Integration: mirror + concurrency tests | pending |
| 5 | Bench gates wired | pending |
| 6 | Cross-compile artifacts + install.sh | pending |
| 7 | GitHub release + demo post | pending |

---

## License

MIT. See [LICENSE](LICENSE).
