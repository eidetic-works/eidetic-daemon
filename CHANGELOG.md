# Changelog

All notable changes to eidetic-daemon. Format inspired by [Keep a Changelog](https://keepachangelog.com/); semver via git tags.

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

## Unreleased

W2+ candidates (per spec § 1 cuts list, none of these target a current PR):
- Latency histograms on `/metrics` (P50/P95/P99 of /engrams query times, capture parse latency).
- Counter `_created` timestamps on OpenMetrics output (per spec optional extension).
- Cloudflare D1+R2+Workers cloud sync (per ADR-005, encrypted blobs only).
- Compliance daemon (W2 per spec § 1).
- PyPI publication of `eidetic-mcp` package.
- GH-Actions ubuntu+wine matrix step for Windows runtime smoke (deferred per ADR-017).

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
