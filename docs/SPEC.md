# Eidetic Works Daemon — W1 Spec

**Date:** 2026-05-10 (Day 0)
**Status:** Draft for Pre-Day-1 review (cc-peer ADR-013; cc-tb ADR-014)
**Owner:** cc-main
**References:** PLAN.md § 7-Day Kickoff Sprint (line 472), § Risks (line 584), § Critical Files (line 564); DECISIONS.md ADR-002, ADR-005, ADR-008, ADR-012

---

## 1. Scope (binding)

W1 ships a single Go binary that does **three things only**:

1. **Engram capture** — `fsnotify` watchers on Cursor session JSONL, Cowork files, Claude Code session dir. New text → engram row in <50ms of file-write.
2. **Engram retrieval** — Local Unix socket / TCP-localhost API: `GET /engrams?surface=X&limit=N` → JSON array. **P95 <100ms SLO** on 10K-row engrams table.
3. **Multi-surface mirror** — All three surfaces feed one SQLite-WAL store. Single canonical engrams table. B-tree index on `(surface, ts DESC)` is the retrieval hot path.

**Explicitly NOT in W1** (deferred to W2-W7 per PLAN.md § 7-Day Kickoff Sprint and § Week 2-8 Arc):
- ❌ Compliance daemon (W2)
- ❌ Swarm orchestration (W3)
- ❌ Pro tier billing + Stripe (W4)
- ❌ Tier routing daemon (W5)
- ❌ TB Personal AI training pipeline (W6-7)
- ❌ Tauri desktop UI (W2-3)
- ❌ Cloudflare D1+R2+Workers cloud sync (W2-3)
- ❌ Audit log mirror to R2 (W4+)
- ❌ Any consumer mobile / browser code (gated by Day-30 decision)
- ❌ MCP bridge to existing Python `nucleus-mcp` (Day 6 stretch goal in PLAN.md, but kept out of binary v0; see § 7 Open Questions)

This narrow-scope discipline is the entire reason for the Go rewrite. Per PLAN.md Risk #13 ("the architectural rewrite itself becomes a 6-week rabbit hole"): if anything in this section grows during W1, name the escape and refuse the scope creep.

---

## 2. Architecture

### 2.1 Process model

Single long-running Go process: `eidetic-daemon`. Started via launchd (macOS) / systemd-user (Linux). No multi-process supervision in W1; daemon survives via OS-level service manager.

### 2.2 Storage

**Database file:** `~/.eidetic/engrams.db` (XDG-overridable via `$EIDETIC_DATA_DIR`).
**Driver:** `github.com/mattn/go-sqlite3` (CGO).
**Mode:** WAL mandatory (`PRAGMA journal_mode=WAL`); `synchronous=NORMAL`; `cache=shared`.
**Schema:**

```sql
CREATE TABLE IF NOT EXISTS engrams (
  id      INTEGER PRIMARY KEY,
  surface TEXT    NOT NULL,
  ts      INTEGER NOT NULL,        -- unix epoch nanoseconds
  payload TEXT    NOT NULL,        -- raw text content
  meta    TEXT                     -- JSON: source path, file offset, parser version
);
CREATE INDEX IF NOT EXISTS idx_surface_ts ON engrams(surface, ts DESC);
```

Schema is intentionally minimal. Per ADR-012 (cc-tb spike), this exact schema with this exact index returns P95 ~80-110µs on 10K rows. No additional columns or tables in W1. FTS5, vector embeddings, denormalized topic columns are W2+.

### 2.3 Capture path (mirror)

For each surface, an `fsnotify` watcher monitors the relevant filesystem location:

| Surface | Watch path |
|---|---|
| `cursor` | `~/Library/Application Support/Cursor/User/workspaceStorage/**/*.json` (macOS); equivalent on Linux |
| `cowork` | `$HOME/.cowork/sessions/**/*.json` |
| `claude_code` | `~/.claude/projects/**/*.jsonl` |

On `WRITE`/`CREATE`/`RENAME` events, the watcher invokes a per-surface incremental parser. Parser tails the file from last-known offset (stored in `.eidetic/state.json`), extracts new engram-shaped records, INSERTs them in a single transaction.

**Latency budget for capture:** <50ms from file-write to row-committed. Validated by integration test in CI.

### 2.4 Retrieve path (read API)

Local Unix domain socket (UDS) at `/tmp/eidetic-daemon.sock` (Mac) / `/var/run/eidetic.sock` (Linux), TCP fallback on `127.0.0.1:9876` if `EIDETIC_TCP=1`.

