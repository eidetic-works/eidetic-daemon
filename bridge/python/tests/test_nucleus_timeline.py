"""Tests for the DaemonClient.timeline() method (v0.0.7+ bridge / v0.0.47+ daemon).

End-to-end MCP test (Tool dispatched, daemon returns timeline) lives in
scripts/demo-smoke.sh — requires a live daemon and mcp SDK installed.

The unit tests below spin up a mock UDS HTTP server (mirroring
test_nucleus_digest.py's idiom) and exercise:
  - happy path (200 + valid JSON dict body returned verbatim)
  - surface filter pass-through (comma-joined query string)
  - bad limit (0, negative, >1000) → ValueError client-side
  - DaemonError on 500
"""

from __future__ import annotations

import http.server
import json
import os
import socketserver
import threading
import time
from pathlib import Path

import pytest

from eidetic_mcp.client import DaemonClient, DaemonError


# Captured request bookkeeping — verifies the URL the client builds for /timeline.
class _Captured:
    def __init__(self) -> None:
        self.paths: list[str] = []
        self.status_to_send: int = 200
        self.body_to_send: dict | str = {
            "engrams": [
                {
                    "id": 1,
                    "surface": "claude_code",
                    "ts": 1000,
                    "payload": "alpha",
                    "meta": "",
                },
                {
                    "id": 2,
                    "surface": "cursor",
                    "ts": 2000,
                    "payload": "beta",
                    "meta": "",
                },
            ],
            "count": 2,
            "surfaces": [],
        }


def _build_handler(captured: _Captured):
    class _Handler(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_args, **_kw):  # silence test output
            pass

        def do_GET(self):  # noqa: N802
            captured.paths.append(self.path)
            if not self.path.startswith("/timeline"):
                self.send_response(404)
                self.end_headers()
                return

            status = captured.status_to_send
            if status == 200:
                payload = captured.body_to_send
                if isinstance(payload, str):
                    body = payload.encode()
                    ctype = "text/plain"
                else:
                    body = json.dumps(payload).encode()
                    ctype = "application/json"
                self.send_response(200)
                self.send_header("Content-Type", ctype)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
                return

            # Non-200 path — mimic daemon error body for DaemonError formatting.
            body = b"internal error\n"
            self.send_response(status)
            self.send_header("Content-Type", "text/plain")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

    return _Handler


class _UDSServer(socketserver.UnixStreamServer):
    allow_reuse_address = True


@pytest.fixture()
def timeline_server(tmp_path: Path):
    """Spin up a UDS HTTP server with mutable response state.

    Mirrors test_nucleus_digest.py's short-path discipline so we stay under
    macOS's 104-byte sun_path limit. Yields (captured, socket_path) so each
    test can flip status_to_send / body_to_send before calling the client.
    """
    short = Path(f"/tmp/eidetic-tl-test-{os.getpid()}-{int(time.time()*1000)}.sock")
    if len(str(short)) >= 104:
        short = Path(f"/tmp/etl-{int(time.time()*1000) % 1_000_000}.sock")
    if short.exists():
        short.unlink()

    captured = _Captured()
    server = _UDSServer(str(short), _build_handler(captured))
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    deadline = time.time() + 1.0
    while time.time() < deadline:
        if short.exists():
            break
        time.sleep(0.01)
    try:
        yield captured, str(short)
    finally:
        server.shutdown()
        server.server_close()
        if short.exists():
            short.unlink()


# --- happy path -----------------------------------------------------------


def test_timeline_happy_path_defaults(timeline_server):
    """timeline() with no args sends /timeline?limit=200 and returns body verbatim."""
    captured, sock = timeline_server
    client = DaemonClient(uds_path=sock)
    body = client.timeline()
    assert isinstance(body, dict)
    assert body["count"] == 2
    assert len(body["engrams"]) == 2
    assert body["engrams"][0]["surface"] == "claude_code"
    assert body["engrams"][1]["surface"] == "cursor"
    # No since/before/surfaces in defaults; only limit goes on the wire.
    assert captured.paths == ["/timeline?limit=200"]


def test_timeline_returns_json_verbatim(timeline_server):
    """The full daemon payload — engrams + count + surfaces — is returned untouched."""
    captured, sock = timeline_server
    client = DaemonClient(uds_path=sock)
    body = client.timeline()
    for key in ("engrams", "count", "surfaces"):
        assert key in body, f"missing key {key!r} in timeline response"
    assert body["engrams"][0]["payload"] == "alpha"
    assert body["engrams"][1]["payload"] == "beta"


