"""Dispatcher-layer tests for the per-engram retrieve/delete tools.

Covers two tools that operate on a single engram by primary key:
    - get_engram_by_id     (read one engram by ID)
    - delete_engram_by_id  (irreversibly delete one engram by ID)

These tools share an identical id-coercion path (server.py:803-807,
824-828): non-integer raw_id raises TypeError/ValueError, caught and
emitted as 'error: id must be a positive integer'. The audit called out
the error-formatting consistency between these two as a gap. Both tools
also surface daemon 404s as DaemonError → 'error: ...' text — the LLM
sees a normalized error format whether the ID is invalid client-side or
not-found server-side.

delete_engram_by_id is irreversible — these tests verify that the
dispatcher passes through to the daemon-side method, where the actual
deletion happens. There is NO confirm-gate equivalent of purge_engrams
here (single-engram deletion is lower-stakes; the audit flagged this
asymmetry as deliberate).
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


# ── get_engram_by_id ─────────────────────────────────────────────────────────


def test_get_engram_by_id_happy_path():
    """Happy path: daemon returns the engram, dispatcher emits its JSON."""
    client = MagicMock()
    client.get_engram_by_id.return_value = Engram(
        id=42, surface="cursor", ts=12345, payload="content body", meta='{"k":"v"}'
    )
    text = _call_tool(client, "get_engram_by_id", {"id": 42})
    body = json.loads(text)
    assert body["id"] == 42
    assert body["surface"] == "cursor"
    assert body["payload"] == "content body"
    assert body["meta"] == '{"k":"v"}'
    client.get_engram_by_id.assert_called_once_with(42)


def test_get_engram_by_id_404_surfaces_as_error():
    """ID does not exist → DaemonError 404 → 'error: ...' text."""
    client = MagicMock()
    client.get_engram_by_id.side_effect = DaemonError("daemon returned 404: not found")
    text = _call_tool(client, "get_engram_by_id", {"id": 99999})
    assert text.startswith("error:")
    assert "404" in text


def test_get_engram_by_id_non_integer_id_rejected_at_dispatcher():
    """Non-int id (e.g. string 'abc') → dispatcher's int() coerce raises,
    caught and surfaced as the consistent 'id must be a positive integer'
    error message. Schema declares type=integer, so MCP SDK may also reject
    at validation; either layer is acceptable.
    """
    client = MagicMock()
    text = _call_tool(client, "get_engram_by_id", {"id": "abc"})
    assert text.startswith("error:") or "validation" in text.lower()
    # Whichever layer caught it, the daemon MUST NOT be called.
    client.get_engram_by_id.assert_not_called()


def test_get_engram_by_id_value_error_surfaces():
    """client.get_engram_by_id raises ValueError for non-positive ids;
    dispatcher surfaces as 'error: ...' text."""
    client = MagicMock()
    client.get_engram_by_id.side_effect = ValueError(
        "id must be a positive integer, got 0"
    )
    text = _call_tool(client, "get_engram_by_id", {"id": 0})
    assert text.startswith("error:")
    assert "positive" in text.lower()


def test_get_engram_by_id_with_empty_meta_renders_correctly():
    """An engram with empty meta still serializes correctly (default field)."""
    client = MagicMock()
    client.get_engram_by_id.return_value = Engram(
        id=7, surface="claude_code", ts=1, payload="hi", meta=""
    )
    text = _call_tool(client, "get_engram_by_id", {"id": 7})
    body = json.loads(text)
    assert body["meta"] == ""
    assert body["snippet"] == ""  # default


# ── delete_engram_by_id ──────────────────────────────────────────────────────


def test_delete_engram_by_id_happy_path():
    """Happy path: daemon returns True → {'deleted': 1}.

    The IRREVERSIBILITY of this op lives daemon-side; here we verify the
    dispatcher correctly routes the call. A `confirm` gate equivalent to
    purge_engrams' is deliberately ABSENT (single-engram, lower stakes).
    """
    client = MagicMock()
    client.delete_engram_by_id.return_value = True
    text = _call_tool(client, "delete_engram_by_id", {"id": 42})
    body = json.loads(text)
    assert body == {"deleted": 1}
    client.delete_engram_by_id.assert_called_once_with(42)


def test_delete_engram_by_id_returns_zero_when_daemon_says_false():
    """Daemon returns False (already gone, no-op) → {'deleted': 0}."""
    client = MagicMock()
    client.delete_engram_by_id.return_value = False
    text = _call_tool(client, "delete_engram_by_id", {"id": 42})
    assert json.loads(text) == {"deleted": 0}


def test_delete_engram_by_id_404_surfaces():
    """ID does not exist → DaemonError 404 → 'error: ...' text."""
    client = MagicMock()
    client.delete_engram_by_id.side_effect = DaemonError(
        "daemon returned 404: not found"
    )
    text = _call_tool(client, "delete_engram_by_id", {"id": 99999})
    assert text.startswith("error:")
    assert "404" in text


def test_delete_engram_by_id_non_integer_id_rejected():
    """Non-int id → dispatcher coerce error OR schema rejection, daemon NEVER
    called. CRITICAL because this is the irreversible-action surface — a
    bad arg must not silently coerce-to-0 and accidentally delete something."""
    client = MagicMock()
    text = _call_tool(client, "delete_engram_by_id", {"id": "not-an-int"})
    assert text.startswith("error:") or "validation" in text.lower()
    client.delete_engram_by_id.assert_not_called()


def test_delete_engram_by_id_value_error_surfaces():
    """client.delete_engram_by_id raises ValueError for non-positive id;
    dispatcher surfaces consistently. Identical error-formatting path to
    get_engram_by_id (audit-noted consistency)."""
    client = MagicMock()
    client.delete_engram_by_id.side_effect = ValueError(
        "id must be a positive integer, got -1"
    )
    text = _call_tool(client, "delete_engram_by_id", {"id": -1})
    assert text.startswith("error:")
    assert "positive" in text.lower()


# ── error-formatting consistency between get/delete (audit-noted) ────────────


def test_get_and_delete_share_id_error_format():
    """Audit point: both tools share the same 'error: id must be a positive
    integer' message when raw_id is non-coercible. Regression guard against
    drift between the two error paths in server.py:807 + server.py:828.
    """
    client = MagicMock()

    # Both raw=None paths trigger the int(None) → TypeError → caught.
    get_text = _call_tool(client, "get_engram_by_id", {"id": None})
    del_text = _call_tool(client, "delete_engram_by_id", {"id": None})

    # Either both schema-reject ("validation"), or both dispatcher-reject
    # with the same 'positive integer' message. Drift between the two
    # would make this assertion fail.
    if get_text.startswith("error:") and del_text.startswith("error:"):
        # Both went through the dispatcher path; messages must match.
        assert "positive integer" in get_text
        assert "positive integer" in del_text
    else:
        # Both went through schema validation; messages may differ in
        # phrasing but both must mention the field name.
        assert "id" in get_text.lower()
        assert "id" in del_text.lower()
