"""Server-side dispatch tests for the eidetic-mcp 0.0.10 audit fixes.

Exercises the MCP `_call_tool` handler that the host LLM actually triggers
(versus client.py methods, which are covered in test_client.py / test_nucleus_*).

Covers three audit-derived behaviors:
    1. purge_engrams REQUIRES confirm=true — defense against autonomous LLM
       invocation that would silently wipe a surface's history.
    2. purge_engrams forwards to daemon.purge_engrams() only when confirm=true.
    3. insert_engram preserves whitespace in payload — markdown engrams with
       leading/trailing whitespace must round-trip byte-equivalent.

The handler is reached via the registered request_handlers dict on the MCP
Server, mirroring how the live stdio server dispatches. We mock the
DaemonClient so the test stays in-process (no UDS, no daemon binary).
"""

from __future__ import annotations

import asyncio
import json
from unittest.mock import MagicMock

import mcp.types as types

from eidetic_mcp.server import build_server


def _call_tool(client: MagicMock, name: str, arguments: dict) -> str:
    """Drive the MCP server's CallToolRequest handler and return the first
    TextContent's text. Mirrors what a real MCP client triggers over stdio."""
    server = build_server(client=client)
    handler = server.request_handlers[types.CallToolRequest]
    req = types.CallToolRequest(
        method="tools/call",
        params=types.CallToolRequestParams(name=name, arguments=arguments),
    )
    result = asyncio.run(handler(req))
    # result.root.content is the [TextContent(...)] list returned by _call_tool
    return result.root.content[0].text


# ── Fix 1: purge_engrams confirm gate ────────────────────────────────────────

def test_purge_engrams_rejects_without_confirm():
    """confirm omitted → schema-level rejection by the MCP SDK (required
    field), daemon.purge_engrams NEVER called. This is the first layer of
    defense; the dispatcher-side check below catches the confirm=False case.
    """
    client = MagicMock()
    text = _call_tool(client, "purge_engrams", {"surface": "scratch"})
    # MCP SDK input validator rejects with "Input validation error: 'confirm'
    # is a required property" — different prefix than the dispatcher "error:"
    # path, but the net effect (rejection + no daemon call) is what matters.
    assert "confirm" in text.lower()
    client.purge_engrams.assert_not_called()


def test_purge_engrams_rejects_confirm_false():
    """confirm=False (explicit) → dispatcher-side rejection, daemon.purge_engrams
    NEVER called. Schema only enforces presence, so falsy must be caught by
    the dispatcher — this is the audit-fix code path.
    """
    client = MagicMock()
    text = _call_tool(
        client, "purge_engrams", {"surface": "scratch", "confirm": False}
    )
    assert text.startswith("error:")
    assert "confirm" in text.lower()
    client.purge_engrams.assert_not_called()


def test_purge_engrams_proceeds_with_confirm_true():
    """confirm=True → daemon.purge_engrams called with surface + before."""
    client = MagicMock()
    client.purge_engrams.return_value = 7
    text = _call_tool(
        client,
        "purge_engrams",
        {"surface": "scratch", "before": 1_700_000_000_000_000_000, "confirm": True},
    )
    body = json.loads(text)
    assert body == {"deleted": 7}
    client.purge_engrams.assert_called_once_with(
        surface="scratch", before=1_700_000_000_000_000_000
    )


def test_purge_engrams_still_validates_surface():
    """Empty surface still rejected even with confirm=true."""
    client = MagicMock()
    text = _call_tool(client, "purge_engrams", {"surface": "", "confirm": True})
    assert text.startswith("error:")
    assert "surface" in text.lower()
    client.purge_engrams.assert_not_called()


# ── Fix 2: insert_engram payload whitespace preservation ─────────────────────

def test_insert_engram_preserves_leading_trailing_whitespace():
    """Multi-line markdown payload with leading/trailing whitespace must
    round-trip unmodified through the dispatcher.

    Before 0.0.10, server.py called .strip() on the payload, mangling
    markdown engrams. insert_engrams_batch did NOT strip — the per-item
    inconsistency was the audit finding.
    """
    client = MagicMock()
    client.insert_engram.return_value = 42
    payload = "\n  # Heading\n\n  body line\n\n  trailing ws  \n\n"
    text = _call_tool(
        client,
        "insert_engram",
        {"surface": "manual", "payload": payload},
    )
    body = json.loads(text)
    assert body == {"id": 42}
    # Verify the daemon got the EXACT payload string — no leading/trailing
    # whitespace removed by the dispatcher.
    client.insert_engram.assert_called_once_with(
        surface="manual", payload=payload, ts=0, meta=""
    )


def test_insert_engram_still_rejects_whitespace_only_payload():
    """A payload that is only whitespace is still 'empty' — the IF check
    uses .strip() but the stored value is untouched. We assert the rejection
    so the empty-payload defense remains in place."""
    client = MagicMock()
    text = _call_tool(
        client, "insert_engram", {"surface": "manual", "payload": "   \n\t  "}
    )
    assert text.startswith("error:")
    assert "payload" in text.lower()
    client.insert_engram.assert_not_called()


def test_insert_engram_still_rejects_empty_surface():
    """Empty surface still rejected (regression guard)."""
    client = MagicMock()
    text = _call_tool(
        client, "insert_engram", {"surface": "", "payload": "real content"}
    )
    assert text.startswith("error:")
    assert "surface" in text.lower()
    client.insert_engram.assert_not_called()
