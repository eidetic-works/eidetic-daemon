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

---

## ADR-019 (2026-05-18): Cloudflare cloud sync architecture — R2 file-level sync preferred over D1 row-level sync

**Decision:** W2 cloud sync ships as **R2 file-level sync** (periodic upload of `~/.eidetic/engrams.db`) rather than D1 row-level sync (one Cloudflare D1 API call per engram INSERT). D1 row-level sync is rejected as the W2 default on cost + complexity grounds. Architecture: a Cloudflare Worker receives a signed upload trigger from the daemon; daemon POSTs the SQLite file (or a WAL-based delta) to a pre-signed R2 URL; Worker stores at `engrams/<device-id>/engrams-<ts>.db`.

**Reason (spike 2026-05-18, against live Cloudflare pricing):**

| Architecture | Monthly cost at 50K engrams (existing backfill) | Monthly cost at 3K new engrams/day | Notes |
|---|---|---|---|
| D1 row-level sync | $0.05 backfill + $5.00/mo Workers base (paid plan required) | $5.00/mo (paid) | Free tier 100K writes/day is plenty for new writes, but **requires paid Workers plan to access D1** — $5/mo floor even at zero load |
| R2 file-level sync | ~$0.38/mo (25 MB × $0.015/GB) | same — storage grows slowly | **Stays in R2 free tier** for 25 MB dataset; Class A ops (PUT) < 1M/mo = free; Class B ops (GET) << 10M/mo = free |
| Hybrid (R2 + Workers relay) | ~$0.50-2.00/mo | same | Workers free tier: 100K req/day — adequate for sync triggers |

D1 requires a $5/mo Workers paid plan as a prerequisite, making it a $60/year mandatory cost even for zero-engram users. R2 stays within the free tier at current dataset size (~25 MB for 50K engrams at ~500 bytes/row average). At 10× growth (500K engrams, ~250 MB), R2 cost rises to ~$3.75/mo — still below the D1 paid-plan floor.

