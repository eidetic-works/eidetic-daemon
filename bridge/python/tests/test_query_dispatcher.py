"""Dispatcher-layer tests for the read/listing tools.

Covers four tools that the host LLM uses to enumerate or page through engrams:
    - query_engrams    (the workhorse paging / surface-filter retrieval)
    - recent_engrams   (cross-surface newest-first snapshot)
    - count_engrams    (cheap monitoring badge)
    - list_surfaces    (discovery before querying a surface)

These exercise the MCP `_call_tool` dispatcher — argument coercion, error
formatting, JSON-encoding of the daemon's reply. Sibling to test_purge_confirm
(audit fix tests) — same MagicMock + request_handler pattern. The underlying
client.* methods are exercised by test_client.py against a fake UDS daemon;
here we ONLY test the dispatcher surface that the host LLM actually triggers.
"""

from __future__ import annotations

import asyncio
import json
from unittest.mock import MagicMock

import mcp.types as types

from eidetic_mcp.client import DaemonError, Engram
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
    return result.root.content[0].text


# ── query_engrams ────────────────────────────────────────────────────────────


def test_query_engrams_happy_path_returns_json_list():
    """Happy path: daemon returns 2 engrams → dispatcher emits a JSON list."""
    client = MagicMock()
    client.query_engrams.return_value = (
        Engram(id=1, surface="claude_code", ts=10, payload="first", meta=""),
        Engram(id=2, surface="claude_code", ts=20, payload="second", meta='{"k":"v"}'),
    )
    text = _call_tool(client, "query_engrams", {"surface": "claude_code", "limit": 10})
    body = json.loads(text)
    assert isinstance(body, list) and len(body) == 2
    assert body[0]["id"] == 1 and body[0]["payload"] == "first"
    assert body[1]["meta"] == '{"k":"v"}'
    client.query_engrams.assert_called_once_with(
        surface="claude_code", limit=10, since=0, before=0, asc=False
    )


def test_query_engrams_defaults_when_args_omitted():
    """No args → dispatcher fills in the documented defaults."""
    client = MagicMock()
    client.query_engrams.return_value = ()
    text = _call_tool(client, "query_engrams", {})
    assert text == "[]"
    client.query_engrams.assert_called_once_with(
        surface="", limit=50, since=0, before=0, asc=False
    )


def test_query_engrams_daemon_error_surfaces_as_error_text():
    """DaemonError → 'error: ...' text, NOT a stack trace or 500."""
    client = MagicMock()
    client.query_engrams.side_effect = DaemonError("daemon returned 503: temporarily unavailable")
    text = _call_tool(client, "query_engrams", {"surface": "claude_code"})
    assert text.startswith("error:")
    assert "503" in text


def test_query_engrams_value_error_surfaces_as_error_text():
    """ValueError from the client (bad arg) also reaches the LLM as text."""
    client = MagicMock()
    client.query_engrams.side_effect = ValueError("limit must be positive")
    text = _call_tool(client, "query_engrams", {})
    assert text.startswith("error:")
    assert "limit" in text.lower()


def test_query_engrams_raw_chunks_flag_propagates_via_reassembly_bypass():
    """raw_chunks=true → server skips reassemble_chunks (single chunked engram
    stays as one row instead of being merged). With a single non-chunked
    engram either path yields the same result; the assertion is that the
    daemon call args don't carry raw_chunks (only the dispatcher uses it)."""
    client = MagicMock()
    client.query_engrams.return_value = (
        Engram(id=5, surface="cursor", ts=99, payload="solo", meta=""),
    )
    text = _call_tool(client, "query_engrams", {"raw_chunks": True})
    body = json.loads(text)
    assert len(body) == 1 and body[0]["id"] == 5
    # raw_chunks is NOT a daemon-side arg — it's only a dispatcher hint.
    _, kwargs = client.query_engrams.call_args
    assert "raw_chunks" not in kwargs


# ── recent_engrams ───────────────────────────────────────────────────────────


