# eidetic-daemon

**Local-first Go daemon** that captures every Claude Code / Cursor / Cowork session into a single SQLite-WAL engram store, and serves it back in <100ms P95.

278,561 engrams. 803 sessions. 3.3 GB. Two weeks of real dogfood. Free and MIT.

**→ [eidetic.works](https://eidetic.works)** · [latest release](https://github.com/eidetic-works/eidetic-daemon/releases/latest) · [CHANGELOG](./CHANGELOG.md) · [Pro $29/mo](https://eideticworks.gumroad.com/l/eidetic-pro)

---

## What it does

- **Engram capture** — `fsnotify` watches each surface's session files; new text → engram row in <50ms of file-write.
- **Engram retrieval** — `GET /engrams?[surface=X]&limit=N&since=unix-ns[&before=unix-ns][&order=asc]` over local Unix socket. P95 <100ms on 10K-row store. `surface` is optional (v0.0.23+) — omit to retrieve across all surfaces. `since`+`before` define a time window; `order=asc` returns oldest-first (default newest-first).
- **Engram insertion** — `POST /engrams` — direct API-side insert; bypasses the fsnotify capture path. Accepts `{"surface":"...","payload":"...","ts":unix-ns}`, returns `{"id": N}`. Enables injection from mobile, webhooks, relay pipelines.
- **Bulk insertion** — `POST /engrams/batch` — JSON array of engrams in one atomic transaction; returns `{"inserted": N}`. Efficient for relay sync, session replay, bulk import.
- **Point lookup** — `GET /engrams/{id}` — fetch a single engram by primary key; 404 when not found. Use after a `POST /engrams` to confirm the stored row.
- **Point delete** — `DELETE /engrams/{id}` — surgical removal of a single engram by primary key; 404 when not found. Use to remove accidentally captured sensitive data or dedup relay noise.
- **Count** — `GET /engrams/count?[surface=X][&since=unix-ns]` — returns `{"count": N}` without fetching rows. Use for monitoring badges, health dashboards, and sync-diff checks.
- **Engram purge** — `DELETE /engrams?surface=X[&before=unix-ns]` — remove by surface (with optional timestamp cutoff); returns `{"deleted": N}`.
- **Surface listing** — `GET /surfaces` — map of every active surface to its engram count; live view of what the daemon has seen.
- **Full-text search** — `GET /search?q=...` — FTS5 keyword/phrase/boolean search over engram payloads, ranked by relevance. Answers "what did I say about X?"
- **Recent activity** — `GET /recent?[since=unix-ns][&before=unix-ns]&limit=N` — newest engrams across all surfaces, newest-first. `since`+`before` enable sliding-window polling. Answers "what happened lately?" without a keyword or surface filter.
- **AI-powered recall** — `GET /ask?question=<text>` (v0.0.38+) — extracts keywords from a natural-language question, FTS5-retrieves top-K engrams, returns them wrapped in answer-scaffolding for the caller's host LLM. Same semantics as the `nucleus_ask` MCP tool. LRU+TTL cache so repeat questions don't re-hit the index (v0.0.45+).
- **Bulk export** — `GET /export[?surface=X][&since=ns][&before=ns]` (v0.0.42+) — paginated NDJSON stream of every engram, asc timestamp order. `curl -O` saves as `engrams-export.ndjson`; pipe to `jq` for one-engram-per-line consumption. Memory-bounded — safe against 10M-row stores.
- **Multi-surface mirror** — Cursor / Cowork / Claude Code all feed one canonical store, indexed `(surface, ts DESC)`. v0.0.41+: Cursor capture filters to `chatSessions/*.json` (excludes per-workspace `workspace.json` noise).

Single static binary. No CGO. Cross-compiles to darwin-arm64 + linux-amd64 + linux-arm64 + windows-amd64.

---

## Install

```sh
# One-liner (macOS + Linux)
curl -fsSL https://eidetic.works/install.sh | sh

# Homebrew (macOS, recommended)
brew tap eidetic-works/nucleus
brew install eideticd
```

To remove:

```sh
eideticd -uninstall          # v0.0.44+: stops service, removes plist/unit, prompts y/N to delete data
eideticd -uninstall -purge   # unattended: skip prompt, delete <dataDir>
```

Latest release artifacts: darwin-arm64, linux-amd64, linux-arm64, windows-amd64 — pure-Go, no CGO, statically linked. See `scripts/install.sh` for what the one-line installer runs. Homebrew formula auto-updates on every tag push (v0.0.43+).

Full demo flow with expected outputs at every step: [`docs/demo.md`](./docs/demo.md). Architecture decisions: [`docs/DECISIONS.md`](./docs/DECISIONS.md). Release notes per version: [`CHANGELOG.md`](./CHANGELOG.md).

### Cloud sync (opt-in)

Drop a `~/.eidetic/sync.json` and the daemon syncs `engrams.db` to Cloudflare R2 every hour. Restore on a new machine in 60 seconds:

```sh
# Sync now (requires sync.json)
eideticd --sync-now

# Restore latest backup on a new machine
eideticd --restore
# ✓ Downloaded 3.3 GB engrams.db from cloud backup
#   restart eideticd to use the restored database
```

**Bring your own R2 (free tier):** create an R2 bucket, deploy the sync Worker from `bridge/cloudflare/`, set `EIDETIC_API_KEY`, drop `sync.json`. No ongoing cost for personal use.

**[Pro — $29/mo](https://eideticworks.gumroad.com/l/eidetic-pro):** Eidetic Works hosts the bucket. Personal API key + `sync.json` delivered within 24h. Includes `nucleus_ask` AI recall + web dashboard. First 50 subscribers keep this price.

**[Team — $99/mo](mailto:hi@eidetic.works?subject=eidetic%20Team):** 5 seats with shared-team engram pooling (v0.0.39+: `X-Team-ID` header dual-writes to `engrams/team/<team_id>/<device_id>/` for cross-seat recall).

### Web dashboard

Browse + search engrams in the browser without MCP:

```sh
# Expose the daemon over a TCP bridge with auth + CORS (v0.0.31+)
eideticd -bridge :8421

# Visit https://eidetic.works/dashboard
# Paste http://127.0.0.1:8421 + the token from ~/.eidetic/bridge-token
```

Pure static HTML, no backend — page talks only to YOUR daemon (or your Cloudflare tunnel if you've exposed it). Credentials stored in localStorage, never POSTed anywhere.

### MCP bridge (Cursor / Claude Code / Cline / any MCP client)

Python wrapper exposing the daemon's UDS API as MCP tools. Process-isolated from the daemon (separate install, separate crash surface).

```sh
pip install eidetic-mcp
claude mcp add eidetic -- python -m eidetic_mcp.server
# Or for Cursor / other MCP clients:
#   {"eidetic": {"command": "python", "args": ["-m", "eidetic_mcp.server"]}}
```

Tools (eidetic-mcp 0.0.10+, 17 registered): `query_engrams`, `search_engrams`, `recent_engrams`, `count_engrams`, `get_engram_by_id`, `delete_engram_by_id`, `insert_engram`, `insert_engrams_batch`, `purge_engrams`, `list_surfaces`, `daemon_status`, `daemon_metrics`, **`nucleus_ask(question)`** — RAG over your local engrams: extracts keywords, retrieves top-K via FTS5, returns answer-scaffolding for the host LLM. Your engrams never leave your machine. Plus four more recall tools: `nucleus_digest(window)` (24h/7d/30d summary), `nucleus_timeline(window, surfaces?)` (cross-tool chronology), `nucleus_link(engram_id)` (related-engram neighborhood), `nucleus_curate(query, limit?)` (de-noised top-K for downstream LLM context). See [`bridge/python/README.md`](./bridge/python/README.md) for per-client config + [`docs/PROMPT.md`](./docs/PROMPT.md) for 5 integration recipes.

If the daemon is running with caller auth on (v0.0.9+, opt-in), the bridge auto-discovers the token from `<dataDir>/auth-token` (or `EIDETIC_AUTH_TOKEN` env var). No bridge config change required.

After `curl | sh`, the daemon registers and spawns automatically. For Homebrew or manual installs, register the service with one command:

```sh
eideticd -install   # launchd on macOS, systemd-user on Linux
```

To verify:

```sh
# See your engram stats (works whether daemon is running or not)
eideticd --stats
# → eideticd v0.0.62 — engram statistics
# →   engrams:    278561
# →     claude_code          274203
# →     cursor                 4135
# →     cowork                  223
# →   oldest:     2026-03-01
# →   newest:     2026-05-20
# →   db size:    3.3 GB
# →   P95 fetch:  0.27 ms
# →
# →   cloud sync:
# →     last sync:  2026-05-20 09:00:00
# →     last key:   engrams/macbook-m2/engrams-1779235200000.db
# →     last size:  3.3 MB

# Diagnose your cloud sync (v0.0.34+ — Pro users use this if sync feels broken)
eideticd --check
# → worker: ✓ reachable (200 OK)
# → last sync: 2026-05-20 09:00 (4m ago)
# → status: ✓ sync healthy

# Browse last 10 cloud backups (v0.0.36+)
eideticd --backups

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

# Bulk insert — atomic, one round-trip (v0.0.17+).
curl -X POST --unix-socket /tmp/eidetic-daemon.sock \
  -H 'Content-Type: application/json' \
  -d '[{"surface":"mobile","payload":"note 1"},{"surface":"mobile","payload":"note 2"}]' \
  http://localhost/engrams/batch
# → {"inserted": 2}

# Fetch a single engram by ID (v0.0.18+).
curl --unix-socket /tmp/eidetic-daemon.sock 'http://localhost/engrams/1234'
# → {"id":1234,"surface":"mobile","ts":...,"payload":"...","meta":""}
# 404 when ID not found; 400 on non-integer or zero.

# Delete a single engram by ID (v0.0.19+).
curl -X DELETE --unix-socket /tmp/eidetic-daemon.sock 'http://localhost/engrams/1234'
# → {"deleted":1}
# 404 when ID not found; 400 on non-integer or zero.

# Count engrams (v0.0.20+): all, by surface, by time window.
curl --unix-socket /tmp/eidetic-daemon.sock 'http://localhost/engrams/count'
curl --unix-socket /tmp/eidetic-daemon.sock 'http://localhost/engrams/count?surface=claude_code'
curl --unix-socket /tmp/eidetic-daemon.sock 'http://localhost/engrams/count?since=1747500000000000000'
# → {"count": N}

# Time-window retrieval (v0.0.21+): before= upper bound on /engrams and /recent.
curl --unix-socket /tmp/eidetic-daemon.sock 'http://localhost/engrams?surface=claude_code&before=1747500000000000000'
curl --unix-socket /tmp/eidetic-daemon.sock 'http://localhost/engrams?surface=claude_code&since=1747000000000000000&before=1747500000000000000'
# → []Engram JSON for the half-open window (since, before)

# Oldest-first retrieval (v0.0.22+): order=asc for replay consumers.
curl --unix-socket /tmp/eidetic-daemon.sock 'http://localhost/engrams?surface=claude_code&order=asc'
# → []Engram JSON, ts ASC (oldest first)

# Recent activity across all surfaces, newest-first (v0.0.15+).
curl --unix-socket /tmp/eidetic-daemon.sock 'http://localhost/recent'
curl --unix-socket /tmp/eidetic-daemon.sock 'http://localhost/recent?since=1747500000000000000&limit=20'
curl --unix-socket /tmp/eidetic-daemon.sock 'http://localhost/recent?before=1747500000000000000&limit=20'
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

Live at v0.0.62 (2026-05-21) — 62 releases since v0.0.2. Pro launch complete: managed Cloudflare R2 sync, Gumroad subscription product (`eideticworks.gumroad.com/l/eidetic-pro`), Team tier ($99/mo, multi-seat), web dashboard at `eidetic.works/dashboard`, AI-powered recall via `nucleus_ask` MCP tool. See `CHANGELOG.md` for per-version detail.

### Phase table (W1)

Track via `docs/IMPLEMENTATION_PLAN.md` § 11 phase sequencing.

| Phase | What | State |
|---|---|---|
| 0 | Repo + scaffold | ✅ |
| 1 | `internal/store` complete | ✅ (#1) |
| 2 | `internal/api` complete | ✅ (#2) |
| 3 | `internal/capture` 3 parsers + state + race fix | ✅ (#4) |
| 4 | Integration: mirror + concurrency tests | ✅ (rolled into #4) |
| 5 | Bench gates wired | ✅ (#5) |
| 6 | Cross-compile artifacts + install.sh + service files | ✅ (#5) |
| 7 | GitHub release + demo post | ✅ v0.0.2 → v0.0.23 released; demo at `docs/demo.md`; repo public; DO post fired Day 8 |
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
| 21 | `POST /engrams/batch` — bulk atomic insert, 32 MiB body cap | ✅ v0.0.17 (#50) |
| 22 | `GET /engrams/{id}` — point lookup by primary key, `ErrNotFound` → 404 | ✅ v0.0.18 (#52) |
| 23 | `DELETE /engrams/{id}` — surgical single-engram removal, `ErrNotFound` → 404 | ✅ v0.0.19 (#53) |
| 24 | `GET /engrams/count` — fast count with optional surface+since filters | ✅ v0.0.20 (#54) |
| 25 | `before=unix-ns` upper-bound filter on `GET /engrams` and `GET /recent` | ✅ v0.0.21 (#54) |
| 26 | `order=asc` on `GET /engrams` — oldest-first retrieval for replay consumers | ✅ v0.0.22 (#55) |
| 27 | `surface` optional on `GET /engrams` — cross-surface retrieval with full query power | ✅ v0.0.23 (#56) |

---

## Support

🐛 Bug reports + feature requests: https://github.com/eidetic-works/eidetic-issues

Private/billing questions → support@eidetic.works.

---

## License

MIT. See [LICENSE](LICENSE).
