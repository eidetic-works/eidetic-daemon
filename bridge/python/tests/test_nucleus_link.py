"""Tests for the DaemonClient.link() method (v0.0.8+ bridge).

End-to-end MCP test (Tool dispatched, daemon returns linked engrams) lives in
scripts/demo-smoke.sh — requires a live daemon and mcp SDK installed.

The unit tests below spin up a mock UDS HTTP server (mirroring
test_nucleus_timeline.py's idiom) and exercise:
  - happy path (anchor + 5 adjacent engrams across surfaces)
  - bad engram_id (0, negative) → ValueError
  - bad window_minutes (0, negative, >1440) → ValueError
  - anchor 404 → DaemonError
  - 500 mid-flight on /timeline → DaemonError

link() composes TWO daemon calls (GET /engrams/{id} then GET /timeline?...),
so the mock dispatches on path prefix.
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


# Captured request bookkeeping — verifies BOTH paths the client builds
# (one for anchor fetch, one for timeline). Mock state is mutable per test.
class _Captured:
    def __init__(self) -> None:
        self.paths: list[str] = []
        # Per-route status overrides. Default = 200 on both routes.
        self.anchor_status: int = 200
        self.timeline_status: int = 200
        # Anchor body — daemon's /engrams/{id} shape (single engram dict).
        # Chosen ts = 1_000_000_000_000_000_000 (an arbitrary "round" ns
        # value so window math is easy to eyeball in tests).
        self.anchor_body: dict | str = {
            "id": 100,
            "surface": "claude_code",
            "ts": 1_000_000_000_000_000_000,
            "payload": "anchor payload",
            "meta": "",
        }
        # Timeline body — daemon's /timeline shape. Includes the anchor +
        # 5 adjacent across surfaces. The bridge's link() strips the anchor
        # from adjacent_engrams server-side-of-the-bridge before returning.
        self.timeline_body: dict | str = {
            "engrams": [
                {
                    "id": 98,
                    "surface": "cursor",
                    "ts": 1_000_000_000_000_000_000 - 600_000_000_000,  # -10 min
                    "payload": "before-1",
                    "meta": "",
                },
                {
                    "id": 99,
                    "surface": "cowork",
                    "ts": 1_000_000_000_000_000_000 - 300_000_000_000,  # -5 min
                    "payload": "before-2",
                    "meta": "",
                },
                {  # anchor itself — should be filtered out of adjacent
                    "id": 100,
                    "surface": "claude_code",
                    "ts": 1_000_000_000_000_000_000,
                    "payload": "anchor payload",
                    "meta": "",
                },
                {
                    "id": 101,
                    "surface": "cursor",
                    "ts": 1_000_000_000_000_000_000 + 60_000_000_000,  # +1 min
                    "payload": "after-1",
                    "meta": "",
                },
                {
                    "id": 102,
                    "surface": "claude_code",
                    "ts": 1_000_000_000_000_000_000 + 600_000_000_000,  # +10 min
                    "payload": "after-2",
                    "meta": "",
                },
                {
                    "id": 103,
                    "surface": "cowork",
                    "ts": 1_000_000_000_000_000_000 + 900_000_000_000,  # +15 min
                    "payload": "after-3",
                    "meta": "",
                },
            ],
            "count": 6,
            "surfaces": [],
        }


def _write_status(handler, status: int, body_obj):
    """Common JSON / text body writer used by both anchor + timeline routes."""
    if isinstance(body_obj, str):
        body = body_obj.encode()
        ctype = "text/plain"
    else:
        body = json.dumps(body_obj).encode()
        ctype = "application/json"
    handler.send_response(status)
    handler.send_header("Content-Type", ctype)
    handler.send_header("Content-Length", str(len(body)))
    handler.end_headers()
    handler.wfile.write(body)


def _build_handler(captured: _Captured):
    class _Handler(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_args, **_kw):  # silence test output
            pass

        def do_GET(self):  # noqa: N802
            captured.paths.append(self.path)

            # Route 1: /engrams/{id} — anchor fetch. Match by prefix +
            # excluding /engrams/count, /engrams/batch so we don't false-hit.
            if (
                self.path.startswith("/engrams/")
                and not self.path.startswith("/engrams/count")
                and not self.path.startswith("/engrams/batch")
            ):
                status = captured.anchor_status
                if status == 200:
                    _write_status(self, 200, captured.anchor_body)
                else:
                    _write_status(self, status, "anchor error\n")
                return

            # Route 2: /timeline?...
            if self.path.startswith("/timeline"):
                status = captured.timeline_status
                if status == 200:
                    _write_status(self, 200, captured.timeline_body)
                else:
                    _write_status(self, status, "timeline error\n")
                return

            self.send_response(404)
            self.end_headers()

    return _Handler


class _UDSServer(socketserver.UnixStreamServer):
    allow_reuse_address = True


@pytest.fixture()
def link_server(tmp_path: Path):
    """Spin up a UDS HTTP server with mutable response state.

    Mirrors test_nucleus_timeline.py's short-path discipline so we stay under
    macOS's 104-byte sun_path limit. Yields (captured, socket_path) so each
    test can flip status overrides and bodies before calling the client.
    """
    short = Path(f"/tmp/eidetic-link-test-{os.getpid()}-{int(time.time()*1000)}.sock")
    if len(str(short)) >= 104:
        short = Path(f"/tmp/el-{int(time.time()*1000) % 1_000_000}.sock")
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


def test_link_happy_path_returns_anchor_and_adjacent(link_server):
    """link() returns anchor + 5 adjacent (anchor stripped from list)."""
    captured, sock = link_server
    client = DaemonClient(uds_path=sock)
    body = client.link(engram_id=100)

    assert isinstance(body, dict)
    # Anchor is returned as a dict (asdict-ed from Engram), payload preserved.
    assert body["anchor_engram"]["id"] == 100
    assert body["anchor_engram"]["surface"] == "claude_code"
    assert body["anchor_engram"]["payload"] == "anchor payload"
    # Anchor must NOT appear in adjacent_engrams (mock returned 6 incl anchor;
    # bridge filters it to 5).
    assert len(body["adjacent_engrams"]) == 5
    assert all(row["id"] != 100 for row in body["adjacent_engrams"])
    # Window + instructions are surfaced for the host LLM.
    assert body["window_minutes"] == 30
    assert "adjacent" in body["instructions"].lower() or "around" in body["instructions"].lower()


def test_link_default_window_30_minutes(link_server):
    """Default window_minutes=30 → ±30 min around anchor ts on /timeline."""
    captured, sock = link_server
    client = DaemonClient(uds_path=sock)
    client.link(engram_id=100)

    # First call is anchor fetch; second is timeline.
    assert len(captured.paths) == 2
    assert captured.paths[0] == "/engrams/100"
    # 30 min in ns = 30 * 60 * 1e9 = 1.8e12. Bridge shifts by ±1 ns to make
    # daemon's exclusive bounds inclusive of the boundary.
    window_ns = 30 * 60 * 1_000_000_000
    anchor_ts = 1_000_000_000_000_000_000
    expected_since = anchor_ts - window_ns - 1
    expected_before = anchor_ts + window_ns + 1
    # /timeline always carries limit=...; default link limit is 20.
    assert captured.paths[1] == (
        f"/timeline?since={expected_since}&before={expected_before}&limit=20"
    )


def test_link_custom_window_and_limit(link_server):
    """window_minutes=60 + limit=50 are forwarded into the /timeline URL."""
    captured, sock = link_server
    client = DaemonClient(uds_path=sock)
    client.link(engram_id=100, window_minutes=60, limit=50)

    assert len(captured.paths) == 2
    window_ns = 60 * 60 * 1_000_000_000
    anchor_ts = 1_000_000_000_000_000_000
    expected_since = anchor_ts - window_ns - 1
    expected_before = anchor_ts + window_ns + 1
    assert captured.paths[1] == (
        f"/timeline?since={expected_since}&before={expected_before}&limit=50"
    )


def test_link_anchor_ts_near_zero_clamps_since(link_server):
    """Anchor ts < window → since clamps to 0 (no negative since param)."""
    captured, sock = link_server
    # Anchor at ts=10 ns with 30 min window would compute since = -1.8e12;
    # bridge clamps to 0.
    captured.anchor_body = {
        "id": 100,
        "surface": "claude_code",
        "ts": 10,
        "payload": "early",
        "meta": "",
    }
    client = DaemonClient(uds_path=sock)
    client.link(engram_id=100)

    # since=0 is omitted from /timeline URL per DaemonClient.timeline()'s
    # `if since > 0` gate; only before + limit go on the wire.
    window_ns = 30 * 60 * 1_000_000_000
    expected_before = 10 + window_ns + 1
    assert captured.paths[1] == f"/timeline?before={expected_before}&limit=20"


# --- client-side validation -----------------------------------------------


def test_link_engram_id_zero_raises_value_error(link_server):
    """engram_id=0 fails client-side; no HTTP is dispatched."""
    captured, sock = link_server
    client = DaemonClient(uds_path=sock)
    with pytest.raises(ValueError):
        client.link(engram_id=0)
    assert captured.paths == []


def test_link_engram_id_negative_raises_value_error(link_server):
    captured, sock = link_server
    client = DaemonClient(uds_path=sock)
    with pytest.raises(ValueError):
        client.link(engram_id=-5)
    assert captured.paths == []


def test_link_window_minutes_zero_raises_value_error(link_server):
    captured, sock = link_server
    client = DaemonClient(uds_path=sock)
    with pytest.raises(ValueError):
        client.link(engram_id=100, window_minutes=0)
    assert captured.paths == []


def test_link_window_minutes_negative_raises_value_error(link_server):
    captured, sock = link_server
    client = DaemonClient(uds_path=sock)
    with pytest.raises(ValueError):
        client.link(engram_id=100, window_minutes=-30)
    assert captured.paths == []


def test_link_window_minutes_too_large_raises_value_error(link_server):
    """window_minutes > 1440 (24h) fails client-side."""
    captured, sock = link_server
    client = DaemonClient(uds_path=sock)
    with pytest.raises(ValueError):
        client.link(engram_id=100, window_minutes=1441)
    assert captured.paths == []


def test_link_window_minutes_1_and_1440_accepted(link_server):
    """Boundary values 1 and 1440 are accepted."""
    captured, sock = link_server
    client = DaemonClient(uds_path=sock)
    # Two successful calls → 4 HTTP requests total (anchor + timeline × 2).
    client.link(engram_id=100, window_minutes=1)
    client.link(engram_id=100, window_minutes=1440)
    assert len(captured.paths) == 4


def test_link_limit_out_of_range_raises_value_error(link_server):
    """limit outside 1..1000 fails client-side (same gate as timeline())."""
    captured, sock = link_server
    client = DaemonClient(uds_path=sock)
    with pytest.raises(ValueError):
        client.link(engram_id=100, limit=0)
    with pytest.raises(ValueError):
        client.link(engram_id=100, limit=1001)
    assert captured.paths == []


# --- error path -----------------------------------------------------------


def test_link_anchor_404_raises_daemon_error(link_server):
    """Missing anchor → DaemonError on the /engrams/{id} call; /timeline
    is never reached."""
    captured, sock = link_server
    captured.anchor_status = 404
    client = DaemonClient(uds_path=sock)
    with pytest.raises(DaemonError) as exc_info:
        client.link(engram_id=999)
    assert "404" in str(exc_info.value)
    # Only the anchor fetch went on the wire; /timeline must not be called
    # after a 404 anchor.
    assert len(captured.paths) == 1
    assert captured.paths[0] == "/engrams/999"


def test_link_timeline_500_mid_flight_raises_daemon_error(link_server):
    """Anchor returns 200, /timeline returns 500 → DaemonError."""
    captured, sock = link_server
    captured.timeline_status = 500
    client = DaemonClient(uds_path=sock)
    with pytest.raises(DaemonError) as exc_info:
        client.link(engram_id=100)
    assert "500" in str(exc_info.value)
    # Both paths went on the wire — anchor succeeded, timeline failed.
    assert len(captured.paths) == 2
    assert captured.paths[0] == "/engrams/100"
    assert captured.paths[1].startswith("/timeline")


def test_link_unreachable_raises_daemon_error(tmp_path: Path):
    """No daemon at socket path → DaemonError (transport error) on anchor fetch."""
    nowhere = tmp_path / "no-daemon.sock"
    client = DaemonClient(uds_path=str(nowhere), timeout=0.5)
    with pytest.raises(DaemonError):
        client.link(engram_id=100)
