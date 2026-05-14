# Changelog

All notable changes to eidetic-daemon. Format inspired by [Keep a Changelog](https://keepachangelog.com/); semver via git tags.

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
- HARD RULE applied: `feedback_static_audit_needs_runtime_pair.md` — runtime spike caught what static review missed.
- HARD RULE applied: `feedback_no_test_before_one_success.md` — live-fire dogfood proved fix BEFORE codifying.

---

## [v0.0.5] — 2026-05-14

Capture-side hard-wall removal + bridge reassembly + brand alignment per entity-wide ADR-017 + ADR-018 (locked: brand = "Eidetic Works", monolithic Notion-style; "Eidetic" alone not used).

### Added
- **Chunked-capture for arbitrarily-large records** (`internal/capture/parser_jsonl.go`) — JSONL lines exceeding `chunkPayloadBudget` (7 MiB; sized below `store.MaxPayloadBytes` 8 MiB to leave room for meta+wire overhead) are split into ⌈len/budget⌉ chunks, each tagged with `chunk_id` (sha256-prefix of full payload, 16 hex chars; **idempotent on resume**) + `chunk_seq` (0-indexed) + `chunk_total` in meta JSON. Records ≤ budget emit 1 engram with no `chunk_*` meta fields (backward-compat). Consumer-side reassembly: group by `chunk_id`, sort by `chunk_seq`, concatenate `payload`. Eliminates the 8 MiB hard wall — daemon now handles records of any size, bounded only by SQLite per-row limits + writer-pool throughput. **daemon-repo ADR-018**, PR #14.
- 7 new tests in `internal/capture/parser_chunked_test.go` cover normal-line-no-chunking, oversized-line-splits, chunk-ID-idempotent, mixed-sizes-in-one-file, reassembly-roundtrip, state-offset-advances-past-oversized + sanity gate (chunk-budget < store cap with ≥ 256 KiB headroom). 1 test renamed in `oversized_skip_test.go` (`TestWatcherOversizedPayloadCounted` → `TestWatcherOversizedPayloadChunked`) reflecting new contract: counter stays at 0 on normal chunked records (chunks fit by construction); counter is now defense-in-depth only.
- **Bridge-side reassembly** (`bridge/python/eidetic_mcp/reassemble.py`) — `reassemble_chunks(rows)` consumer helper for the chunked-capture contract. `query_engrams` MCP tool runs reassembly by default; `raw_chunks=true` bypasses for debugging. MCP clients (Cursor / Claude Code / Cline) see ONE engram per logical record regardless of underlying chunking. Edge cases (warn + best-effort, never silent-drop): missing chunks, total mismatch, malformed meta. Idempotent via stripped `chunk_*` from merged engram's meta. PR #15.
- 14 new tests in `bridge/python/tests/test_reassemble.py` (bridge total: 25/25 green).

### Changed
- **Brand alignment per entity-wide ADR-018** (locked = "Eidetic Works"): `docs/SPEC.md` title + `docs/IMPLEMENTATION_PLAN.md` title + `scripts/install.sh` header comment + systemd unit Description = "Eidetic Works daemon" (was "Eidetic daemon"). Plumbing identifiers stay (`eidetic-daemon` repo, `eideticd` binary, `eidetic_mcp` package, `~/.eidetic/` data dir, `EIDETIC_*` env vars) per ADR-017 distribution corollary ("package identifier is plumbing and stays").

### Reference
- PRs #14 (chunked-capture), #15 (bridge reassembly), #16 (this release commit; brand alignment + version cut)
- daemon-repo ADR-018 (chunked-capture decision)
- entity-wide ADR-017 + ADR-018 (brand-architecture decision + Tier 0.5 outcome) — see `mcp-server-nucleus/DECISIONS.md`

---

## [v0.0.4] — 2026-05-14

Python MCP bridge + ship-readiness polish on top of v0.0.3 (no Go behavioral change).

### Added
- **`bridge/python/`** — Python MCP stdio server (spec § 7 Open Q #5; pulled forward from Day-6 stretch). Two tools: `query_engrams(surface, limit, since)` + `daemon_status()`. Pure-stdlib UDS client (no requests/httpx dep); MCP SDK loaded lazily (server.py import-only-when-running) so client + tests run without it. 11 unit + integration tests via `PYTHONPATH=. pytest tests/`. Live-fire validated against real eideticd. Install: `pip install -e bridge/python` (not yet on PyPI per 90d pivot — substrate-publication decision deferred to W2+). PR #12.
- **`scripts/demo-smoke.sh`** + Makefile + ci.yml step — end-to-end gate validating spec § 8 acceptance criteria #3 (write→capture→read against real binary, including `-version` flag check + `/healthz` round-trip + JSONL write to watched dir + marker assertion in `/engrams` response). Locally PASSES in ~2-3 sec including modernc cold-init. PR #10.
- **`docs/demo.md`** — Day-7 spec § 8 acceptance flow text-script with expected outputs at every step. Distribution Officer Day-7 demo post hyperlink target. PR #8.
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
- `.github/workflows/track-tag-check.yml` — PR-gate ported from monorepo (track-tag-required + track-c-word-block); narrower scope vs monorepo (drops STATUS/PLAN-coupled checks).

### Changed
- `store.MaxPayloadBytes`: **1 MiB → 8 MiB** (3.3× headroom over largest observed real Claude session JSONL chunk: 2.41 MiB; cc-tb runtime spike showed 8+ engrams dropped silently in first 1s with 1 MiB cap).
- `internal/capture/watcher.go` — pre-filters at `parseAndCommit` before `InsertBatch` so one oversized record no longer fails the WHOLE batch via pre-tx validation. Per-batch log-line surfaces skip count.
- `Makefile` — all build targets now inject `-ldflags "-X main.Version=$(git describe --tags --always --dirty)"`.

### Fixed
- (Implicit) v0.0.2 binaries had no `-version` flag — printed default usage when invoked.
- (Implicit) v0.0.2 `MaxPayloadBytes = 1 MiB` silently dropped real Claude session JSONL chunks above 1 MiB.

### Reference
- PR #6 (`63af7f4`), PR #7 (`d70ffe3`), PR #8 (`e0739c0`)
- cc-tb SPIKE-RESULT relay `_152711_52fb5738`
- cc-peer DRIFT-AUDIT-LITE-RESULT relay `_215000` + AUDIT-AMENDMENT relay `_225000`
- HARD RULE landed: `feedback_static_audit_needs_runtime_pair.md` (monorepo MEMORY.md)

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
- Cumulative: 6 PRs (#1–#5 with stacking from cc-main on top of operator's #1+#2 Phase 0-2)
- W1 spec: `docs/SPEC.md`
- ADRs: `docs/IMPLEMENTATION_PLAN.md` references entity-wide ADR-008 + ADR-011 + ADR-012 + ADR-013 + ADR-014 + ADR-016 (in monorepo `mcp-server-nucleus/DECISIONS.md`)
- Release workflow billing-blocked per `project_eidetic_works_ci_billing.md`; assets uploaded manually.

---

## Unreleased

W2+ candidates (per spec § 1 cuts list, none of these target a current PR):
- Caller authentication on the API (per-process token in HTTP header).
- Prometheus-format `/metrics` (currently JSON-only).
- Bridge fold-in to `mcp-server-nucleus` (substrate-paused per `project_eidetic_works_90d_pivot_2026_05_10.md`).
- Cloudflare D1+R2+Workers cloud sync (per ADR-005, encrypted blobs only).
- Compliance daemon (W2 per spec § 1).
- PyPI publication of `eidetic-mcp` package (deferred per 90d pivot).
- GH-Actions ubuntu+wine matrix step for Windows runtime smoke (deferred per daemon-repo ADR-017; gates on billing reset 2026-05-19).
- Acquire `eideticworks.com` ($1-5K) post-W4 if probe validates (per entity-wide ADR-018; logged in `mcp-server-nucleus/docs/brand-migration.md`).

[v0.0.7]: https://github.com/eidetic-works/eidetic-daemon/releases/tag/v0.0.7
[v0.0.6]: https://github.com/eidetic-works/eidetic-daemon/releases/tag/v0.0.6
[v0.0.5]: https://github.com/eidetic-works/eidetic-daemon/releases/tag/v0.0.5
[v0.0.4]: https://github.com/eidetic-works/eidetic-daemon/releases/tag/v0.0.4
[v0.0.3]: https://github.com/eidetic-works/eidetic-daemon/releases/tag/v0.0.3
[v0.0.2]: https://github.com/eidetic-works/eidetic-daemon/releases/tag/v0.0.2
