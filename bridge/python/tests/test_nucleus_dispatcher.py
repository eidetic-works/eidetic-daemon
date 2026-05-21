"""Dispatcher-layer tests for the nucleus_* tools (digest/timeline/link/curate).

Covers the four high-level recall/curation tools at the MCP `_call_tool`
dispatcher layer:
    - nucleus_digest    (server-side `instructions` reordering at server.py:893-898)
    - nucleus_timeline  (server-side `instructions` injection + surfaces list validation)
    - nucleus_link      (id-coerce + arg-coerce + daemon round-trip)
    - nucleus_curate    (id-coerce + action/note pass-through)

The underlying client.* methods are exercised in test_nucleus_digest.py /
test_nucleus_timeline.py / test_nucleus_link.py / test_nucleus_curate.py
against a fake UDS daemon. Here we ONLY test the dispatcher surface — what
the host LLM actually receives.

The audit specifically called out the `instructions` field promotion logic
in nucleus_digest (server.py:893-898) and nucleus_timeline (server.py:920-929)
as uncovered. These tests pin both: when the daemon returns a dict with
`instructions` as the Nth key, the dispatcher MUST promote it to the FIRST
key. This is the host-LLM-rendering contract.
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


# ── nucleus_digest ───────────────────────────────────────────────────────────


def test_nucleus_digest_happy_path_default_window():
    """No args → window='7d' default → daemon called, full payload returned."""
    client = MagicMock()
    daemon_body = {
        "window": "7d",
        "total_engrams": 100,
        "by_surface": {"claude_code": 80, "cursor": 20},
        "instructions": "Render as a markdown digest with sections.",
    }
    client.digest.return_value = daemon_body
    text = _call_tool(client, "nucleus_digest", {})
    body = json.loads(text)
    assert body["total_engrams"] == 100
    assert body["window"] == "7d"
    assert body["instructions"] == "Render as a markdown digest with sections."
    client.digest.assert_called_once_with(window="7d")


def test_nucleus_digest_window_arg_passes_through():
    """window='24h' reaches the daemon-client method intact."""
    client = MagicMock()
    client.digest.return_value = {"window": "24h", "total_engrams": 5}
    _call_tool(client, "nucleus_digest", {"window": "24h"})
    client.digest.assert_called_once_with(window="24h")


def test_nucleus_digest_instructions_promoted_to_top():
    """REGRESSION GUARD for server.py:893-898.

    The daemon may return `instructions` as the Nth key (e.g. last). The
    dispatcher MUST reorder the dict so `instructions` is the FIRST key —
    host LLMs that render top-keys-first see the instructions before they
    see the data. Without this reorder, the LLM may fail to follow the
    rendering instructions.
    """
    client = MagicMock()
    # Note: instructions is the LAST key in the daemon response.
    client.digest.return_value = {
        "window": "7d",
        "total_engrams": 50,
        "by_surface": {"claude_code": 50},
        "top_terms": [],
        "instructions": "TOP-INSTRUCTION: render brief digest",
    }
    text = _call_tool(client, "nucleus_digest", {})
    body = json.loads(text)
    keys = list(body.keys())
    # After dispatcher reordering, 'instructions' must be the FIRST key.
    assert keys[0] == "instructions", f"expected instructions first, got {keys}"
    assert body["instructions"] == "TOP-INSTRUCTION: render brief digest"
    # All other keys still present (no data lost in reorder).
    assert {"window", "total_engrams", "by_surface", "top_terms"}.issubset(set(keys))


def test_nucleus_digest_without_instructions_field_passes_through_untouched():
    """If daemon body has no `instructions` key (e.g. older daemon), the
    dispatcher should NOT crash or add an empty one — just return the body
    as-is. Tests the `if isinstance(body, dict) and 'instructions' in body`
    guard at server.py:893."""
    client = MagicMock()
    client.digest.return_value = {"window": "7d", "total_engrams": 50}
    text = _call_tool(client, "nucleus_digest", {})
    body = json.loads(text)
    assert body == {"window": "7d", "total_engrams": 50}
    assert "instructions" not in body


def test_nucleus_digest_bad_window_surfaces_as_error():
    """Invalid window string → ValueError from client → 'error: ...' text.
    Schema declares enum=['24h','7d','30d'] so the MCP SDK may also reject
    at input-validation; either layer is acceptable."""
    client = MagicMock()
    client.digest.side_effect = ValueError("window must be one of '24h', '7d', '30d', got '1h'")
    text = _call_tool(client, "nucleus_digest", {"window": "1h"})
    assert text.startswith("error:") or "validation" in text.lower()


def test_nucleus_digest_daemon_error_surfaces():
    """Daemon-side failure → 'error: ...' text."""
    client = MagicMock()
    client.digest.side_effect = DaemonError("daemon returned 500: db unavailable")
    text = _call_tool(client, "nucleus_digest", {})
    assert text.startswith("error:")
    assert "500" in text


# ── nucleus_timeline ─────────────────────────────────────────────────────────


def test_nucleus_timeline_happy_path_injects_instructions():
    """REGRESSION GUARD for server.py:916-929.

    The daemon's /timeline does NOT author `instructions` server-side
    (digest does, timeline does NOT). The dispatcher INJECTS a static
    instruction string telling the LLM to render the result as a brief
    activity narrative. The injected `instructions` must be the FIRST key.
    """
    client = MagicMock()
    client.timeline.return_value = {
        "engrams": [{"id": 1, "surface": "x", "ts": 1, "payload": "p", "meta": ""}],
        "count": 1,
        "surfaces": [],
    }
    text = _call_tool(client, "nucleus_timeline", {})
    body = json.loads(text)
    keys = list(body.keys())
    # 'instructions' injected as first key.
    assert keys[0] == "instructions", f"expected instructions first, got {keys}"
    assert "activity narrative" in body["instructions"]
    # Daemon fields still present.
    assert body["count"] == 1
    assert len(body["engrams"]) == 1


def test_nucleus_timeline_surfaces_list_filter_passes_through():
    """surfaces arg (list of strings) reaches daemon-client method intact."""
    client = MagicMock()
    client.timeline.return_value = {"engrams": [], "count": 0, "surfaces": ["claude_code", "cursor"]}
    _call_tool(
        client,
        "nucleus_timeline",
        {"surfaces": ["claude_code", "cursor"], "limit": 100},
    )
    # Confirm the daemon was called with the surfaces list.
    _, kwargs = client.timeline.call_args
    assert kwargs["surfaces"] == ["claude_code", "cursor"]
    assert kwargs["limit"] == 100


def test_nucleus_timeline_surfaces_must_be_list():
    """Audit gap: validate that `surfaces` is a list. A string (common
    LLM mistake) → 'error: surfaces must be a list of strings'.

    Note: schema declares type=array, so the MCP SDK may catch this at
    input-validation BEFORE the dispatcher's isinstance() check at
    server.py:903-905. Either layer's rejection is acceptable.
    """
    client = MagicMock()
    text = _call_tool(
        client,
        "nucleus_timeline",
        {"surfaces": "claude_code"},  # string, not list
    )
    assert text.startswith("error:") or "validation" in text.lower()
    client.timeline.assert_not_called()


def test_nucleus_timeline_empty_string_surfaces_filtered_out():
    """Audit gap: surfaces entries that are empty after .strip() are
    filtered out at server.py:906 — daemon receives only the non-empty ones.
    Guards against LLMs sending [''] or ['  '] as a no-op filter."""
    client = MagicMock()
    client.timeline.return_value = {"engrams": [], "count": 0, "surfaces": []}
    _call_tool(
        client,
        "nucleus_timeline",
        {"surfaces": ["", "  ", "claude_code", " "]},
    )
    _, kwargs = client.timeline.call_args
    # Only 'claude_code' should reach the daemon — empty/whitespace stripped.
    assert kwargs["surfaces"] == ["claude_code"]


def test_nucleus_timeline_bad_window_surfaces_as_error():
    """window='1h' → _window_to_since_ns raises ValueError → 'error: ...'.

    NOTE: schema declares enum=['24h','7d','30d'], so MCP SDK input-validation
    will reject before the dispatcher's `_window_to_since_ns` raises. Either
    rejection layer is acceptable — the daemon must NOT be called.
    """
    client = MagicMock()
    text = _call_tool(client, "nucleus_timeline", {"window": "1h"})
    assert text.startswith("error:") or "validation" in text.lower()
    client.timeline.assert_not_called()


def test_nucleus_timeline_daemon_error_surfaces():
    """Daemon-side failure → 'error: ...' text."""
    client = MagicMock()
    client.timeline.side_effect = DaemonError("daemon returned 500")
    text = _call_tool(client, "nucleus_timeline", {})
    assert text.startswith("error:")
    assert "500" in text


def test_nucleus_timeline_non_dict_response_passes_through_unchanged():
    """If daemon returns a non-dict (shouldn't happen, but defensive code
    path), the dispatcher does NOT inject instructions — body passes through
    as-is. Guards the `if isinstance(body, dict)` check at server.py:924."""
    client = MagicMock()
    # Unusual return type — wrap in dict to satisfy json.dumps.
    client.timeline.return_value = []  # list, not dict
    text = _call_tool(client, "nucleus_timeline", {})
    body = json.loads(text)
    # No instructions injection on non-dict body.
    assert body == []


# ── nucleus_link ─────────────────────────────────────────────────────────────


def test_nucleus_link_happy_path():
    """Happy path: anchor + adjacent + instructions returned to LLM."""
    client = MagicMock()
    client.link.return_value = {
        "anchor_engram": {"id": 5, "surface": "claude_code", "ts": 1000, "payload": "anchor", "meta": ""},
        "adjacent_engrams": [
            {"id": 6, "surface": "cursor", "ts": 1100, "payload": "after", "meta": ""},
        ],
        "window_minutes": 30,
        "instructions": "These engrams happened around the time of the anchor.",
    }
    text = _call_tool(
        client,
        "nucleus_link",
        {"engram_id": 5, "window_minutes": 30, "limit": 10},
    )
    body = json.loads(text)
    assert body["anchor_engram"]["id"] == 5
    assert len(body["adjacent_engrams"]) == 1
    assert body["adjacent_engrams"][0]["id"] == 6
    assert "instructions" in body
    client.link.assert_called_once_with(engram_id=5, window_minutes=30, limit=10)


def test_nucleus_link_default_window_and_limit():
    """No window_minutes/limit args → daemon called with documented defaults."""
    client = MagicMock()
    client.link.return_value = {
        "anchor_engram": {"id": 1, "surface": "x", "ts": 1, "payload": "p", "meta": ""},
        "adjacent_engrams": [],
        "window_minutes": 30,
        "instructions": "...",
    }
    _call_tool(client, "nucleus_link", {"engram_id": 1})
    client.link.assert_called_once_with(engram_id=1, window_minutes=30, limit=20)


def test_nucleus_link_non_integer_engram_id_rejected():
    """Non-int engram_id → dispatcher coerce error OR schema rejection.
    Schema declares type=integer minimum=1; either layer is acceptable."""
    client = MagicMock()
    text = _call_tool(client, "nucleus_link", {"engram_id": "abc"})
    assert text.startswith("error:") or "validation" in text.lower()
    client.link.assert_not_called()


def test_nucleus_link_anchor_404_surfaces():
    """Anchor engram doesn't exist → DaemonError 404 → 'error: ...' text."""
    client = MagicMock()
    client.link.side_effect = DaemonError("daemon returned 404: not found")
    text = _call_tool(client, "nucleus_link", {"engram_id": 99999})
    assert text.startswith("error:")
    assert "404" in text


