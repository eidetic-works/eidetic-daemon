# Changelog

All notable changes to eidetic-daemon. Format inspired by [Keep a Changelog](https://keepachangelog.com/); semver via git tags.

## [Unreleased]

---

## [v0.0.47] — 2026-05-20

`/timeline` cross-tool query + `/digest` weekly recap endpoints.

### Added

- **`GET /timeline?[since=ns][&before=ns][&surfaces=a,b,c][&limit=N]`** — engrams across configured surfaces interleaved by timestamp asc. Default: all surfaces. With `surfaces=` filter, fetches per surface and merges. Answers "what was I doing on a given day across every tool at once?"
- **`GET /digest?[window=24h|7d|30d]`** — structured recap with `by_surface` counts, `top_hours`, `top_terms` (4+-char tokenizer w/ stop-words from ADR-020), 20 sampled engrams (head/middle/tail), and host-LLM rendering instructions. Designed to back a `nucleus_digest` MCP tool or weekly recap email.
- 5 new tests: cross-surface default, surfaces filter, empty store, invalid window → 400, real data → shape verified.

### Why

These are the foundations for the "weekly digest email" Pro feature (Day 30 sketch in PRO_LAUNCH.md) and the cross-tool unified query the dashboard will need next.

---

## [v0.0.46] — 2026-05-20

`eideticd -init` — first-run interactive setup wizard.

### Added

- **`-init` flag** — six-step walkthrough: confirm dataDir, detect surfaces (claude_code/cursor/cowork), register service, generate Bearer token (optional), paste Pro sync.json (optional), smoke-test /healthz.
- **`-yes` flag** — non-interactive mode with sensible defaults; for install.sh scripts and CI.
- **`udsDialer()` helper** — first in-tree consumer; lets http.Client speak to the daemon via UDS using http://localhost/ URLs.
- 2 new tests on token generation (length, uniqueness across 32 rounds).

---

## [v0.0.45] — 2026-05-20

`/ask` result cache: LRU + TTL keeps the web dashboard responsive under polling.

### Added

- **`askCache`** in `internal/api/ask_cache.go` — 64-entry LRU + 5-min TTL. Thread-safe via sync.Mutex; lazy expiry on Get.
- **Cache hit/miss header** — `X-Ask-Cache: hit` or `miss` on every `/ask` response. Useful for debugging dashboard caching behavior.
- 5 unit tests: GetMiss, PutThenGet, TTLExpiry, LRUEviction, PutRefreshesExisting.

### Why

Web dashboard at `/dashboard` and any future polling client (mobile, Slack bot) will fire the same `/ask` query repeatedly. Without caching, every poll re-runs FTS5. 5-min TTL is short enough that fresh engrams surface quickly; 64-entry cap bounds memory.

---

## [v0.0.44] — 2026-05-20

`eideticd -uninstall` — symmetric to `-install`. Lowers conversion friction.

### Added

- **`-uninstall` flag** — stops the daemon, removes the launchd plist (macOS) or systemd-user unit (Linux), then prompts to delete `<dataDir>` interactively.
- **`-purge` flag** — with `-uninstall`, skips the interactive prompt and deletes data directly (for unattended uninstall scripts).
- **Homebrew uninstall hint** — printed at the end so users on `brew install` see `brew uninstall eideticd` as the final cleanup step.
- 1 new test: `TestUninstallPurgeDeletesDataDir` — verifies the data-deletion contract under purge=true.

### Why

Pre-v0.0.44: `-install` was one command but uninstall required manual `launchctl bootout` + `rm -rf ~/.eidetic`. Friction for "let me try this" prospects. Now: `eideticd -install` to install, `eideticd -uninstall` to remove. Symmetric.

---

## [v0.0.42] — 2026-05-20

`/export` HTTP endpoint: stream every engram as newline-delimited JSON.

### Added

- **`GET /export[?surface=X][&since=ns][&before=ns]`** — paginated server-side iteration (1000-row pages), streams NDJSON one engram per line, asc timestamp order. Memory-bounded — safe against 10M-row stores.
- **Content-Type: `application/x-ndjson`** + `Content-Disposition: attachment; filename="engrams-export.ndjson"` — `curl -O` saves the file with a sensible name; `jq` consumes one line at a time.
- **Final summary line** — `{"_export_complete": true, "_count": N}` so clients can verify completion.
- 3 new tests: method allow-list, NDJSON shape + summary line, surface filter.

### Why

Right-to-export. "I own my data" was a Pro promise; this is the proof. Also enables: backup before uninstall, migrate to another store, audit trail for compliance, bulk re-ingest into a future vector store.

---

## [v0.0.41] — 2026-05-20

Cursor capture noise reduction: only capture `chatSessions/*.json`, skip per-workspace `workspace.json` stubs.

### Added

- **`SurfaceConfig.PathContains`** — optional substring filter that runs alongside the basename `Glob`. When set, paths must contain the substring to be captured.
- **Cursor surface now sets `PathContains: "chatSessions/"`** — every workspace's `workspace.json` (3-line noise: `{"folder":"file:///..."}`) is excluded. Only actual conversation history (`chatSessions/*.json`) flows through to engrams.
- 1 new test: `TestWatcherPathContainsFilter` — verifies workspace.json ignored, chatSessions captured.

### Why

Pre-v0.0.41: every Cursor workspace produced a single useless `workspace.json` engram + real session engrams from `chatSessions/`. On a developer with 50 workspaces, that's 50 noise engrams polluting FTS results.

---

## [v0.0.39] — 2026-05-20

Shared-team surface foundation: daemon sends `X-Team-ID` header for Team subscribers.

### Added

- **`Config.TeamID`** — new optional field in sync.json. Format: 4-32 lowercase alphanum/-/_, e.g. `"acme-engineering"`. Omit for solo Pro.
- **`X-Team-ID` header** — sent on every `/sync` upload + `/download` request when TeamID is configured. Worker can use this to bucket uploads under `engrams/team/<team_id>/<device_id>/...` for shared listing.
- **`--check` shows `team_id`** — when configured, prints `(shared-team mode)` annotation.
- 1 new test: `TestUploadSendsXTeamIDWhenSet` — header sent when set, omitted when not.

### Worker contract (op-assistant follow-up)

Daemon now sends `X-Team-ID`. Worker should:
1. When `/sync` arrives with X-Team-ID, store the blob at TWO keys: per-device (current) AND `engrams/team/<team_id>/<device_id>/<ts>.db` (new)
2. Add `GET /team-engrams?team_id=X` endpoint that lists across all team uploads
3. Until then, the header is silently accepted and ignored — safe to ship before Worker is updated.

---

## [v0.0.37] — 2026-05-20

Self-updating `update_available` flag via 24h GitHub releases poll.

### Added

- **`internal/versioncheck` package** — polls `github.com/eidetic-works/eidetic-daemon/releases/latest` every 24h. Caches result to `<dataDir>/version-check.json`. Network failures are silent (returns last successful cache or empty).
- **`Metrics.LatestVersion` + `Metrics.UpdateAvailable`** — new additive fields on `/metrics` JSON. Empty when no successful poll has fired (offline daemon).
- **`--stats` upgrade hint** — when a newer release is cached, prints `⬆ update available: vX.Y.Z → vA.B.C` with `brew upgrade eideticd` recipe.
- **`semverLess()`** — conservative semver comparison; returns false on parse failure so we never falsely claim an update.
- 5 unit tests on the version comparator: ordering, equal, dev fallback, pre-release stripping, parse-fail safe-default.

---

## [v0.0.38] — 2026-05-20

`/ask` HTTP endpoint: nucleus_ask semantics for non-MCP clients (web dashboard, mobile, curl).

### Added

- **`GET /ask?question=<text>[&surface=X][&limit=N]`** — extracts keywords from a natural-language question, FTS5-retrieves top-K engrams, returns `{question, fts_query, instructions, engrams}` for the caller's LLM. Mirrors the eidetic-mcp `nucleus_ask` tool exactly so MCP + HTTP /ask have identical retrieval behavior.
- **`questionToFTS()`** Go helper in `internal/api/routes.go` — stop-word stripping + OR-joined keyword list. Kept in sync with the Python `_question_to_fts` in eidetic-mcp.
- 4 new tests: missing question → 400, POST → 405, RAG response shape, no-match instructions warn against fabrication.

---

## [v0.0.36] — 2026-05-20

`--backups` flag: at-a-glance cloud backup history.

### Added

- **`SyncState.History`** — ring buffer of last 10 backups `{synced_at, key, bytes}`. Pushed after every successful upload.
- **`--backups`** — prints the history table (timestamp, R2 key, MB). Falls back to single LastSync row for pre-v0.0.36 state files.
- 1 new test: `TestUploadAppendsHistory` (ring buffer cap, newest-first ordering).

---

## [v0.0.35] — 2026-05-20

Sync.json hot-reload: Pro customers no longer need to restart the daemon after dropping their config.

### Added

- **`WatchConfig(ctx, dataDir, onChange)`** — fsnotify on dataDir for sync.json create/modify/remove. 300ms debounce.
- **Hot-reload goroutine** in main.go — swaps the Syncer pointer (RWMutex-protected) on every config change, fires an immediate upload to confirm.
- 2 new tests: create+remove flow, invalid-JSON → nil.

### Why

Before v0.0.35, Pro onboarding required: (1) drop sync.json, (2) restart daemon via `launchctl kickstart`. Now step 2 is automatic — first upload fires within ~1s of file appearance.

---

## [v0.0.34] — 2026-05-19

Sync health check: `eideticd --check` validates sync.json and tests Worker connectivity.

### Added

- **`--check` flag** — prints sync config (worker_url, device_id, interval), pings the Worker `/healthz` endpoint with the configured api_key, and reports last sync state from sync-state.json. Exits 0 if healthy, 1 if config is missing or Worker is unreachable.
- **`CheckConfig(cfg, dataDir)`** — standalone function (no Syncer, no store required); used by `--check` before `store.Open`.
- 3 new tests: `TestCheckConfig_NilConfig`, `TestCheckConfig_WorkerOK`, `TestCheckConfig_WorkerUnauth` — 18/18 pass.


---

## [v0.0.33] — 2026-05-19

Sync-state persistence: `--stats` now shows last cloud backup time across daemon restarts.

### Added

- **`SyncState` struct** — `{last_sync, last_key, last_bytes}` persisted to `<dataDir>/sync-state.json` after every successful upload. Survives daemon restarts.
- **`LoadSyncState(dataDir)`** — reads sync-state.json; returns zero-value (not error) if file does not exist yet.
- **`--stats` cloud sync block** — when sync-state.json exists, `eideticd --stats` prints last sync time (local), last R2 key, and last upload size.
- **`saveSyncState`** — atomic write (tmp → rename, 0600); called by `upload()` on 201 OK; never fails the upload.
- 2 new tests: `TestLoadSyncState_Missing` (missing file → zero-value), `TestUploadWritesSyncState` (upload → state file written with correct fields). 14/14 pass.

---

## [v0.0.32] — 2026-05-19

Cloud restore: download the latest R2 backup in one command.

### Added

- **`--restore` flag** — downloads the most recent R2 backup for the configured device and atomically replaces the local `engrams.db`. The previous file is saved to `engrams.db.bak`. Runs before `store.Open` so there is no write-lock conflict. Usage: `eideticd --restore`.
- **Worker `GET /download` endpoint** — auth-gated (KV + fallback), streams the latest R2 object for the device with `X-Backup-Key` and `X-Uploaded-At` headers. Requires `EIDETIC_SYNC_BUCKET` R2 binding.
- 3 new tests in `internal/sync/syncer_test.go`: happy path (download → atomic replace → backup preserved), nil-config error, 404 from Worker.

---

## [v0.0.31] — 2026-05-19

Bridge dual-listener mode for Cloudflare tunnel integration.

### Added

- **`-bridge <addr>` flag** — starts a second TCP listener alongside the primary UDS server. Both servers share the same `*store.Store` (one SQLite write path, no lock contention). Intended for Cloudflare tunnel (`cloudflared`) exposure so remote clients (iPhone app, Claude.ai web, ChatGPT web browser context) can reach the local engram store without requiring UDS access.
- **Bridge auth always-on** — the bridge listener generates its own Bearer token at daemon startup (independent of the primary `EIDETIC_AUTH` flag). Token written to `~/.eidetic/bridge-token` (0600). Rotates every restart.
- **CORS middleware** (`api.Options.CORS bool`) — when `CORS: true`, every response from that server instance includes `Access-Control-Allow-Origin: *` + `Allow-Methods: GET, POST, OPTIONS` + `Allow-Headers: Authorization, Content-Type`. OPTIONS preflight returns 204. Only set on bridge listener; never on UDS listener.
- **`api.Options.CORS`** — new field in `api.Options`. If false (default, all existing callers), no CORS headers are added. Zero-config backward compat.

### Architecture note

The bridge feature is built and the `-bridge` flag exists, but the launchd plist (`~/Library/LaunchAgents/works.eidetic.eideticd.plist`) does NOT activate it by default. Port assignment must be coordinated with other services — in particular, port 8420 is owned by the XTTS voice daemon (Sovereign tunnel). Use `:8421` or higher for eidetic bridge if activating.

---

## [v0.0.30] — 2026-05-19

Windows capture path fix.

### Fixed

- **Windows Claude Code path** — `DefaultSurfaces()` was using `~\.claude\projects` on Windows, which is wrong. Claude Code (Electron) stores session JSONLs at `%APPDATA%\Claude\projects`. New `paths_windows.go` (build tag `windows`) reads `APPDATA` env var with home-dir fallback.
- **Windows Cursor path** — `paths_other.go` (previously `!darwin`, so it covered Windows) returned `~\.config\Cursor\...` which is the Linux path. Windows Cursor uses `%APPDATA%\Cursor\User\workspaceStorage`. Fixed in `paths_windows.go`.
- **Build tag narrowed** — `paths_other.go` tag changed from `!darwin` to `linux`. `claudeRoot(home)` extracted to all three platform files so `DefaultSurfaces()` is path-agnostic.

---

## [v0.0.29] — 2026-05-19

`-install` flag for one-command service registration.

### Added

- **`eideticd -install`** — registers eideticd as a login-time service: launchd `works.eidetic.eideticd` on macOS, systemd-user `eideticd.service` on Linux. Binary path resolved via `os.Executable()` + `EvalSymlinks` so Homebrew shims resolve correctly. Live-tested: daemon starts and passes `/healthz` in under 1 second. Fixes broken Homebrew caveats that referenced this flag before it existed.

---

## [v0.0.28] — 2026-05-19

linux-arm64 platform + FTS5 search snippets + eidetic-mcp 0.0.4.

### Added

- **linux-arm64 binary** — added to release matrix (`make build-linux-arm64`, `verify-cross-compile.sh`, `release.yml`). Covers AWS Graviton, Oracle Cloud free tier, Raspberry Pi 4/5.
- **FTS5 search snippets** — `search_engrams` results now include a `snippet` field: ~200-char context window around the match keyword (via `snippet(engrams_fts, 0, '', '', '...', 20)`). Previously returned 10KB raw JSON blobs; now MCP clients can read relevant context without parsing the full payload.
- **`eidetic-mcp 0.0.4`** — Python `Engram` dataclass gains `snippet: str = ""` field; `_parse_engram` picks it up from search results; `search_engrams` tool description updated to document the snippet field.

---

## [v0.0.27] — 2026-05-19

Windows support: TCP mode, PowerShell installer, eidetic-mcp 0.0.2.

### Added

- **`scripts/install.ps1`** — Windows one-line installer (`irm https://eidetic.works/install.ps1 | iex`). No admin required. Downloads `eideticd-windows-amd64.exe` from GitHub release, installs to `%LOCALAPPDATA%\EideticWorks\bin\`, registers HKCU Run key for login-startup, starts daemon in TCP mode.
- **`eideticd-windows-amd64.exe`** — Windows binary added to release matrix (was already cross-compiling cleanly via `make build-all`; now explicitly in release artifacts).
- **`eidetic-mcp 0.0.2`** — MCP bridge auto-selects TCP transport on `sys.platform == "win32"`. Previous version fell through to the Darwin UDS path (`/tmp/eidetic-daemon.sock`) and silently failed on Windows.
- **`docs/spikes/tauri-cross-compile-spike.md`** — Tauri spike verdict: Go cross-compile already covers darwin/linux/windows cleanly. Tauri adds 5× binary size + webkit2gtk system dep with no GUI gain for a daemon. Deferred to >100 user threshold.
- **`docs/strategy/`** — distribution package strategy and nucleus-mcp vs eidetic-mcp brainstorm documented.

### Fixed

- Windows MCP bridge: `DaemonClient` now auto-enables TCP mode on Windows without requiring `EIDETIC_TCP=1` env var.

---

## [v0.0.26] — 2026-05-18

`--stats` flag for at-a-glance database summary.

### Added

- **`eideticd --stats`** — prints engram count, per-surface breakdown, oldest/newest timestamps, database file size, and P95 retrieval latency; then exits. Works whether the daemon is running or not (opens the SQLite file directly in read-only mode). Useful for sharing your own numbers ("run `eideticd --stats` to see yours").
- **`store.Stats(ctx)`** — new method returning a `StatsSnapshot` struct (total, BySurface map, OldestNs, NewestNs, DBBytes, P95LatNs). P95 is measured via 20 timed `GetByID` probes across the rowid range.
- **2 new tests** — `TestStatsEmptyStore` + `TestStatsWithEngrams` (`internal/store/stats_test.go`).

---

## [v0.0.25] — 2026-05-18

Compliance daemon, PyPI publish, and scheduler files.

### Added

- **`cmd/eideticd-compliance`** — compliance daemon for per-surface data retention. Reads `~/.eidetic/retention-policy.json` (or `$EIDETIC_DATA_DIR/retention-policy.json`), purges rows older than configured day thresholds per surface, appends audit lines to `~/.eidetic/compliance.log`. `--dry-run` flag reports without deleting. Designed to run via cron / launchd timer / systemd timer — runs one pass and exits. Ships as a separate binary (`eideticd-compliance`). **Zero impact on daemon uptime** — operates against the same `engrams.db` via the existing writer pool.
- **`scripts/retention-policy.example.json`** — example policy file (claude_code: 30d, cursor: 90d, cowork: 365d). Copy to `~/.eidetic/retention-policy.json` to enable.
- **`make build-compliance`** — new Makefile target builds `bin/eideticd-compliance`.
- **7 tests** (`cmd/eideticd-compliance/main_test.go`): policy load (valid/not-exist/invalid-JSON), data-path resolution (env override/db override), retention integration test (3 old rows deleted, 2 fresh rows kept), policy roundtrip.
- **`eidetic-mcp` published to PyPI** — `pip install eidetic-mcp` now works. `docs/mcp-integration.md` updated to use PyPI install path. `bridge/python` is still the source of truth; PyPI follows each release.
- **Compliance scheduler files** — `scripts/launchd-compliance.plist` (macOS, fires at 03:00 daily) and `scripts/systemd-compliance.{service,timer}` (Linux, 03:00 daily with `Persistent=true` catch-up). Install instructions in each file's header.

---

## [v0.0.24] — 2026-05-18

Cloud sync, Windows CI gate, MCP integration guide, and pre-public docs cleanup.

### Added

- **`internal/sync` package** — opt-in Cloudflare R2 file-level sync (ADR-019). Zero runtime cost when sync.json absent (disabled by default). Config: `~/.eidetic/sync.json` with `{worker_url, api_key, device_id, sync_interval}`. `Syncer.TriggerIfDue()` uploads when `sync_interval` elapsed + idle (no new rows for one 60s poll). `Syncer.SyncNow()` uploads immediately.
- **`bridge/cloudflare/worker.js`** — Cloudflare Worker receiving upload POSTs from daemon. Auth: `Authorization: Bearer <EIDETIC_API_KEY>` + `X-Device-ID` header. Stores at R2 key `engrams/{device_id}/engrams-{ts}.db`. Auto-prunes: keeps 5 most recent backups per device. Endpoints: `POST /sync`, `GET /latest`, `GET /healthz`. 500 MB guard matches Worker R2 put limit.
- **`bridge/cloudflare/wrangler.toml`** — Wrangler deploy config. R2 bucket binding `EIDETIC_SYNC_BUCKET`. API key is a Wrangler secret (`wrangler secret put EIDETIC_API_KEY`).
- **`eideticd --sync-now` flag** — upload engrams.db immediately and exit. Useful for manual one-shot backup or post-export before machine wipe.
- **60s sync poll goroutine in `cmd/eideticd/main.go`** — calls `syncer.TriggerIfDue()` every 60s. No-ops (nil guard) when sync.json absent.
- **10 unit tests** (`internal/sync/syncer_test.go`): config load (missing/valid/bad-fields/malformed-json), nil-Syncer nil-safety (TriggerIfDue+SyncNow), HTTP contract (correct auth headers/body/Content-Type), non-201 worker response surfaces as error, missing-DB surfaces as error.
- **Windows CI smoke job** (`.github/workflows/ci.yml`) — `windows-latest` runner builds `eideticd.exe` natively (pure-Go, no cross-compile toolchain), validates `-version` and `-h` flags. Closes ADR-017's deferred Windows runtime gate without Wine dependency.
- **`docs/mcp-integration.md`** — user-facing MCP setup guide for Claude Code (`claude mcp add`), Cursor, Cline, and any MCP stdio client. Covers all 12 MCP tools, env overrides, quick-start prompts, chunked-record reassembly, and troubleshooting.

### Decision recorded

- **ADR-019 (2026-05-18)** in `docs/DECISIONS.md` — R2 file-level sync ($0.38/mo) chosen over D1 row-level sync ($5/mo paid floor). R2 stays in free tier at current 25MB dataset. See ADR for full cost table and architectural rationale.

---

## [v0.0.23] — 2026-05-18

`surface` is now optional on `GET /engrams` — omitting it returns engrams across **all** surfaces, ordered by timestamp (respects `order`, `since`, `before`, `limit`). This gives callers the full retrieval query power of `/engrams` (paging, ordering, time windows) on cross-surface data, which `/recent` did not expose. **Zero breaking change — all existing surface-scoped calls are unaffected.**

### Added

- **`GET /engrams` with optional `surface=`** (`internal/store/store.go`, `internal/api/routes.go`):
  - `store.Retrieve` no longer errors on `surface=""`. Uses a dynamic WHERE-clause builder (`strings.Join`) instead of the previous 4-branch switch, eliminating the 8-branch explosion that optional surface would have required. `surface=""` drops the `surface = ?` predicate; all other filters (since, before, order) still apply.
  - `handleEngramsGET` removes the `surface required` → 400 guard; empty surface now passes through.
  - Cross-surface queries use `idx_ts ON engrams(ts DESC)` added to `schema.sql` to avoid full-table scans.
  - **4 store tests** (`internal/store/store_test.go`): `TestRetrieveAllSurfaces`, `TestRetrieveAllSurfacesRespectsSince`, `TestRetrieveAllSurfacesDescOrder`, `TestRetrieveNoFiltersReturnsAll`.
  - **3 API tests** (`internal/api/server_test.go`): `TestGetEngramsNoSurface`, `TestGetEngramsNoSurfaceWithFilters`, `TestGetEngramsNoSurfaceAscOrder`.

- **MCP bridge — `surface` now optional on `query_engrams`** (`bridge/python/eidetic_mcp/client.py`, `server.py`):
  - `DaemonClient.query_engrams(surface="", …)` — `surface` defaults to `""`. When empty, the `surface=` query param is omitted from the request.
  - `query_engrams` MCP tool inputSchema: `surface` moved out of `required`; description updated.
  - **3 bridge tests** (`bridge/python/tests/test_client.py`): no-surface default, explicit `surface=""`, no-surface with filters.

### Changed (non-breaking)

- `store.Retrieve` internal implementation: 4-branch `switch` on since/before combinations replaced by dynamic WHERE-clause builder (`[]string clauses + strings.Join`). Behaviour for all non-empty-surface call sites is identical; refactor is covered by existing tests.
- `schema.sql` gains `idx_ts ON engrams(ts DESC)` for cross-surface ts-ordered queries.
- `TestGET_MissingSurfaceReturns400` renamed to `TestGET_MissingSurfaceReturns200CrossSurface` and expectation flipped to 200.
- `test_client_query_engrams_requires_surface` renamed to `test_client_query_engrams_empty_surface_is_valid` and expectation updated.

### Reference

PR #56 · tag v0.0.23

---

## [v0.0.22] — 2026-05-18

`order=asc` on `GET /engrams` — callers can now retrieve engrams oldest-first by appending `?order=asc`. Enables replay consumers, incremental import pipelines, and chronological feed UIs without a post-sort. Default (`order=desc` or omitted) is unchanged. **Zero breaking change.**

### Added

- **`GET /engrams?…&order=asc`** (`internal/api/routes.go`, `internal/store/store.go`):
  - `store.Retrieve` gains `asc bool` as the last parameter. `false` (default) = `ORDER BY ts DESC`; `true` = `ORDER BY ts ASC`.
  - `handleEngramsGET` parses `q.Get("order") == "asc"` and passes through. Any value other than `"asc"` retains DESC.
  - **1 store test** (`internal/store/store_test.go`): `TestRetrieveAscOrder` — inserts 5 rows, asserts desc[0]>desc[-1] and asc[0]<asc[-1], verifies same IDs reversed.
  - **2 API tests** (`internal/api/server_test.go`): `TestGetEngramsOrderAsc`, `TestGetEngramsDefaultOrderDesc`.

- **MCP bridge — `asc` kwarg on `query_engrams`** (`bridge/python/eidetic_mcp/client.py`, `server.py`):
  - `DaemonClient.query_engrams(…, asc=False)` — adds `order=asc` to query params when `asc=True`.
  - `query_engrams` MCP tool inputSchema gains optional `asc` boolean.
  - **2 bridge tests** (`bridge/python/tests/test_client.py`): `asc=True` round-trips, `asc=False` default round-trips.

### Changed (non-breaking)

- `store.Retrieve` signature gains `asc bool` as the new last arg. All internal callers updated to pass `false`.

### Reference

PR #55 · tag v0.0.22

---

## [v0.0.21] — 2026-05-18

`before` upper-bound filter on `GET /engrams` and `GET /recent` — callers can now scope queries to a time window (`since=<lower>&before=<upper>`) or just a ceiling (`before=<cutoff>`). Enables polling diffs, sliding windows, and cursor-based pagination without fetching all rows. **Zero breaking change — all existing calls omit `before` and see unchanged behaviour.**

### Added

- **`GET /engrams?…&before=unix-ns`** (`internal/api/routes.go`, `internal/store/store.go`):
  - `store.Retrieve` now accepts `before int64` between `since` and `limit` (4-branch switch: neither, since-only, before-only, both).
  - `handleEngramsGET` parses `before` from query params; zero/absent = no upper bound.
  - **2 store tests** (`internal/store/store_test.go`): `TestRetrieveBeforeFilter`, `TestRetrieveSinceAndBefore`.
  - **2 API tests** (`internal/api/server_test.go`): `TestGetEngramsBeforeFilter`, `TestGetEngramsSinceAndBefore`.

- **`GET /recent?…&before=unix-ns`** (`internal/api/routes.go`, `internal/store/store.go`):
  - `store.Recent` now accepts `before int64` between `since` and `limit`; same 4-branch switch.
  - `handleRecent` parses `before` from query params; zero/absent = no upper bound.
  - **2 store tests** (`internal/store/recent_test.go`): `TestRecentBeforeFilter`, `TestRecentSinceAndBefore`.
  - **1 API test** (`internal/api/server_test.go`): `TestGetRecentBeforeFilter`.

- **MCP bridge — `before` kwarg on `query_engrams` and `recent_engrams`** (`bridge/python/eidetic_mcp/client.py`, `server.py`):
  - `DaemonClient.query_engrams(…, before=0)` — forwards `before` to `GET /engrams` when non-zero.
  - `DaemonClient.recent_engrams(…, before=0, …)` — forwards `before` to `GET /recent` when non-zero.
  - Tool inputSchemas updated to expose `before` as optional integer.
  - **4 bridge tests** (`bridge/python/tests/test_client.py`): query with before, query with since+before, recent with before, recent with since+before.

### Changed (non-breaking)

- `store.Retrieve` and `store.Recent` signatures gain `before int64` as a new positional arg (between `since` and `limit`). All internal callers (bench, capture, api tests) updated to pass `0`.

### Reference

PR #54 · tag v0.0.21

---

## [v0.0.20] — 2026-05-18

Count endpoint — `GET /engrams/count` returns `{"count": N}` for fast badge counts and monitoring without fetching rows. Surface and since filters match the `/engrams` retrieval semantics. **Zero breaking change.**

### Added

- **`GET /engrams/count?[surface=X][&since=unix-ns]`** (`internal/api/routes.go`, `internal/api/server.go`):
  - Returns `{"count": N}`. surface is optional (empty = all surfaces). since (optional) filters to engrams with ts > since.
  - Registered as a literal pattern before `/engrams/{id}` (Go 1.22+ routing: literal always wins over wildcard).
  - **`store.CountEngrams(ctx, surface, since)`** — single `SELECT COUNT(*)` with 0-2 WHERE clauses; reader-pool query, does not block writers.
  - **5 store tests** (`internal/store/count_test.go`): empty, all surfaces, by surface, by since, surface+since combined.
  - **4 API tests** (`internal/api/server_test.go`): empty → 0, all surfaces → N, by surface → N, 405 on POST.

- **MCP bridge — `count_engrams` tool** (`bridge/python/eidetic_mcp/server.py`, `client.py`):
  - `DaemonClient.count_engrams(surface="", since=0)` → `int`. Raises DaemonError on transport failure.
  - `count_engrams` MCP tool with optional surface + since in inputSchema.
  - **3 bridge tests** (`bridge/python/tests/test_client.py`): no args, with surface, with since.

### Reference

PR #53 · tag v0.0.20

---

## [v0.0.19] — 2026-05-18

Surgical single-engram removal — `DELETE /engrams/{id}` completes the point-CRUD surface (GET + DELETE by primary key). The existing surface-level `DELETE /engrams?surface=X` is unchanged. **Zero breaking change.**

### Added

- **`DELETE /engrams/{id}`** (`internal/api/routes.go`):
  - `handleEngramsByID` now dispatches both GET and DELETE on `/engrams/{id}` (renamed from `handleEngramsGetByID`; all existing GET tests continue to pass).
  - Returns `200 + {"deleted": 1}` on success; 404 when id not found; 400 on non-integer or non-positive id; 405 on unsupported methods (e.g. PUT).
  - **`store.DeleteByID(ctx, id)`** — single `DELETE FROM engrams WHERE id = ?`; returns `ErrNotFound` when `RowsAffected() == 0`. Runs on writer pool for WAL consistency.
  - **3 store tests** (`internal/store/delete_by_id_test.go`): removes + confirms 404, 999999 → ErrNotFound, 0 → ErrNotFound.
  - **4 API tests** (`internal/api/server_test.go`): 200+deleted=1, GET after DELETE → 404, 404 on unknown id, 400 on non-integer.

- **MCP bridge — `delete_engram_by_id` tool** (`bridge/python/eidetic_mcp/server.py`, `client.py`):
  - `DaemonClient.delete_engram_by_id(id)` → `bool` (True on success). Validates id > 0 client-side; raises ValueError on invalid, DaemonError on 404/transport failure.
  - `delete_engram_by_id` MCP tool with `id` integer in inputSchema.
  - **4 bridge tests** (`bridge/python/tests/test_client.py`): returns True, 404 raises DaemonError, zero raises ValueError, negative raises ValueError.

### Reference

PR #52 · tag v0.0.19

---

## [v0.0.18] — 2026-05-18

Point-lookup by primary key — `GET /engrams/{id}` lets callers fetch a single engram when they already know its ID (e.g. after a `POST /engrams` insert). Completes the basic CRUD surface. **Zero breaking change.**

### Added

- **`GET /engrams/{id}`** (`internal/api/routes.go`, `internal/api/server.go`):
  - Path parameter `{id}` parsed via Go 1.22 `r.PathValue("id")`.
  - Returns `200 + Engram JSON` on success; 400 on non-integer or non-positive id; 404 when no row matches; 405 on non-GET.
  - **`store.ErrNotFound`** sentinel: `GetByID` returns `ErrNotFound` wrapping `sql.ErrNoRows`; HTTP handler maps to 404.
  - **3 store tests** (`internal/store/get_by_id_test.go`): returns engram, 999999 → ErrNotFound, 0 → ErrNotFound.
  - **5 API tests** (`internal/api/server_test.go`): 200+engram, 404 on unknown id, 400 on non-integer, 400 on zero, 405 on DELETE.

- **MCP bridge — `get_engram_by_id` tool** (`bridge/python/eidetic_mcp/server.py`, `client.py`):
  - `DaemonClient.get_engram_by_id(id)` → `Engram`. Validates id > 0 client-side; raises ValueError on invalid, DaemonError on 404/transport failure.
  - `get_engram_by_id` MCP tool with `id` integer in inputSchema.
  - **4 bridge tests** (`bridge/python/tests/test_client.py`): returns Engram, 404 raises DaemonError, zero raises ValueError, negative raises ValueError.

### Reference

PR #52 · tag v0.0.18

---

## [v0.0.17] — 2026-05-18

Bulk insert via `POST /engrams/batch` — one round-trip for N engrams in a single transaction. Complements the single-insert `POST /engrams` from v0.0.16. **Zero breaking change.**

### Added

- **`POST /engrams/batch`** (`internal/api/routes.go`, `internal/api/server.go`):
  - Accepts a JSON array of engram objects: `[{"surface":"...","payload":"...","ts":...,"meta":"..."}, ...]`.
  - All items inserted via `store.InsertBatch` in a single transaction — any validation failure rolls back the entire batch.
  - `ts` defaults to `time.Now().UnixNano()` per-item (same `now` value for all items in the batch).
  - Body capped at 32 MiB via `http.MaxBytesReader`. Empty array → `201 + {"inserted": 0}` (no-op, not error).
  - Returns `201 Created + {"inserted": N}` on success; 400 on validation failure or invalid JSON; 405 on non-POST.
  - **6 API tests** (`internal/api/server_test.go`): 201+count, empty-array no-op, all-retrievable, auto-ts, missing-surface 400, 405 on GET.

- **MCP bridge — `insert_engrams_batch` tool** (`bridge/python/eidetic_mcp/server.py`, `client.py`):
  - `DaemonClient.insert_engrams_batch(items)` → `int` (count). Client-side validates surface+payload before sending.
  - `insert_engrams_batch` MCP tool with `items` array in inputSchema.
  - **4 bridge tests** (`bridge/python/tests/test_client.py`): returns count, optional fields, empty raises ValueError, missing surface raises ValueError.

### Reference

PR #50 · tag v0.0.17

---

## [v0.0.16] — 2026-05-18

API-side engram insertion — `POST /engrams` turns the daemon from a read-only query layer into a writable store reachable from any caller (mobile, webhooks, relay pipelines, manual annotations). **Zero breaking change** to any prior caller; all existing GET/DELETE semantics unchanged.

### Added

- **`POST /engrams`** (`internal/api/routes.go`, `internal/store/store.go`):
  - Accepts `{"surface":"...","payload":"...","ts":unix-ns,"meta":"..."}` JSON body.
  - `surface` and `payload` required; `ts` defaults to `time.Now().UnixNano()` server-side when 0 or omitted; `meta` optional.
  - Returns `201 Created` + `{"id": N}` on success; 400 on missing fields / invalid JSON / payload > MaxPayloadBytes.
  - Body capped at `MaxPayloadBytes + 4096` bytes via `http.MaxBytesReader` before JSON decode.
  - Dispatched via the existing `handleEngrams` switch (GET/POST/DELETE).
  - **`store.ErrInvalidEngram`** sentinel: `Insert`/`InsertBatch` now wrap `validateEngram` failures with `ErrInvalidEngram` so HTTP handlers map them to 400, not 500. `errors.Is(err, store.ErrInvalidEngram)` is the correct check.
  - **5 store tests** (`internal/store/insert_test.go`): returns ID, surface required → ErrInvalidEngram, zero TS → ErrInvalidEngram, empty payload → ErrInvalidEngram, is retrievable after insert.
  - **6 API tests** (`internal/api/server_test.go`): 201 + ID, auto-timestamp when ts omitted, 400 on missing surface, 400 on missing payload, 400 on invalid JSON, inserted row retrievable via GET.

- **MCP bridge — `insert_engram` tool** (`bridge/python/eidetic_mcp/server.py`, `client.py`):
  - `DaemonClient._post_json(path, payload)` — POST transport with `Content-Type: application/json`, handles 201.
  - `DaemonClient.insert_engram(surface, payload, ts=0, meta="")` → `int` (assigned ID).
  - `insert_engram` MCP tool with `surface` (required), `payload` (required), `ts`, `meta` in inputSchema.
  - **4 bridge tests** (`bridge/python/tests/test_client.py`): returns id, ts+meta accepted, surface required → ValueError, payload required → ValueError.

### Reference

PR #49 · tag v0.0.16

---

## [v0.0.15] — 2026-05-18

Cross-surface recent engrams — answers "what happened lately?" without a keyword or surface filter. Complements `/search` (relevance-ranked by keyword) and `/engrams` (surface-scoped). **Zero breaking change** to any prior caller.

### Added

- **`GET /recent?since=unix-ns&limit=N`** (`internal/api/routes.go`, `internal/store/store.go`):
  - Returns up to `limit` engrams across **all surfaces**, ordered newest-first (`ts DESC`).
  - Optional `since` (Unix nanoseconds, exclusive lower bound); `0` or omitted = no lower bound.
  - `limit`: 1-500, default 50 (same clamp as `GET /engrams`).
  - Returns same `[]Engram` JSON shape as all other retrieval endpoints.
  - `Store.Recent(ctx, since, limit)` in `internal/store/store.go`.
  - **6 store tests** (`internal/store/recent_test.go`): default limit, limit clamp, since filter, cross-surface, empty DB, limit > 500 clamped.
  - **4 API tests** (`internal/api/server_test.go`): 405 on non-GET, newest-first ordering, since filter, empty-DB empty array.

- **MCP bridge — `recent_engrams` tool** (`bridge/python/eidetic_mcp/server.py`, `client.py`):
  - `DaemonClient.recent_engrams(since=0, limit=50)` — `GET /recent` exposed as a bridge method.
  - `recent_engrams` MCP tool with `since` + `limit` params in inputSchema.
  - **3 bridge tests** (`bridge/python/tests/test_client.py`): happy path, since+limit, default-params round-trip.

### Reference

PR #48 · tag v0.0.15

---

## [v0.0.14] — 2026-05-18

Full-text search over engram payloads — the first endpoint that answers "what did I say about X?" rather than scrolling reverse-chronological. **Zero breaking change** to any prior caller.

### Added

- **`GET /search?q=...&surface=X&limit=N`** (`internal/api/routes.go`, `internal/store/store.go`):
  - FTS5 full-text search over `engrams.payload`. Results ordered by relevance rank (best match first).
  - `q` is an FTS5 match expression: bare keywords, `"phrase queries"`, `OR`/`AND`/`NOT` boolean operators.
  - Optional `surface` filter restricts to one surface; optional `limit` (default 50, cap 500).
  - Returns same `[]Engram` JSON shape as `GET /engrams` for client compatibility.
  - `Store.Search(ctx, q, surface, limit)` + `store.ErrEmptyQuery` sentinel (callers get 400, not 500).
  - `backfillFTS()` at `Open()`: detects empty FTS index on existing databases and bulk-populates from `engrams` in one `INSERT … SELECT`. `AFTER INSERT` / `AFTER DELETE` triggers keep the index live after backfill.
  - **7 store tests** (`internal/store/search_test.go`): empty q, keyword, phrase, surface filter, limit clamp, no-results, backfill-on-reopen.
  - **5 API tests** (`internal/api/server_test.go`): 400 on missing q, 405, returns matches, surface filter, empty array on no-match.

- **MCP bridge — `search_engrams` tool** (`bridge/python/eidetic_mcp/server.py`, `client.py`):
  - `DaemonClient.search_engrams(q, surface="", limit=50)` — `GET /search` exposed as a bridge method.
  - `search_engrams` MCP tool with FTS5 expression examples in description (so AI clients know the syntax).
  - **3 bridge tests** (`bridge/python/tests/test_client.py`): happy path, surface+limit, empty-q raises ValueError.

### Reference

- PR #47

---

## [v0.0.13] — 2026-05-18

Engram purge endpoint + surface listing + uninstall script. Three independent
capabilities that close the full lifecycle: query (v0.0.2), observe (v0.0.7),
authenticate (v0.0.9), and now **purge + discover + clean-uninstall**. All
additive — **zero breaking change** to any prior caller.

### Added

- **`DELETE /engrams?surface=X[&before=unix-ns]`** (`internal/api/routes.go`, `internal/store/store.go`):
  - Purges all engrams for a surface when `before` is absent (or 0).
  - Purges only engrams with `ts < before` when `before` is set — age-gated cleanup without touching recent data.
  - Returns `{"deleted": N}` (rows affected from the writer pool).
  - Auth-gated when caller auth is on (v0.0.9+ Bearer token required, same as `/engrams` GET).
  - `Store.Purge(ctx, surface, before)` in the store layer; writer-pool exec; two-branch query path mirrors `Retrieve`'s `since`-branch design.
  - **5 new store tests** (`internal/store/store_test.go`): `TestStorePurgeAll`, `TestStorePurgeBefore`, `TestStorePurgeEmptySurface`, `TestStorePurgeNonExistentSurface` + round-trip.
  - **5 new API tests** (`internal/api/server_test.go`): DELETE happy path, `before=` cutoff, missing surface 400, wrong method (PATCH) 405, auth-protected 401.

- **`GET /surfaces`** (`internal/api/routes.go`):
  - Returns a `map[string]int64` of surface name → engram count — same data already computed by `/metrics` `engram_by_surface`, now exposed as a standalone route.
  - Empty store → `{}` (not null).
  - Auth-gated when caller auth is on.
  - **3 new API tests**: `TestSurfacesEmpty`, `TestSurfacesReturnsCounts`, `TestSurfacesMethodNotAllowed`.

- **`scripts/uninstall.sh`**: curl-pipeable removal companion to `scripts/install.sh`.
  - Stops launchd (macOS) or systemd-user (Linux), kills stray `eideticd` procs via `pkill -x`.
  - Removes UDS socket (`/tmp/eidetic-daemon.sock`) and binary (`${PREFIX}/bin/eideticd`).
  - **Engram data retained by default** — `~/.eidetic/` untouched unless `--purge-data` is passed.
  - Flags: `--purge-data` (wipes `$EIDETIC_DATA_DIR` or `~/.eidetic/`), `--prefix=PATH` (default `/usr/local`).
  - Usage: `curl -fsSL https://eidetic.works/uninstall.sh | sh`

- **MCP bridge — two new tools** (`bridge/python/eidetic_mcp/server.py`, `client.py`):
  - `list_surfaces()` — `GET /surfaces` exposed as an MCP tool; returns surface → count map.
  - `purge_engrams(surface, before=0)` — `DELETE /engrams` exposed as an MCP tool; returns `{"deleted": N}`.
  - `DaemonClient._request_json(method, path)` consolidates GET + DELETE transport with auth-header wiring.
  - **5 new bridge tests** (`bridge/python/tests/test_client.py`): `test_client_surfaces_against_fake_server`, `test_client_surfaces_unreachable_raises`, `test_client_purge_engrams_against_fake_server`, `test_client_purge_engrams_with_before`, `test_client_purge_engrams_requires_surface`.

### Reference

- PRs #38 (`DELETE /engrams`), #39 (`GET /surfaces`), #40 (`uninstall.sh`), #42 (bridge tools)
- Compounds v0.0.12 latency tracker → v0.0.13 lifecycle completion.

---

## [v0.0.11] — 2026-05-15

OpenMetrics 1.0.0 exposition format on `/metrics` via Accept-header negotiation. IETF successor to Prometheus exposition (CNCF-graduated). Compounds on v0.0.10 — additive, **zero breaking change to v0.0.10 callers** (Prometheus + JSON paths unchanged).

### Added
- **OpenMetrics content negotiation** (`internal/api/metrics.go`):
  - `Accept: application/openmetrics-text` → OpenMetrics format (with optional `version=1.0.0` parameter)
  - **OpenMetrics takes precedence over Prometheus** when both clauses present in Accept (matches real Prometheus scraper behavior — they prefer OpenMetrics if the server supports it; the dual-Accept header `application/openmetrics-text;version=1.0.0,text/plain;version=0.0.4;q=0.5,*/*;q=0.1` now returns OpenMetrics, not Prometheus)
  - `Accept: text/plain` (alone, no openmetrics-text clause) → Prometheus exposition (v0.0.10 contract preserved)
  - Default Accept → JSON (v0.0.7 contract preserved)
- **`Metrics.MarshalOpenMetrics()`** — renders 6 metric families per OpenMetrics 1.0.0 spec compliance:
  - `# UNIT` comments where applicable (`seconds`, `bytes`)
  - Counter naming: declared TYPE name `eidetic_capture_skipped` (no `_total` suffix per spec § Counter Metric type), value line `eidetic_capture_skipped_total <n>` (suffix appended to value line only)
  - Gauges per spec (no `_total` on gauge value lines — `eidetic_engrams_by_surface` not `eidetic_engrams_by_surface_total`)
  - Mandatory `# EOF` trailer
  - Surfaces sorted alphabetically; empty per-surface map suppresses block (same as Prometheus path)
- **4 new tests** in `internal/api/metrics_test.go` (all green under `-race`):
  - `TestMetricsOpenMetricsFormat` — full schema verification (UNIT comments, EOF trailer, counter naming convention)
  - `TestMetricsOpenMetricsTakesPrecedenceOverPrometheus` — scraper-style multi-type Accept → OpenMetrics
  - `TestMetricsPlainTextStillReturnsPrometheus` — text/plain alone (no openmetrics) still Prometheus; regression gate
  - `TestMarshalOpenMetricsCounterNaming` — spec compliance (declared name has no `_total`; value line has it; never both)
  - `TestMetricsAcceptMultipleWithoutOpenMetricsHonorsTextPlain` — renamed v0.0.10 test for clarity (legacy scraper behavior)
- **Live-fire validation**: `eideticd -version` → `eideticd v0.0.11-rc1`. 4 Accept variants verified against real binary: explicit `application/openmetrics-text` returns full OpenMetrics with UNIT comments + EOF; scraper-style multi-clause Accept returns OpenMetrics (precedence honored); `text/plain` alone returns Prometheus (no EOF — regression-clean); default Accept returns JSON. v0.0.6 shutdown drain + v0.0.9 caller auth + v0.0.10 Prometheus path all still clean.

### Changed
- `TestMetricsAcceptMultipleHonorsTextPlain` (v0.0.10) → renamed `TestMetricsAcceptMultipleWithoutOpenMetricsHonorsTextPlain` and updated to send Accept WITHOUT openmetrics-text clause. The old assertion (scraper-style Accept → text/plain) is no longer correct as a v0.0.11 contract — that scenario now correctly returns OpenMetrics. Behavior change is intentional + spec-aligned.

### Reference
- PR #27 (this release commit folded in)
- W2+ list "OpenMetrics format on /metrics" — promoted into v0.0.11
- OpenMetrics 1.0.0 spec: https://github.com/OpenObservability/OpenMetrics/blob/main/specification/OpenMetrics.md
- Compounds v0.0.10 Accept negotiation; same MarshalX pattern; OpenMetrics handler shares writeGauge helper. Live-fire 4 Accept variants against the real binary before any test was codified.

---

## [v0.0.10] — 2026-05-15

Prometheus exposition format on `/metrics` via Accept-header content negotiation. Compounds on v0.0.7 JSON `/metrics` — additive, zero breaking change. Promotes "Prometheus-format `/metrics`" from the W2+ Unreleased candidate list into v0.0.10.

### Added
- **Content negotiation on `/metrics`** (`internal/api/metrics.go`):
  - `Accept: text/plain` → Prometheus exposition format (with optional `version=0.0.4` parameter, per Prometheus convention)
  - `Accept: application/json` OR missing OR `*/*` → JSON Metrics body (v0.0.7 contract — backward-compat default)
  - Multi-type Accept (e.g., scraper-style `application/openmetrics-text;version=1.0.0,text/plain;version=0.0.4;q=0.5,*/*;q=0.1`) honors text/plain
  - Default-JSON preserves v0.0.7 caller behavior; switch-to-Prometheus-default may happen at v1.0 ADR if scraper-share warrants
- **`Metrics.MarshalPrometheus()`** — renders 6 metric families with HELP+TYPE comments per Prometheus exposition format spec:
  - `eidetic_uptime_seconds` (gauge)
  - `eidetic_engrams_total` (gauge)
  - `eidetic_engrams_by_surface_total{surface="..."}` (gauge with label; surfaces sorted alphabetically for diff-stable output)
  - `eidetic_capture_skipped_total` (counter)
  - `eidetic_db_size_bytes` (gauge)
  - `eidetic_build_info{version="v..."}` (gauge value=1, version in label — Prometheus convention for build metadata)
  - Empty `EngramBySurface` map suppresses the per-surface block (no dangling HELP/TYPE without a value)
- **5 new tests** in `internal/api/metrics_test.go`:
  - `TestMetricsPrometheusFormat` — full schema verification (all 6 families, HELP/TYPE comments, label format)
  - `TestMetricsAcceptDefaultsJSON` — missing Accept defaults to JSON (backward-compat regression)
  - `TestMetricsAcceptStarStarReturnsJSON` — `*/*` wildcard does not trigger Prometheus
  - `TestMetricsAcceptMultipleHonorsTextPlain` — multi-type Accept including text/plain → Prometheus
  - `TestMarshalPrometheusEmptyBySurface` — empty surface map suppresses block
  - `TestMarshalPrometheusDeterministicSurfaceOrder` — alphabetical sort for stable output
  - All green under `-race`. Existing 4 tests (HappyPath / NoProvider503 / ProviderError500 / MethodNotAllowed) regression-clean.
- **Live-fire validation**: `eideticd -version` → `eideticd v0.0.10-rc1`; default Accept returns JSON (55,245 engrams in 5s capture); `Accept: text/plain` returns full Prometheus exposition (8 metric lines + 12 comment lines); scraper-style multi-type Accept honors text/plain. v0.0.6 shutdown drain + v0.0.9 caller auth still clean.

### Reference
- PR #26 (this release commit folded in)
- W2+ list "Prometheus-format `/metrics`" — promoted into v0.0.10
- Prometheus exposition format spec: https://prometheus.io/docs/instrumenting/exposition_formats/
- Compounds v0.0.7 Metrics struct + handler; Accept-header negotiation is additive. Live-fire 3 Accept variants against the real binary before any test was codified.

---

## [v0.0.9] — 2026-05-15

Opt-in caller authentication on the daemon API. Defense-in-depth on top of UDS `0600` trust boundary — prevents other-process-on-same-uid impersonation when enabled. Off by default; preserves the W1 single-user UDS-trust model documented in `SECURITY.md`.

### Added
- **`internal/auth` package** (`internal/auth/auth.go`) — Token type with `Generate()` (32-byte crypto/rand → 64-char hex), `WriteFile(dataDir, token)` (`0600` perms, rotates on every restart, no cross-restart persistence), `ReadFile(dataDir)` (client-side discovery), `Set/Get/Enabled` atomic accessors, `Validate(header)` constant-time comparison (accepts both `Bearer <token>` and bare-token forms), and `Middleware(next, openPaths...)` http.Handler wrapper. PR #25.
- **`api.Options.AuthToken *auth.Token`** — opt-in field on the api package. Nil OR disabled token → middleware passes through transparently (preserves backward-compat for callers that don't set `EIDETIC_AUTH=1`). When enabled, `/engrams` + `/metrics` require `Authorization: Bearer <token>`; `/healthz` stays open (liveness probe contract; service managers + load balancers expect this).
- **`cmd/eideticd/main.go`** — new `-auth` flag + `EIDETIC_AUTH=1` env var. On startup with auth enabled: generate token via crypto/rand, write to `<dataDir>/auth-token` (0600), log `auth: enabled — token written to <path> (0600), rotates each restart`. `dataDir` is now MkdirAll'd at 0700 before any file write (covers token + state.json + engrams.db).
- **Bridge auto-discovery** (`bridge/python/eidetic_mcp/client.py`) — `DaemonClient` constructor adds optional `auth_token` kwarg; resolution order: explicit kwarg → `EIDETIC_AUTH_TOKEN` env → `<EIDETIC_DATA_DIR>/auth-token` file. When token is present, all requests carry `Authorization: Bearer <token>`. Backward-compat: when daemon is not in auth-mode, the header is harmless; when daemon is in auth-mode and bridge has no token, requests get 401 (transparent failure).
- **9 new Go tests** in `internal/auth/auth_test.go` — Generate uniqueness + 64-char shape, WriteFile 0600 perms, WriteFile rotation, ReadFile missing → error, Token Set/Get/Enabled, Validate Bearer + bare + whitespace-tolerance, Validate rejects mismatch + length-mismatch, Middleware passes-through when disabled (regression), Middleware gates protected routes (401 + WWW-Authenticate), Middleware open-path bypasses, Middleware valid-token passes-through. All green under `-race`.
- **4 new bridge tests** in `bridge/python/tests/test_client.py` — explicit kwarg, env var override, file auto-discovery, absent-token (no Authorization header sent). Bridge total: 31/31 (was 27/27).
- **Stage 8 in `scripts/demo-smoke.sh`** — auth contract regression gate. Spawns a SECOND daemon instance with `EIDETIC_AUTH=1`, verifies (a) `auth-token` file exists with 0600 perms + 64-char content, (b) `/healthz` 200 even with auth on (open path), (c) `/metrics` 401 without token, (d) `/metrics` 401 with wrong token, (e) `/metrics` 200 with valid Bearer header. Catches future auth contract regressions when CI billing returns ~2026-05-19.
- **Live-fire validation**: `eideticd -version` → `eideticd v0.0.9-rc1`; auth-disabled regression PASS (no auth-token file written, /metrics works without token); auth-enabled 7 contract cases PASS (file 0600 64ch, /healthz open, /metrics 401/401/401/200/200 across no-token/wrong-token/valid-Bearer/valid-bare). Bridge end-to-end: token auto-loaded from file (64 chars), `c.metrics()` returns valid JSON via Bearer header. v0.0.6 shutdown drain still clean (0 "database is closed" errors on SIGTERM).

### Reference
- PR #25 (this release commit folded in)
- W2+ list "Caller authentication on the API (per-process token in HTTP header)" — promoted into v0.0.9
- Compounds existing Options/middleware pattern; reuses the 0600 conventions from store + UDS. 7 contract cases dogfooded against the real binary before any test was codified.

### Threat model amendment to `SECURITY.md`
- Pre-v0.0.9: UDS `0600` was the only trust boundary. Other processes running as the same uid could read engrams.
- v0.0.9 with `EIDETIC_AUTH=1`: caller must possess the per-process Bearer token. Token rotates each restart (no stale-token replay if dataDir is ever world-readable between sessions). Bridge clients auto-discover via file. Token file shares dataDir's 0700 protection + own 0600.
- Off-by-default for backward-compat. Operators who want the harder boundary turn it on with one env var.

---

## [v0.0.8] — 2026-05-14

Bridge surface for v0.0.7 `/metrics` — `daemon_metrics()` MCP tool exposes daemon observability counters to MCP clients (Cursor / Claude Code / Cline) via tool-call.

### Added
- **`DaemonClient.metrics()`** (`bridge/python/eidetic_mcp/client.py`) — pure-stdlib UDS HTTP GET on `/metrics`; returns the JSON body as dict, raises `DaemonError` on transport / parse / 503 (daemon predates v0.0.7) failures.
- **`daemon_metrics` MCP tool** (`bridge/python/eidetic_mcp/server.py`) — third tool registered alongside `query_engrams` + `daemon_status`. No arguments; returns the daemon's `/metrics` JSON pretty-printed. Schema is additive-only across versions.
- **2 new bridge tests**:
  - `test_client_metrics_against_fake_server` — UDS HTTP fake server with `/metrics` route; asserts client returns dict with all 7 fields populated.
  - `test_client_metrics_unreachable_raises_daemon_error` — no-daemon → `DaemonError` (not silent empty dict; analog to existing healthy() fallback contract).
- Bridge total: **27/27 tests green** (was 25/25).
- **Live-fire validation**: `DaemonClient(uds_path='/tmp/.../sock').metrics()` against real v0.0.7 daemon returned `version='v0.0.7'`, `engram_total=64650`, `engram_by_surface={'claude_code': 65313}`, `db_size_bytes=379797504`.

### Reference
- PR #21 (this release commit folded in)
- Compounds with v0.0.7 `/metrics` (PR #20) — bridge surface compounds the daemon's observability surface.

---

## [v0.0.7] — 2026-05-14

First observability surface on top of v0.0.6 — `GET /metrics` JSON endpoint exposing daemon-side counters that DO posts (and any future ops dashboard) can cite live.

### Added
- **`GET /metrics`** (`internal/api/metrics.go` + `internal/api/server.go`) — returns JSON Metrics body: `version`, `uptime_seconds`, `engram_total`, `engram_by_surface`, `capture_skipped`, `db_path`, `db_size_bytes`. Schema is **additive-only** across versions: callers can rely on existing fields continuing to exist; new fields may appear. Per-request timeout from `Server.timeout`. PR #20.
- **`store.Count(ctx)`** + **`store.CountBySurface(ctx)`** (`internal/store/store.go`) — reader-pool queries (do not block writers) for the /metrics endpoint.
- **`api.MetricsProvider func(ctx) (Metrics, error)`** + **`api.Options.Metrics`** field — keeps the api package decoupled from cmd-side state (Watcher pointer, process start time, build version). Provider is supplied by `main()`; `nil` provider → `/metrics` returns `503 metrics not configured` so callers can detect daemons that predate v0.0.7 wiring.
- **5 new tests** under `-race`:
  - `TestMetricsHappyPath` — TCP-bound server + canned provider; assert 200 + JSON schema fields populate correctly
  - `TestMetricsNoProviderReturns503` — nil provider fallback
  - `TestMetricsProviderError` — provider error → 500 with body
  - `TestMetricsMethodNotAllowed` — POST → 405
  - `TestCountEmpty` + `TestCountAndCountBySurfaceAfterInsert` — store-level coverage
- **Live-fire validation**: `eideticd -version` → `eideticd v0.0.7-rc1`; 10s capture against real `~/.claude/projects/`; `curl --unix-socket /tmp/eidetic-metrics-test/sock http://localhost/metrics` returned `engram_total=139751`, `db_size_bytes=659046400`, `capture_skipped=0`. v0.0.6 shutdown-drain still clean (0 "database is closed" errors on SIGTERM).

### Note
- `engram_total` and `sum(engram_by_surface)` may differ by tens during heavy live ingestion — the two reader-pool queries run on different WAL snapshots a moment apart. Not a bug; total is approximate during burst ingest. For exact total + per-surface read together, prefer `engram_by_surface` and sum client-side.

### Reference
- PR #20 (this release commit folded in)
- W2+ list (CHANGELOG Unreleased) entry "/metrics HTTP endpoint surfacing capture skip-counter + bench numbers" — promoted into v0.0.7

---

## [v0.0.6] — 2026-05-14

Shutdown-race fix on top of v0.0.5 (no behavioral change to capture/store/API).

### Fixed
- **Issue #17 — SIGTERM shutdown race** (`internal/capture/watcher.go` + `cmd/eideticd/main.go`). v0.0.5 daemons fired ~30 `capture: insert claude_code: begin batch tx: sql: database is closed` errors per shutdown because in-flight `parseAndCommit` goroutines (from `scanInitial` walks + debounced `scheduleParse` AfterFunc timers) raced `defer s.Close()` in main(). Fix: introduce `Watcher.inflight sync.WaitGroup` tracking parse goroutines spawned at schedule-time (not fire-time) so AfterFunc timers stopped via `flushAll` correctly release their slot; `Watcher.Run` defers `inflight.Wait()` so it returns only after every InsertBatch drains; `main()` waits on a `captureDone` channel between `srv.Serve(ctx)` returning and the `defer s.Close()` firing. **Real-world dogfood**: same scenario that surfaced the bug now produces 0 "database is closed" errors over 30s capture + SIGTERM (was ~30 in v0.0.5). PR #18.
- `scanInitial` now bails on `ctx.Err() != nil` via `filepath.SkipAll` so SIGTERM during a hot ~/.claude/projects walk no longer queues additional InsertBatch calls past shutdown.
- `main()` `srv.Serve(ctx)` error path demoted from `log.Fatalf` → `log.Printf` to preserve the issue #17 invariant (capture-drain runs even on serve errors before store closes).

### Added
- 2 regression tests in `internal/capture/shutdown_drain_test.go`:
  - `TestWatcherDrainsBeforeShutdown` — closableSink with 5ms InsertBatch delay; cancel ctx mid-walk; assert 0 post-Close calls land on the sink. Pre-fix: this fails (post-close calls > 0). Post-fix: 0 races.
  - `TestWatcherDrainBalancesInflight` — synthetic 50-call burst on the same path within debounce window; asserts inflight WG arithmetic balances (no panic, no Wait deadlock).
- Both green under `-race`.

### Reference
- PRs #18 (this fix; release commit folded in)
- daemon-repo issue #17 (filed during v0.0.5 dogfood; closed by this release)
- Runtime spike caught what static review missed; live-fire dogfood proved the fix before any test was codified.

---

## [v0.0.5] — 2026-05-14

Capture-side hard-wall removal + bridge reassembly + brand alignment (brand = "Eidetic Works"; "Eidetic" alone not used).

### Added
- **Chunked-capture for arbitrarily-large records** (`internal/capture/parser_jsonl.go`) — JSONL lines exceeding `chunkPayloadBudget` (7 MiB; sized below `store.MaxPayloadBytes` 8 MiB to leave room for meta+wire overhead) are split into ⌈len/budget⌉ chunks, each tagged with `chunk_id` (sha256-prefix of full payload, 16 hex chars; **idempotent on resume**) + `chunk_seq` (0-indexed) + `chunk_total` in meta JSON. Records ≤ budget emit 1 engram with no `chunk_*` meta fields (backward-compat). Consumer-side reassembly: group by `chunk_id`, sort by `chunk_seq`, concatenate `payload`. Eliminates the 8 MiB hard wall — daemon now handles records of any size, bounded only by SQLite per-row limits + writer-pool throughput. **daemon-repo ADR-018**, PR #14.
- 7 new tests in `internal/capture/parser_chunked_test.go` cover normal-line-no-chunking, oversized-line-splits, chunk-ID-idempotent, mixed-sizes-in-one-file, reassembly-roundtrip, state-offset-advances-past-oversized + sanity gate (chunk-budget < store cap with ≥ 256 KiB headroom). 1 test renamed in `oversized_skip_test.go` (`TestWatcherOversizedPayloadCounted` → `TestWatcherOversizedPayloadChunked`) reflecting new contract: counter stays at 0 on normal chunked records (chunks fit by construction); counter is now defense-in-depth only.
- **Bridge-side reassembly** (`bridge/python/eidetic_mcp/reassemble.py`) — `reassemble_chunks(rows)` consumer helper for the chunked-capture contract. `query_engrams` MCP tool runs reassembly by default; `raw_chunks=true` bypasses for debugging. MCP clients (Cursor / Claude Code / Cline) see ONE engram per logical record regardless of underlying chunking. Edge cases (warn + best-effort, never silent-drop): missing chunks, total mismatch, malformed meta. Idempotent via stripped `chunk_*` from merged engram's meta. PR #15.
- 14 new tests in `bridge/python/tests/test_reassemble.py` (bridge total: 25/25 green).

### Changed
- **Brand alignment** (locked = "Eidetic Works"): `docs/SPEC.md` title + `docs/IMPLEMENTATION_PLAN.md` title + `scripts/install.sh` header comment + systemd unit Description = "Eidetic Works daemon" (was "Eidetic daemon"). Plumbing identifiers stay (`eidetic-daemon` repo, `eideticd` binary, `eidetic_mcp` package, `~/.eidetic/` data dir, `EIDETIC_*` env vars) — package identifier is plumbing and stays.

### Reference
- PRs #14 (chunked-capture), #15 (bridge reassembly), #16 (this release commit; brand alignment + version cut)
- daemon-repo ADR-018 (chunked-capture decision)
- ADR-017 (brand-architecture decision) + ADR-018 (chunked-capture, in `docs/DECISIONS.md`)

---

## [v0.0.4] — 2026-05-14

Python MCP bridge + ship-readiness polish on top of v0.0.3 (no Go behavioral change).

### Added
- **`bridge/python/`** — Python MCP stdio server (spec § 7 Open Q #5; pulled forward from Day-6 stretch). Two tools: `query_engrams(surface, limit, since)` + `daemon_status()`. Pure-stdlib UDS client (no requests/httpx dep); MCP SDK loaded lazily (server.py import-only-when-running) so client + tests run without it. 11 unit + integration tests via `PYTHONPATH=. pytest tests/`. Live-fire validated against real eideticd. Install: `pip install -e bridge/python` (not yet on PyPI; planned for W2+). PR #12.
- **`scripts/demo-smoke.sh`** + Makefile + ci.yml step — end-to-end gate validating spec § 8 acceptance criteria #3 (write→capture→read against real binary, including `-version` flag check + `/healthz` round-trip + JSONL write to watched dir + marker assertion in `/engrams` response). Locally PASSES in ~2-3 sec including modernc cold-init. PR #10.
- **`docs/demo.md`** — Day-7 spec § 8 acceptance flow text-script with expected outputs at every step. Release demo post hyperlink target. PR #8.
- **`CHANGELOG.md`** + README polish linking `docs/demo.md`, `docs/DECISIONS.md`, releases. PR #9.
- **`.github/pull_request_template.md`** — `Track:` + `Week-scope:` prefilled to reduce track-tag-check CI gate friction; documents Track-C tripwire vocabulary inline. PR #11.
- README "MCP bridge" section + CHANGELOG entries surfacing the bridge. PR #13.

### Reference
- PRs #8, #9, #10, #11, #12, #13.

---

## [v0.0.3] — 2026-05-14

Hardening release on top of v0.0.2 (no behavioral break for v0.0.2 users; recommended upgrade for anyone hitting silent payload-skips on real Claude session JSONLs).

### Added
- `eideticd -version` flag — prints `eideticd vX.Y.Z` from build-time `-ldflags "-X main.Version=$(git describe ...)"` injection.
- `Watcher.SkippedPayloadTooLarge() uint64` — atomic count of capture-side engrams skipped due to payload exceeding `store.MaxPayloadBytes`. Surfaces silent data loss to telemetry / tests.
- `docs/DECISIONS.md` with **ADR-017** — v0.0.2 cross-compile runtime smoke verdict (darwin + linux PASS; windows static-cleared, runtime deferred).
- `docs/demo.md` — Day-7 spec § 8 acceptance flow text-script + expected outputs.
- `.github/workflows/track-tag-check.yml` — PR-gate enforcing track-tag-required + Track-C tripwire vocabulary block on incoming PRs.

### Changed
- `store.MaxPayloadBytes`: **1 MiB → 8 MiB** (3.3× headroom over largest observed real Claude session JSONL chunk: 2.41 MiB; runtime spike showed 8+ engrams dropped silently in first 1s with 1 MiB cap).
- `internal/capture/watcher.go` — pre-filters at `parseAndCommit` before `InsertBatch` so one oversized record no longer fails the WHOLE batch via pre-tx validation. Per-batch log-line surfaces skip count.
- `Makefile` — all build targets now inject `-ldflags "-X main.Version=$(git describe --tags --always --dirty)"`.

### Fixed
- (Implicit) v0.0.2 binaries had no `-version` flag — printed default usage when invoked.
- (Implicit) v0.0.2 `MaxPayloadBytes = 1 MiB` silently dropped real Claude session JSONL chunks above 1 MiB.

### Reference
- PR #6 (`63af7f4`), PR #7 (`d70ffe3`), PR #8 (`e0739c0`)
- Runtime-spike-driven hardening cycle (caught silent data loss that static review missed).

---

## [v0.0.2] — 2026-05-13

First W1 release. Daemon W1 spec functionally complete through Phase 6.

### Added
- **Store** (Phase 1): SQLite-WAL via `modernc.org/sqlite` (pure-Go, no CGO; ADR-016). Writer/reader pool split per ADR-014 #3 (writer `MaxOpenConns=1`, reader `MaxOpenConns=8`, mode=ro). Schema + `idx_surface_ts` composite index. `MaxPayloadBytes = 1 MiB` (raised in v0.0.3).
- **API** (Phase 2): `GET /engrams?surface=X&limit=N&since=unix-ns` over Unix domain socket (default) or TCP loopback (`EIDETIC_TCP=1`). UDS `chmod 0600` post-listen. `New(store, Options) (*Server, error)` constructor; explicit `Close()`. (PR #2)
- **API enablers** (Phase 3 prereq): `store.InsertBatch(ctx, []Engram) error` (ADR-014 #4 prepared-stmt + tx). `store.ExplainQuery(ctx) (string, error)` test helper. `GET /healthz` returning `{"status":"ok"}`. (PR #3)
- **Capture** (Phase 3): `fsnotify` multiplexer + 3 surface parsers (claude_code + cowork JSONL line-tail; cursor whole-file SHA-256 dedup) + `~/.eidetic/state.json` offset persistence + per-path `*sync.Mutex` serializer (race fix). 25 tests including burst stress + race-mode. (PR #4)
- **Bench** (Phase 5): 10K-engram fixture seed; 3 P95 gates fail-the-build per spec § 3 / ADR-014 gaps A+C — Retrieve `0.27 ms`, Write `0.65 ms`, Concurrent `3.5 ms`. (PR #5)
- **Distribution** (Phase 6): `scripts/install.sh` one-liner installer (detects OS+arch, refuses darwin/amd64, registers launchd LaunchAgent / systemd-user unit, smoke-tests `-version`). `scripts/launchd.plist` + `scripts/systemd.service` templates. `.github/workflows/release.yml` fires on `v*` tags. (PR #5)
- **SECURITY.md**: explicit threat model + storage modes (`0700` dir, `0600` file) + 5 known W1 limitations + hardening roadmap. (PR #5)

### Reference
- Cumulative: 6 PRs (#1–#5).
- W1 spec: `docs/SPEC.md`.
- ADR-014 (writer/reader pool split + InsertBatch + concurrent-read gate) + ADR-016 (CGO-free SQLite driver) — see `docs/DECISIONS.md`.
- Release workflow gated on CI billing in early releases; assets uploaded manually until billing window resumes.

---

## [v0.0.12] — 2026-05-17

Query latency percentiles on `/metrics`. Every `/engrams` call is timed; P50/P95/P99 surface via all three `/metrics` formats (JSON, Prometheus summary, OpenMetrics summary). Compounds on v0.0.11 — additive, **zero breaking change** (new fields are `omitempty` in JSON; new metric block gated on ≥ 2 samples).

### Added
- **`LatencyTracker`** (`internal/api/latency.go`): lock-guarded ring-buffer reservoir sampler (configurable capacity; default 1000 → ~8 KB). `Record(time.Duration)` stores microsecond values; `Percentiles()` returns P50/P95/P99 via linear interpolation on a sorted snapshot; returns `math.NaN` when < 2 samples. `Count()` returns live fill (capped at capacity after wrap).
- **`Options.QueryLatency *LatencyTracker`** (`internal/api/server.go`): optional — zero timing overhead when nil; wired in `main.go` with a 1000-sample reservoir.
- **`/engrams` timing** (`internal/api/routes.go`): `time.Since(start)` around `store.Retrieve`; recorded to `queryLatency` when non-nil.
- **`Metrics` struct fields** (`internal/api/metrics.go`): `QueryP50Us`, `QueryP95Us`, `QueryP99Us *float64` (omitempty) + `QueryCount int` (omitempty). Nil when < 2 samples.
- **Prometheus summary block**: `eidetic_query_duration_microseconds{quantile="0.5/0.95/0.99"}` + `_count` line. Omitted when no data.
- **OpenMetrics summary block**: same metric with `# UNIT eidetic_query_duration microseconds`. Omitted when no data; EOF preserved unconditionally.
- **Tests** (`internal/api/latency_test.go`): 6 tests — empty, single-sample, 100-sample percentile accuracy (P50 ±2 µs, P95 ±2 µs, P99 ±2 µs), ring-buffer wrap (slot reuse verified), zero-capacity no-panic, concurrent safety (8 goroutines × 200 samples).
- **Tests** (`internal/api/metrics_test.go`): 3 new tests — `TestMetricsQueryLatencyJSONOmitEmpty` (nil fields absent in JSON, set fields present), `TestMetricsPrometheusQueryLatencySummary` (block absent/present, all 6 expected lines), `TestMetricsOpenMetricsQueryLatencySummary` (block absent/present, UNIT comment, EOF unconditional).

### Reference
- Compounds: v0.0.7 `/metrics` JSON → v0.0.10 Prometheus → v0.0.11 OpenMetrics → v0.0.12 query latency.
- NaN-check idiom in `main.go`: `p50 == p50` (NaN is the only float64 that doesn't equal itself — avoids importing `math` in `cmd/`).
- Reservoir cap = 1000 → ~8 KB overhead. Ring wraps after 1000 samples; older measurements drop.

---

## Unreleased

W2+ candidates (per spec § 1 cuts list, none of these target a current PR):
- Counter `_created` timestamps on OpenMetrics output (per spec optional extension).
- Compliance daemon (W2 per spec § 1).
- PyPI publication of `eidetic-mcp` package.
- Client-side encryption of R2 backups (W3+ per ADR-019 privacy posture).
- `wrangler deploy` live-fire validation (blocked on account-level R2 enable — one-time dashboard toggle).

[v0.0.24]: https://github.com/eidetic-works/eidetic-daemon/releases/tag/v0.0.24
[v0.0.23]: https://github.com/eidetic-works/eidetic-daemon/releases/tag/v0.0.23
[v0.0.13]: https://github.com/eidetic-works/eidetic-daemon/releases/tag/v0.0.13
[v0.0.12]: https://github.com/eidetic-works/eidetic-daemon/releases/tag/v0.0.12
[v0.0.11]: https://github.com/eidetic-works/eidetic-daemon/releases/tag/v0.0.11
[v0.0.10]: https://github.com/eidetic-works/eidetic-daemon/releases/tag/v0.0.10
[v0.0.9]: https://github.com/eidetic-works/eidetic-daemon/releases/tag/v0.0.9
[v0.0.8]: https://github.com/eidetic-works/eidetic-daemon/releases/tag/v0.0.8
[v0.0.7]: https://github.com/eidetic-works/eidetic-daemon/releases/tag/v0.0.7
[v0.0.6]: https://github.com/eidetic-works/eidetic-daemon/releases/tag/v0.0.6
[v0.0.5]: https://github.com/eidetic-works/eidetic-daemon/releases/tag/v0.0.5
[v0.0.4]: https://github.com/eidetic-works/eidetic-daemon/releases/tag/v0.0.4
[v0.0.3]: https://github.com/eidetic-works/eidetic-daemon/releases/tag/v0.0.3
[v0.0.2]: https://github.com/eidetic-works/eidetic-daemon/releases/tag/v0.0.2
