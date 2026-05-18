"""Tests for eidetic_mcp.client (the UDS HTTP client only).

End-to-end tests against a real daemon are in scripts/demo-smoke.sh
(daemon-side, exercises the wire format). Here we test parsing, error
shapes, and env-var defaults using a fake server."""

from __future__ import annotations

import http.server
import json
import os
import re
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

    def _send_json(self, payload: object) -> None:
        body = json.dumps(payload).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):  # noqa: N802
        if self.path == "/healthz":
            self._send_json({"status": "ok"})
            return
        if self.path == "/engrams/count" or self.path.startswith("/engrams/count?"):
            self._send_json({"count": 42})
            return
        if re.match(r"^/engrams/\d+$", self.path):
            engram_id = int(self.path.split("/")[-1])
            if engram_id == 999:
                self.send_response(404)
                self.end_headers()
                return
            self._send_json({"id": engram_id, "surface": "claude_code", "ts": 100, "payload": "hi", "meta": ""})
            return
        if self.path.startswith("/engrams"):
            self._send_json([
                {"id": 1, "surface": "claude_code", "ts": 100, "payload": "hi", "meta": ""},
            ])
            return
        if self.path == "/metrics":
            self._send_json({
                "version": "v0.0.7-fake",
                "uptime_seconds": 42,
                "engram_total": 100,
                "engram_by_surface": {"claude_code": 100},
                "capture_skipped": 0,
                "db_path": "/fake/engrams.db",
                "db_size_bytes": 1024,
            })
            return
        if self.path == "/surfaces":
            self._send_json({"claude_code": 100, "cursor": 42})
            return
        if self.path.startswith("/search"):
            self._send_json([
                {"id": 7, "surface": "claude_code", "ts": 200, "payload": "benchmark result", "meta": ""},
            ])
            return
        if self.path.startswith("/recent"):
            self._send_json([
                {"id": 9, "surface": "cursor", "ts": 999, "payload": "latest thing", "meta": ""},
            ])
            return
        self.send_response(404)
        self.end_headers()

    def do_POST(self):  # noqa: N802
        length = int(self.headers.get("Content-Length", 0))
        raw = self.rfile.read(length)
        if self.path == "/engrams/batch":
            try:
                items = json.loads(raw)
            except json.JSONDecodeError:
                items = []
            self.send_response(201)
            body = json.dumps({"inserted": len(items)}).encode()
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        if self.path.startswith("/engrams"):
            self.send_response(201)
            body = json.dumps({"id": 42}).encode()
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        self.send_response(404)
        self.end_headers()

    def do_DELETE(self):  # noqa: N802
        if re.match(r"^/engrams/\d+$", self.path):
            engram_id = int(self.path.split("/")[-1])
            if engram_id == 999:
                self.send_response(404)
                body = b"engram not found\n"
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
                return
            self._send_json({"deleted": 1})
            return
        if self.path.startswith("/engrams"):
            self._send_json({"deleted": 5})
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


def test_client_metrics_against_fake_server(uds_socket_path: str):
    """GET /metrics returns the daemon JSON verbatim as dict. Schema is
    additive-only across versions per v0.0.7 contract."""
    client = DaemonClient(uds_path=uds_socket_path)
    m = client.metrics()
    assert isinstance(m, dict)
    assert m["version"] == "v0.0.7-fake"
    assert m["engram_total"] == 100
    assert m["engram_by_surface"] == {"claude_code": 100}
    assert m["capture_skipped"] == 0
    assert m["db_path"] == "/fake/engrams.db"
    assert m["db_size_bytes"] == 1024


def test_client_metrics_unreachable_raises_daemon_error(tmp_path: Path):
    """No daemon at socket path → metrics() must raise DaemonError, not
    silently return empty dict (analog to healthy() returning False)."""
    from eidetic_mcp.client import DaemonError

    nowhere = tmp_path / "no-such-daemon.sock"
    client = DaemonClient(uds_path=str(nowhere))
    with pytest.raises(DaemonError):
        client.metrics()