def test_nucleus_link_value_error_surfaces():
    """Out-of-range window_minutes → ValueError → 'error: ...' text."""
    client = MagicMock()
    client.link.side_effect = ValueError("window_minutes must be in 1..1440, got 5000")
    # Note: schema declares max=1440; some args may be schema-rejected first.
    # We use mock side_effect to force the value-error path through the dispatcher.
    text = _call_tool(client, "nucleus_link", {"engram_id": 5, "window_minutes": 30})
    assert text.startswith("error:")
    assert "1440" in text or "window_minutes" in text


# ── nucleus_curate ───────────────────────────────────────────────────────────


def test_nucleus_curate_happy_path_canonical():
    """Happy path action='canonical' → daemon called → response returned."""
    client = MagicMock()
    client.curate.return_value = {
        "ok": True,
        "curation_engram_id": 123,
        "target_engram_id": 42,
        "action": "canonical",
        "surface": "curation",
    }
    text = _call_tool(
        client,
        "nucleus_curate",
        {"engram_id": 42, "action": "canonical", "note": "this is the right one"},
    )
    body = json.loads(text)
    assert body["ok"] is True
    assert body["curation_engram_id"] == 123
    assert body["target_engram_id"] == 42
    assert body["action"] == "canonical"
    client.curate.assert_called_once_with(
        engram_id=42, action="canonical", note="this is the right one"
    )