def test_timeline_since_before_passed_through(timeline_server):
    """since/before are forwarded as integer query-string values."""
    captured, sock = timeline_server
    client = DaemonClient(uds_path=sock)
    client.timeline(since=1000, before=9000, limit=50)
    assert captured.paths == ["/timeline?since=1000&before=9000&limit=50"]


# --- surface filter -------------------------------------------------------


def test_timeline_surface_filter_passed_through(timeline_server):
    """surfaces=[a,b,c] is comma-joined into one query parameter."""
    captured, sock = timeline_server
    captured.body_to_send = {
        **captured.body_to_send,  # type: ignore[arg-type]
        "surfaces": ["claude_code", "cursor"],
    }
    client = DaemonClient(uds_path=sock)
    body = client.timeline(surfaces=["claude_code", "cursor"])
    assert body["surfaces"] == ["claude_code", "cursor"]
    assert captured.paths == ["/timeline?surfaces=claude_code%2Ccursor&limit=200"]


def test_timeline_empty_surfaces_omits_param(timeline_server):
    """surfaces=[] sends no `surfaces` query param (means: all surfaces)."""
    captured, sock = timeline_server
    client = DaemonClient(uds_path=sock)
    client.timeline(surfaces=[])
    assert captured.paths == ["/timeline?limit=200"]


def test_timeline_blank_strings_in_surfaces_are_dropped(timeline_server):
    """Empty / whitespace-only entries in surfaces are stripped before joining."""
    captured, sock = timeline_server
    client = DaemonClient(uds_path=sock)
    client.timeline(surfaces=["claude_code", "", "cursor"])
    assert captured.paths == ["/timeline?surfaces=claude_code%2Ccursor&limit=200"]


# --- client-side validation -----------------------------------------------


def test_timeline_bad_limit_zero_raises_no_http(timeline_server):
    """limit=0 fails client-side; no HTTP request is dispatched."""
    captured, sock = timeline_server
    client = DaemonClient(uds_path=sock)
    with pytest.raises(ValueError):
        client.timeline(limit=0)
    assert captured.paths == []


def test_timeline_bad_limit_negative_raises_no_http(timeline_server):
    captured, sock = timeline_server
    client = DaemonClient(uds_path=sock)
    with pytest.raises(ValueError):
        client.timeline(limit=-1)
    assert captured.paths == []


def test_timeline_bad_limit_too_large_raises_no_http(timeline_server):
    """limit > 1000 fails client-side; no HTTP request is dispatched."""
    captured, sock = timeline_server
    client = DaemonClient(uds_path=sock)
    with pytest.raises(ValueError):
        client.timeline(limit=1001)
    assert captured.paths == []


def test_timeline_limit_1_and_1000_accepted(timeline_server):
    """Boundary values 1 and 1000 are accepted."""
    captured, sock = timeline_server
    client = DaemonClient(uds_path=sock)
    client.timeline(limit=1)
    client.timeline(limit=1000)
    assert captured.paths == ["/timeline?limit=1", "/timeline?limit=1000"]


# --- error path -----------------------------------------------------------


def test_timeline_500_raises_daemon_error(timeline_server):
    """5xx from the daemon surfaces as DaemonError carrying the status + body."""
    captured, sock = timeline_server
    captured.status_to_send = 500
    client = DaemonClient(uds_path=sock)
    with pytest.raises(DaemonError) as exc_info:
        client.timeline()
    assert "500" in str(exc_info.value)


def test_timeline_non_object_body_raises(timeline_server):
    """Daemon returning a non-dict (e.g. array, string) should fail with
    DaemonError rather than silently returning the wrong shape."""
    captured, sock = timeline_server
    captured.body_to_send = "not an object"
    client = DaemonClient(uds_path=sock)
    with pytest.raises(DaemonError):
        client.timeline()


def test_timeline_unreachable_raises_daemon_error(tmp_path: Path):
    """No daemon at socket path → DaemonError (transport error)."""
    nowhere = tmp_path / "no-daemon.sock"
    client = DaemonClient(uds_path=str(nowhere), timeout=0.5)
    with pytest.raises(DaemonError):
        client.timeline()
