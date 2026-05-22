"""Dispatcher-layer tests for nucleus_synth_over_engrams (W4 deliverable).

Mirrors PR #67's pattern (see test_nucleus_dispatcher.py): drive the MCP
server's CallToolRequest handler through `build_server(client=MagicMock())`
and assert on the TextContent payload. Underlying daemon-client method
(`search_engrams`) is mocked; the TB endpoint POST is mocked via
`urllib.request.urlopen` patch since it's a stdlib call inside the
dispatcher, not a daemon-client method.

Coverage:
    - happy path: FTS retrieval + TB synthesis success → full payload
    - graceful degradation: TB endpoint failure → FTS IDs returned with error
    - demo=True default per W4 demo-only contract
    - demo=False explicit suppression for internal-eval callers
    - empty query → 'error: query required'
    - k clamp to 30 (cap) and 1 (floor)
    - sovereignty=sovereign hard-forced regardless of caller args
    - daemon.search_engrams error path → 'error: ...' text
"""

from __future__ import annotations

import asyncio
import json
from unittest.mock import MagicMock, patch

import mcp.types as types

from eidetic_mcp.client import DaemonError, Engram
from eidetic_mcp.server import build_server


def _call_tool(client: MagicMock, name: str, arguments: dict) -> str:
    """Drive the MCP server's CallToolRequest handler and return text."""
    server = build_server(client=client)
    handler = server.request_handlers[types.CallToolRequest]
    req = types.CallToolRequest(
        method="tools/call",
        params=types.CallToolRequestParams(name=name, arguments=arguments),
    )
    result = asyncio.run(handler(req))
    return result.root.content[0].text


def _make_engram(eid: int, surface: str = "claude_code", payload: str = "hit") -> Engram:
    return Engram(id=eid, surface=surface, ts=1, payload=payload, meta="", snippet="")


def _mock_tb_response(output: str = "synthesized text", ok: bool = True) -> MagicMock:
    """Build a urlopen context-manager mock that returns a TB-shaped JSON body."""
    body = json.dumps({"ok": ok, "output": output}).encode("utf-8")
    resp = MagicMock()
    resp.read.return_value = body
    cm = MagicMock()
    cm.__enter__.return_value = resp
    cm.__exit__.return_value = False
    return cm


# ── happy path ──────────────────────────────────────────────────────────────


def test_synth_happy_path_returns_full_payload():
    """FTS retrieve 2 engrams + TB POST ok → synthesis + IDs returned."""
    client = MagicMock()
    client.search_engrams.return_value = [
        _make_engram(11, payload="first hit"),
        _make_engram(22, payload="second hit"),
    ]
    with patch("eidetic_mcp.server.urllib.request.urlopen", return_value=_mock_tb_response("answer")):
        text = _call_tool(client, "nucleus_synth_over_engrams", {"query": "test q"})
    body = json.loads(text)
    assert body["query"] == "test q"
    assert body["retrieved_engram_ids"] == [11, 22]
    assert body["n_retrieved"] == 2
    assert body["synthesis"] == "answer"
    assert "error" not in body
    # demo=True by default → disclaimer present
    assert "demo_disclaimer" in body
    assert "concept proof" in body["demo_disclaimer"].lower()


def test_synth_calls_daemon_search_with_query_and_k():
    """k arg reaches daemon.search_engrams as limit; surface='' (cross-surface)."""
    client = MagicMock()
    client.search_engrams.return_value = []
    with patch("eidetic_mcp.server.urllib.request.urlopen", return_value=_mock_tb_response()):
        _call_tool(client, "nucleus_synth_over_engrams", {"query": "q", "k": 5})
    _, kwargs = client.search_engrams.call_args
    assert kwargs["q"] == "q"
    assert kwargs["limit"] == 5
    assert kwargs["surface"] == ""  # cross-surface by default


# ── graceful degradation ────────────────────────────────────────────────────


def test_synth_tb_endpoint_failure_returns_fts_with_error():
    """TB POST raises → result still has FTS IDs + error='tb_endpoint_unresponsive'."""
    import urllib.error
    client = MagicMock()
    client.search_engrams.return_value = [_make_engram(7)]
    with patch(
        "eidetic_mcp.server.urllib.request.urlopen",
        side_effect=urllib.error.URLError("connection refused"),
    ):
        text = _call_tool(client, "nucleus_synth_over_engrams", {"query": "q"})
    body = json.loads(text)
    assert body["retrieved_engram_ids"] == [7]
    assert body["synthesis"] == ""
    assert body["error"] == "tb_endpoint_unresponsive"
    assert "error_detail" in body
    assert "connection refused" in body["error_detail"]


