# Eidetic Works Daemon W1 — Implementation Plan (file-by-file scaffold blueprint)

**Date:** 2026-05-12 (Day 2 EOD; Day 3 starts tomorrow)
**Status:** Pre-Day-3 scaffold blueprint (NOT executed yet — read by Day-3 morning before any Go is written)
**Owner:** Daemon repo owner (with sub-agent delegation for bulk Go code)
**Authority anchors:** spec `eidetic-daemon-w1.md` @ `b3caa126`; ADR-012 (Go feasibility GO); ADR-014 (5 carry-forward patterns + 3 measurement gaps); ADR-016 (modernc.org/sqlite + Tauri sidecar + spawn-at-startup)
**Repo target:** `eidetic-works/eidetic-daemon` (NEW GitHub repo; operator-keyboard create)

---

## 0. Pre-Day-3 operator action (one item)

Create the GitHub repo `eidetic-works/eidetic-daemon` (private at first; flip to public on Day-7 release per spec § 8). Empty repo is fine — the implementer will push the initial scaffold.

Everything below is implementer + sub-agent work once the repo exists.

---

## 1. Package layout (mainline rebuild — NOT spike-code-harvest)

```
eidetic-daemon/
├── cmd/
│   └── eideticd/
│       └── main.go                  # entry point, wires everything
├── internal/
│   ├── store/
│   │   ├── store.go                 # SQLite wrapper, prepared stmts, conn pools
│   │   ├── store_test.go            # schema migration, WAL assertion, EXPLAIN check
│   │   └── schema.sql               # exact DDL from spec §2.2
│   ├── capture/
│   │   ├── watcher.go               # fsnotify multiplexer
│   │   ├── parser_cursor.go         # incremental Cursor JSONL parser
│   │   ├── parser_cowork.go         # incremental Cowork file parser
│   │   ├── parser_claude_code.go    # incremental Claude Code session parser
│   │   ├── parser_test.go           # per-spec edge cases (empty/partial/malformed/encoding)
│   │   └── state.go                 # state.json offset tracking
│   ├── api/
│   │   ├── server.go                # UDS listener + http handler
│   │   ├── server_test.go           # request shape + JSON marshal correctness
│   │   └── routes.go                # GET /engrams handler
│   └── engram/
│       └── engram.go                # type definition (id, surface, ts, payload, meta)
├── bench/
│   ├── bench_retrieve_test.go       # P95 retrieval (closes ADR-014 gap re-validates)
│   ├── bench_write_test.go          # P95 write under 100req/s burst (ADR-014 gap #1)
│   ├── bench_concurrent_test.go     # P95 with 5 readers + 1 writer (ADR-014 gap #3)
│   └── seed.go                      # 10K-engram synthetic fixture generator
├── scripts/
│   ├── install.sh                   # curl-pipe-sh installer (Day-7 deliverable)
│   ├── launchd.plist                # macOS service manager
│   └── systemd.service              # Linux service manager
├── .github/
│   └── workflows/
│       ├── ci.yml                   # build + test + bench-fail-on-regression
│       └── release.yml              # cross-compile + tarball + GitHub release
├── go.mod                           # Go 1.23+; modernc.org/sqlite; fsnotify
├── go.sum
├── Makefile                         # build, test, cross-compile, bench targets
├── README.md                        # one-line install + Latency section + spec link
├── LICENSE                          # operator decision (default: MIT or Apache-2.0)
└── .gitignore
```

**File count:** ~25 files. Pure scaffolding (~10) + production code (~10) + tests (~5). Bulk delegation candidates: parsers + tests (sub-agent); production hot path (implementer directly).

---

## 2. go.mod + dependency choices (locked by ADR-016)

```go
module github.com/eidetic-works/eidetic-daemon

go 1.23

require (
    modernc.org/sqlite v1.34.0     // ADR-016: pure-Go, cross-compile-clean default
    github.com/fsnotify/fsnotify v1.7.0
)

// NO mattn/go-sqlite3 — ADR-016 rejected as default due to silent cross-compile
// failure mode (memory feedback_cgo_cross_compile_silent_failure).
// Reintroduce only if W1 measures show modernc P95 fails 100ms SLO under
// production load (we have 252× margin per ADR-016, so this won't happen).
```