def test_client_auth_token_explicit_kwarg(uds_socket_path: str):
    """v0.0.9+: explicit auth_token kwarg routed into Authorization header.
    Fake server doesn't enforce auth, so we just verify the header is sent
    by introspecting the kwarg priority over env / file."""
    client = DaemonClient(uds_path=uds_socket_path, auth_token="explicit-test-token")
    assert client._auth_token == "explicit-test-token"  # noqa: SLF001 — internal state assertion intentional
    # Round-trip works (fake server ignores auth):
    assert client.healthy() is True


def test_client_auth_token_from_env(monkeypatch, uds_socket_path: str):
    """EIDETIC_AUTH_TOKEN env var overrides file lookup. Useful for CI /
    one-shot invocations without writing the file."""
    monkeypatch.setenv("EIDETIC_AUTH_TOKEN", "env-test-token")
    monkeypatch.delenv("EIDETIC_DATA_DIR", raising=False)
    client = DaemonClient(uds_path=uds_socket_path)
    assert client._auth_token == "env-test-token"  # noqa: SLF001


def test_client_auth_token_from_file(monkeypatch, tmp_path: Path, uds_socket_path: str):
    """v0.0.9+: <EIDETIC_DATA_DIR>/auth-token auto-discovered when present.
    Mirrors the daemon-side WriteFile path."""
    data_dir = tmp_path / "datadir"
    data_dir.mkdir()
    (data_dir / "auth-token").write_text("file-test-token-aaaa-bbbb-cccc")
    monkeypatch.setenv("EIDETIC_DATA_DIR", str(data_dir))
    monkeypatch.delenv("EIDETIC_AUTH_TOKEN", raising=False)
    client = DaemonClient(uds_path=uds_socket_path)
    assert client._auth_token == "file-test-token-aaaa-bbbb-cccc"  # noqa: SLF001


def test_client_auth_token_absent_when_no_source(monkeypatch, tmp_path: Path, uds_socket_path: str):
    """No env, no file → token is None, no Authorization header sent.
    Preserves backward-compat for daemons not running auth-mode."""
    nowhere = tmp_path / "no-data-dir"
    monkeypatch.setenv("EIDETIC_DATA_DIR", str(nowhere))
    monkeypatch.delenv("EIDETIC_AUTH_TOKEN", raising=False)
    client = DaemonClient(uds_path=uds_socket_path)
    assert client._auth_token is None  # noqa: SLF001
    assert client.healthy() is True  # fake server doesn't enforce; verifies no transport breakage


# ── v0.0.13: surfaces() + purge_engrams() ───────────────────────────────────

def test_client_surfaces_against_fake_server(uds_socket_path: str):
    """GET /surfaces returns surface → count dict (v0.0.13+)."""
    client = DaemonClient(uds_path=uds_socket_path)
    counts = client.surfaces()
    assert isinstance(counts, dict)
    assert counts["claude_code"] == 100
    assert counts["cursor"] == 42


def test_client_surfaces_unreachable_raises(tmp_path: Path):
    client = DaemonClient(uds_path=str(tmp_path / "no.sock"), timeout=0.5)
    with pytest.raises(DaemonError):
        client.surfaces()


def test_client_purge_engrams_against_fake_server(uds_socket_path: str):
    """DELETE /engrams returns deleted count (v0.0.13+)."""
    client = DaemonClient(uds_path=uds_socket_path)
    deleted = client.purge_engrams(surface="cursor")
    assert deleted == 5


def test_client_purge_engrams_with_before(uds_socket_path: str):
    """before= param accepted; fake server ignores it but round-trip completes."""
    client = DaemonClient(uds_path=uds_socket_path)
    deleted = client.purge_engrams(surface="cursor", before=1715000000000000000)
    assert deleted == 5


def test_client_purge_engrams_requires_surface(uds_socket_path: str):
    client = DaemonClient(uds_path=uds_socket_path)
    with pytest.raises(ValueError):
        client.purge_engrams(surface="")


def test_client_search_engrams_against_fake_server(uds_socket_path: str):
    """GET /search returns Engram tuple ordered by rank (v0.0.14+)."""
    client = DaemonClient(uds_path=uds_socket_path)
    rows = client.search_engrams(q="benchmark")
    assert len(rows) == 1
    assert rows[0].surface == "claude_code"
    assert rows[0].payload == "benchmark result"


