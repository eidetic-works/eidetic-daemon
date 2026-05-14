# Changelog

All notable changes to eidetic-daemon. Format inspired by [Keep a Changelog](https://keepachangelog.com/); semver via git tags.

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

W2 candidates (per spec § 1 cuts list):
- Chunked-capture for arbitrarily-large records (replaces the 8 MiB cap as a hard wall).
- `/metrics` HTTP endpoint surfacing capture skip-counter + bench numbers.
- Caller authentication on the API (per-process token in HTTP header).
- MCP bridge as a separate Python wrapper around UDS API (NOT in daemon binary; spec § 7 Open Q #5).
- Cloudflare D1+R2+Workers cloud sync (per ADR-005, encrypted blobs only).
- Compliance daemon (W2 per spec § 1).

[v0.0.3]: https://github.com/eidetic-works/eidetic-daemon/releases/tag/v0.0.3
[v0.0.2]: https://github.com/eidetic-works/eidetic-daemon/releases/tag/v0.0.2
