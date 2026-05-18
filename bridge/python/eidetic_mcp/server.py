"""MCP stdio server exposing the eidetic-daemon as MCP tools.

Spec § 7 Open Q #5 fulfillment: thin Python wrapper over the daemon's
HTTP-over-UDS API. Speaks MCP stdio so any MCP client (Cursor, Claude Code,
Cline, etc.) can list + call tools.

Tools exposed:

  query_engrams(surface, limit=50, since=0)
    Returns a list of engrams from the daemon's local store, indexed by
    (surface, ts DESC). Use surface ∈ {claude_code, cowork, cursor, ...}.
    `since` is unix epoch nanoseconds; 0 = no lower bound.

  daemon_status()
    Returns {'healthy': bool} via /healthz round-trip. Useful as a
    diagnostic before invoking query_engrams.

  daemon_metrics()
    Returns the daemon's /metrics JSON (v0.0.7+): version, uptime_seconds,
    engram_total, engram_by_surface, capture_skipped, db_path,
    db_size_bytes. Schema additive-only across versions. Daemons
    predating v0.0.7 return error 'metrics not configured'.

  list_surfaces()
    Returns a map of surface name → engram count for every surface the
    daemon has seen (v0.0.13+). Empty dict when store is empty.

  purge_engrams(surface, before=0)
    Delete engrams for a surface (v0.0.13+). `before` is unix epoch
    nanoseconds; 0 (default) removes all engrams for that surface.
    Returns {"deleted": N}. Irreversible — use with care.

  search_engrams(q, surface="", limit=50)
    Full-text search over engram payloads (v0.0.14+). `q` is an FTS5
    match expression — bare keywords, phrase queries in double quotes
    ("benchmark result"), OR/AND/NOT boolean operators. Results ordered
    by relevance rank. Optional `surface` filter narrows to one surface.

  recent_engrams(since=0, limit=50)
    Return newest engrams across all surfaces, ordered newest-first
    (v0.0.15+). `since` is unix epoch nanoseconds; 0 = no lower bound.
    Useful for a cross-surface activity snapshot without a keyword query.

  insert_engram(surface, payload, ts=0, meta="")
    Directly insert an engram into the daemon store (v0.0.16+). Bypasses
    the fsnotify capture path. surface + payload required; ts defaults to
    server-side now; meta is optional. Returns {"id": N}.

  insert_engrams_batch(items)
    Bulk insert a list of engram dicts in one atomic transaction (v0.0.17+).
    Each item needs surface + payload; ts + meta optional. Returns {"inserted": N}.

Run:

    eideticd &                          # daemon listens on UDS
    python -m eidetic_mcp.server        # MCP stdio server attached to client

Configure MCP client (e.g. Cursor's mcp.json / Claude Code's
~/.claude/mcp.json) with:

    {
      "eidetic": {
        "command": "python",
        "args": ["-m", "eidetic_mcp.server"],
        "env": {}
      }
    }

Set EIDETIC_TCP=1 to use TCP loopback (127.0.0.1:9876) instead of UDS.
Set EIDETIC_UDS_PATH to override the default UDS path.
"""

from __future__ import annotations

import json
from dataclasses import asdict
from typing import Any

from .client import DaemonClient, DaemonError
from .reassemble import reassemble_chunks


# Lazy import of mcp SDK so client.py + tests can be exercised without it.
def _mcp_imports() -> tuple[Any, Any, Any, Any]:
    """Import mcp SDK lazily — raises ImportError with a clear install hint."""
    try:
        import mcp.server  # type: ignore
        import mcp.server.stdio  # type: ignore
        import mcp.types  # type: ignore
    except ImportError as exc:  # pragma: no cover
        raise ImportError(
            "mcp SDK not installed. Run: pip install 'mcp>=1.0' (see bridge/python/README.md)"
        ) from exc
    return mcp.server.Server, mcp.server.stdio.stdio_server, mcp.types.Tool, mcp.types.TextContent


