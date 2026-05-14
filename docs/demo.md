# Demo — Day-7 spec § 8 acceptance flow (text-script)

This walks through the demo flow that Distribution Officer references on Day-7 release. Text-script + expected outputs so an operator (or anyone) can run it without guessing.

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
eideticd v0.0.3
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
eideticd v0.0.3
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

Expected:
```json
{
  "version": "v0.0.7",
  "uptime_seconds": 142,
  "engram_total": 139751,
  "engram_by_surface": { "claude_code": 141314 },
  "capture_skipped": 0,
  "db_path": "/Users/<you>/.eidetic/engrams.db",
  "db_size_bytes": 659046400
}
```

Schema is additive-only across versions. Use this to verify scale / throughput claims directly from the daemon — no trust-me framing. Daemons predating v0.0.7 return `503 metrics not configured`; check `version` to confirm.

If you have the bridge installed (`bridge/python/`), the same data is available to MCP tool-calling AIs via `daemon_metrics()` (v0.0.8+).

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

If the watcher hits an engram larger than `store.MaxPayloadBytes` (8 MiB per ADR-017, raised from 1 MiB after cc-tb runtime spike showed real Claude session JSONL chunks at 1.18-2.41 MiB), it skips that record and logs:

```
capture: claude_code /Users/<you>/.claude/projects/.../session.jsonl: skipped 1 oversized engrams (>8388608 bytes); total skipped this session: N
```

You can also read the count programmatically (see `Watcher.SkippedPayloadTooLarge()` in `internal/capture/watcher.go`). Chunked-capture for arbitrarily-large records lands in W2.

---

**Distribution Officer**: this script is the source-of-truth for the Day-7 demo post. Hyperlink the headline numbers (P95 0.27 ms, ~370× under SLO) directly to this file.