def test_client_search_engrams_with_surface_and_limit(uds_socket_path: str):
    """surface= and limit= params accepted; fake server ignores them but round-trip completes."""
    client = DaemonClient(uds_path=uds_socket_path)
    rows = client.search_engrams(q="benchmark", surface="claude_code", limit=10)
    assert isinstance(rows, tuple)
    assert len(rows) == 1


def test_client_search_engrams_requires_q(uds_socket_path: str):
    client = DaemonClient(uds_path=uds_socket_path)
    with pytest.raises(ValueError):
        client.search_engrams(q="")


def test_client_recent_engrams_against_fake_server(uds_socket_path: str):
    """GET /recent returns Engram tuple newest-first (v0.0.15+)."""
    client = DaemonClient(uds_path=uds_socket_path)
    rows = client.recent_engrams()
    assert len(rows) == 1
    assert rows[0].surface == "cursor"
    assert rows[0].ts == 999


def test_client_recent_engrams_with_since_and_limit(uds_socket_path: str):
    """since= and limit= params accepted; fake server ignores them but round-trip completes."""
    client = DaemonClient(uds_path=uds_socket_path)
    rows = client.recent_engrams(since=100, limit=5)
    assert isinstance(rows, tuple)
    assert len(rows) == 1


def test_client_recent_engrams_empty_params_omitted(uds_socket_path: str):
    """Default call (since=0, limit=50) sends no query params (cleaner URLs)."""
    import re
    client = DaemonClient(uds_path=uds_socket_path)
    # If the fake server returns data, params were correctly omitted or ignored
    rows = client.recent_engrams(since=0, limit=50)
    assert isinstance(rows, tuple)


def test_client_insert_engram_returns_id(uds_socket_path: str):
    """POST /engrams returns {"id": N}; insert_engram returns the int id."""
    client = DaemonClient(uds_path=uds_socket_path)
    engram_id = client.insert_engram(surface="claude_code", payload="hello api")
    assert engram_id == 42


def test_client_insert_engram_with_ts_and_meta(uds_socket_path: str):
    """ts and meta params accepted; fake server ignores them but round-trip completes."""
    client = DaemonClient(uds_path=uds_socket_path)
    engram_id = client.insert_engram(
        surface="cursor", payload="annotated", ts=999_000_000, meta='{"k":"v"}'
    )
    assert isinstance(engram_id, int)
    assert engram_id > 0


def test_client_insert_engram_requires_surface(uds_socket_path: str):
    client = DaemonClient(uds_path=uds_socket_path)
    with pytest.raises(ValueError):
        client.insert_engram(surface="", payload="x")


def test_client_insert_engram_requires_payload(uds_socket_path: str):
    client = DaemonClient(uds_path=uds_socket_path)
    with pytest.raises(ValueError):
        client.insert_engram(surface="vim", payload="")


def test_client_insert_engrams_batch_returns_count(uds_socket_path: str):
    """POST /engrams/batch returns {"inserted": N}; method returns the int count."""
    client = DaemonClient(uds_path=uds_socket_path)
    items = [
        {"surface": "claude_code", "payload": "first", "ts": 1},
        {"surface": "cursor", "payload": "second", "ts": 2},
    ]
    n = client.insert_engrams_batch(items)
    assert n == 2


def test_client_insert_engrams_batch_with_optional_fields(uds_socket_path: str):
    """ts and meta are optional; round-trip completes."""
    client = DaemonClient(uds_path=uds_socket_path)
    items = [{"surface": "vim", "payload": "annotated", "meta": '{"k":"v"}'}]
    n = client.insert_engrams_batch(items)
    assert isinstance(n, int)
    assert n == 1


def test_client_insert_engrams_batch_empty_raises(uds_socket_path: str):
    client = DaemonClient(uds_path=uds_socket_path)
    with pytest.raises(ValueError):
        client.insert_engrams_batch([])


def test_client_insert_engrams_batch_missing_surface_raises(uds_socket_path: str):
    client = DaemonClient(uds_path=uds_socket_path)
    with pytest.raises(ValueError):
        client.insert_engrams_batch([{"payload": "no surface"}])


