"""Tests for _nucleus_emit_call call-count instrumentation (Server C, eidetic bridge).

Treatment sweep #002 carry — mirrors the instrumentation test suite in
mcp-server-nucleus/tests/runtime/test_tool_instrumentation.py for Server C.
"""

from __future__ import annotations

import asyncio
import json
from pathlib import Path
from unittest.mock import MagicMock

import mcp.types as types

from eidetic_mcp.server import _nucleus_emit_call, build_server


def test_emit_writes_jsonl(tmp_path, monkeypatch):
    """_nucleus_emit_call appends a valid JSONL record when not disabled."""
    monkeypatch.setenv("NUCLEUS_INSTRUMENT_PATH", str(tmp_path))
    monkeypatch.delenv("NUCLEUS_INSTRUMENT_DISABLED", raising=False)

    _nucleus_emit_call("daemon_status")

    files = list(tmp_path.glob("*.jsonl"))
    assert len(files) == 1, "Expected exactly one daily JSONL file"
    record = json.loads(files[0].read_text().strip())
    assert record["tool"] == "daemon_status"
    assert record["server"] == "eidetic"
    assert "ts" in record


def test_emit_disabled_is_noop(tmp_path, monkeypatch):
    """NUCLEUS_INSTRUMENT_DISABLED=1 means nothing is written."""
    monkeypatch.setenv("NUCLEUS_INSTRUMENT_PATH", str(tmp_path))
    monkeypatch.setenv("NUCLEUS_INSTRUMENT_DISABLED", "1")

    _nucleus_emit_call("search_engrams")

    files = list(tmp_path.glob("*.jsonl"))
    assert len(files) == 0, "No file should be created when instrumentation is disabled"


def test_emit_session_tag(tmp_path, monkeypatch):
    """CC_SESSION_ROLE is recorded as session field when set."""
    monkeypatch.setenv("NUCLEUS_INSTRUMENT_PATH", str(tmp_path))
    monkeypatch.setenv("CC_SESSION_ROLE", "peer")
    monkeypatch.delenv("NUCLEUS_INSTRUMENT_DISABLED", raising=False)

    _nucleus_emit_call("query_engrams")

    files = list(tmp_path.glob("*.jsonl"))
    assert len(files) == 1
    record = json.loads(files[0].read_text().strip())
    assert record["session"] == "peer"


def test_emit_no_session_tag_when_unset(tmp_path, monkeypatch):
    """session field is absent when CC_SESSION_ROLE is not set."""
    monkeypatch.setenv("NUCLEUS_INSTRUMENT_PATH", str(tmp_path))
    monkeypatch.delenv("CC_SESSION_ROLE", raising=False)
    monkeypatch.delenv("NUCLEUS_INSTRUMENT_DISABLED", raising=False)

    _nucleus_emit_call("daemon_metrics")

    files = list(tmp_path.glob("*.jsonl"))
    record = json.loads(files[0].read_text().strip())
    assert "session" not in record


def test_emit_multiple_calls_in_same_file(tmp_path, monkeypatch):
    """Multiple calls append to the same daily JSONL file."""
    monkeypatch.setenv("NUCLEUS_INSTRUMENT_PATH", str(tmp_path))
    monkeypatch.delenv("NUCLEUS_INSTRUMENT_DISABLED", raising=False)

    _nucleus_emit_call("daemon_status")
    _nucleus_emit_call("query_engrams")
    _nucleus_emit_call("search_engrams")

    files = list(tmp_path.glob("*.jsonl"))
    assert len(files) == 1
    lines = [l for l in files[0].read_text().splitlines() if l.strip()]
    assert len(lines) == 3
    tools = [json.loads(l)["tool"] for l in lines]
    assert tools == ["daemon_status", "query_engrams", "search_engrams"]


def test_call_tool_fires_instrumentation(tmp_path, monkeypatch):
    """_call_tool dispatches to _nucleus_emit_call on every MCP tool invocation."""
    monkeypatch.setenv("NUCLEUS_INSTRUMENT_PATH", str(tmp_path))
    monkeypatch.delenv("NUCLEUS_INSTRUMENT_DISABLED", raising=False)

    client = MagicMock()
    client.healthy.return_value = True
    server = build_server(client=client)
    handler = server.request_handlers[types.CallToolRequest]
    req = types.CallToolRequest(
        method="tools/call",
        params=types.CallToolRequestParams(name="daemon_status", arguments={}),
    )
    asyncio.run(handler(req))

    files = list(tmp_path.glob("*.jsonl"))
    assert len(files) == 1
    record = json.loads(files[0].read_text().strip())
    assert record["tool"] == "daemon_status"
    assert record["server"] == "eidetic"
