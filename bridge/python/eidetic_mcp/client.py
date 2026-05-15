"""Pure-stdlib UDS HTTP client for the eidetic-daemon API.

No external deps. Wraps the two endpoints the daemon exposes today
(GET /healthz, GET /engrams) so the MCP server in server.py can call them
without dragging in requests/httpx (deliberate — keeps install footprint
to just the `mcp` SDK).

Usage:

    client = DaemonClient()  # defaults to /tmp/eidetic-daemon.sock
    if client.healthy():
        rows = client.query_engrams(surface="claude_code", limit=20, since=0)
"""

from __future__ import annotations

import http.client
import json
import os
import socket
from dataclasses import dataclass
from typing import Optional, Sequence
from urllib.parse import urlencode


DEFAULT_UDS_PATH_DARWIN = "/tmp/eidetic-daemon.sock"
DEFAULT_UDS_PATH_LINUX = "/var/run/eidetic.sock"
DEFAULT_TCP_HOST = "127.0.0.1"
DEFAULT_TCP_PORT = 9876
DEFAULT_TIMEOUT_SEC = 5.0


@dataclass(frozen=True)
class Engram:
    """Mirror of the daemon's engram.Engram type. Fields match wire JSON."""

    id: int
    surface: str
    ts: int  # unix epoch nanoseconds
    payload: str
    meta: str = ""


class DaemonError(Exception):
    """Raised on any non-200 response or transport failure."""


class _UDSConnection(http.client.HTTPConnection):
    """HTTPConnection that dials a Unix-domain socket instead of TCP."""

    def __init__(self, path: str, timeout: float):
        super().__init__("localhost", timeout=timeout)
        self._uds_path = path

    def connect(self) -> None:  # type: ignore[override]
        sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        sock.settimeout(self.timeout)
        sock.connect(self._uds_path)
        self.sock = sock


class DaemonClient:
    """Thin client over the daemon's HTTP-over-UDS (default) or HTTP-over-TCP API.

    Constructor selects transport by EIDETIC_TCP=1 env var (TCP) else UDS.
    UDS path resolution: $EIDETIC_UDS_PATH > platform default.
    """

    def __init__(
        self,
        uds_path: Optional[str] = None,
        tcp_host: str = DEFAULT_TCP_HOST,
        tcp_port: int = DEFAULT_TCP_PORT,
        timeout: float = DEFAULT_TIMEOUT_SEC,
        auth_token: Optional[str] = None,
    ) -> None:
        self._timeout = timeout
        if os.environ.get("EIDETIC_TCP") == "1":
            self._mode = "tcp"
            self._tcp_host = tcp_host
            self._tcp_port = tcp_port
        else:
            self._mode = "uds"
            self._uds_path = uds_path or os.environ.get("EIDETIC_UDS_PATH") or _default_uds()

        # v0.0.9+: auto-discover Bearer token from <dataDir>/auth-token if
        # the daemon is auth-enabled. Resolution order:
        #   1. explicit auth_token kwarg (test injection)
        #   2. EIDETIC_AUTH_TOKEN env var
        #   3. <EIDETIC_DATA_DIR>/auth-token file (default ~/.eidetic/auth-token)
        # Empty/missing token = no Authorization header sent. Daemons not
        # running auth-mode pass through transparently; daemons in auth-mode
        # without a token return 401 on protected paths.
        self._auth_token: Optional[str] = (
            auth_token
            or os.environ.get("EIDETIC_AUTH_TOKEN")
            or _read_auth_token_file()
        )

    def _conn(self) -> http.client.HTTPConnection:
        if self._mode == "uds":
            return _UDSConnection(self._uds_path, self._timeout)
        return http.client.HTTPConnection(self._tcp_host, self._tcp_port, timeout=self._timeout)

    def _get_json(self, path: str) -> object:
        conn = self._conn()
        try:
            headers = {}
            if self._auth_token:
                headers["Authorization"] = f"Bearer {self._auth_token}"
            conn.request("GET", path, headers=headers)
            resp = conn.getresponse()
            body = resp.read().decode("utf-8")
            if resp.status != 200:
                raise DaemonError(f"daemon returned {resp.status}: {body}")
            return json.loads(body)
        except (OSError, json.JSONDecodeError) as exc:
            raise DaemonError(f"daemon transport / parse error: {exc}") from exc
        finally:
            conn.close()

    def healthy(self) -> bool:
        """Return True iff /healthz responds 200 with {'status':'ok'}."""
        try:
            body = self._get_json("/healthz")
        except DaemonError:
            return False
        return isinstance(body, dict) and body.get("status") == "ok"

    def metrics(self) -> dict:
        """GET /metrics — daemon observability endpoint (v0.0.7+).

        Returns the JSON body verbatim as dict. Schema is additive-only
        across versions; callers should treat unknown fields as forward-compat.
        Raises DaemonError if the daemon predates v0.0.7 (returns 503
        'metrics not configured') or on any transport / parse failure.
        """
        body = self._get_json("/metrics")
        if not isinstance(body, dict):
            raise DaemonError(f"expected object, got {type(body).__name__}")
        return body

    def query_engrams(
        self, surface: str, limit: int = 50, since: int = 0
    ) -> Sequence[Engram]:
        """Spec § 2.4 retrieval endpoint. surface required; limit defaults to
        50 (daemon-side capped at 500); since=0 means no lower bound."""
        if not surface:
            raise ValueError("surface required")
        params: dict[str, str] = {"surface": surface}
        if limit:
            params["limit"] = str(limit)
        if since > 0:
            params["since"] = str(since)
        body = self._get_json(f"/engrams?{urlencode(params)}")
        if not isinstance(body, list):
            raise DaemonError(f"expected array, got {type(body).__name__}")
        return tuple(_parse_engram(row) for row in body)


def _parse_engram(row: object) -> Engram:
    if not isinstance(row, dict):
        raise DaemonError(f"engram row not object: {row!r}")
    try:
        return Engram(
            id=int(row["id"]),
            surface=str(row["surface"]),
            ts=int(row["ts"]),
            payload=str(row["payload"]),
            meta=str(row.get("meta", "")),
        )
    except (KeyError, TypeError, ValueError) as exc:
        raise DaemonError(f"engram row missing/invalid field: {row!r} ({exc})") from exc


def _default_uds() -> str:
    import sys

    return DEFAULT_UDS_PATH_LINUX if sys.platform.startswith("linux") else DEFAULT_UDS_PATH_DARWIN


def _read_auth_token_file() -> Optional[str]:
    """Read <dataDir>/auth-token if present (v0.0.9+ Bearer-token discovery).

    dataDir resolution: $EIDETIC_DATA_DIR or ~/.eidetic. Returns the
    stripped token string, or None if the file doesn't exist / isn't
    readable. No exception on missing — auth-disabled daemons are the
    common case.
    """
    data_dir = os.environ.get("EIDETIC_DATA_DIR")
    if not data_dir:
        home = os.environ.get("HOME")
        if not home:
            return None
        data_dir = os.path.join(home, ".eidetic")
    token_path = os.path.join(data_dir, "auth-token")
    try:
        with open(token_path, "r", encoding="utf-8") as f:
            return f.read().strip() or None
    except OSError:
        return None