**No CGO.** Build is pure Go, single static binary, cross-compile to all 3 targets in <10sec each. Per ADR-016 spike measurements.

**No MCP framing library in W1** per spec §7 Open Q #1 + review guardrail #4. Daemon ships UDS-only with HTTP framing. MCP bridge is post-W1 (Python wrapper around UDS API, lives in separate repo).

---

## 3. Schema (verbatim from spec §2.2 + ADR-014 pragmas)

`internal/store/schema.sql`:

```sql
CREATE TABLE IF NOT EXISTS engrams (
  id      INTEGER PRIMARY KEY,
  surface TEXT    NOT NULL,
  ts      INTEGER NOT NULL,         -- unix epoch nanoseconds
  payload TEXT    NOT NULL,
  meta    TEXT                      -- JSON blob
);
CREATE INDEX IF NOT EXISTS idx_surface_ts ON engrams(surface, ts DESC);
```

Open-string pragmas applied at `sql.Open` per ADR-014 pattern #1:

```
file:%s?_journal=WAL&_synchronous=NORMAL&_busy_timeout=5000&cache=shared
```

(Substitute `%s` with `~/.eidetic/engrams.db` resolved at runtime via `os.UserHomeDir()` + `$EIDETIC_DATA_DIR` override.)

---

## 4. Connection pool shape (ADR-014 pattern #3)

`internal/store/store.go` opens TWO `sql.DB` instances:

- **Writer:** `sql.Open("sqlite", file:...?...)` + `db.SetMaxOpenConns(1)`. Single goroutine owns all INSERTs. Eliminates "database is locked" cascade.
- **Reader pool:** `sql.Open("sqlite", file:...?mode=ro")` + `db.SetMaxOpenConns(8)`. Read-only, reader pool sized for 5 surfaces + headroom.

API layer uses reader pool; capture layer uses writer.

Both share the same DB file (SQLite WAL allows reader/writer concurrency).

---

## 5. Daemon lifecycle (ADR-016 spawn-at-app-startup mandate)

`cmd/eideticd/main.go` shape:

```go
func main() {
    // 1. resolve data dir (env override OR ~/.eidetic/)
    // 2. open store (writer + reader pool) — INCURS modernc 1.75s cold-init
    //    (acceptable here because daemon launches at app startup, not on user request)
    // 3. start fsnotify watchers (3 surfaces)
    // 4. start UDS listener
    // 5. signal handlers (SIGTERM clean shutdown, SIGHUP log rotation)
    // 6. block on context Done
}
```

Service manager files (`scripts/launchd.plist`, `scripts/systemd.service`) configure:
- `KeepAlive=true` / `Restart=always`
- `RunAtLoad=true` / `WantedBy=default.target`
- This ensures daemon spawns when user logs in, NOT when first request arrives. Absorbs the 1.75s modernc cold-init behind login UI.

---

## 6. fsnotify capture path (spec §2.3)

`internal/capture/watcher.go`:

```go
// One fsnotify.Watcher; Add() each of 3 surface roots (recursive walk on init).
// Event loop: on WRITE/CREATE/RENAME, debounce 10ms (event coalescing per
// ADR-013 concern #2); dispatch to per-surface parser.
//
// Parsers tail their file from last-known offset stored in
// ~/.eidetic/state.json. Single transaction per file-event-batch.
//
// Latency target: <50ms file-write → row-committed. Validated by
// mirror_test.go integration test in CI.
```

Per ADR-013 concern #2 + spec §7 Open Q #2: macOS `fsnotify` event coalescing + editor-replace-via-temp+rename pattern need real-fixture validation BEFORE Day 4 capture-correctness gate. Day 3 includes a small reproducer that asserts Cursor-shape JSONL writes and Claude Code session writes both produce capture events with no row loss.

---

## 7. Retrieval API (spec §2.4)

`internal/api/server.go`:

```go
// UDS at /tmp/eidetic-daemon.sock (Mac) / /var/run/eidetic.sock (Linux).
// TCP fallback on 127.0.0.1:9876 if EIDETIC_TCP=1.
//
// Single endpoint:
//   GET /engrams?surface=X&limit=N&since=unix-ns
//   → 200 JSON array of engrams
//
// Hot path uses prepared statement, scan, json.Marshal, write. ADR-014 pattern
// confirmed: Go runtime (json.Marshal + net/http allocation) is bottleneck at
// this scale, NOT SQLite. No further serialization tuning in W1.
```

