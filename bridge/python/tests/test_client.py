"""Tests for eidetic_mcp.client (the UDS HTTP client only).

End-to-end tests against a real daemon are in scripts/demo-smoke.sh
(daemon-side, exercises the wire format). Here we test parsing, error
shapes, and env-var defaults using a fake server."""

from __future__ import annotations

import http.server
import json
import os
import socket
import socketserver
import threading
import time
from pathlib import Path

import pytest

from eidetic_mcp.client import (
    DaemonClient,
    DaemonError,
    Engram,
    _parse_engram,
)


# ── Unit tests on the parser (pure-fn, no network) ──────────────────────────

def test_parse_engram_full_row():
    row = {"id": 7, "surface": "claude_code", "ts": 12345, "payload": "hi", "meta": '{"k":"v"}'}
    e = _parse_engram(row)
    assert e == Engram(id=7, surface="claude_code", ts=12345, payload="hi", meta='{"k":"v"}')


def test_parse_engram_missing_meta_defaults_empty():
    row = {"id": 7, "surface": "x", "ts": 1, "payload": "p"}
    e = _parse_engram(row)
    assert e.meta == ""


def test_parse_engram_non_dict_raises():
    with pytest.raises(DaemonError):
        _parse_engram("not a dict")


def test_parse_engram_missing_required_field_raises():
    with pytest.raises(DaemonError):
        _parse_engram({"id": 1, "surface": "x"})  # no ts/payload


def test_parse_engram_bad_id_type_raises():
    with pytest.raises(DaemonError):
        _parse_engram({"id": "not int", "surface": "x", "ts": 1, "payload": "p"})


# ── Integration: spin up a UDS HTTP server, point client at it ──────────────

class _UDSHandler(http.server.BaseHTTPRequestHandler):
    """Mimics the daemon's response shape on /healthz + /engrams."""

    def log_message(self, *_args, **_kw):  # silence test output
        pass

    def do_GET(self):  # noqa: N802
        if self.path == "/healthz":
            body = json.dumps({"status": "ok"}).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        if self.path.startswith("/engrams"):
            # 1 row, fake but parseable
            body = json.dumps([
                {"id": 1, "surface": "claude_code", "ts": 100, "payload": "hi", "meta": ""},
            ]).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        self.send_response(404)
        self.end_headers()


class _UDSServer(socketserver.UnixStreamServer):
    allow_reuse_address = True


@pytest.fixture()
def uds_socket_path(tmp_path: Path):
    """Spin up a real UDS HTTP server backed by a tmp socket; yield path.

    macOS sun_path limit is 104 bytes — pytest tmp_path can blow past that
    (~120 chars on macOS-Homebrew-pytest). Mirror Go server_test.go's
    shortUDSPath helper: prefer /tmp directly, fall back to tmp_path only
    if /tmp would collide.
    """
    short = Path(f"/tmp/eidetic-bridge-test-{os.getpid()}-{int(time.time()*1000)}.sock")
    if len(str(short)) >= 104:  # extremely unlikely but keep the gate honest
        short = Path(f"/tmp/eb-{int(time.time()*1000) % 1_000_000}.sock")
    if short.exists():
        short.unlink()
    server = _UDSServer(str(short), _UDSHandler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    deadline = time.time() + 1.0
    while time.time() < deadline:
        if short.exists():
            break
        time.sleep(0.01)
    try:
        yield str(short)
    finally:
        server.shutdown()
        server.server_close()
        if short.exists():
            short.unlink()


def test_client_healthy_against_fake_server(uds_socket_path: str):
    client = DaemonClient(uds_path=uds_socket_path)
    assert client.healthy() is True


def test_client_healthy_returns_false_on_unreachable(tmp_path: Path):
    client = DaemonClient(uds_path=str(tmp_path / "nope.sock"), timeout=0.5)
    assert client.healthy() is False


def test_client_query_engrams_against_fake_server(uds_socket_path: str):
    client = DaemonClient(uds_path=uds_socket_path)
    rows = client.query_engrams(surface="claude_code")
    assert len(rows) == 1
    assert rows[0].surface == "claude_code"
    assert rows[0].payload == "hi"


def test_client_query_engrams_requires_surface(uds_socket_path: str):
    client = DaemonClient(uds_path=uds_socket_path)
    with pytest.raises(ValueError):
        client.query_engrams(surface="")


def test_client_uses_env_uds_path(monkeypatch, uds_socket_path: str):
    monkeypatch.delenv("EIDETIC_TCP", raising=False)
    monkeypatch.setenv("EIDETIC_UDS_PATH", uds_socket_path)
    client = DaemonClient()
    assert client.healthy() is True


def test_client_tcp_mode_picked_when_env_set(monkeypatch):
    monkeypatch.setenv("EIDETIC_TCP", "1")
    client = DaemonClient()
    # Just verify it picks TCP mode + no transport call yet.
    assert client._mode == "tcp"  # noqa: SLF001 — internal state assertion is intentional
