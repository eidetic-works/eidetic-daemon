# MCP Integration Guide

Connect eidetic-daemon to Claude Code, Cursor, or any other MCP-compatible AI assistant in under 5 minutes.

## What this does

Once connected, your AI assistant gets 17 new tools — `query_engrams`, `search_engrams`, `daemon_metrics`, `list_surfaces`, plus the recall family (`nucleus_ask`, `nucleus_digest`, `nucleus_timeline`, `nucleus_link`, `nucleus_curate`), and more. The assistant can retrieve the engrams your daemon has been quietly capturing from Claude Code sessions, Cursor workspaces, and Cowork sessions without any manual copy-paste.

```
  AI assistant  ←── stdio ──→  eidetic-mcp  ←── HTTP/UDS ──→  eideticd
  (Cursor /                   (Python bridge)                  (Go daemon)
   Claude Code /
   Cline / ...)
```

## Prerequisites

1. `eideticd` running. Verify:

   ```sh
   curl --unix-socket /tmp/eidetic-daemon.sock http://localhost/healthz
   # → {"status":"ok"}
   ```

   Not running? See the [quickstart](../README.md#quickstart).

2. Python ≥ 3.11.

## Install the bridge

```sh
pip install eidetic-mcp
```

Verify:

```sh
eidetic-mcp --version 2>&1 || python -m eidetic_mcp.server --help
```

Or install from source (for development / latest unreleased):

```sh
pip install -e path/to/eidetic-daemon/bridge/python
```

## Configure Claude Code

Claude Code supports MCP servers added via the `claude mcp add` command (v0.2+):

```sh
claude mcp add eidetic -- python -m eidetic_mcp.server
```

That's it. Claude Code writes the entry to `~/.claude/mcp.json` and picks it up on next launch.

### Manual config (if `claude mcp add` is unavailable)

Edit `~/.claude/mcp.json` (create if absent):

```json
{
  "mcpServers": {
    "eidetic": {
      "command": "python",
      "args": ["-m", "eidetic_mcp.server"],
      "env": {}
    }
  }
}
```

### Verify in Claude Code

Ask Claude:

```
daemon_metrics()
```

You should see your engram count, uptime, and DB path in the response.

## Configure Cursor

Cursor reads MCP config from `.cursor/mcp.json` in your project root, or `~/.cursor/mcp.json` globally.

Create or edit `~/.cursor/mcp.json`:

```json
{
  "mcpServers": {
    "eidetic": {
      "command": "python",
      "args": ["-m", "eidetic_mcp.server"],
      "env": {}
    }
  }
}
```

Restart Cursor. The `eidetic` server should appear in Settings → MCP Servers with a green status dot.

## Configure other clients (Cline, Cody, etc.)

Any MCP stdio client works. The command is always:

```
python -m eidetic_mcp.server
```

Refer to your client's MCP server setup docs for where to add it.

## Environment overrides

| Var | Default | Effect |
|-----|---------|--------|
| `EIDETIC_UDS_PATH` | `/tmp/eidetic-daemon.sock` (macOS), `/var/run/eidetic.sock` (Linux) | Override UDS path |
| `EIDETIC_TCP` | unset | Set to `1` to use TCP loopback (`127.0.0.1:9876`) instead of UDS |
| `EIDETIC_AUTH_TOKEN` | auto-read from `~/.eidetic/auth-token` | Override Bearer token for `-auth` mode daemons |

Set them in the `env` block of the MCP config:

```json
{
  "mcpServers": {
    "eidetic": {
      "command": "python",
      "args": ["-m", "eidetic_mcp.server"],
      "env": {
        "EIDETIC_TCP": "1"
      }
    }
  }
}
```

## Available tools

| Tool | What it does |
|------|-------------|
| `list_surfaces` | Discover what surfaces the daemon has captured (`claude_code`, `cursor`, `cowork`, ...) |
| `daemon_status` | Check daemon health |
| `daemon_metrics` | Engram counts, uptime, DB size, query latency percentiles |
| `query_engrams` | Retrieve engrams by surface, time window, order |
| `search_engrams` | Full-text search across payloads (FTS5 — supports `AND`/`OR`/`NOT`, phrase queries) |
| `recent_engrams` | Newest N engrams across all surfaces |
| `count_engrams` | Count without fetching rows |
| `get_engram_by_id` | Fetch single engram by primary key |
| `insert_engram` | Inject an engram directly (mobile, webhooks, manual annotations) |
| `insert_engrams_batch` | Bulk insert N engrams atomically |
| `delete_engram_by_id` | Remove a single engram by ID |
| `purge_engrams` | Wipe a surface (or all engrams before a timestamp) |

Full argument docs: [`bridge/python/README.md`](../bridge/python/README.md#tools).

## Quick start prompts

Once connected, paste one of these into your assistant:

```
What surfaces has eidetic captured? Use list_surfaces.
```

```
Show me the last 20 Claude Code engrams.
```

```
Search my engrams for anything about "authentication" and summarize what I was working on.
```

```
How many engrams do I have total? When did the daemon last restart?
```

## Chunked records

Large JSONL lines (> 7 MiB) are split into chunks automatically (ADR-018). The bridge reassembles them transparently — your assistant sees one logical record, not N fragment rows. Pass `raw_chunks=true` to `query_engrams` only if you're debugging chunking directly.

## Troubleshooting

**MCP server doesn't appear in Claude Code / Cursor**

- Confirm `python -m eidetic_mcp.server` runs without error from the same Python that your config points to.
- Use an absolute path to the Python executable if needed: `"command": "/usr/local/bin/python3"`.

**`daemon_status()` returns `{"healthy": false}`**

- `eideticd` isn't running. Start it: `eideticd &` or via launchd/systemd-user (see [README](../README.md#running-as-a-service)).

**`daemon_metrics()` returns `error: metrics not configured`**

- Your daemon is older than v0.0.7. Upgrade to the latest release.

**Auth errors (if running with `-auth` flag)**

- Ensure `~/.eidetic/auth-token` exists and is readable. The token rotates each daemon restart — restart the bridge after restarting the daemon.
