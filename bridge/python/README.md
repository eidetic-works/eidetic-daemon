# eidetic-mcp — Python MCP bridge for eidetic-daemon

Thin MCP stdio server that exposes the [eidetic-daemon](../..) UDS API as MCP tools. Any MCP client (Cursor, Claude Code, Cline, Cody, etc.) can list + call these tools to query the daemon's local engram store.

Per spec § 7 Open Q #5: this is the "separate Python wrapper" path — the daemon binary itself ships independently. The bridge is co-located in this repo for convenience; eventual fold into [`mcp-server-nucleus`](https://github.com/eidetic-works/mcp-server-nucleus) (the existing Nucleus MCP server) is an architecture decision deferred to W2+.

## Tools

| Tool | Args | Returns |
|---|---|---|
| `query_engrams` | `surface` (optional v0.0.23+, omit for all surfaces), `limit` (default 50, cap 500), `since` (unix ns, default 0), `before` (unix ns, default 0, v0.0.21+), `asc` (bool, default false, v0.0.22+), `raw_chunks` (bool, default false) | JSON array of engrams ordered by timestamp descending (or ascending when `asc=true`). Omit `surface` to retrieve across all surfaces. `since`+`before` define a half-open time window. Chunked records (per ADR-018) are reassembled by default; `raw_chunks=true` returns chunks as separate engrams. |
| `daemon_status` | (none) | `{"healthy": bool}` from `/healthz` round-trip |
| `daemon_metrics` | (none) | Daemon's `/metrics` JSON (v0.0.7+): `version`, `uptime_seconds`, `engram_total`, `engram_by_surface`, `capture_skipped`, `db_path`, `db_size_bytes`. Schema additive-only across versions. Daemons predating v0.0.7 return `error: metrics not configured`. |
| `list_surfaces` | (none) | `{"surface": count, ...}` — every surface the daemon has seen with its engram count (v0.0.13+). Empty store → `{}`. Use for discovery before `query_engrams`. |
| `purge_engrams` | `surface` (required), `before` (unix ns, default 0) | `{"deleted": N}`. Removes all engrams for the surface when `before=0`; only removes engrams with `ts < before` when set. Irreversible (v0.0.13+). |
| `search_engrams` | `q` (required — FTS5 expression), `surface` (default all), `limit` (default 50, cap 500) | JSON array of engrams ordered by relevance rank (best match first). Same shape as `query_engrams`. `q` accepts bare keywords, `"phrase queries"`, `OR`/`AND`/`NOT` boolean operators (v0.0.14+). |
| `recent_engrams` | `since` (unix ns, default 0), `before` (unix ns, default 0, v0.0.21+), `limit` (default 50, cap 500) | JSON array of newest engrams across **all surfaces**, newest-first. `since`+`before` define a sliding time window. Poll diff: pass last-seen `ts` as `since`. Same `[]Engram` shape (v0.0.15+). |
| `insert_engram` | `surface` (required), `payload` (required), `ts` (unix ns, default server-now), `meta` (optional string) | Directly insert an engram, bypassing fsnotify. Returns `{"id": N}`. Immediately searchable + retrievable. Use for mobile, webhooks, relay pipelines, manual annotations (v0.0.16+). |
| `insert_engrams_batch` | `items` (required — array of `{surface, payload, ts?, meta?}`) | Bulk insert N engrams in one atomic transaction. Returns `{"inserted": N}`. Efficient for relay sync, session replay, bulk import (v0.0.17+). |
| `get_engram_by_id` | `id` (required — positive integer) | Fetch a single engram by primary key. Returns full Engram JSON. Error on non-existent ID or non-positive integer (v0.0.18+). |
| `count_engrams` | `surface` (optional), `since` (unix ns, optional) | Return `{"count": N}` for engrams matching optional surface and time filters. Never fetches rows (v0.0.20+). |
| `delete_engram_by_id` | `id` (required — positive integer) | Remove a single engram by primary key. Returns `{"deleted": 1}`. Error on non-existent ID or non-positive integer. Irreversible (v0.0.19+). |
| `nucleus_digest` | `window` (one of `"24h"`, `"7d"` (default), `"30d"`) | Windowed activity recap (v0.0.6+; requires daemon v0.0.47+). Calls `GET /digest?window=...` and returns the JSON verbatim: `window`, `since`, `total_engrams`, `by_surface`, `top_hours`, `top_terms`, `sample_engrams`, plus an `instructions` field (promoted to the top of the payload) telling the host LLM how to render the recap. |
| `nucleus_timeline` | `window` (one of `"24h"`, `"7d"` (default), `"30d"`), `surfaces` (optional list, empty = all surfaces), `limit` (default 200, max 1000) | Cross-surface chronological engram stream (v0.0.7+; requires daemon v0.0.47+). Calls `GET /timeline?since=...&limit=...&surfaces=...` and returns the daemon JSON: `engrams` (interleaved by `ts` ascending across the requested surfaces), `count`, `surfaces`, plus an `instructions` field (promoted to the top of the payload) telling the host LLM to render the result as a brief activity narrative. Pairs naturally with `nucleus_digest` for stats-then-narrative recaps. |

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

