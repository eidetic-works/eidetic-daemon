# eidetic-mcp — Python MCP bridge for eidetic-daemon

Thin MCP stdio server that exposes the [eidetic-daemon](../..) UDS API as MCP tools. Any MCP client (Cursor, Claude Code, Cline, Cody, etc.) can list + call these tools to query the daemon's local engram store.

Per spec § 7 Open Q #5: this is the "separate Python wrapper" path — the daemon binary itself ships independently. The bridge is co-located in this repo for convenience; eventual fold into [`mcp-server-nucleus`](https://github.com/eidetic-works/mcp-server-nucleus) (the existing Nucleus MCP server) is a substrate-work decision deferred to W2+.

## Tools

| Tool | Args | Returns |
|---|---|---|
| `query_engrams` | `surface` (required), `limit` (default 50, cap 500), `since` (unix ns, default 0), `raw_chunks` (bool, default false) | JSON array of engrams ordered by timestamp descending. Chunked records (per ADR-018) are reassembled by default; `raw_chunks=true` returns chunks as separate engrams. |
| `daemon_status` | (none) | `{"healthy": bool}` from `/healthz` round-trip |
| `daemon_metrics` | (none) | Daemon's `/metrics` JSON (v0.0.7+): `version`, `uptime_seconds`, `engram_total`, `engram_by_surface`, `capture_skipped`, `db_path`, `db_size_bytes`. Schema additive-only across versions. Daemons predating v0.0.7 return `error: metrics not configured`. |

### Chunked-record reassembly (ADR-018)

The daemon's JSONL parser splits records exceeding 7 MiB into chunks tagged with `chunk_id`/`chunk_seq`/`chunk_total` in `meta`. The bridge's `query_engrams` runs `reassemble_chunks()` by default — clients see ONE row per logical record regardless of chunking. Set `raw_chunks=true` to bypass reassembly (useful for debugging or when your consumer handles the contract directly).

`reassemble_chunks()` is also exported as a public function for callers that want to reassemble a `tuple[Engram, ...]` from the lower-level `DaemonClient.query_engrams()`:

```python
from eidetic_mcp.client import DaemonClient
from eidetic_mcp.reassemble import reassemble_chunks

client = DaemonClient()
raw_rows = client.query_engrams(surface="claude_code", limit=20)
merged = reassemble_chunks(raw_rows)  # returns tuple[Engram, ...] with chunked records merged
```

Idempotent — safe to call on already-reassembled output. Best-effort on incomplete chunk groups (warns + emits partial; does NOT silent-drop).

P95 retrieval round-trip on a 10K-row store: ~0.27 ms (daemon-side; per ADR-016 + spec § 3 measurement).

## Install

Requires Python ≥ 3.11 + a running `eideticd` (the daemon, separate package).

```sh
# From a checkout of this repo:
cd bridge/python
pip install -e .          # editable install (or: pip install .)

# Verify:
python -m eidetic_mcp.server  # starts MCP stdio server; ^C to exit
```

The bridge is **not yet on PyPI**. Install from this repo until the substrate-work decision on `mcp-server-nucleus` fold-in lands (W2+).

## Configure your MCP client

Add to your client's MCP config (paths vary):

| Client | Config path |
|---|---|
| Claude Code | `~/.claude/mcp.json` |
| Cursor | Settings → MCP Servers → New |
| Cline | VS Code settings → Cline: MCP servers |

Add an entry:

```json
{
  "eidetic": {
    "command": "python",
    "args": ["-m", "eidetic_mcp.server"],
    "env": {}
  }
}
```

Override transport:
- `EIDETIC_TCP=1` — talk to daemon's TCP loopback (`127.0.0.1:9876`) instead of UDS.
- `EIDETIC_UDS_PATH=/some/path` — override default UDS path (`/tmp/eidetic-daemon.sock` on macOS, `/var/run/eidetic.sock` on Linux).

## Usage from a tool-calling AI

Once the MCP client picks up the server, your AI can call:

```
query_engrams(surface="claude_code", limit=20)
→ [{"id":N, "surface":"claude_code", "ts":..., "payload":"...", "meta":"..."}, ...]

daemon_status()
→ {"healthy": true}

daemon_metrics()
→ {
    "version": "v0.0.7",
    "uptime_seconds": 142,
    "engram_total": 139751,
    "engram_by_surface": {"claude_code": 141314},
    "capture_skipped": 0,
    "db_path": "/Users/.../engrams.db",
    "db_size_bytes": 659046400
  }
```

Surfaces depend on what the daemon is watching (default: `claude_code`, `cowork`, `cursor`).

`daemon_metrics()` is the v0.0.7+ verifiability surface — your AI can introspect what's been captured without raw curl, useful for "what's in scope right now?" pre-flight before a `query_engrams` call.

## Development

```sh
cd bridge/python
pip install -e '.[dev]'
pytest tests/
```

`tests/test_client.py` spins up a real Unix-domain HTTP server backed by a tmp socket and verifies parser + transport + env-var defaults. Server-side end-to-end is covered by [`scripts/demo-smoke.sh`](../../scripts/demo-smoke.sh) at the daemon repo root.

## Architecture

```
                  stdio                       HTTP over UDS
  AI client  ←─────────→  eidetic_mcp.server  ←─────────→  eideticd (Go)
  (Cursor /                (this package)                   (separate)
   Claude Code /
   Cline / ...)
```

The bridge is process-isolated from the daemon: bridge crashes don't take the daemon down, and vice versa. Bridge has zero local state (the daemon owns the SQLite store).

## Security

The bridge has the same trust boundary as the daemon (single-user, single-host, UDS chmod 0600). It does not introduce auth, TLS, or rate-limiting. Anything callable from the daemon's `/engrams` endpoint is callable through the bridge — including reads of every captured engram (which may contain pasted secrets per [SECURITY.md](../../SECURITY.md) § 1). Treat MCP-bridge access the same way you'd treat shell access to the same uid.

## License

MIT — same as the parent eidetic-daemon repo. See [LICENSE](../../LICENSE).
