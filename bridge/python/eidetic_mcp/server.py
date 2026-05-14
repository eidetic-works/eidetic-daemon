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
                    "page. P95 retrieval latency on a 10K-row store is ~0.27 ms."
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
        ]

    @server.call_tool()
    async def _call_tool(name: str, arguments: dict) -> list:  # type: ignore[misc]
        if name == "query_engrams":
            surface = str(arguments.get("surface", "")).strip()
            if not surface:
                return [TextContent(type="text", text="error: surface required")]
            limit = int(arguments.get("limit", 50))
            since = int(arguments.get("since", 0))
            try:
                rows = daemon.query_engrams(surface=surface, limit=limit, since=since)
            except (DaemonError, ValueError) as exc:
                return [TextContent(type="text", text=f"error: {exc}")]
            payload = [asdict(r) for r in rows]
            return [TextContent(type="text", text=json.dumps(payload, indent=2))]

        if name == "daemon_status":
            return [TextContent(type="text", text=json.dumps({"healthy": daemon.healthy()}))]

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