def build_server(client: DaemonClient | None = None) -> Any:
    """Build the MCP server with two tools registered. Caller-supplied
    `client` lets tests inject a fake DaemonClient without spinning up a
    real daemon."""
    Server, _stdio_server, Tool, TextContent = _mcp_imports()

    if client is None:
        client = DaemonClient()
    daemon = client

    server = Server("eidetic-daemon-bridge")

    @server.list_tools()
    async def _list_tools() -> list:  # type: ignore[misc]
        return [
            Tool(
                name="query_engrams",
                description=(
                    "Query the local eidetic-daemon engram store for recent records "
                    "from a specific surface. Returns up to `limit` rows ordered by "
                    "timestamp descending. Surfaces are tool-specific tags like "
                    "'claude_code', 'cowork', 'cursor'. Use `since` (unix ns) to "
                    "page. P95 retrieval latency on a 10K-row store is ~0.27 ms.\n\n"
                    "By default, chunked records (per ADR-018: lines >7 MiB split "
                    "into N chunks tagged with chunk_id/seq/total in meta) are "
                    "reassembled into single rows before return. Set `raw_chunks=true` "
                    "to disable reassembly + see chunks as separate engrams (useful "
                    "for debugging or surface-aware consumers that handle chunking)."
                ),
                inputSchema={
                    "type": "object",
                    "properties": {
                        "surface": {
                            "type": "string",
                            "description": "Surface tag, e.g. claude_code | cowork | cursor",
                        },
                        "limit": {
                            "type": "integer",
                            "description": "Max rows to return (daemon-side default 50, cap 500)",
                            "minimum": 1,
                            "maximum": 500,
                        },
                        "since": {
                            "type": "integer",
                            "description": "Unix nanoseconds lower bound (0 = no bound)",
                            "minimum": 0,
                        },
                        "raw_chunks": {
                            "type": "boolean",
                            "description": "If true, skip chunk-reassembly + return chunks as separate engrams (default false)",
                        },
                    },
                    "required": ["surface"],
                },
            ),
            Tool(
                name="daemon_status",
                description=(
                    "Check whether the eidetic-daemon is reachable and healthy. "
                    "Returns {'healthy': bool}. Use as a pre-flight before query_engrams."
                ),
                inputSchema={"type": "object", "properties": {}, "required": []},
            ),
            Tool(
                name="daemon_metrics",
                description=(
                    "Read live observability counters from the eidetic-daemon "
                    "(v0.0.7+). Returns the daemon's /metrics JSON: version, "
                    "uptime_seconds, engram_total, engram_by_surface, "
                    "capture_skipped, db_path, db_size_bytes. Schema is "
                    "additive-only across versions. Daemons predating v0.0.7 "
                    "return error 'metrics not configured'."
                ),
                inputSchema={"type": "object", "properties": {}, "required": []},
            ),
            Tool(
                name="list_surfaces",
                description=(
                    "List every surface the eidetic-daemon has seen, with its "
                    "engram count (v0.0.13+). Returns a JSON object mapping "
                    "surface name to count, e.g. {'claude_code': 1234, 'cursor': 567}. "
                    "Use as a discovery step before querying a specific surface."
                ),
                inputSchema={"type": "object", "properties": {}, "required": []},
            ),
            Tool(
                name="purge_engrams",
                description=(
                    "Delete engrams from the daemon's store for a given surface "
                    "(v0.0.13+). Irreversible. Returns {'deleted': N}.\n\n"
                    "With `before` (unix epoch nanoseconds): only removes engrams "
                    "older than that timestamp, leaving newer ones intact.\n"
                    "Without `before` (or before=0): removes ALL engrams for the surface."
                ),
                inputSchema={
                    "type": "object",
                    "properties": {
                        "surface": {
                            "type": "string",
                            "description": "Surface tag to purge, e.g. claude_code | cursor",
                        },
                        "before": {
                            "type": "integer",
                            "description": "Unix epoch nanoseconds cutoff. Purges ts < before. 0 = purge all.",
                            "minimum": 0,
                        },
                    },
                    "required": ["surface"],
                },
            ),
            Tool(
                name="search_engrams",
                description=(
                    "Full-text search over engram payloads (v0.0.14+). Results are "
                    "ordered by relevance rank (best match first) and use the same "
                    "JSON shape as query_engrams.\n\n"
                    "`q` is an FTS5 match expression:\n"
                    "  - bare keywords: benchmark latency\n"
                    '  - phrase query: "benchmark result"\n'
                    "  - boolean: benchmark AND NOT cursor\n\n"
                    "Optional `surface` filter restricts to one surface. Optional "
                    "`limit` (default 50, max 500)."
                ),
                inputSchema={
                    "type": "object",
                    "properties": {
                        "q": {
                            "type": "string",
                            "description": "FTS5 match expression. Bare keywords or quoted phrase.",
                        },
                        "surface": {
                            "type": "string",
                            "description": "Restrict to one surface, e.g. claude_code. Empty = search all.",
                        },
                        "limit": {
                            "type": "integer",
                            "description": "Max results (default 50, max 500).",
                            "minimum": 1,
                            "maximum": 500,
                        },
                    },
                    "required": ["q"],
                },
            ),
            Tool(
                name="recent_engrams",
                description=(
                    "Return the most recent engrams across all surfaces, newest first "
                    "(v0.0.15+). Useful for getting a quick snapshot of recent activity "
                    "without a surface filter or keyword query.\n\n"
                    "Optional `since`: Unix nanoseconds; only return engrams with "
                    "ts > since (0 or omit = all). Optional `limit` (default 50, max 500)."
                ),
                inputSchema={
                    "type": "object",
                    "properties": {
                        "since": {
                            "type": "integer",
                            "description": "Unix nanoseconds lower bound (exclusive). 0 = no filter.",
                        },
                        "limit": {
                            "type": "integer",
                            "description": "Max results (default 50, max 500).",
                            "minimum": 1,
                            "maximum": 500,
                        },
                    },
                    "required": [],
                },
            ),
            Tool(
                name="insert_engram",
                description=(
                    "Directly insert an engram into the daemon store (v0.0.16+). "
                    "Bypasses the fsnotify capture path — use this to inject engrams "
                    "from sources the daemon doesn't watch (mobile, webhooks, API calls, "
                    "manual annotations).\n\n"
                    "`surface` and `payload` are required. `ts` defaults to now "
                    "(Unix nanoseconds). `meta` is optional free-form JSON string.\n\n"
                    "Returns the assigned engram ID. The engram is immediately "
                    "searchable via search_engrams and retrievable via query_engrams."
                ),
                inputSchema={
                    "type": "object",
                    "properties": {
                        "surface": {
                            "type": "string",
                            "description": "Surface tag, e.g. claude_code, cursor, mobile.",
                        },
                        "payload": {
                            "type": "string",
                            "description": "The engram content — text, JSON, markdown, etc.",
                        },
                        "ts": {
                            "type": "integer",
                            "description": "Timestamp in Unix nanoseconds. 0 or omit = server now.",
                        },
                        "meta": {
                            "type": "string",
                            "description": "Optional free-form JSON metadata string.",
                        },
                    },
                    "required": ["surface", "payload"],
                },
            ),
            Tool(
                name="insert_engrams_batch",
                description=(
                    "Bulk insert a list of engrams in one atomic transaction (v0.0.17+). "
                    "All items succeed or none do. Use when you have multiple engrams to "
                    "inject at once — relay sync, bulk import, session replay.\n\n"
                    "Each item requires `surface` and `payload`; `ts` and `meta` are "
                    "optional. Returns the count of inserted engrams."
                ),
                inputSchema={
                    "type": "object",
                    "properties": {
                        "items": {
                            "type": "array",
                            "description": "Array of engram objects.",
                            "items": {
                                "type": "object",
                                "properties": {
                                    "surface": {"type": "string"},
                                    "payload": {"type": "string"},
                                    "ts": {"type": "integer"},
                                    "meta": {"type": "string"},
                                },
                                "required": ["surface", "payload"],
                            },
                        },
                    },
                    "required": ["items"],
                },
            ),
        ]

    @server.call_tool()
    async def _call_tool(name: str, arguments: dict) -> list:  # type: ignore[misc]
        if name == "query_engrams":
            surface = str(arguments.get("surface", "")).strip()
            if not surface:
                return [TextContent(type="text", text="error: surface required")]
            limit = int(arguments.get("limit", 50))
            since = int(arguments.get("since", 0))
            raw_chunks = bool(arguments.get("raw_chunks", False))
            try:
                rows = daemon.query_engrams(surface=surface, limit=limit, since=since)
            except (DaemonError, ValueError) as exc:
                return [TextContent(type="text", text=f"error: {exc}")]
            if not raw_chunks:
                rows = reassemble_chunks(rows)
            payload = [asdict(r) for r in rows]
            return [TextContent(type="text", text=json.dumps(payload, indent=2))]

        if name == "daemon_status":
            return [TextContent(type="text", text=json.dumps({"healthy": daemon.healthy()}))]

        if name == "daemon_metrics":
            try:
                m = daemon.metrics()
            except DaemonError as exc:
                return [TextContent(type="text", text=f"error: {exc}")]
            return [TextContent(type="text", text=json.dumps(m, indent=2))]

        if name == "list_surfaces":
            try:
                counts = daemon.surfaces()
            except DaemonError as exc:
                return [TextContent(type="text", text=f"error: {exc}")]
            return [TextContent(type="text", text=json.dumps(counts, indent=2))]

        if name == "purge_engrams":
            surface = str(arguments.get("surface", "")).strip()
            if not surface:
                return [TextContent(type="text", text="error: surface required")]
            before = int(arguments.get("before", 0))
            try:
                deleted = daemon.purge_engrams(surface=surface, before=before)
            except (DaemonError, ValueError) as exc:
                return [TextContent(type="text", text=f"error: {exc}")]
            return [TextContent(type="text", text=json.dumps({"deleted": deleted}))]

        if name == "search_engrams":
            q = str(arguments.get("q", "")).strip()
            if not q:
                return [TextContent(type="text", text="error: q required")]
            surface = str(arguments.get("surface", "")).strip()
            limit = int(arguments.get("limit", 50))
            try:
                rows = daemon.search_engrams(q=q, surface=surface, limit=limit)
            except (DaemonError, ValueError) as exc:
                return [TextContent(type="text", text=f"error: {exc}")]
            payload = [asdict(r) for r in rows]
            return [TextContent(type="text", text=json.dumps(payload, indent=2))]

        if name == "recent_engrams":
            since = int(arguments.get("since", 0))
            limit = int(arguments.get("limit", 50))
            try:
                rows = daemon.recent_engrams(since=since, limit=limit)
            except DaemonError as exc:
                return [TextContent(type="text", text=f"error: {exc}")]
            payload = [asdict(r) for r in rows]
            return [TextContent(type="text", text=json.dumps(payload, indent=2))]

        if name == "insert_engram":
            surface = str(arguments.get("surface", "")).strip()
            payload_text = str(arguments.get("payload", "")).strip()
            if not surface:
                return [TextContent(type="text", text="error: surface required")]
            if not payload_text:
                return [TextContent(type="text", text="error: payload required")]
            ts = int(arguments.get("ts", 0))
            meta = str(arguments.get("meta", ""))
            try:
                engram_id = daemon.insert_engram(
                    surface=surface, payload=payload_text, ts=ts, meta=meta
                )
            except (DaemonError, ValueError) as exc:
                return [TextContent(type="text", text=f"error: {exc}")]
            return [TextContent(type="text", text=json.dumps({"id": engram_id}))]

        if name == "insert_engrams_batch":
            items = arguments.get("items", [])
            if not isinstance(items, list) or not items:
                return [TextContent(type="text", text="error: items must be non-empty array")]
            try:
                n = daemon.insert_engrams_batch(items)
            except (DaemonError, ValueError) as exc:
                return [TextContent(type="text", text=f"error: {exc}")]
            return [TextContent(type="text", text=json.dumps({"inserted": n}))]

        return [TextContent(type="text", text=f"error: unknown tool {name!r}")]

    return server


async def main() -> None:  # pragma: no cover
    """Entry point for `python -m eidetic_mcp.server`."""
    Server, stdio_server, _Tool, _TextContent = _mcp_imports()
    server = build_server()
    async with stdio_server() as (read, write):
        # InitializationOptions vary across mcp SDK versions; keep minimal.
        await server.run(read, write, server.create_initialization_options())


if __name__ == "__main__":  # pragma: no cover
    import asyncio

    asyncio.run(main())
