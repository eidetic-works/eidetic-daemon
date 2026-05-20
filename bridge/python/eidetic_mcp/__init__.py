"""eidetic_mcp — minimal Python wrapper exposing the eidetic-daemon UDS API as MCP tools.

Per spec § 7 Open Q #5: this module implements the separate Python wrapper that calls
the daemon's UDS API. The daemon binary itself ships independently.

Usage (stdio MCP server, runs alongside the daemon):

    eideticd &                          # daemon listens on UDS
    python -m eidetic_mcp.server        # MCP stdio server; tool calls hit UDS
"""

__version__ = "0.0.8"