Query (verbatim from spec §2.4):

```sql
SELECT id, surface, ts, payload, meta
FROM engrams
WHERE surface = ? AND (? IS NULL OR ts > ?)
ORDER BY ts DESC
LIMIT ?
```

`limit` defaults to 50, capped at 500. `since` optional.

---

## 8. Cross-compile build flow (ADR-016 validated)

`Makefile`:

```makefile
.PHONY: build build-all test bench

build:
	go build -o bin/eideticd ./cmd/eideticd

build-darwin-arm64:
	GOOS=darwin GOARCH=arm64 go build -o dist/eideticd-darwin-arm64 ./cmd/eideticd

build-linux-amd64:
	GOOS=linux GOARCH=amd64 go build -o dist/eideticd-linux-amd64 ./cmd/eideticd

build-windows-amd64:
	GOOS=windows GOARCH=amd64 go build -o dist/eideticd-windows-amd64.exe ./cmd/eideticd

build-all: build-darwin-arm64 build-linux-amd64 build-windows-amd64
	@./scripts/verify-cross-compile.sh   # file + size check per memory feedback_cgo_cross_compile_silent_failure

test:
	go test ./...

bench:
	go test -bench=. -benchtime=10s ./bench
```

Per ADR-016: each cross-compile target produces a 9MB single static binary in <10sec. No external toolchain required (modernc is pure Go).

`scripts/verify-cross-compile.sh` encodes the verification gate from `feedback_cgo_cross_compile_silent_failure`:

```sh
for target in darwin-arm64 linux-amd64 windows-amd64; do
    binary="dist/eideticd-${target}"
    [ "${target}" = "windows-amd64" ] && binary="${binary}.exe"
    file "$binary" || { echo "FAIL: ${binary} missing"; exit 1; }
    size=$(stat -f%z "$binary" 2>/dev/null || stat -c%s "$binary")
    [ "$size" -gt 5000000 ] || { echo "FAIL: ${binary} suspiciously small ($size bytes)"; exit 1; }
done
```

(File-presence + size-floor; smoke-test via QEMU/container is a Day-4+ enhancement.)

---

## 9. CI gates (`.github/workflows/ci.yml`)

```yaml
on: [push, pull_request]
jobs:
  test-and-bench:
    runs-on: macos-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.23' }
      - run: make test
      - run: make build-all                # cross-compile all 3 targets
      - run: ./scripts/verify-cross-compile.sh
      - run: make bench
      - name: Fail if P95 >100ms
        run: |
          # bench output captured to file; parse + fail if SLO violated
          # encodes spec §3 ship-gate
```

The bench-gate is NON-NEGOTIABLE per spec §3 ("Daemon does not ship if P95 >100ms"). PLAN.md Risk #12 mitigation depends on this gate firing.

---

## 10. ADR-014 measurement gap closure (W1 deliverables)

ADR-014 flagged 3 gaps the Pre-Day-1 spike didn't cover. State now:

| Gap | ADR-014 spec | Status as of Day 2 EOD | W1 plan |
|---|---|---|---|
| **A. Write P95** | <50ms under 100 req/s sustained 60s | OPEN | `bench/bench_write_test.go` Day 4. Drives `internal/store` writer-pool decisions. |
| **B. Cold P95** | <500ms first 10 reqs cold-start | **CLOSED 2026-05-12 by ADR-016 spike** (1.75s on modernc — exceeds 500ms but mitigated by spawn-at-app-startup mandate) | None — covered by ADR-016 mitigation. |
| **C. Concurrent P95** | <100ms with 5 readers + 1 writer over 60s | OPEN | `bench/bench_concurrent_test.go` Day 4. Drives reader-pool size decisions. |

A + C remain → Move B (next section) drafts a follow-on [SPIKE-DIRECTIVE] for these, gated to fire when daemon scaffold has insert+retrieve handlers wired so the spike has real code to bench against.

---

## 11. Day 3 → Day 7 sequencing

Activity-gated, not calendar (phases fire on observable preconditions, not dates):

