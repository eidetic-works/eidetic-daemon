# Architecture Decision Records — eidetic-daemon

This file is the append-only ADR log scoped to **daemon-specific** decisions (W1 daemon + later phases). ADR numbering starts at 017 because earlier ADRs (001-016) governed broader architectural decisions across other components and are not surfaced in this public repo.

---

## ADR-017 (2026-05-13): v0.0.2 cross-compile runtime smoke — darwin + linux validated; windows deferred

**Decision:** v0.0.2 cross-compile assets validated at runtime on darwin-arm64 (native M2) and linux-amd64 (docker ubuntu:22.04). Windows-amd64 runtime verification deferred — Wine absent on the spike host — but static analysis ruled out the CGO-silent-strip pattern that ADR-016 guards against. Distribution claim "cross-platform binaries available" is honest for darwin + linux as of v0.0.2; Windows requires a separate runtime gate before being claimed.

**Reason:** Runtime-smoke spike (50 min, worktree-isolated). Darwin native run: socket created, `/healthz` returned `{"status":"ok"}` HTTP 200, no startup errors. Linux container run (ubuntu:22.04, --platform linux/amd64): identical result. Windows binary static analysis: 8977 `modernc.org/*` symbols, 0 `mattn/go-sqlite3` references, `EIDETIC_TCP` env-var symbol present, `127.0.0.1:9876` default TCP address compiled in, flag help text `TCP listen address (overrides default; opt-in via EIDETIC_TCP=1)` compiled in, `/healthz` handler present. The silent-CGO-strip failure mode (build succeeds, binary stripped, crashes at runtime) is ruled out by modernc-symbol presence — pure-Go SQLite is statically linked. Runtime gate for Windows can be satisfied by a GH-Actions ubuntu+wine matrix step or manual run on a Windows host.

**Additional findings worth recording (out of spike scope but operator-relevant):**

1. v0.0.2 binaries had no `-version` flag — spike directive expected `eideticd 0.0.1` print. **Closed by this PR**: `-version` flag added; Makefile injects via `-ldflags "-X main.Version=$(git describe ...)"`.
2. Real-world capture against `~/.claude/projects/` hit `MaxPayloadBytes=1048576` (1 MiB) cap on 8+ engrams within the first 1s of darwin run; observed sizes 1.18 MiB, 2.21 MiB, 2.41 MiB — graceful per-engram skip (not fatal). **Partially closed by this PR**: cap raised from 1 MiB to 8 MiB (3.3× headroom over largest observed); capture layer now pre-filters at parse boundary + tracks `skippedPayloadTooLarge` counter; log-line surfaces skip count per-file per-batch. Chunked-capture (true fix for arbitrarily-large records) was originally deferred to W2 and ultimately landed earlier — see ADR-018 below.
3. Default DB path `$HOME/.eidetic/engrams.db`; capture auto-probes `~/.claude/projects`, `~/.cowork/sessions`, `~/.config/Cursor/User/workspaceStorage` — document for install/uninstall.

**Posture:** spike work stays spike-only; no production commits from the spike worktree itself. The 3 follow-on items (`-version`, MaxPayloadBytes tuning, default-paths docs) land via standard PR on top of `main`. Meta-lesson recorded for future cross-compile gates: static citation ≠ closure on numeric caps/thresholds; runtime evidence must be reconciled before audit close.

**Reference:** v0.0.2 release tag at commit `26215fc1`; ADR-016 (cross-compile-friendly modernc default — see project-wide decision archive).

---

## ADR-018 (2026-05-14): Chunked-capture for arbitrarily-large records — meta-encoded, no schema change

**Decision:** JSONL parser splits lines exceeding `chunkPayloadBudget` (7 MiB; sized below `store.MaxPayloadBytes` 8 MiB to leave room for meta + wire overhead) into N chunks. Each chunk is a separate engram row sharing a `chunk_id` (sha256-prefix of full payload, 16 hex chars) + `chunk_seq` (0-indexed) + `chunk_total` (count) encoded in the engram's `meta` JSON. Records ≤ budget emit 1 engram with no `chunk_*` meta fields (backward-compat with pre-chunking consumers). Consumer-side reassembly: group by `chunk_id`, sort by `chunk_seq`, concatenate `payload`. Schema unchanged.

**Reason:** v0.0.3 raised `MaxPayloadBytes` 1 MiB → 8 MiB after the runtime spike measured real Claude session-JSONL chunks at 1.18 / 2.21 / 2.41 MiB (ADR-017). The 8 MiB cap covered observed sizes with 3.3× headroom, but a single 50 MiB Cursor JSON state file (or any future surface producing arbitrarily-large records) would still get dropped silently in the capture-side skip-counter. Chunked-capture eliminates that hard wall: daemon now handles records of any size, bounded only by SQLite per-row limits (~1 GB practical) + writer-pool throughput. The 7 MiB budget per chunk leaves ~1 MiB of headroom for meta JSON + HTTP-wire overhead so a chunk never approaches the per-engram cap.

**Why meta-encoded vs schema change:** spec § 1 binds the schema. Adding `chunk_seq INTEGER, chunk_total INTEGER, chunk_parent_id INTEGER` columns would require an ADR amendment + migration. Encoding in the existing `meta` TEXT column is spec-respecting + works with the existing API surface unchanged. Cost: client-side reassembly logic required for >cap records (single-record consumers see N rows for one logical line). Pay: zero schema migration, zero API breakage.

**Why sha256-prefix vs UUID for `chunk_id`:** sha256 of the full payload is **idempotent on resume**. If the daemon restarts mid-batch and re-parses the same line, it produces the same `chunk_id` — consumers can dedupe by `chunk_id` without timing-coordination tricks. UUIDs would re-issue different IDs on re-parse, causing dupes at the consumer.

**Posture:** chunked-capture lands in `parser_jsonl.go`; `parser_cursor.go` (whole-file replace + SHA-256 dedup) doesn't need it (Cursor surface uses different overflow semantics — file-size-bounded by editor, not request-bounded). The watcher's `SkippedPayloadTooLarge()` counter remains as defense-in-depth (any chunk somehow still exceeding `store.MaxPayloadBytes` → skip + count); on normal chunked records the counter stays at 0 because chunks fit by construction. CHANGELOG.md "W2+" candidate "Chunked-capture for arbitrarily-large records (replaces the 8 MiB cap as a hard wall)" pulled forward Day 4.

**Reference:** PR with this commit, ADR-017 (the cap-tuning that this supersedes for the >cap case); 6 new tests in `internal/capture/parser_chunked_test.go` (normal-line-no-chunking, oversized-line-splits, chunk-ID-idempotent, mixed-sizes-in-one-file, reassembly-roundtrip, state-offset-advances-past-oversized) + 1 sanity gate (chunk-budget < store cap with ≥ 256 KiB headroom) + updated `oversized_skip_test.go` (renamed `TestWatcherOversizedPayloadCounted` → `TestWatcherOversizedPayloadChunked` reflecting new contract).