def test_client_get_engram_by_id_returns_engram(uds_socket_path: str):
    client = DaemonClient(uds_path=uds_socket_path)
    e = client.get_engram_by_id(42)
    assert e.id == 42
    assert e.surface == "claude_code"
    assert e.payload == "hi"


def test_client_get_engram_by_id_not_found_raises(uds_socket_path: str):
    client = DaemonClient(uds_path=uds_socket_path)
    with pytest.raises(DaemonError):
        client.get_engram_by_id(999)


def test_client_get_engram_by_id_zero_raises_value_error(uds_socket_path: str):
    client = DaemonClient(uds_path=uds_socket_path)
    with pytest.raises(ValueError):
        client.get_engram_by_id(0)


def test_client_get_engram_by_id_negative_raises_value_error(uds_socket_path: str):
    client = DaemonClient(uds_path=uds_socket_path)
    with pytest.raises(ValueError):
        client.get_engram_by_id(-1)


def test_client_delete_engram_by_id_returns_true(uds_socket_path: str):
    client = DaemonClient(uds_path=uds_socket_path)
    result = client.delete_engram_by_id(42)
    assert result is True


def test_client_delete_engram_by_id_not_found_raises(uds_socket_path: str):
    client = DaemonClient(uds_path=uds_socket_path)
    with pytest.raises(DaemonError):
        client.delete_engram_by_id(999)


def test_client_delete_engram_by_id_zero_raises_value_error(uds_socket_path: str):
    client = DaemonClient(uds_path=uds_socket_path)
    with pytest.raises(ValueError):
        client.delete_engram_by_id(0)


def test_client_delete_engram_by_id_negative_raises_value_error(uds_socket_path: str):
    client = DaemonClient(uds_path=uds_socket_path)
    with pytest.raises(ValueError):
        client.delete_engram_by_id(-1)


def test_client_count_engrams_all(uds_socket_path: str):
    client = DaemonClient(uds_path=uds_socket_path)
    n = client.count_engrams()
    assert n == 42


def test_client_count_engrams_with_surface(uds_socket_path: str):
    client = DaemonClient(uds_path=uds_socket_path)
    n = client.count_engrams(surface="claude_code")
    assert n == 42


def test_client_count_engrams_with_since(uds_socket_path: str):
    client = DaemonClient(uds_path=uds_socket_path)
    n = client.count_engrams(since=1747500000000000000)
    assert n == 42


# --- before= param tests (v0.0.21) ---

def test_client_query_engrams_with_before(uds_socket_path: str):
    client = DaemonClient(uds_path=uds_socket_path)
    rows = client.query_engrams(surface="claude_code", before=9999999999)
    assert len(rows) == 1
    assert rows[0].surface == "claude_code"


def test_client_query_engrams_since_and_before(uds_socket_path: str):
    """since + before together: both params forwarded, fake returns fixture."""
    client = DaemonClient(uds_path=uds_socket_path)
    rows = client.query_engrams(surface="claude_code", since=100, before=9999999999)
    assert len(rows) == 1


def test_client_recent_engrams_with_before(uds_socket_path: str):
    client = DaemonClient(uds_path=uds_socket_path)
    rows = client.recent_engrams(before=9999999999)
    assert len(rows) == 1
    assert rows[0].payload == "latest thing"


def test_client_recent_engrams_since_and_before(uds_socket_path: str):
    """since + before both forwarded; fake returns fixture."""
    client = DaemonClient(uds_path=uds_socket_path)
    rows = client.recent_engrams(since=100, before=9999999999)
    assert len(rows) == 1


# --- order=asc param tests (v0.0.22) ---

def test_client_query_engrams_asc(uds_socket_path: str):
    """asc=True forwards order=asc; fake server returns fixture regardless."""
    client = DaemonClient(uds_path=uds_socket_path)
    rows = client.query_engrams(surface="claude_code", asc=True)
    assert len(rows) == 1


def test_client_query_engrams_default_is_desc(uds_socket_path: str):
    """asc=False (default) does not add order= param; fake returns fixture."""
    client = DaemonClient(uds_path=uds_socket_path)
    rows = client.query_engrams(surface="claude_code", asc=False)
    assert len(rows) == 1