| Phase | Gate (fires when) | Deliverable | Owner |
|---|---|---|---|
| **Phase 0** | GitHub repo `eidetic-works/eidetic-daemon` exists | Initial scaffold push (sections 1+2 of this doc — files, go.mod, schema.sql, README skeleton) | implementer + sub-agent (bulk file creation) |
| **Phase 1** | Phase 0 done | `internal/store` complete + `store_test.go` green (schema migration, WAL assertion, EXPLAIN check) | implementer directly (hot path) |
| **Phase 2** | Phase 1 done | `internal/api` complete + `server_test.go` green (request shape + JSON correctness) + first end-to-end `curl --unix-socket` smoke | implementer directly |
| **Phase 3** | Phase 2 done | `internal/capture` 3 parsers + `parser_test.go` (per-spec edge cases) | sub-agent (charter: parsers per spec, no architectural decisions) |
| **Phase 4** | Phase 3 done | Integration: `mirror_test.go` + `concurrency_test.go`. Real fixture validation (Cursor JSONL + Claude Code JSONL writes via fsnotify, no row loss). | implementer directly (per review concern #2 — capture-side correctness is the real W1 risk, not retrieval P95) |
| **Phase 5** | Phase 4 green AND spike-result on gaps A+C in hand | Bench gates wired in CI; benchmark numbers in README §Latency | implementer + sub-agent (CI YAML) |
| **Phase 6** | Phase 5 green | Cross-compile artifacts + install.sh + launchd/systemd files | sub-agent |
| **Phase 7** | Phase 6 green AND demo recorded AND ship-review clean | GitHub release + distribution post | implementer + operator (release click) |

Calendar sketch (per PLAN.md Day-3→Day-7) maps roughly to Phase 0 → Phase 1+2 → Phase 3 → Phase 4 → Phase 5 → Phase 6 → Phase 7. If any phase slips, ADR-008 triage rule fires at the Phase-4-equivalent (capture correctness gate) — defer Phase 6 (MCP bridge / marketplace) and Phase 7 (investor pitch deck) to W2 if not green.

---

## 12. Cut-line (refuse if it grows beyond)

Per spec §1 + review guardrail #5 (triage rule pre-committed):

- **NO** Cloudflare D1+R2+Workers wiring in W1 (W2)
- **NO** Tauri UI in W1 (W2-3; ADR-016 validated the pattern but UI shell is post-W1)
- **NO** MCP bridge in W1 binary (Day-6 stretch was already de-scoped per review guardrail #3)
- **NO** Compliance daemon (W2)
- **NO** Stripe / billing (W4)
- **NO** Tag-filter retrieval beyond the canonical `(surface, ts DESC)` shape (ADR-014 architectural surprise — would require schema change; W2 if user signals)
- **NO** FTS5, vector embeddings, denormalized topic columns (W2+)

If a Day-3+ commit touches any of the above, refuse + escalate to operator with the spec-§1-violation flag.

---

## 13. References

- `docs/specs/eidetic-daemon-w1.md` @ `b3caa126` — the binding spec
- `DECISIONS.md` ADR-012 — Pre-Day-1 Go feasibility (P95 baseline)
- `DECISIONS.md` ADR-013 — 5-guardrail conditional GO (review)
- `DECISIONS.md` ADR-014 — 5 carry-forward patterns + 3 measurement gaps
- `DECISIONS.md` ADR-016 — Tauri sidecar + modernc.org/sqlite + spawn-at-app-startup
- Cross-compile gate: verify produced binary with file + size + runtime smoke (CGO-silent-strip lesson)
- Activity-gated sequencing: phases fire on observable preconditions, not calendar dates
- `PLAN.md` § 7-Day Kickoff Sprint — original calendar (read activities, not dates)
- `PLAN.md` Risks #4, #11, #12, #13 — spec §4 mitigations

> **Note on ADR refs:** ADRs 011-016 governed broader cross-component decisions and live in the project archive (not surfaced in this public repo). The public `DECISIONS.md` log starts at ADR-017. References to ADR-011/-012/-013/-014/-016 above are retained for historical traceability of the Day-2 blueprint.
- ADR-008 § triage rule — capacity-collapse pre-mortem trigger at Phase-4-equivalent
