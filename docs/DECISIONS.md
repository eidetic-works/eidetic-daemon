# Architecture Decision Records — eidetic-daemon

This file is the append-only ADR log scoped to **this repo's** decisions (W1 daemon + later phases). Cross-repo ADRs (ADR-001 through ADR-016) live in `mcp-server-nucleus/DECISIONS.md`; references inline.

ADR-001 through ADR-016: see `mcp-server-nucleus/DECISIONS.md` (entity-wide).

---

## ADR-017 (2026-05-13): v0.0.2 cross-compile runtime smoke — darwin + linux validated; windows deferred

**Decision:** v0.0.2 cross-compile assets validated at runtime on darwin-arm64 (native M2) and linux-amd64 (docker ubuntu:22.04). Windows-amd64 runtime verification deferred — Wine absent on cc-tb spike host — but static analysis ruled out the CGO-silent-strip pattern that ADR-016 + `feedback_cgo_cross_compile_silent_failure.md` guard against. Distribution claim "cross-platform binaries available" is honest for darwin + linux as of v0.0.2; Windows requires a separate runtime gate before being claimed.

**Reason:** cc-tb runtime-smoke spike (relay `20260513_140937_e5468011`, 50 min runtime, worktree-isolated). Darwin native run: socket created, `/healthz` returned `{"status":"ok"}` HTTP 200, no startup errors. Linux container run (ubuntu:22.04, --platform linux/amd64): identical result. Windows binary static analysis: 8977 `modernc.org/*` symbols, 0 `mattn/go-sqlite3` references, `EIDETIC_TCP` env-var symbol present, `127.0.0.1:9876` default TCP address compiled in, flag help text `TCP listen address (overrides default; opt-in via EIDETIC_TCP=1)` compiled in, `/healthz` handler present. The silent-CGO-strip failure mode (build succeeds, binary stripped, crashes at runtime) is ruled out by modernc-symbol presence — pure-Go SQLite is statically linked. Runtime gate for Windows can be satisfied by GH-Actions ubuntu+wine matrix step (post-CI-billing-reset 2026-05-19+) or operator manual run on Windows host.

**Additional findings worth recording (out of spike scope but operator-relevant):**

1. v0.0.2 binaries had no `-version` flag — spike directive expected `eideticd 0.0.1` print. **Closed by this PR**: `-version` flag added; Makefile injects via `-ldflags "-X main.Version=$(git describe ...)"`.
2. Real-world capture against `~/.claude/projects/` hit `MaxPayloadBytes=1048576` (1 MiB) cap on 8+ engrams within the first 1s of darwin run; observed sizes 1.18 MiB, 2.21 MiB, 2.41 MiB — graceful per-engram skip (not fatal). **Partially closed by this PR**: cap raised from 1 MiB to 8 MiB (3.3× headroom over largest observed); capture layer now pre-filters at parse boundary + tracks `skippedPayloadTooLarge` counter; log-line surfaces skip count per-file per-batch. Chunked-capture (true fix for arbitrarily-large records) deferred to W2 per cc-peer audit-amendment recommendation.
3. Default DB path `$HOME/.eidetic/engrams.db`; capture auto-probes `~/.claude/projects`, `~/.cowork/sessions`, `~/.config/Cursor/User/workspaceStorage` — document for install/uninstall.

**Posture:** cc-tb stays charter-bound to spike-only work; no production commits from this spike. Spike worktree is scratch and not harvested. The 3 follow-on items (`-version`, MaxPayloadBytes tuning, default-paths docs) land via cc-main PR on top of `main`. Memory rule `feedback_static_audit_needs_runtime_pair.md` (just-saved, indexed in mcp-server-nucleus MEMORY.md HARD RULES) codifies the meta-lesson: static citation ≠ closure on numeric caps/thresholds; runtime evidence must be reconciled before audit close.

**Reference:** Worktree `.claude/worktrees/agent-spike-v002-runtime-20260513-2005/spike-runtime-smoke/` (scratch); v0.0.2 release tag at commit `26215fc1`; ADR-016 (cross-compile-friendly modernc default); `feedback_cgo_cross_compile_silent_failure.md` (empirical lesson driving this gate); cc-peer audit-amendment `relay_20260513_225000_audit_amendment` (MaxPayloadBytes tuning-debt flag).
