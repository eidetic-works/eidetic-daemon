# eidetic-daemon

**Local-first Go daemon** that captures work across IDE / AI surfaces (Cursor, Cowork, Claude Code) into a single SQLite-WAL engram store, and serves it back over a local Unix socket in <100ms P95.

Part of [Nucleus](https://nucleusos.dev). 90-day public probe (started 2026-05-10).

---

## What it does

- **Engram capture** — `fsnotify` watches each surface's session files; new text → engram row in <50ms of file-write.
- **Engram retrieval** — `GET /engrams?surface=X&limit=N&since=unix-ns` over local Unix socket. P95 <100ms on 10K-row store.
- **Engram insertion** — `POST /engrams` — direct API-side insert; bypasses the fsnotify capture path. Accepts `{"surface":"...","payload":"...","ts":unix-ns}`, returns `{"id": N}`. Enables injection from mobile, webhooks, relay pipelines.
- **Engram purge** — `DELETE /engrams?surface=X[&before=unix-ns]` — remove by surface (with optional timestamp cutoff); returns `{"deleted": N}`.
- **Surface listing** — `GET /surfaces` — map of every active surface to its engram count; live view of what the daemon has seen.
- **Full-text search** — `GET /search?q=...` — FTS5 keyword/phrase/boolean search over engram payloads, ranked by relevance. Answers "what did I say about X?"
- **Recent activity** — `GET /recent?since=unix-ns&limit=N` — newest engrams across all surfaces, newest-first. Answers "what happened lately?" without a keyword or surface filter.
- **Multi-surface mirror** — Cursor / Cowork / Claude Code all feed one canonical store, indexed `(surface, ts DESC)`.

Single static binary. No CGO. Cross-compiles to darwin-arm64 + linux-amd64 + windows-amd64.

---

## Install (Day 7+)

```sh
curl -fsSL https://nucleusos.dev/install.sh | sh
```

To remove:

```sh
curl -fsSL https://eidetic.works/uninstall.sh | sh          # stops service, removes binary; keeps ~/.eidetic/
curl -fsSL https://eidetic.works/uninstall.sh | sh -s -- --purge-data  # also wipes engram data (irreversible)
```

Not yet shipped publicly. Latest release: [v0.0.11](https://github.com/eidetic-works/eidetic-daemon/releases/tag/v0.0.11) (3 cross-compile assets + `SHA256SUMS.txt` attached; pure-Go, no CGO). See `scripts/install.sh` and `scripts/uninstall.sh` for what the one-line installers run.

Full demo flow with expected outputs at every step: [`docs/demo.md`](./docs/demo.md). Architecture decisions: [`docs/DECISIONS.md`](./docs/DECISIONS.md). Release notes per version: [`CHANGELOG.md`](./CHANGELOG.md).

### MCP bridge (Cursor / Claude Code / Cline / any MCP client)

Optional Python wrapper exposing the daemon's UDS API as MCP tools. Lives at [`bridge/python/`](./bridge/python). Process-isolated from the daemon (separate install, separate crash surface).

```sh
pip install -e bridge/python
# Then add to your MCP client config:
#   {"eidetic": {"command": "python", "args": ["-m", "eidetic_mcp.server"]}}
```

Tools: `query_engrams(surface, limit, since)` + `daemon_status()` + `daemon_metrics()` (v0.0.8+ — wraps the daemon's `/metrics` endpoint as an MCP-callable tool). See [`bridge/python/README.md`](./bridge/python/README.md) for full setup + per-client config snippets.

If the daemon is running with caller auth on (v0.0.9+, opt-in), the bridge auto-discovers the token from `<dataDir>/auth-token` (or `EIDETIC_AUTH_TOKEN` env var). No bridge config change required.

After install, the daemon spawns at login via launchd / systemd-user. To verify:

```sh
# Confirm the daemon is alive.
curl --unix-socket /tmp/eidetic-daemon.sock http://localhost/healthz
# → {"status":"ok"}

# Open Cursor or Claude Code, write something. Then read it back.
curl --unix-socket /tmp/eidetic-daemon.sock 'http://localhost/engrams?surface=claude_code&limit=5'

# All surfaces the daemon has seen, with engram counts (v0.0.13+).
curl --unix-socket /tmp/eidetic-daemon.sock http://localhost/surfaces
# → {"claude_code": 1234, "cursor": 567, "cowork": 89}

# Full-text search over engram payloads (v0.0.14+).
curl --unix-socket /tmp/eidetic-daemon.sock 'http://localhost/search?q=benchmark'
curl --unix-socket /tmp/eidetic-daemon.sock 'http://localhost/search?q="benchmark+result"&surface=claude_code&limit=10'
# → same []Engram JSON shape as /engrams, ordered by relevance rank

# Direct API-side insert — bypasses fsnotify (v0.0.16+).
curl -X POST --unix-socket /tmp/eidetic-daemon.sock \
  -H 'Content-Type: application/json' \
  -d '{"surface":"mobile","payload":"noted from phone"}' \
  http://localhost/engrams
# → {"id": 1234}

# Recent activity across all surfaces, newest-first (v0.0.15+).
curl --unix-socket /tmp/eidetic-daemon.sock 'http://localhost/recent'
curl --unix-socket /tmp/eidetic-daemon.sock 'http://localhost/recent?since=1747500000000000000&limit=20'
# → []Engram JSON, ts DESC, max 500

# Purge all engrams for a surface (v0.0.13+).
curl -X DELETE --unix-socket /tmp/eidetic-daemon.sock 'http://localhost/engrams?surface=cursor'
# → {"deleted": 567}

# Purge only engrams older than a timestamp (unix nanoseconds).
curl -X DELETE --unix-socket /tmp/eidetic-daemon.sock 'http://localhost/engrams?surface=cursor&before=1715000000000000000'
# → {"deleted": 42}

# Live metrics (v0.0.7+): version, uptime, engram counts per surface,
# capture skip-counter, DB size, query latency P50/P95/P99 (v0.0.12+).
# Schema is additive-only across versions. Three formats via Accept-header:
curl --unix-socket /tmp/eidetic-daemon.sock http://localhost/metrics                                              # JSON (default; v0.0.7)
curl -H 'Accept: text/plain' --unix-socket /tmp/eidetic-daemon.sock http://localhost/metrics                      # Prometheus exposition (v0.0.10+)
curl -H 'Accept: application/openmetrics-text' --unix-socket /tmp/eidetic-daemon.sock http://localhost/metrics    # OpenMetrics 1.0.0 (v0.0.11+)
# v0.0.12+ adds query_latency_p50_us / _p95_us / _p99_us to JSON and
# eidetic_query_duration_microseconds{quantile=...} summary to Prometheus/OpenMetrics.
```

Real Prometheus scrapers send a multi-type Accept by default (`application/openmetrics-text;version=1.0.0,text/plain;version=0.0.4;q=0.5,*/*;q=0.1`); v0.0.11 honors the openmetrics clause and returns OpenMetrics (precedence) — drop into your existing scrape config, no shim.

### Caller auth (v0.0.9+, opt-in)

Off by default — preserves the W1 single-user UDS-trust model in [SECURITY.md](./SECURITY.md). Operators wanting a harder boundary turn it on with one env var or flag:

```sh
EIDETIC_AUTH=1 eideticd      # env var (recommended for service managers)
eideticd -auth                # flag (recommended for one-shot invocations)
```

On enable, the daemon writes `<dataDir>/auth-token` (0600 perms, 64-char hex from `crypto/rand`). Token rotates every restart — no stale-token replay. `/healthz` stays open even with auth on; `/engrams` + `/metrics` require `Authorization: Bearer <token>` (or bare token).

---

## Real-data dogfood

v0.0.5 ran against this developer's real `~/.claude/projects/` for 12 seconds (2026-05-14):

- **141,502 engrams ingested** from **564 distinct Claude Code session files**
- **657 MB DB written** at **~11.8K engrams/sec sustained**
- **0 records skipped** (capture skip-counter at zero)
- **0 crashes**; max payload 3.54 MiB; mean 3.96 KB

The chunked-capture path (records >7 MiB → split into idempotent `chunk_id`-tagged engrams; reassembled on the bridge side so MCP clients see ONE engram per logical record) didn't trigger — real Claude Code data tops out around 2.4 MiB per record. Chunked-capture is defense-in-depth, not common-case.

v0.0.6 fixed a SIGTERM shutdown race surfaced during this same dogfood (issue #17 — daemon was logging ~30 noisy "database is closed" errors per stop, no data loss; now 0).

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

W1 complete — 14 releases v0.0.2 → v0.0.13 (v0.0.12/v0.0.13 pending CI). Track via `docs/IMPLEMENTATION_PLAN.md` § 11 phase sequencing.

| Phase | What | State |
|---|---|---|
| 0 | Repo + scaffold | ✅ |
| 1 | `internal/store` complete | ✅ (#1) |
| 2 | `internal/api` complete | ✅ (#2) |
| 3 | `internal/capture` 3 parsers + state + race fix | ✅ (#4) |
| 4 | Integration: mirror + concurrency tests | ✅ (rolled into #4) |
| 5 | Bench gates wired | ✅ (#5) |
| 6 | Cross-compile artifacts + install.sh + service files | ✅ (#5) |
| 7 | GitHub release + demo post | ✅ v0.0.2 → v0.0.13 released; demo at `docs/demo.md`; public-flip + DO post pending |
| 8 | MCP bridge (Python stdio server) | ✅ v0.0.4 (#12); reassembly v0.0.5 (#15); `daemon_metrics()` v0.0.8 (#22) |
| 9 | Chunked-capture (no payload-size hard wall) | ✅ v0.0.5 (#14) |
| 10 | Shutdown drain (issue #17) | ✅ v0.0.6 (#18) |
| 11 | Observability — `/metrics` JSON | ✅ v0.0.7 (#19) |
| 12 | Caller auth — Bearer token (opt-in) | ✅ v0.0.9 (#25) |
| 13 | Prometheus exposition format on `/metrics` | ✅ v0.0.10 (#26) |
| 14 | OpenMetrics 1.0.0 exposition format on `/metrics` | ✅ v0.0.11 (#27) |
| 15 | Query latency tracker — P50/P95/P99 on `/metrics` | ✅ v0.0.12 (#37, pending CI) |
| 16 | `DELETE /engrams` + `GET /surfaces` | ✅ v0.0.13 (#38 #39, pending CI) |
| 17 | `uninstall.sh` — clean daemon removal + optional data purge | ✅ (#40, pending CI) |
| 18 | `GET /search` — FTS5 full-text search, ranked by relevance | ✅ v0.0.14 (#47) |
| 19 | `GET /recent` — cross-surface recent engrams, newest-first | ✅ v0.0.15 (#48) |
| 20 | `POST /engrams` — API-side direct insert, `ErrInvalidEngram` sentinel | ✅ v0.0.16 (#49) |

---

## License

MIT. See [LICENSE](LICENSE).