def test_recent_engrams_happy_path():
    """Happy path: daemon returns newest engrams; dispatcher emits JSON list."""
    client = MagicMock()
    client.recent_engrams.return_value = (
        Engram(id=99, surface="claude_code", ts=999, payload="latest", meta=""),
    )
    text = _call_tool(client, "recent_engrams", {"limit": 25})
    body = json.loads(text)
    assert len(body) == 1 and body[0]["id"] == 99 and body[0]["payload"] == "latest"
    client.recent_engrams.assert_called_once_with(since=0, before=0, limit=25)


def test_recent_engrams_empty_result_returns_empty_list():
    """Empty store → '[]' (LLM sees no data, NOT an error)."""
    client = MagicMock()
    client.recent_engrams.return_value = ()
    text = _call_tool(client, "recent_engrams", {})
    assert text == "[]"
    client.recent_engrams.assert_called_once_with(since=0, before=0, limit=50)


def test_recent_engrams_daemon_unreachable_surfaces_as_error():
    """Daemon down → DaemonError caught + emitted as 'error: ...' text."""
    client = MagicMock()
    client.recent_engrams.side_effect = DaemonError("daemon transport / parse error: [Errno 2]")
    text = _call_tool(client, "recent_engrams", {})
    assert text.startswith("error:")
    assert "transport" in text


def test_recent_engrams_since_and_before_pass_through():
    """since + before args reach the daemon-client method intact."""
    client = MagicMock()
    client.recent_engrams.return_value = ()
    _call_tool(
        client,
        "recent_engrams",
        {"since": 1_700_000_000_000_000_000, "before": 1_800_000_000_000_000_000, "limit": 7},
    )
    client.recent_engrams.assert_called_once_with(
        since=1_700_000_000_000_000_000,
        before=1_800_000_000_000_000_000,
        limit=7,
    )


# ── count_engrams ────────────────────────────────────────────────────────────


def test_count_engrams_happy_path():
    """Happy path: returns {'count': N}."""
    client = MagicMock()
    client.count_engrams.return_value = 1234
    text = _call_tool(client, "count_engrams", {"surface": "claude_code"})
    body = json.loads(text)
    assert body == {"count": 1234}
    client.count_engrams.assert_called_once_with(surface="claude_code", since=0)


def test_count_engrams_no_args_counts_all():
    """No args → counts all surfaces, since=0."""
    client = MagicMock()
    client.count_engrams.return_value = 0
    text = _call_tool(client, "count_engrams", {})
    assert json.loads(text) == {"count": 0}
    client.count_engrams.assert_called_once_with(surface="", since=0)


def test_count_engrams_daemon_error_surfaces():
    """DaemonError → 'error: ...' text."""
    client = MagicMock()
    client.count_engrams.side_effect = DaemonError("daemon returned 500: db locked")
    text = _call_tool(client, "count_engrams", {})
    assert text.startswith("error:")
    assert "500" in text


def test_count_engrams_since_filter_passes_through():
    """since arg reaches daemon-client method intact."""
    client = MagicMock()
    client.count_engrams.return_value = 42
    _call_tool(client, "count_engrams", {"since": 12345})
    client.count_engrams.assert_called_once_with(surface="", since=12345)


# ── list_surfaces ────────────────────────────────────────────────────────────


def test_list_surfaces_happy_path():
    """Happy path: dict of surface → count."""
    client = MagicMock()
    client.surfaces.return_value = {"claude_code": 1101, "cursor": 179}
    text = _call_tool(client, "list_surfaces", {})
    body = json.loads(text)
    assert body == {"claude_code": 1101, "cursor": 179}
    client.surfaces.assert_called_once_with()


def test_list_surfaces_empty_store_returns_empty_dict():
    """Empty store → '{}' (LLM sees no surfaces, NOT an error)."""
    client = MagicMock()
    client.surfaces.return_value = {}
    text = _call_tool(client, "list_surfaces", {})
    assert json.loads(text) == {}


def test_list_surfaces_daemon_error_surfaces():
    """Daemons predating v0.0.13 surface as 'error: ...' text — LLM-readable."""
    client = MagicMock()
    client.surfaces.side_effect = DaemonError("daemon returned 404: not implemented")
    text = _call_tool(client, "list_surfaces", {})
    assert text.startswith("error:")
    assert "404" in text
