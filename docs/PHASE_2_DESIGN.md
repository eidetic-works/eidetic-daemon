# Phase 2 Design — `internal/api` UDS server

**Status:** Design sketch (Phase 1 PR #1 still open as of 2026-05-12 EOD).
**Authority:** [SPEC.md § 2.4](SPEC.md#24-retrieve-path-read-api) + [IMPLEMENTATION_PLAN.md § 7](IMPLEMENTATION_PLAN.md).
**Implements:** Phase 2 of the 7-phase activity-gated sequencing.

---

## What Phase 2 ships

A single HTTP endpoint over a local Unix domain socket that serves engram retrieval from the `internal/store` reader pool. No authentication (local-only by socket file permissions). No write paths (capture layer is Phase 3). No MCP framing (out of W1 binary per [review guardrail #4](../docs/SPEC.md)).

```
GET unix:///tmp/eidetic-daemon.sock /engrams?surface=X&limit=N&since=unix-ns
→ 200 OK
  Content-Type: application/json
  [{"id":..., "surface":..., "ts":..., "payload":..., "meta":...}, ...]
```

TCP fallback (`127.0.0.1:9876`) when `EIDETIC_TCP=1` — testing + CI use this.

---

## Files

| File | Role | Estimated LOC |
|---|---|---|
| `internal/api/server.go` | Listener setup, signal handling, graceful shutdown | ~80 |
| `internal/api/routes.go` | `GET /engrams` handler, param parsing, JSON marshal | ~70 |
| `internal/api/server_test.go` | Request-shape + JSON-correctness + error-path tests | ~130 |

Total: ~280 LOC. Same-size envelope as Phase 1. Single PR to `feat/internal-api` branch with `--head feat/internal-api --base main`. Cold-read review before merge.

---

## `internal/api/server.go` shape

```go
package api

import (
    "context"
    "net"
    "net/http"
    "os"
    "time"
)

type Server struct {
    httpSrv *http.Server
    listener net.Listener
    store    *store.Store
}

// New constructs a server bound to the given path (UDS) or addr (TCP).
// Caller chooses one — empty path means use addr. UDS is the default;
// TCP is opt-in via the EIDETIC_TCP=1 toggle handled in cmd/eideticd.
func New(s *store.Store, opts Options) (*Server, error) { ... }

type Options struct {
    UDSPath  string        // e.g. "/tmp/eidetic-daemon.sock"
    TCPAddr  string        // e.g. "127.0.0.1:9876"
    Timeout  time.Duration // request timeout
}

// Serve blocks until ctx is cancelled. Listens on the configured socket;
// handles GET /engrams via routes.go.
func (s *Server) Serve(ctx context.Context) error { ... }

// Close releases the listener + unlinks the UDS file if applicable.
func (s *Server) Close() error { ... }
```

**UDS file permissions:** `0600` (owner read/write only). Defense against multi-user box accidentally exposing engrams.

**Graceful shutdown:** `http.Server.Shutdown(ctx)` with a configurable timeout (default 5s). On shutdown, the UDS file is removed.

**Listener choice:**
- If `UDSPath != ""`: `net.Listen("unix", UDSPath)` after `os.Remove(UDSPath)` for stale-socket cleanup.
- Else if `TCPAddr != ""`: `net.Listen("tcp", TCPAddr)`.
- Else: error (caller misconfigured).

**No reverse-proxy or TLS in W1.** Local socket = trust boundary. If we ever expose this beyond localhost, that's an ADR moment.

---

## `internal/api/routes.go` shape

```go
package api

import (
    "context"
    "encoding/json"
    "net/http"
    "strconv"
)

func (s *Server) handleEngramsGET(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet {
        http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
        return
    }

    q := r.URL.Query()
    surface := q.Get("surface")
    if surface == "" {
        http.Error(w, "surface required", http.StatusBadRequest)
        return
    }

    limit, _ := strconv.Atoi(q.Get("limit"))   // store.Retrieve clamps invalid → default
    since, _ := strconv.ParseInt(q.Get("since"), 10, 64)

    ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
    defer cancel()

    rows, err := s.store.Retrieve(ctx, surface, since, limit)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(rows)
}
```

**Decisions encoded:**

1. **Param parsing is forgiving.** Bad `limit` / `since` parse to 0 → `store.Retrieve` applies defaults/clamps per spec § 2.4. No 400 for bad numeric params — they get the default behavior. Surface is the only required string.
2. **No CORS / preflight.** UDS is local-only. TCP fallback is `127.0.0.1` only.
3. **Streaming JSON via `json.NewEncoder(w).Encode`.** No pre-buffer. At 500-row cap with ~1-5KB engrams each, response is bounded (<2.5 MB). Buffer-or-stream doesn't matter at this scale per ADR-014 (Go runtime overhead is the bottleneck, not allocation).
4. **No pagination headers in W1.** `limit` + `since` is sufficient cursor; clients re-fetch with `since=last_seen_ts`.

---

## `internal/api/server_test.go` shape

Uses `net/http/httptest` for the HTTP machinery + `t.TempDir()` UDS path for socket lifecycle.

| Test | Asserts |
|---|---|
| `TestEndToEndUDS_GetEngrams` | Start server on UDS, insert 5 rows via store, GET /engrams via `unix:` dialer, parse JSON, assert count + ordering |
| `TestEndToEndTCP_GetEngrams` | Same but TCP fallback on `127.0.0.1:0` (random port) |
| `TestGET_MissingSurfaceReturns400` | Empty/missing `surface` query param → 400 |
| `TestGET_WrongMethodReturns405` | POST/PUT/DELETE all 405 |
| `TestGET_LimitDefaultsAndClampsAcrossAPIBoundary` | `limit=999` clamps to 500 via store; `limit=missing` defaults to 50 |
| `TestGET_SinceFilterAppliedViaQuery` | Insert 10 rows, `?since=<midpoint>` returns 4 (consistent with `TestRetrieveSinceFilter`) |
| `TestServerCloseRemovesUDSFile` | After `Close()`, UDS file is unlinked |
| `TestServerCloseHandlesStaleSocket` | Pre-existing socket file at the path is replaced cleanly on New |
| `TestRequestTimeoutAppliesToStoreLayer` | Set `Timeout: 1ms`; assert context cancellation surfaces 500 |

9 cases. Mirror the Phase 1 test density.

**No benchmark in Phase 2.** Bench lives in `bench/` per Phase 5 (gated on the W1 bench-gaps spike directive).

---

## `cmd/eideticd/main.go` wiring (lands in Phase 2 PR)

```go
func main() {
    path := os.Getenv("EIDETIC_DATA_DIR") // empty → default ~/.eidetic
    s, err := store.Open(filepath.Join(path, "engrams.db"))
    if err != nil { log.Fatalf("store: %v", err) }
    defer s.Close()

    opts := api.Options{Timeout: 5 * time.Second}
    if os.Getenv("EIDETIC_TCP") == "1" {
        opts.TCPAddr = "127.0.0.1:9876"
    } else {
        opts.UDSPath = "/tmp/eidetic-daemon.sock"
    }

    srv, err := api.New(s, opts)
    if err != nil { log.Fatalf("api: %v", err) }
    defer srv.Close()

    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()

    if err := srv.Serve(ctx); err != nil && err != http.ErrServerClosed {
        log.Fatalf("serve: %v", err)
    }
}
```

**Phase 2 ship gate:** end-to-end smoke against built binary:

```sh
make build
./bin/eideticd &
PID=$!
sleep 0.5  # daemon spawn-at-app-startup absorbs modernc 1.75s cold-init
# Note: 0.5s NOT enough for modernc cold-init; production launchd holds the gate.
# Smoke-test variant uses TCP + slow-start probe loop instead.
curl --unix-socket /tmp/eidetic-daemon.sock 'http://localhost/engrams?surface=cursor&limit=5'
kill $PID
```

Cold-init handling in dev: smoke-test script polls `/engrams?surface=ping&limit=1` for 3s before declaring server ready. Documented in Makefile target `smoke`.

---

## Out of Phase 2 (deferred per spec § 1)

- **fsnotify capture path** → Phase 3
- **MCP bridge / framing library** → post-W1 separate repo (review guardrail #4)
- **Auth / TLS** → post-W1 (UDS perms are the trust boundary)
- **Write API** → Phase 3 (insert flows from capture, not API)
- **Pagination / streaming chunked responses** → only if Phase 5 bench shows >2.5MB responses common (unlikely)
- **OpenAPI / Swagger spec** → post-W1 (single endpoint doesn't justify the ceremony)

---

## Sequencing within Phase 2

1. Write `internal/api/server.go` (listener + lifecycle).
2. Write `internal/api/routes.go` (handler).
3. Write `cmd/eideticd/main.go` (wires store + api).
4. Write `internal/api/server_test.go` covering the 9 cases.
5. `go build ./...` clean + `go test ./...` green.
6. Add `make smoke` target + smoke-test script.
7. PR to `main`, request cold-read review.
8. Address review, merge.
9. Phase 3 (capture) opens.

**Estimated implementer wall-clock:** 1-2 hours of focused work once Phase 1 merges. Sub-agent could handle test scaffolding (`server_test.go`); the lifecycle code (`server.go`) is implementer directly per implementation plan.

---

## References

- [SPEC.md § 2.4](SPEC.md) — retrieve API contract (binding)
- [IMPLEMENTATION_PLAN.md § 7](IMPLEMENTATION_PLAN.md) — retrieval shape
- [IMPLEMENTATION_PLAN.md § 11](IMPLEMENTATION_PLAN.md) — phase sequencing
- ADR-013 guardrail #4 — defer MCP framing to post-W1
- ADR-014 pattern #3 — store separates reader pool, used here for `Retrieve`
- ADR-016 — daemon spawn-at-app-startup mandate (informs smoke-test cold-init handling)
- PR #1 — Phase 1 (`internal/store`) — must merge before Phase 2 implementation begins
