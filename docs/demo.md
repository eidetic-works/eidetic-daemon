# Demo — Day-7 spec § 8 acceptance flow (text-script)

Text-script + expected outputs for the Day-7 release demo, so anyone can run it without guessing.

For a video recording: pair this with [asciinema](https://asciinema.org/) — `asciinema rec docs/demo.cast` while running these commands.

**Prerequisites**: macOS or Linux. ~2 minutes.

---

## 1. Install

The Day-7 deliverable is a one-line installer:

```sh
curl -fsSL https://eidetic.works/install.sh | sh
```

Expected:
```
install: target: darwin-arm64
install: fetching https://github.com/eidetic-works/eidetic-daemon/releases/latest/download/eideticd-darwin-arm64.tar.gz
install: installed /usr/local/bin/eideticd
install: registered LaunchAgent at /Users/<you>/Library/LaunchAgents/works.eidetic.eideticd.plist
install: launchd: started works.eidetic.eideticd
install: smoke test
eideticd v0.0.11
install: OK — daemon installed and registered. UDS: /tmp/eidetic-daemon.sock (Mac) or /var/run/eidetic.sock (Linux)
```

(Until the public landing flips on, operate from a local clone: `git clone … && make build && ./bin/eideticd &`.)

---

## 2. Confirm the daemon is alive

```sh
curl --unix-socket /tmp/eidetic-daemon.sock http://localhost/healthz
```

Expected:
```
{"status":"ok"}
```

Round-trip <5 ms typical. If 404 or connection-refused, check `tail -f /tmp/eideticd.err.log` (Mac) or `journalctl --user -u eideticd` (Linux).

---

## 3. Verify the version

```sh
eideticd -version
```

Expected:
```
eideticd v0.0.11
```

(Version is injected at build via `-ldflags "-X main.Version=$(git describe --tags --always --dirty)"` per ADR-017.)

---

## 4. Capture in flight

The daemon watches three default surface roots (`~/.claude/projects/`, `~/.cowork/sessions/`, `~/Library/Application Support/Cursor/User/workspaceStorage/` on macOS). Open one of those tools and write something — the daemon captures the session-log write within ~50 ms (spec § 2.3).

Or simulate by writing directly to a watched dir:

```sh
mkdir -p ~/.claude/projects/demo-session
echo '{"role":"user","payload":"What did the last benchmark say?","ts":"2026-05-14T00:00:00Z"}' \
  >> ~/.claude/projects/demo-session/session.jsonl
```

Within ~50 ms, the engram is committed to `~/.eidetic/engrams.db`. Verify:

```sh
sqlite3 ~/.eidetic/engrams.db "SELECT id, surface, ts FROM engrams ORDER BY ts DESC LIMIT 1"
```

Expected (one recent row):
```
N|claude_code|<unix_ns_timestamp>
```

---

## 5. Read it back via UDS

```sh
curl --unix-socket /tmp/eidetic-daemon.sock \
  'http://localhost/engrams?surface=claude_code&limit=5'
```

Expected:
```json
[
  {"id":N,"surface":"claude_code","ts":...,"payload":"{\"role\":\"user\",\"payload\":\"What did the last benchmark say?\",\"ts\":\"2026-05-14T00:00:00Z\"}","meta":"{\"path\":\"/Users/<you>/.claude/projects/demo-session/session.jsonl\",\"offset_end\":...,\"parser\":\"jsonl/v1\"}"}
]
```

P95 round-trip latency: **0.27 ms** on a 10K-row store (M4, mainline build, 2026-05-13 measurement). Spec § 3 SLO is ≤100 ms — this clears it by ~370×.

---

## 5a. Inspect daemon state (v0.0.7+)

```sh
curl --unix-socket /tmp/eidetic-daemon.sock http://localhost/metrics
```

Expected (default JSON format, v0.0.7+):
```json
{
  "version": "v0.0.11",
  "uptime_seconds": 142,
  "engram_total": 139751,
  "engram_by_surface": { "claude_code": 141314 },
  "capture_skipped": 0,
  "db_path": "/Users/<you>/.eidetic/engrams.db",
  "db_size_bytes": 659046400
}
```

Schema is additive-only across versions. Use this to verify scale / throughput claims directly from the daemon — no trust-me framing. Daemons predating v0.0.7 return `503 metrics not configured`; check `version` to confirm.

### Prometheus + OpenMetrics formats (v0.0.10/v0.0.11+)

Same endpoint, content-negotiated via `Accept` header. Drop into your existing scrape config — no exporter sidecar:

```sh
# Prometheus exposition format (v0.0.10+)
curl -H 'Accept: text/plain' --unix-socket /tmp/eidetic-daemon.sock http://localhost/metrics

# OpenMetrics 1.0.0 (v0.0.11+, with EOF trailer + UNIT comments)
curl -H 'Accept: application/openmetrics-text' --unix-socket /tmp/eidetic-daemon.sock http://localhost/metrics
```

Real Prometheus scrapers send a multi-type Accept by default (`application/openmetrics-text;version=1.0.0,text/plain;version=0.0.4;q=0.5,*/*;q=0.1`); v0.0.11 honors the openmetrics clause and returns OpenMetrics (precedence) — matches scraper expectation. v0.0.10 callers (text/plain alone) are unchanged.

If you have the bridge installed (`bridge/python/`), the same data is available to MCP tool-calling AIs via `daemon_metrics()` (v0.0.8+).

---

## 5b. Caller auth (v0.0.9+, opt-in)

Off by default — preserves the W1 single-user UDS-trust model in [SECURITY.md](../SECURITY.md). To turn it on:

```sh
EIDETIC_AUTH=1 eideticd      # env var (recommended for service managers)
eideticd -auth                # flag (recommended for one-shot invocations)
```

On enable, the daemon writes `<dataDir>/auth-token` (0600 perms, 64-char hex from `crypto/rand`). Token rotates every restart — no stale-token replay. `/healthz` stays open even with auth on; `/engrams` + `/metrics` require `Authorization: Bearer <token>` (or bare token):

```sh
TOKEN=$(cat ~/.eidetic/auth-token)
curl -H "Authorization: Bearer $TOKEN" --unix-socket /tmp/eidetic-daemon.sock http://localhost/metrics
```

The Python MCP bridge auto-discovers the token (file → env var → explicit kwarg). No bridge config change required when daemon enables auth.

---

## 5c. Surface listing + purge (v0.0.13+)

Discover what the daemon has seen, then prune stale data:

```sh
# List every surface with its engram count.
curl --unix-socket /tmp/eidetic-daemon.sock http://localhost/surfaces
# → {"claude_code": 139751, "cursor": 23, "cowork": 414}

# Purge all engrams for a surface (irreversible).
curl -X DELETE --unix-socket /tmp/eidetic-daemon.sock \
  'http://localhost/engrams?surface=cursor'
# → {"deleted": 23}

# Purge only engrams older than a timestamp (unix nanoseconds).
# Engrams newer than the cutoff are retained.
curl -X DELETE --unix-socket /tmp/eidetic-daemon.sock \
  'http://localhost/engrams?surface=claude_code&before=1715000000000000000'
# → {"deleted": 98214}
```

Both endpoints are auth-gated when `EIDETIC_AUTH=1` — same `Authorization: Bearer <token>` header required.

Via the MCP bridge (`bridge/python/`), the same operations are available as tool calls:

```
list_surfaces()
→ {"claude_code": 139751, "cursor": 23}

purge_engrams(surface="cursor")
→ {"deleted": 23}
```

---

## 5d. Full-text search (v0.0.14+)

```sh
# Bare keyword — find engrams that mention "benchmark"
curl --unix-socket /tmp/eidetic-daemon.sock \
  'http://localhost/search?q=benchmark'

# Phrase query — exact phrase match
curl --unix-socket /tmp/eidetic-daemon.sock \
  'http://localhost/search?q="benchmark+result"'

# Surface filter + limit
curl --unix-socket /tmp/eidetic-daemon.sock \
  'http://localhost/search?q=latency&surface=claude_code&limit=5'

# Boolean operators
curl --unix-socket /tmp/eidetic-daemon.sock \
  'http://localhost/search?q=Anjali+AND+meetup'
```

Expected (same `[]Engram` JSON shape as `/engrams`, ordered by relevance rank):
```json
[
  {"id":N,"surface":"claude_code","ts":...,"payload":"...benchmark...","meta":"..."}
]
```

**q** is an FTS5 match expression — bare keywords, `"phrase queries"`, `OR`/`AND`/`NOT`. Returns `[]` on no match (never 404).

**Upgrade path for existing installs:** on first start after v0.0.14, the daemon backfills the FTS index from `engrams` in one bulk insert. After that, triggers keep it live automatically.

**Via MCP bridge** (`bridge/python/`, v0.0.14+):
```python
search_engrams(q="benchmark latency")
search_engrams(q='"meetup tomorrow"', surface="cursor", limit=3)
```

---

## 5e. Recent activity — cross-surface (v0.0.15+)

```sh
# 50 most recent engrams across all surfaces, newest-first
curl --unix-socket /tmp/eidetic-daemon.sock 'http://localhost/recent'

# Since a timestamp (Unix nanoseconds) — only newer engrams
curl --unix-socket /tmp/eidetic-daemon.sock \
  'http://localhost/recent?since=1747500000000000000&limit=20'
```

Expected (same `[]Engram` JSON shape, `ts` descending):
```json
[
  {"id":N,"surface":"cursor","ts":1747500123456789,"payload":"...","meta":"..."},
  {"id":N-1,"surface":"claude_code","ts":1747500100000000,"payload":"...","meta":"..."}
]
```

No surface filter — returns engrams from all surfaces. Useful for a cross-surface activity snapshot, polling diffs (send `since=<last-seen-ts>`), or UI "recent" feeds.

**Via MCP bridge** (`bridge/python/`, v0.0.15+):
```python
recent_engrams()
recent_engrams(since=1747500000000000000, limit=20)
```

---

## 5f. Direct API-side insert (v0.0.16+)

```sh
# Insert an engram bypassing the fsnotify capture path
curl -X POST --unix-socket /tmp/eidetic-daemon.sock \
  -H 'Content-Type: application/json' \
  -d '{"surface":"mobile","payload":"noted from phone"}' \
  http://localhost/engrams

# With explicit timestamp (unix nanoseconds) and metadata
curl -X POST --unix-socket /tmp/eidetic-daemon.sock \
  -H 'Content-Type: application/json' \
  -d '{"surface":"webhook","payload":"event body here","ts":1747500123456789,"meta":"{\"source\":\"stripe\"}"}' \
  http://localhost/engrams
```

Expected:
```json
{"id": 1234}
```

Returns `201 Created`. `surface` and `payload` required; `ts` defaults to `time.Now().UnixNano()` server-side. The inserted engram is immediately searchable via `GET /search` and retrievable via `GET /engrams`.

**Via MCP bridge** (`bridge/python/`, v0.0.16+):
```python
insert_engram(surface="mobile", payload="noted from phone")
insert_engram(surface="webhook", payload="event body", ts=1747500123456789, meta='{"source":"stripe"}')
# → 1234  (integer ID)
```

---

## 5i. Count engrams (v0.0.20+)

```sh
# Total count across all surfaces
curl --unix-socket /tmp/eidetic-daemon.sock 'http://localhost/engrams/count'

# Count for one surface
curl --unix-socket /tmp/eidetic-daemon.sock 'http://localhost/engrams/count?surface=claude_code'

# Count since a timestamp (unix nanoseconds)
curl --unix-socket /tmp/eidetic-daemon.sock 'http://localhost/engrams/count?since=1747500000000000000'
```

Expected:
```json
{"count": 42}
```

Returns `200 OK` always (empty store returns `{"count": 0}`). Useful for monitoring badges, health dashboards, and sync-diff checks without transferring row data.

**Via MCP bridge** (`bridge/python/`, v0.0.20+):
```python
count_engrams()
count_engrams(surface="claude_code")
count_engrams(since=1747500000000000000)
# → integer count
```

---

## 5h. Delete by ID (v0.0.19+)

```sh
# Remove a single engram by its primary-key ID
curl -X DELETE --unix-socket /tmp/eidetic-daemon.sock 'http://localhost/engrams/1234'
```

Expected:
```json
{"deleted": 1}
```

Returns `200 OK`. `404 Not Found` when the ID doesn't exist. `400 Bad Request` on a non-integer or zero id. Irreversible — use to remove accidentally captured sensitive data or relay duplicates.

**Via MCP bridge** (`bridge/python/`, v0.0.19+):
```python
delete_engram_by_id(id=1234)
# → True  (boolean — True on success)
```

---

## 5g. Point lookup by ID (v0.0.18+)

```sh
# Fetch a single engram by its primary-key ID
curl --unix-socket /tmp/eidetic-daemon.sock 'http://localhost/engrams/1234'
```

Expected:
```json
{"id":1234,"surface":"mobile","ts":1747500000000000000,"payload":"noted from phone","meta":""}
```

Returns `200 OK`. `404 Not Found` when the ID doesn't exist. `400 Bad Request` on a non-integer or zero id.

**Via MCP bridge** (`bridge/python/`, v0.0.18+):
```python
e = get_engram_by_id(id=1234)
# → Engram(id=1234, surface='mobile', ts=..., payload='noted from phone', meta='')
```

---

## 6. Latency

The bench gates ship in CI:

```sh
make bench
```

Expected (3 runs, M4, mainline):
```
BenchmarkRetrievePer95-10  run 1: P50=160µs P95=308µs P99=405µs max=783µs
BenchmarkRetrievePer95-10  run 2: P50=150µs P95=267µs P99=313µs max=425µs
BenchmarkRetrievePer95-10  run 3: P50=133µs P95=253µs P99=293µs max=435µs
BenchmarkWritePer95-10     write@100Hz×5s: P50=157µs P95=652µs P99=3.0ms max=42ms
BenchmarkConcurrentReadWritePer95-10  read@5×concurrent + write@100Hz×5s: P50=2.1ms P95=3.5ms P99=5.0ms max=31ms
PASS
```

CI fails the build if P95 retrieval >100 ms (per spec § 3 ship-gate).

---

## 7. Stop the daemon

```sh
launchctl bootout "gui/$(id -u)/works.eidetic.eideticd"   # macOS
# or
systemctl --user stop eideticd                             # Linux
```

To reinstall/update:

```sh
curl -fsSL https://eidetic.works/install.sh | sh   # idempotent; replaces binary + re-registers
```

To uninstall completely:

```sh
# Stops service, removes binary + socket. Engram data (~/.eidetic/) retained.
curl -fsSL https://eidetic.works/uninstall.sh | sh

# Also wipe engram data (irreversible).
curl -fsSL https://eidetic.works/uninstall.sh | sh -s -- --purge-data
```

---

## 8. Where engrams live

| Path | Mode | Notes |
|---|---|---|
| `~/.eidetic/engrams.db` (+ `.db-wal`, `.db-shm`) | `0700` dir, `0600` file | SQLite WAL store |
| `~/.eidetic/state.json` | `0700` dir, `0600` file | Per-file capture offsets |
| `/tmp/eidetic-daemon.sock` (Mac) / `/var/run/eidetic.sock` (Linux) | `0600` | UDS listener |

`EIDETIC_DATA_DIR=/path/to/dir` overrides the data root.

See [SECURITY.md](./SECURITY.md) for the full threat model + storage modes before relying on the daemon for anything sensitive.

---

## 9. Skipped engrams

If the watcher hits an engram larger than `store.MaxPayloadBytes` (8 MiB per ADR-017, raised from 1 MiB after a runtime spike showed real Claude session JSONL chunks at 1.18-2.41 MiB), it skips that record and logs:

```
capture: claude_code /Users/<you>/.claude/projects/.../session.jsonl: skipped 1 oversized engrams (>8388608 bytes); total skipped this session: N
```

You can also read the count programmatically (see `Watcher.SkippedPayloadTooLarge()` in `internal/capture/watcher.go`). Chunked-capture for arbitrarily-large records lands in W2.

---

**Release demo post**: headline numbers (P95 0.27 ms, ~370× under SLO) should hyperlink directly to this file.
