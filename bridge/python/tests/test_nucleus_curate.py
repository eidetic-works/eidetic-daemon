"""Tests for the DaemonClient.curate() method (v0.0.9+ bridge).

curate() composes TWO daemon calls: GET /engrams/{id} (verify target exists)
then POST /engrams (insert the curation overlay on surface='curation').
The mock dispatches on path + method, mirroring test_nucleus_link.py's idiom.

End-to-end MCP dispatch test (Tool registration + Tool call → curate())
lives in scripts/demo-smoke.sh — requires a live daemon and mcp SDK.
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


class _Captured:
    def __init__(self) -> None:
        self.paths: list[tuple[str, str]] = []  # (method, path)
        self.post_bodies: list[dict] = []
        # /engrams/{id} response (the target lookup). Default = 200 with a
        # sample target on surface 'claude_code' at a representative ns ts.
        self.target_status: int = 200
        self.target_body: dict = {
            "id": 100,
            "surface": "claude_code",
            "ts": 1_700_000_000_000_000_000,
            "payload": "the engram being curated",
            "meta": "",
        }
        # POST /engrams response (the insert). Default = 200 with new id.
        self.insert_status: int = 200
        self.insert_body: dict = {"id": 9999}


@pytest.fixture
def mock_daemon(tmp_path: Path):
    """Spin up a UDS HTTP mock and yield (uds_path, captured).

    Uses /tmp/ short paths per test_nucleus_link.py's discipline (macOS's
    104-byte sun_path limit means pytest tmp_path is too long).
    """
    captured = _Captured()
    short = Path(f"/tmp/eidetic-curate-{os.getpid()}-{int(time.time()*1000)}.sock")
    if len(str(short)) >= 104:
        short = Path(f"/tmp/ec-{int(time.time()*1000) % 1_000_000}.sock")
    if short.exists():
        short.unlink()
    uds_path = str(short)

    class Handler(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_args, **_kwargs):
            pass

        def do_GET(self):  # noqa: N802
            captured.paths.append(("GET", self.path))
            if self.path.startswith("/engrams/"):
                self._respond(captured.target_status, captured.target_body)
                return
            self._respond(404, {"error": "not found"})

        def do_POST(self):  # noqa: N802
            captured.paths.append(("POST", self.path))
            length = int(self.headers.get("Content-Length", "0"))
            raw = self.rfile.read(length) if length else b""
            try:
                captured.post_bodies.append(json.loads(raw.decode("utf-8")))
            except Exception:
                captured.post_bodies.append({"_raw": raw.decode("utf-8", errors="replace")})
            if self.path == "/engrams":
                self._respond(captured.insert_status, captured.insert_body)
                return
            self._respond(404, {"error": "not found"})

        def _respond(self, status: int, body: dict):
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps(body).encode("utf-8"))

    class UDSServer(socketserver.UnixStreamServer):
        allow_reuse_address = True

    srv = UDSServer(uds_path, Handler)
    th = threading.Thread(target=srv.serve_forever, daemon=True)
    th.start()
    try:
        yield uds_path, captured
    finally:
        srv.shutdown()
        srv.server_close()


def test_curate_canonical_happy_path(mock_daemon):
    uds_path, captured = mock_daemon
    client = DaemonClient(uds_path=uds_path)
    result = client.curate(engram_id=100, action="canonical", note="this is THE answer")

    assert result["ok"] is True
    assert result["curation_engram_id"] == 9999
    assert result["target_engram_id"] == 100
    assert result["action"] == "canonical"
    assert result["surface"] == "curation"

    # Confirms client made BOTH calls in order: GET target, POST overlay.
    assert captured.paths[0] == ("GET", "/engrams/100")
    assert captured.paths[1] == ("POST", "/engrams")

    # Verify the inserted body carried the right surface + meta JSON.
    inserted = captured.post_bodies[0]
    assert inserted["surface"] == "curation"
    assert "curation: canonical engram 100" in inserted["payload"]
    assert "this is THE answer" in inserted["payload"]
    meta = json.loads(inserted["meta"])
    assert meta["target_engram_id"] == 100
    assert meta["target_surface"] == "claude_code"
    assert meta["target_ts"] == 1_700_000_000_000_000_000
    assert meta["action"] == "canonical"
    assert meta["note"] == "this is THE answer"


def test_curate_demote_no_note(mock_daemon):
    uds_path, captured = mock_daemon
    client = DaemonClient(uds_path=uds_path)
    result = client.curate(engram_id=100, action="demote")

    assert result["action"] == "demote"
    inserted = captured.post_bodies[0]
    assert inserted["payload"] == "curation: demote engram 100 (surface=claude_code)"
    meta = json.loads(inserted["meta"])
    assert "note" not in meta  # empty note → omit, don't write empty string


def test_curate_archive(mock_daemon):
    uds_path, _ = mock_daemon
    client = DaemonClient(uds_path=uds_path)
    result = client.curate(engram_id=100, action="archive")
    assert result["action"] == "archive"


def test_curate_rejects_invalid_engram_id(mock_daemon):
    uds_path, _ = mock_daemon
    client = DaemonClient(uds_path=uds_path)
    with pytest.raises(ValueError, match="positive integer"):
        client.curate(engram_id=0, action="canonical")
    with pytest.raises(ValueError, match="positive integer"):
        client.curate(engram_id=-5, action="canonical")
    with pytest.raises(ValueError, match="positive integer"):
        client.curate(engram_id="100", action="canonical")  # type: ignore[arg-type]


def test_curate_rejects_invalid_action(mock_daemon):
    uds_path, _ = mock_daemon
    client = DaemonClient(uds_path=uds_path)
    with pytest.raises(ValueError, match="canonical"):
        client.curate(engram_id=100, action="promote")
    with pytest.raises(ValueError, match="canonical"):
        client.curate(engram_id=100, action="")
    with pytest.raises(ValueError, match="canonical"):
        client.curate(engram_id=100, action="CANONICALIZE")


def test_curate_rejects_oversized_note(mock_daemon):
    uds_path, _ = mock_daemon
    client = DaemonClient(uds_path=uds_path)
    big_note = "x" * 2001
    with pytest.raises(ValueError, match="2000-char limit"):
        client.curate(engram_id=100, action="canonical", note=big_note)


def test_curate_propagates_target_404(mock_daemon):
    uds_path, captured = mock_daemon
    captured.target_status = 404
    captured.target_body = {"error": "engram not found"}
    client = DaemonClient(uds_path=uds_path)
    with pytest.raises(DaemonError):
        client.curate(engram_id=100, action="canonical")
    # Critically: no POST should have happened — we abort on target 404.
    assert all(method == "GET" for (method, _path) in captured.paths)


def test_curate_normalizes_action_case(mock_daemon):
    """Action is case-insensitive — 'Canonical' and ' canonical ' both work."""
    uds_path, captured = mock_daemon
    client = DaemonClient(uds_path=uds_path)
    result = client.curate(engram_id=100, action="  CANONICAL  ")
    assert result["action"] == "canonical"
    meta = json.loads(captured.post_bodies[0]["meta"])
    assert meta["action"] == "canonical"