def test_nucleus_curate_happy_path_demote_no_note():
    """action='demote' with no note → daemon called with empty note."""
    client = MagicMock()
    client.curate.return_value = {
        "ok": True,
        "curation_engram_id": 456,
        "target_engram_id": 42,
        "action": "demote",
        "surface": "curation",
    }
    _call_tool(client, "nucleus_curate", {"engram_id": 42, "action": "demote"})
    client.curate.assert_called_once_with(engram_id=42, action="demote", note="")


def test_nucleus_curate_archive_action_passes_through():
    """action='archive' is one of the 3 allowed enums."""
    client = MagicMock()
    client.curate.return_value = {
        "ok": True,
        "curation_engram_id": 789,
        "target_engram_id": 99,
        "action": "archive",
        "surface": "curation",
    }
    _call_tool(client, "nucleus_curate", {"engram_id": 99, "action": "archive"})
    _, kwargs = client.curate.call_args
    assert kwargs["action"] == "archive"


def test_nucleus_curate_non_integer_engram_id_rejected():
    """Non-int engram_id → dispatcher coerce error OR schema rejection.
    Daemon MUST NOT be called."""
    client = MagicMock()
    text = _call_tool(
        client, "nucleus_curate", {"engram_id": "abc", "action": "canonical"}
    )
    assert text.startswith("error:") or "validation" in text.lower()
    client.curate.assert_not_called()


def test_nucleus_curate_invalid_action_surfaces_error():
    """action outside the enum → ValueError from client OR schema rejection.
    Schema declares enum=['canonical','demote','archive']."""
    client = MagicMock()
    client.curate.side_effect = ValueError(
        "action must be one of 'canonical' | 'demote' | 'archive', got 'destroy'"
    )
    text = _call_tool(
        client,
        "nucleus_curate",
        {"engram_id": 42, "action": "destroy"},
    )
    # Either schema rejection or dispatcher error path is acceptable.
    assert text.startswith("error:") or "validation" in text.lower()


def test_nucleus_curate_daemon_error_surfaces():
    """Target engram missing → DaemonError 404 → 'error: ...' text."""
    client = MagicMock()
    client.curate.side_effect = DaemonError("daemon returned 404: not found")
    text = _call_tool(
        client, "nucleus_curate", {"engram_id": 99999, "action": "canonical"}
    )
    assert text.startswith("error:")
    assert "404" in text