**D1 is not rejected forever:** if W2 reveals a strong need for server-side FTS or cross-device query (where R2 opaque-blob sync isn't sufficient), D1 row-level sync can be added as an opt-in paid feature. The daemon's capture path already produces a row-per-engram structure that maps cleanly to D1 INSERT. The decision here is: R2 is the free-by-default W2 path.

**Sync trigger design (deferred to W2 implementation ADR):** Daemon can trigger sync on (a) idle timer (e.g., no new engrams for 60s), (b) engram count watermark (e.g., every 1000 new engrams), or (c) manual `eideticd --sync-now`. The exact policy is not locked here — it fires when W2 sync implementation starts, where latency vs cost tradeoff can be measured on real daemon traffic.

**Privacy posture:** R2 blobs are encrypted at rest by default (Cloudflare-managed keys). Worker-side encryption with a user-supplied key is a W3+ enhancement — not required for W2 launch where the value proposition is "your engrams follow you across machines" not "your engrams are zero-knowledge to Cloudflare."

**Reference:** Cloudflare D1 pricing (developers.cloudflare.com/d1/platform/pricing/); R2 pricing (developers.cloudflare.com/r2/pricing/); Workers pricing (developers.cloudflare.com/workers/platform/pricing/); CHANGELOG.md W2+ candidate "Cloudflare D1+R2+Workers cloud sync (per ADR-005, encrypted blobs only)".

---

## ADR-020 (2026-05-20): Local-first AI privacy posture — every network call is opt-in

**Decision:** Codify the "engrams never leave your machine without explicit user action" promise as a checked-into-source contract. Every code path that touches the network must be (a) opt-in via user-side config and (b) enumerated in this ADR with a curl-reproducible test.

**Why:** PROMPT.md, the landing page, and `nucleus_ask`'s instructions all repeat this claim informally ("engrams never leave your machine"). As features grow (Pro sync, web dashboard, Team shared-context, future AI-recall variants), the promise needs a single source of truth that engineers can verify against — and customers can audit.

**The contract.** As of v0.0.43, eidetic-daemon makes outbound network connections in exactly these cases:

| Code path | Trigger | Opt-in via | What's sent | Recipient |
|---|---|---|---|---|
| `/sync` upload | sync.json present + 60-min timer or `--sync-now` | User drops sync.json | Encrypted-at-rest SQLite blob | Cloudflare R2 (user's bucket on Pro; user's own on BYOR2) |
| `/download` for restore | `eideticd --restore` invocation | User runs the flag | None (GET request) | Cloudflare R2 (same as above) |
| `/healthz` check | `eideticd --check` invocation | User runs the flag | None (HEAD request) | Configured Worker URL |
| GitHub releases poll | 24h timer (v0.0.37+) | Always on (telemetry-free; HEAD-only request to GH redirect) | None | `github.com/eidetic-works/eidetic-daemon/releases/latest` |
| `gumroad-kit-sync` ping | `pip install eidetic-mcp` post-install hook (when enabled) | User-side toggle (opt-out via `EIDETIC_PING=0`) | Single 204-target ping, no payload, no tracking ID | `gumroad-kit-sync.morning-lake-f944.workers.dev/ping` |

**Never sent over the network:** engram payloads (without sync.json), file paths, surface IDs, hostname, OS version, user identity, capture metadata, MCP tool call arguments, MCP tool call results, `nucleus_ask` questions or answers.

**What `nucleus_ask` does NOT do:** call an external LLM, call an embedding service, send the question or matching engrams to any third party. The "AI" is the host LLM (Claude Code / Cursor) reading the local engrams in-context. Daemon and MCP only do FTS retrieval + scaffolding.

**Auditable today.** Every claim above is reproducible:

```sh
# Capture every outbound socket the daemon opens for 60 seconds:
sudo tcpdump -i any -nn 'src host '"$(hostname -I | awk '{print $1}')"' and (tcp[tcpflags] & tcp-syn) != 0' -c 100 &
sleep 60; kill %1
# Expected output (without sync.json): 0 lines OR one HEAD to github.com once per 24h.
```

For the inverse (Pro sync ACTIVE), the same tcpdump shows exactly one POST per 60 min to the configured Worker URL — nothing else.

**Failure modes acknowledged:**
- If a user's Worker URL gets hijacked at DNS (compromised account) the daemon will upload to the attacker. Mitigation: bearer-token auth on the Worker side; users should rotate API keys via `wrangler kv:key delete` if credential leak is suspected.
- The 24h GitHub poll could be used to fingerprint deployment patterns. Mitigation: every install hits the same URL (no per-install identifier sent); poll is HEAD-only so the request body is empty.

**Posture lock-in.** This ADR is a HARD CONTRACT. Any future code adding outbound network calls must (a) add a row to the table above, (b) be opt-in or HEAD-only-with-no-data, (c) pass the tcpdump-audit test. PRs that add network calls without ADR amendment must be rejected.

**Reference:** PROMPT.md ("nucleus_ask: your engrams never leave your machine"); landing 5th bullet; SECURITY.md threat model; CHANGELOG.md v0.0.32 / v0.0.37 / v0.0.42 (the network-touching ships this audits).

---

## ADR-021 (2026-05-20): ADR-011 amendment — scoped TB-unpause carve-out (recall + training-from-engrams)

**Decision:** ADR-011 (90-day eidetic-works pivot, 2026-05-10 → 2026-08-08) is amended to allow scoped TB substrate work that COMPOUNDS with the shipped eidetic-works artifacts. Pure substrate work (PR #309 NL surface, watch-relay v0.4, identity-substrate research) remains paused until W4 bright-line clears.

**What's unpaused:**
- TB Personal AI Phase 4+ (recall + retrieval surfaces that pair with eidetic's nucleus_ask / nucleus_digest / nucleus_timeline)
- Training-from-engrams work — turning eidetic-captured engrams into TB training corpus (real "Work = Training" loop per `project_work_equals_training.md`)
- TB-side bridges that consume eidetic's HTTP endpoints (`/ask`, `/digest`, `/timeline`, `/export`) — TB as an eidetic-mcp client surface
- Sparring loops (TB writes instructions → eidetic captures Claude corrections → trains TB on the delta)

**What stays paused:**
- PR #309 v0.3 NL surface (snazzy-floating-blum.md plan) — pure substrate, no eidetic compounding axis
- watch-relay v0.4 worktree-per-session — substrate-only
- identity-substrate research (`.brain/research/2026-05-09_identity_substrate/`) — substrate-only
- nucleus-delegate v0.4+ — substrate-only
- Pure ADR drafts on substrate topics (defer per `feedback_compounding_shape.md`)

**Why this scope:** Eidetic Works compression (operator directive 2026-05-20) is substantially done — 24+ daemon versions, 14 integration surfaces, 7 Workers, 7 docs, ~60 tests added in ~24h. Distribution + customer purchases are the now-blocker, and those don't consume TB cycles. TB work that creates a compounding loop (eidetic engram → TB training corpus → better recall → more eidetic value) is positive-sum and merits resumption. Pure substrate stays gated by W4 bright-line per the original ADR-011 framing.

**Gate triggers that re-open the rest of substrate:**
- Day-60 hard-kill threshold from ADR-011 remains in force (2026-07-08): if <5 paid Pro Track A → broader pivot decision
- W4 bright-line (2026-06-08): if 5 paid Pro achieved → all substrate work green-lit
- New explicit operator ADR amendment

**Posture:** cc-tb resumes only on items in the "what's unpaused" list above. Any cc-tb work item must self-justify the eidetic-works compounding axis before starting; if no axis exists, item stays in queue. Audit via `nucleus_sync.search_threads from:cc_tb since:2026-05-20`.

**Reference:** cc-tb relay `20260520_000000_relay_tb_work_permission_request.json` (the request); ADR-011 (the original 90-day pause); `project_work_equals_training.md` (the compounding rationale this amendment activates); operator delegation 2026-05-20 ("Do as recommended on my direction items").