**One endpoint:**

```
GET /engrams?surface=<string>&limit=<int>&since=<unix-ns>
→ 200 OK
Content-Type: application/json

[{"id":..., "surface":..., "ts":..., "payload":..., "meta":...}, ...]
```

`limit` defaults to 50, capped at 500. `since` is optional (default = no lower bound).

Query path:
```sql
SELECT id, surface, ts, payload, meta
FROM engrams
WHERE surface = ? AND (? IS NULL OR ts > ?)
ORDER BY ts DESC
LIMIT ?
```

Hot path uses prepared statement; row scan → JSON marshal → response write. Per ADR-012 spike, this clears 100ms by ~150×.

---

## 3. P95 SLO

**Target:** P95 retrieval latency <100ms on a 10K-row engrams table, measured under 1000-request synthetic load.

**Measurement protocol:**
1. Seed 10K synthetic engrams (varied text 50-2000 chars, timestamps spanning 6 weeks, surface tags `{cursor,cowork,claude_code,windsurf,antigravity}` distributed roughly uniformly).
2. Warmup: 50 requests, results discarded.
3. Measure: 1000 requests, record P50/P95/P99/max. Repeat 3 times for stability.
4. Publish to `README.md` § Latency: most recent number, dated.

**Acceptance for Day 7 ship:** P95 ≤100ms across 3 consecutive runs. If P95 exceeds 100ms on any run, daemon **does not ship**. Distribution Officer **does not fire** the demo post until benchmark passes (per PLAN.md Risk #12).

**Reference numbers from ADR-012 (cc-tb spike, 10K rows, indexed retrieval, 1000 req post-warmup):**

| Metric | Run 1 | Run 2 | Run 3 |
|---|---|---|---|
| P50 | 399µs | 386µs | 494µs |
| **P95** | **609µs** | **578µs** | **878µs** |
| P99 | 742µs | 712µs | 1.07ms |
| Max | 826µs | 817µs | 2.49ms |

These are spike-quality numbers; production W1 may regress slightly with capture-side concurrency + JSON column meta scans. Headroom of 110× absorbs reasonable regression.

---

## 4. Risk anchors (mapped to PLAN.md § Risks)

| Risk # | What | Mitigation in this spec |
|---|---|---|
| **#11** | Lokesh-as-reviewer of unfamiliar Go | Every W1 PR ≤200 LOC. cc-peer reviews before merge (§ Build & Ship below). No auto-merge during W1. |
| **#12** | Latency SLO becomes optional and slips | Benchmark gates the ship. README publishes the number. Distribution Officer post is gated on the benchmark passing. |
| **#13** | Architectural rewrite becomes 6-week rabbit hole | Section 1 § Scope is binding. Anything outside the 3-thing list is W2+. Cloud sync, Tauri UI, MCP bridge **are not in W1**. |
| **#4** | 6-8 week build slips to 12-16 weeks | Day 4 latency benchmark is the hard checkpoint. If P95 not green by Day 4 EOD, defer Day 6 (MCP bridge) and Day 7 (marketplace submission); preserve only daemon binary + benchmark per ADR-008 § triage rule. |
| **#11** (CGO) | Cross-compile darwin-arm64 → linux-amd64 with CGO breaks | Day 3 deliverable: build linux-amd64 in CI before any feature work. If broken, drop linux for Day-7 release; ship darwin-arm64 only. |

---

## 5. Test plan

**Unit:**
- `engram_parser_test.go` — per-surface parsers handle: empty file, partial line, multi-record append, malformed JSON, encoding edge cases.
- `db_test.go` — schema migration idempotency, WAL mode assertion, index used in EXPLAIN QUERY PLAN.

**Integration:**
- `mirror_test.go` — write to fixture file under temp watch path; assert engram row in DB within 50ms; assert retrieve API returns it.
- `concurrency_test.go` — 3 surfaces firing simultaneously; assert no row loss, no transaction conflict.

**Benchmark (`bench_test.go`):**
- Seed 10K rows; 1000-request P95 retrieval; assert <100ms. **Fail-the-build threshold.**

**Manual (Day 7 demo acceptance):**
- Open Cursor, type a question; daemon captures within 50ms (visible via `tail -f` of state.json).
- `curl --unix-socket /tmp/eidetic-daemon.sock http://localhost/engrams?surface=cursor&limit=5` returns the engram in <100ms.
- README `## Latency` shows the latest P95 number.

---

## 6. Build & Ship

**Repo:** `eidetic-works/eidetic-daemon` (new, separate from `mcp-server-nucleus`).
**Branch model:** trunk-based. `main` is shippable. Feature branches: `feat/<short-name>`. PRs reviewed by cc-peer before merge per BOOTSTRAP § Sub-agent registry.

**Build commands:**
```sh
CGO_ENABLED=1 go build -o bin/eidetic-daemon ./cmd/daemon
# Cross: requires C toolchain for target. Day-3 spike to confirm.
GOOS=linux GOARCH=amd64 CC=x86_64-linux-musl-cc CGO_ENABLED=1 go build ...
```

**Distribution:**
- Day 7: GitHub release, `.tar.gz` per platform (darwin-arm64 + linux-amd64).
- One-line install: `curl -fsSL https://eidetic.works/install.sh | sh` (script downloads release, places binary in `/usr/local/bin`, registers launchd service).
- No Homebrew tap in W1 (W2 work).

---

## 7. Open questions (Day 1 to resolve)

1. **MCP framing library:** `mark3labs/mcp-go` vs hand-rolled JSON-RPC stdio. cc-tb spike used inline JSON; real W1 needs framing. Decide Day 1 morning. Bench JSON-RPC overhead before committing.
2. **fsnotify edge cases on macOS:** event coalescing under burst writes; file replace via temp+rename pattern (some editors). Validate against real Cursor + Claude Code session writes before Day 4.
3. **Cross-compile darwin → linux with CGO:** confirm build works on Day 3. If broken, accept darwin-only Day-7 release; flag as known limitation in README.
4. **Capture parser version field:** parser changes mid-W1 require backfill semantics. Reserve `meta.parser_version` field; spec backfill rule W2 (out of W1 scope).
5. **MCP bridge to existing `mcp-server-nucleus`:** PLAN.md Day 6 mentions this. Per § 1 Scope, it is **out of W1 binary v0**. cc-main may write a separate Python wrapper that calls the daemon's UDS API as a Day-6 stretch, but the daemon itself ships independently.

---

## 8. Day 7 demo acceptance criteria

The W1 ship is the binary on GitHub release with this exact demo working:

1. User runs `curl -fsSL https://eidetic.works/install.sh | sh` on a fresh laptop. Daemon installs and starts.
2. User opens Cursor, types: "What did the last benchmark say?"
3. Within 50ms of Cursor's session JSONL write, engram row appears in `~/.eidetic/engrams.db` (verifiable via `sqlite3 ~/.eidetic/engrams.db "SELECT * FROM engrams ORDER BY ts DESC LIMIT 1;"`).
4. User runs `curl --unix-socket /tmp/eidetic-daemon.sock 'http://localhost/engrams?surface=cursor&limit=5'`. Returns the engram + 4 prior, all with `ts` populated. **Round-trip <100ms.**
5. README's `## Latency` section shows the most recent benchmark number (e.g., "P95 retrieval 0.6ms on 10K engrams, M2 Air, 2026-05-17").
6. Distribution Officer fires the demo post linking to the GitHub release + this README section.

---

## References

- `PLAN.md` § 7-Day Kickoff Sprint (line ~472) — Day 1-7 calendar
- `PLAN.md` § Risks (line ~584) — #11 Go-as-reviewer, #12 SLO discipline, #13 rabbit-hole, #4 timeline slip
- `PLAN.md` § Critical Files (line ~564) — existing `mcp-server-nucleus` substrate to bridge in Day 6 (out of binary scope)
- `DECISIONS.md` ADR-002 — Go for daemon hot path; Bun+TS fallback documented but parked
- `DECISIONS.md` ADR-005 — Cloudflare D1+R2+Workers (W2+ scope, not in W1 binary)
- `DECISIONS.md` ADR-008 — capacity-collapse pre-mortem; Day-4 triage rule
- `DECISIONS.md` ADR-012 — cc-tb spike GO verdict (P95 0.6-0.9ms, no Bun fallback)
- `STATUS.md` § This week's targets — Pre-W1 → W1 deliverables
- `BOOTSTRAP.md` § Refusal protocol — operator override scope
- `.claude/worktrees/agent-a99923c8258077450/spike/` — cc-tb spike artifact (scratch; mine for SQLite pragma + index pattern + CGO build flow only; do not harvest spike code into mainline directly per ADR-012 reason ¶)