The bridge is **not yet on PyPI**. Install from this repo until the architecture decision on `mcp-server-nucleus` fold-in lands (W2+).

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
    "version": "v0.0.13",
    "uptime_seconds": 142,
    "engram_total": 139751,
    "engram_by_surface": {"claude_code": 141314},
    "capture_skipped": 0,
    "db_path": "/Users/.../engrams.db",
    "db_size_bytes": 659046400
  }

list_surfaces()
→ {"claude_code": 141314, "cursor": 23, "cowork": 414}

purge_engrams(surface="cursor")
→ {"deleted": 23}

purge_engrams(surface="claude_code", before=1715000000000000000)
→ {"deleted": 98214}   # only engrams older than the timestamp

search_engrams(q="benchmark latency")
→ [{"id":N, "surface":"claude_code", "ts":..., "payload":"...", "meta":"..."}, ...]

search_engrams(q='"meetup tomorrow"', surface="cursor", limit=3)
→ [...]   # phrase query, surface-filtered, top 3 by relevance

recent_engrams()
→ [{"id":N, "surface":"cursor", "ts":..., "payload":"...", "meta":"..."}, ...]  # 50 newest, all surfaces

recent_engrams(since=1747500000000000000, limit=20)
→ [...]   # only engrams with ts > since, newest-first

insert_engram(surface="mobile", payload="noted from phone")
→ 1234   # integer engram ID

insert_engram(surface="webhook", payload="stripe event body", meta='{"source":"stripe"}')
→ 1235

insert_engrams_batch([
    {"surface": "mobile", "payload": "note 1"},
    {"surface": "mobile", "payload": "note 2"},
])
→ 2   # integer count inserted

get_engram_by_id(id=1234)
→ Engram(id=1234, surface='mobile', ts=..., payload='noted from phone', meta='')

count_engrams()                               # → 42  (all surfaces, all time)
count_engrams(surface="claude_code")          # → 17  (one surface)
count_engrams(since=1747500000000000000)      # → 5   (since timestamp)

delete_engram_by_id(id=1234)
→ True   # boolean — True on success; DaemonError on 404

nucleus_digest(window="7d")
→ {
    "instructions": "Render the recap as a short markdown digest ...",
    "window": "7d",
    "since": 1746896400000000000,
    "total_engrams": 1280,
    "by_surface": {"claude_code": 1101, "cursor": 179},
    "top_hours": [...],
    "top_terms": [...],
    "sample_engrams": [...]
  }

nucleus_timeline(window="24h", surfaces=["claude_code","cursor"], limit=50)
→ {
    "instructions": "These are cross-tool engrams in chronological order. Render as a brief activity narrative.",
    "engrams": [
      {"id":N,"surface":"claude_code","ts":...,"payload":"...","meta":""},
      {"id":N,"surface":"cursor","ts":...,"payload":"...","meta":""},
      ...
    ],
    "count": 42,
    "surfaces": ["claude_code","cursor"]
  }
```

Surfaces depend on what the daemon is watching (default: `claude_code`, `cowork`, `cursor`). Use `list_surfaces()` to discover what's currently in the store.

`daemon_metrics()` is the v0.0.7+ verifiability surface — your AI can introspect what's been captured without raw curl, useful for "what's in scope right now?" pre-flight before a `query_engrams` call. (The bridge wraps the JSON response from the daemon's `/metrics`. The daemon also speaks Prometheus exposition (v0.0.10+) and OpenMetrics 1.0.0 (v0.0.11+) on the same endpoint via `Accept` header, but the bridge tool surface stays JSON.)

### Caller auth (v0.0.9+, opt-in)

If the daemon is running with `EIDETIC_AUTH=1` (or `-auth` flag), the bridge auto-discovers the rotating Bearer token. Resolution order:

1. Explicit `auth_token=<hex>` kwarg (test injection)
2. `EIDETIC_AUTH_TOKEN` env var
3. `<EIDETIC_DATA_DIR>/auth-token` file (default `~/.eidetic/auth-token`)

Daemons NOT in auth-mode pass through transparently — no bridge config change required either way.

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
