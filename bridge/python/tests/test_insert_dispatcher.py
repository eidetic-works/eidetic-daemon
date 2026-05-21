"""Dispatcher-layer tests for the engram-insertion tools.

Covers two tools that write into the local store:
    - insert_engram          (single-engram POST /engrams)
    - insert_engrams_batch   (atomic POST /engrams/batch)

The insert_engram REGRESSION GUARDS for the 0.0.10 no-strip fix live in
test_purge_confirm.py — they assert leading/trailing whitespace is
preserved. This file adds COMPLEMENTARY coverage: error-formatting paths,
batch atomicity contract, per-item shape errors, and the default-arg
behavior. Together they pin the dispatcher's full contract for the LLM.
"""

from __future__ import annotations

import asyncio
import json
from unittest.mock import MagicMock

import mcp.types as types

from eidetic_mcp.client import DaemonError
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


# ── insert_engram (complementary to test_purge_confirm regression tests) ─────


def test_insert_engram_happy_path_returns_id():
    """Happy path: minimal valid args → daemon called, {'id': N} returned."""
    client = MagicMock()
    client.insert_engram.return_value = 17
    text = _call_tool(
        client,
        "insert_engram",
        {"surface": "manual", "payload": "a real engram"},
    )
    body = json.loads(text)
    assert body == {"id": 17}
    client.insert_engram.assert_called_once_with(
        surface="manual", payload="a real engram", ts=0, meta=""
    )


def test_insert_engram_with_ts_and_meta_passes_through():
    """Optional ts + meta args reach daemon-client method intact."""
    client = MagicMock()
    client.insert_engram.return_value = 99
    text = _call_tool(
        client,
        "insert_engram",
        {
            "surface": "mobile",
            "payload": "from phone",
            "ts": 1_700_000_000_000_000_000,
            "meta": '{"src":"ios"}',
        },
    )
    assert json.loads(text) == {"id": 99}
    client.insert_engram.assert_called_once_with(
        surface="mobile",
        payload="from phone",
        ts=1_700_000_000_000_000_000,
        meta='{"src":"ios"}',
    )


def test_insert_engram_daemon_error_surfaces():
    """Daemon-side rejection (4xx/5xx) → 'error: ...' text."""
    client = MagicMock()
    client.insert_engram.side_effect = DaemonError(
        "daemon returned 413: payload too large"
    )
    text = _call_tool(
        client,
        "insert_engram",
        {"surface": "manual", "payload": "small"},
    )
    assert text.startswith("error:")
    assert "413" in text


def test_insert_engram_surface_with_leading_whitespace_stripped():
    """Surface IS .strip()'d (acceptable — it's a tag, not content). Payload
    is NOT (the audit fix). This test pins the surface-only strip behavior
    so a future overcorrection doesn't accidentally strip payload too."""
    client = MagicMock()
    client.insert_engram.return_value = 1
    _call_tool(
        client,
        "insert_engram",
        {"surface": "  manual  ", "payload": "real content"},
    )
    # surface stripped, payload intact
    client.insert_engram.assert_called_once_with(
        surface="manual", payload="real content", ts=0, meta=""
    )


# ── insert_engrams_batch ─────────────────────────────────────────────────────


def test_insert_engrams_batch_happy_path():
    """Happy path: batch insert → daemon called → {'inserted': N} returned."""
    client = MagicMock()
    client.insert_engrams_batch.return_value = 3
    items = [
        {"surface": "manual", "payload": "first"},
        {"surface": "manual", "payload": "second"},
        {"surface": "cursor", "payload": "third", "ts": 12345, "meta": "x"},
    ]
    text = _call_tool(client, "insert_engrams_batch", {"items": items})
    body = json.loads(text)
    assert body == {"inserted": 3}
    client.insert_engrams_batch.assert_called_once_with(items)


