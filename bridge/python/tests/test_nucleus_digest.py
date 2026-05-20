"""Tests for the DaemonClient.digest() method (v0.0.6+ bridge / v0.0.47+ daemon).

End-to-end MCP test (Tool dispatched, daemon returns digest) lives in
scripts/demo-smoke.sh — requires a live daemon and mcp SDK installed.

The unit tests below spin up a mock UDS HTTP server (mirroring test_client.py's
idiom) and exercise:
  - happy path (200 + valid JSON dict body returned verbatim)
  - bad window arg (client-side validation, no HTTP call)
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


# Captured request bookkeeping — verifies the URL the client builds for /digest.
class _Captured:
    def __init__(self) -> None:
        self.paths: list[str] = []
        self.status_to_send: int = 200
        self.body_to_send: dict | str = {
            "window": "7d",
            "since": 1746896400000000000,
            "total_engrams": 1280,
            "by_surface": {"claude_code": 1101, "cursor": 179},
            "top_hours": [{"hour": 14, "count": 80}],
            "top_terms": [{"term": "engram", "count": 53}],
            "sample_engrams": [
                {"id": 1, "surface": "claude_code", "ts": 1, "payload": "p", "meta": ""}
            ],
            "instructions": (
                "Render the recap as a short markdown digest with sections for "
                "total counts, top surfaces, top hours, and a 1-line callout of "
                "the most active term."
            ),
        }


def _build_handler(captured: _Captured):
    class _Handler(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_args, **_kw):  # silence test output
            pass

        def do_GET(self):  # noqa: N802
            captured.paths.append(self.path)
            if not self.path.startswith("/digest"):
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
def digest_server(tmp_path: Path):
    """Spin up a UDS HTTP server with mutable response state.

    Mirrors test_client.py's short-path discipline so we stay under macOS's
    104-byte sun_path limit. Yields (captured, socket_path) so each test can
    flip status_to_send / body_to_send before calling the client.
    """
    short = Path(f"/tmp/eidetic-digest-test-{os.getpid()}-{int(time.time()*1000)}.sock")
    if len(str(short)) >= 104:
        short = Path(f"/tmp/ed-{int(time.time()*1000) % 1_000_000}.sock")
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


def test_digest_happy_path_default_window(digest_server):
    """digest() with no arg sends /digest?window=7d and returns body verbatim."""
    captured, sock = digest_server
    client = DaemonClient(uds_path=sock)
    body = client.digest()
    assert isinstance(body, dict)
    assert body["window"] == "7d"
    assert body["total_engrams"] == 1280
    assert body["by_surface"] == {"claude_code": 1101, "cursor": 179}
    assert body["instructions"].startswith("Render the recap")
    assert captured.paths == ["/digest?window=7d"]


def test_digest_happy_path_24h(digest_server):
    """window='24h' is forwarded as a query string."""
    captured, sock = digest_server
    captured.body_to_send = {**captured.body_to_send, "window": "24h"}  # type: ignore[arg-type]
    client = DaemonClient(uds_path=sock)
    body = client.digest(window="24h")
    assert body["window"] == "24h"
    assert captured.paths == ["/digest?window=24h"]


def test_digest_happy_path_30d(digest_server):
    """window='30d' is forwarded as a query string."""
    captured, sock = digest_server
    captured.body_to_send = {**captured.body_to_send, "window": "30d"}  # type: ignore[arg-type]
    client = DaemonClient(uds_path=sock)
    body = client.digest(window="30d")
    assert body["window"] == "30d"
    assert captured.paths == ["/digest?window=30d"]


def test_digest_returns_json_verbatim(digest_server):
    """The full daemon payload — including instructions + nested fields — is
    returned untouched. Bridge does not strip or restructure."""
    captured, sock = digest_server
    client = DaemonClient(uds_path=sock)
    body = client.digest(window="7d")
    # All daemon-side keys present:
    for key in (
        "window",
        "since",
        "total_engrams",
        "by_surface",
        "top_hours",
        "top_terms",
        "sample_engrams",
        "instructions",
    ):
        assert key in body, f"missing key {key!r} in digest response"
    assert body["sample_engrams"][0]["payload"] == "p"


# --- client-side validation -----------------------------------------------


def test_digest_bad_window_raises_no_http(digest_server):
    """Bad window arg fails client-side; no HTTP request is dispatched."""
    captured, sock = digest_server
    client = DaemonClient(uds_path=sock)
    with pytest.raises(ValueError):
        client.digest(window="1h")  # not in {24h, 7d, 30d}
    assert captured.paths == []  # no round-trip on bad input


def test_digest_empty_window_raises(digest_server):
    captured, sock = digest_server
    client = DaemonClient(uds_path=sock)
    with pytest.raises(ValueError):
        client.digest(window="")
    assert captured.paths == []


def test_digest_uppercase_window_raises(digest_server):
    """Validation is exact-string — '7D' is not '7d'."""
    captured, sock = digest_server
    client = DaemonClient(uds_path=sock)
    with pytest.raises(ValueError):
        client.digest(window="7D")
    assert captured.paths == []


# --- error path -----------------------------------------------------------


def test_digest_500_raises_daemon_error(digest_server):
    """5xx from the daemon surfaces as DaemonError carrying the status + body."""
    captured, sock = digest_server
    captured.status_to_send = 500
    client = DaemonClient(uds_path=sock)
    with pytest.raises(DaemonError) as exc_info:
        client.digest(window="7d")
    assert "500" in str(exc_info.value)


def test_digest_non_object_body_raises(digest_server):
    """Daemon returning a non-dict (e.g. array, string) should fail with DaemonError
    rather than silently returning the wrong shape."""
    captured, sock = digest_server
    captured.body_to_send = "not an object"
    client = DaemonClient(uds_path=sock)
    with pytest.raises(DaemonError):
        client.digest(window="7d")


def test_digest_unreachable_raises_daemon_error(tmp_path: Path):
    """No daemon at socket path → DaemonError (transport error)."""
    nowhere = tmp_path / "no-daemon.sock"
    client = DaemonClient(uds_path=str(nowhere), timeout=0.5)
    with pytest.raises(DaemonError):
        client.digest(window="7d")