def test_synth_tb_timeout_returns_fts_with_error():
    """TB POST timeout → graceful degradation path."""
    client = MagicMock()
    client.search_engrams.return_value = [_make_engram(9)]
    with patch(
        "eidetic_mcp.server.urllib.request.urlopen",
        side_effect=TimeoutError("read timeout"),
    ):
        text = _call_tool(client, "nucleus_synth_over_engrams", {"query": "q"})
    body = json.loads(text)
    assert body["retrieved_engram_ids"] == [9]
    assert body["error"] == "tb_endpoint_unresponsive"


# ── demo contract ───────────────────────────────────────────────────────────


def test_synth_demo_default_true_stamps_disclaimer():
    """No demo arg → demo=True default → disclaimer stamped."""
    client = MagicMock()
    client.search_engrams.return_value = []
    with patch("eidetic_mcp.server.urllib.request.urlopen", return_value=_mock_tb_response()):
        text = _call_tool(client, "nucleus_synth_over_engrams", {"query": "q"})
    body = json.loads(text)
    assert "demo_disclaimer" in body


def test_synth_demo_false_suppresses_disclaimer():
    """Internal-eval caller passes demo=False → no disclaimer."""
    client = MagicMock()
    client.search_engrams.return_value = []
    with patch("eidetic_mcp.server.urllib.request.urlopen", return_value=_mock_tb_response()):
        text = _call_tool(client, "nucleus_synth_over_engrams", {"query": "q", "demo": False})
    body = json.loads(text)
    assert "demo_disclaimer" not in body


# ── input validation ────────────────────────────────────────────────────────


def test_synth_empty_query_rejected():
    """No query → 'error: query required' before daemon hit."""
    client = MagicMock()
    text = _call_tool(client, "nucleus_synth_over_engrams", {"query": ""})
    assert text.startswith("error:")
    assert "query" in text
    client.search_engrams.assert_not_called()


def test_synth_k_clamped_to_cap_or_schema_rejected():
    """k > 30 (max in schema) either clamped daemon-side OR rejected by SDK schema.

    Mirrors the audit-acceptable pattern from test_nucleus_link.py: schema
    type=integer minimum=1 maximum=30 may reject before the dispatcher runs.
    Either rejection layer is acceptable.
    """
    client = MagicMock()
    client.search_engrams.return_value = []
    with patch("eidetic_mcp.server.urllib.request.urlopen", return_value=_mock_tb_response()):
        text = _call_tool(client, "nucleus_synth_over_engrams", {"query": "q", "k": 999})
    if client.search_engrams.call_args is not None:
        # Dispatcher reached → it clamped to 30
        _, kwargs = client.search_engrams.call_args
        assert kwargs["limit"] == 30
    else:
        # Schema rejected → error surfaced before daemon hit
        assert text.startswith("error:") or "validation" in text.lower()


def test_synth_k_floor_one_or_schema_rejected():
    """k=0 either clamped to 1 daemon-side OR rejected by SDK schema (minimum=1)."""
    client = MagicMock()
    client.search_engrams.return_value = []
    with patch("eidetic_mcp.server.urllib.request.urlopen", return_value=_mock_tb_response()):
        text = _call_tool(client, "nucleus_synth_over_engrams", {"query": "q", "k": 0})
    if client.search_engrams.call_args is not None:
        _, kwargs = client.search_engrams.call_args
        assert kwargs["limit"] == 1
    else:
        assert text.startswith("error:") or "validation" in text.lower()


# ── sovereignty contract ────────────────────────────────────────────────────


def test_synth_sovereignty_hard_forced_in_tb_body():
    """Every /tb/turn POST body MUST include sovereignty='sovereign' regardless of args."""
    client = MagicMock()
    client.search_engrams.return_value = []
    captured = {}

    def _capture_request(req, timeout=None):
        captured["body"] = req.data
        return _mock_tb_response()

    with patch("eidetic_mcp.server.urllib.request.urlopen", side_effect=_capture_request):
        _call_tool(client, "nucleus_synth_over_engrams", {"query": "q"})
    body = json.loads(captured["body"].decode("utf-8"))
    assert body["sovereignty"] == "sovereign"
    assert body["input"] == "q"
    assert body["mode"] == "code"  # default mode


# ── error pass-through ──────────────────────────────────────────────────────


def test_synth_daemon_search_error_surfaces():
    """daemon.search_engrams raises DaemonError → 'error: ...' text."""
    client = MagicMock()
    client.search_engrams.side_effect = DaemonError("daemon returned 500")
    text = _call_tool(client, "nucleus_synth_over_engrams", {"query": "q"})
    assert text.startswith("error:")
    assert "500" in text
