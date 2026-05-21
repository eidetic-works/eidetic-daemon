"""Dispatcher-layer tests for the no-arg health/metrics tools.

Covers two tools the host LLM uses for diagnostics:
    - daemon_status   (cheap healthcheck, swallows DaemonError internally → bool)
    - daemon_metrics  (full /metrics observability JSON, surfaces DaemonError)

These exercise the MCP `_call_tool` dispatcher — no-arg handling, JSON
encoding of the daemon's reply, and error formatting (daemon_metrics only —
daemon_status never raises because client.healthy() catches exceptions and
returns False).
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


# ── daemon_status ────────────────────────────────────────────────────────────


def test_daemon_status_healthy_true():
    """Healthy daemon → {'healthy': true}."""
    client = MagicMock()
    client.healthy.return_value = True
    text = _call_tool(client, "daemon_status", {})
    assert json.loads(text) == {"healthy": True}
    client.healthy.assert_called_once_with()


def test_daemon_status_healthy_false_on_unreachable():
    """Daemon down → {'healthy': false}. client.healthy() swallows DaemonError
    and returns False; the dispatcher does NOT add a 'try' wrapper because the
    contract is that this tool never raises.
    """
    client = MagicMock()
    client.healthy.return_value = False
    text = _call_tool(client, "daemon_status", {})
    assert json.loads(text) == {"healthy": False}


def test_daemon_status_ignores_extra_args():
    """Tool schema has no properties; any extra args are silently ignored at
    the dispatcher (no schema-level rejection because no required fields)."""
    client = MagicMock()
    client.healthy.return_value = True
    text = _call_tool(client, "daemon_status", {"unexpected": "value"})
    assert json.loads(text) == {"healthy": True}


# ── daemon_metrics ───────────────────────────────────────────────────────────


def test_daemon_metrics_happy_path():
    """Happy path: daemon's /metrics JSON returned verbatim."""
    client = MagicMock()
    client.metrics.return_value = {
        "version": "0.0.10",
        "uptime_seconds": 3600,
        "engram_total": 1234,
        "engram_by_surface": {"claude_code": 1000, "cursor": 234},
        "capture_skipped": 0,
        "db_path": "/Users/test/.eidetic/engrams.db",
        "db_size_bytes": 5242880,
    }
    text = _call_tool(client, "daemon_metrics", {})
    body = json.loads(text)
    assert body["version"] == "0.0.10"
    assert body["uptime_seconds"] == 3600
    assert body["engram_total"] == 1234
    assert body["engram_by_surface"]["claude_code"] == 1000
    client.metrics.assert_called_once_with()


def test_daemon_metrics_predaemon_v007_surfaces_as_error():
    """Daemon < v0.0.7 → 'metrics not configured' DaemonError → 'error: ...'."""
    client = MagicMock()
    client.metrics.side_effect = DaemonError(
        "daemon returned 404: metrics not configured"
    )
    text = _call_tool(client, "daemon_metrics", {})
    assert text.startswith("error:")
    assert "metrics not configured" in text


def test_daemon_metrics_transport_error_surfaces():
    """Daemon unreachable mid-flight → DaemonError → 'error: ...'."""
    client = MagicMock()
    client.metrics.side_effect = DaemonError(
        "daemon transport / parse error: [Errno 2] No such file or directory"
    )
    text = _call_tool(client, "daemon_metrics", {})
    assert text.startswith("error:")
    assert "transport" in text


def test_daemon_metrics_empty_dict_ok():
    """Daemon returning an empty dict (cold start, no metrics yet) is valid."""
    client = MagicMock()
    client.metrics.return_value = {}
    text = _call_tool(client, "daemon_metrics", {})
    assert json.loads(text) == {}