def test_insert_engrams_batch_empty_array_rejected():
    """items=[] → dispatcher rejects, daemon NEVER called (defense in depth
    beyond client-side ValueError)."""
    client = MagicMock()
    text = _call_tool(client, "insert_engrams_batch", {"items": []})
    assert text.startswith("error:")
    assert "non-empty" in text.lower() or "items" in text.lower()
    client.insert_engrams_batch.assert_not_called()


def test_insert_engrams_batch_per_item_missing_surface_rejected_at_schema():
    """Per-item missing surface → MCP SDK input validator rejects BEFORE the
    dispatcher runs (schema declares items.*.required=['surface','payload']).
    The net effect (rejection + daemon not called) is what matters for the LLM.

    This catches the schema-level defense: if the per-item required list is
    ever weakened, this test fails and forces explicit re-acknowledgement.
    """
    client = MagicMock()
    text = _call_tool(
        client,
        "insert_engrams_batch",
        {
            "items": [
                {"surface": "manual", "payload": "ok"},
                {"payload": "missing-surface"},  # triggers schema rejection
            ]
        },
    )
    # Either schema-validation prefix or dispatcher 'error:' prefix is
    # acceptable; the daemon MUST NOT be called either way.
    assert "surface" in text.lower() or "validation" in text.lower()
    client.insert_engrams_batch.assert_not_called()


def test_insert_engrams_batch_per_item_missing_payload_rejected_at_schema():
    """Per-item missing payload → schema rejection (mirror of the surface test).
    Schema-level defense pins the per-item required list."""
    client = MagicMock()
    text = _call_tool(
        client,
        "insert_engrams_batch",
        {"items": [{"surface": "manual"}]},
    )
    assert "payload" in text.lower() or "validation" in text.lower()
    client.insert_engrams_batch.assert_not_called()


def test_insert_engrams_batch_value_error_from_daemon_surfaces():
    """If the daemon-client raises ValueError (e.g. client-side validation
    beyond what the schema catches), it reaches the LLM as 'error: ...'.

    Uses a schema-valid batch (surface + payload present) so we test the
    DISPATCHER's error formatting, not the schema layer.
    """
    client = MagicMock()
    client.insert_engrams_batch.side_effect = ValueError(
        "items[1]: meta exceeds 2000 chars"
    )
    text = _call_tool(
        client,
        "insert_engrams_batch",
        {
            "items": [
                {"surface": "manual", "payload": "a"},
                {"surface": "manual", "payload": "b", "meta": "x" * 3000},
            ]
        },
    )
    assert text.startswith("error:")
    assert "items[1]" in text


def test_insert_engrams_batch_atomicity_daemon_rollback_surfaces():
    """Daemon rolls back the entire batch (atomicity contract). The DaemonError
    surface tells the LLM the batch DID NOT partially insert."""
    client = MagicMock()
    client.insert_engrams_batch.side_effect = DaemonError(
        "daemon returned 500: transaction rolled back"
    )
    text = _call_tool(
        client,
        "insert_engrams_batch",
        {
            "items": [
                {"surface": "manual", "payload": "a"},
                {"surface": "manual", "payload": "b"},
            ]
        },
    )
    assert text.startswith("error:")
    assert "rolled back" in text or "500" in text
    # CRITICAL: the LLM seeing "error: rolled back" must know nothing was
    # inserted. The atomicity contract is a description-level promise — the
    # test verifies the error reaches the LLM in a recognizable form so it
    # can communicate the rollback to the user.


def test_insert_engrams_batch_non_list_items_rejected():
    """items must be a list. A dict (common LLM mistake) is rejected with
    a clear error.

    NOTE: MCP SDK input-validation may reject the type mismatch BEFORE the
    dispatcher runs (schema declares type=array). Either rejection layer
    is acceptable as long as the daemon is never called.
    """
    client = MagicMock()
    text = _call_tool(
        client,
        "insert_engrams_batch",
        {"items": {"surface": "x", "payload": "y"}},
    )
    assert text.startswith("error:") or "validation" in text.lower()
    client.insert_engrams_batch.assert_not_called()
