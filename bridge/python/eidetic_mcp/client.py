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
import sys
import threading
from dataclasses import dataclass
from typing import Optional, Sequence
from urllib.parse import urlencode

_TELEMETRY_URL = "https://gumroad-kit-sync.morning-lake-f944.workers.dev/ping"
_PING_VERSION = "0.0.3"
_pinged = False


def _fire_telemetry_ping() -> None:
    """Fire-and-forget telemetry ping on first MCP server startup.

    Sends: version, platform. Opt-out: EIDETIC_NO_TELEMETRY=1.
    Uses CF Worker /ping endpoint — returns 204, no body stored.
    Runs in a daemon thread so it never blocks the caller.
    """
    global _pinged
    if _pinged or os.environ.get("EIDETIC_NO_TELEMETRY") == "1":
        return
    _pinged = True

    def _ping() -> None:
        try:
            params = urlencode({"v": _PING_VERSION, "platform": sys.platform})
            conn = http.client.HTTPSConnection("gumroad-kit-sync.morning-lake-f944.workers.dev", timeout=3)
            conn.request("GET", f"/ping?{params}")
            conn.getresponse()
        except Exception:
            pass

    t = threading.Thread(target=_ping, daemon=True)
    t.start()


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
    # Populated by search_engrams only — ~20-token FTS5 context window around
    # the match. Empty string for all other retrieval paths (v0.0.28+).
    snippet: str = ""


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
        import sys

        self._timeout = timeout
        if os.environ.get("EIDETIC_TCP") == "1" or sys.platform == "win32":
            self._mode = "tcp"
            self._tcp_host = tcp_host
            self._tcp_port = tcp_port
        else:
            self._mode = "uds"
            self._uds_path = uds_path or os.environ.get("EIDETIC_UDS_PATH") or _default_uds()

        _fire_telemetry_ping()

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

    def _request_json(self, method: str, path: str) -> object:
        conn = self._conn()
        try:
            headers = {}
            if self._auth_token:
                headers["Authorization"] = f"Bearer {self._auth_token}"
            conn.request(method, path, headers=headers)
            resp = conn.getresponse()
            body = resp.read().decode("utf-8")
            if resp.status != 200:
                raise DaemonError(f"daemon returned {resp.status}: {body}")
            return json.loads(body)
        except (OSError, json.JSONDecodeError) as exc:
            raise DaemonError(f"daemon transport / parse error: {exc}") from exc
        finally:
            conn.close()

    def _get_json(self, path: str) -> object:
        return self._request_json("GET", path)

    def _delete_json(self, path: str) -> object:
        return self._request_json("DELETE", path)

    def _post_json(self, path: str, payload: object) -> object:
        conn = self._conn()
        try:
            body_bytes = json.dumps(payload).encode("utf-8")
            headers: dict[str, str] = {"Content-Type": "application/json"}
            if self._auth_token:
                headers["Authorization"] = f"Bearer {self._auth_token}"
            conn.request("POST", path, body=body_bytes, headers=headers)
            resp = conn.getresponse()
            body = resp.read().decode("utf-8")
            if resp.status not in (200, 201):
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
        self, surface: str = "", limit: int = 50, since: int = 0, before: int = 0, asc: bool = False
    ) -> Sequence[Engram]:
        """Spec § 2.4 retrieval endpoint. surface is optional (v0.0.23+);
        omit or pass "" to retrieve across all surfaces. limit defaults to
        50 (daemon-side capped at 500); since=0 means no lower bound;
        before=0 means no upper bound (v0.0.21+); asc=False = newest-first,
        asc=True = oldest-first (v0.0.22+)."""
        params: dict[str, str] = {}
        if surface:
            params["surface"] = surface
        if limit:
            params["limit"] = str(limit)
        if since > 0:
            params["since"] = str(since)
        if before > 0:
            params["before"] = str(before)
        if asc:
            params["order"] = "asc"
        qs = f"?{urlencode(params)}" if params else ""
        body = self._get_json(f"/engrams{qs}")
        if not isinstance(body, list):
            raise DaemonError(f"expected array, got {type(body).__name__}")
        return tuple(_parse_engram(row) for row in body)


    def surfaces(self) -> dict[str, int]:
        """GET /surfaces — map of every surface the daemon has seen to its engram count (v0.0.13+).

        Returns an empty dict when the store is empty. Raises DaemonError on
        transport failure or if the daemon predates v0.0.13.
        """
        body = self._get_json("/surfaces")
        if not isinstance(body, dict):
            raise DaemonError(f"expected object, got {type(body).__name__}")
        return {str(k): int(v) for k, v in body.items()}

    def purge_engrams(self, surface: str, before: int = 0) -> int:
        """DELETE /engrams — remove engrams for a surface (v0.0.13+).

        `before` is unix epoch nanoseconds; 0 (default) purges ALL engrams for
        the surface. Returns the number of rows deleted. Raises DaemonError on
        transport failure, if surface is empty, or if the daemon predates v0.0.13.
        """
        if not surface:
            raise ValueError("surface required")
        params: dict[str, str] = {"surface": surface}
        if before > 0:
            params["before"] = str(before)
        body = self._delete_json(f"/engrams?{urlencode(params)}")
        if not isinstance(body, dict) or "deleted" not in body:
            raise DaemonError(f"unexpected purge response: {body!r}")
        return int(body["deleted"])

    def search_engrams(
        self,
        q: str,
        surface: str = "",
        limit: int = 50,
    ) -> tuple[Engram, ...]:
        """GET /search — full-text search over engram payloads (v0.0.14+).

        `q` is an FTS5 match expression: bare keywords, phrase queries in
        double quotes ("benchmark result"), OR/AND/NOT boolean operators.
        Results are ordered by relevance rank (best match first). Returns the
        same Engram tuple shape as query_engrams for client compatibility.

        Raises ValueError if q is empty. Raises DaemonError on transport
        failure or if the daemon predates v0.0.14.
        """
        if not q:
            raise ValueError("q required")
        params: dict[str, str] = {"q": q}
        if surface:
            params["surface"] = surface
        if limit != 50:
            params["limit"] = str(limit)
        body = self._get_json(f"/search?{urlencode(params)}")
        if not isinstance(body, list):
            raise DaemonError(f"expected array, got {type(body).__name__}")
        return tuple(_parse_engram(row) for row in body)

    def recent_engrams(
        self,
        since: int = 0,
        before: int = 0,
        limit: int = 50,
    ) -> tuple[Engram, ...]:
        """GET /recent — newest engrams across all surfaces (v0.0.15+).

        since: Unix nanoseconds; only return engrams with ts > since (0 = all).
        before: Unix nanoseconds; only return engrams with ts < before (0 = all, v0.0.21+).
        limit: 1-500, default 50.
        Results are ordered newest-first. Returns the same Engram tuple shape
        as query_engrams for client compatibility.

        Raises DaemonError on transport failure or if daemon predates v0.0.15.
        """
        params: dict[str, str] = {}
        if since > 0:
            params["since"] = str(since)
        if before > 0:
            params["before"] = str(before)
        if limit != 50:
            params["limit"] = str(limit)
        qs = f"?{urlencode(params)}" if params else ""
        body = self._get_json(f"/recent{qs}")
        if not isinstance(body, list):
            raise DaemonError(f"expected array, got {type(body).__name__}")
        return tuple(_parse_engram(row) for row in body)

    def insert_engram(
        self,
        surface: str,
        payload: str,
        ts: int = 0,
        meta: str = "",
    ) -> int:
        """POST /engrams — direct API-side engram insertion (v0.0.16+).

        surface and payload are required. ts defaults to time.Now().UnixNano()
        server-side when 0 or omitted. meta is optional free-form JSON string.
        Returns the newly assigned engram ID.

        Raises ValueError if surface or payload is empty.
        Raises DaemonError on transport failure or 4xx/5xx from daemon.
        """
        if not surface:
            raise ValueError("surface required")
        if not payload:
            raise ValueError("payload required")
        body: dict[str, object] = {"surface": surface, "payload": payload}
        if ts:
            body["ts"] = ts
        if meta:
            body["meta"] = meta
        result = self._post_json("/engrams", body)
        if not isinstance(result, dict) or "id" not in result:
            raise DaemonError(f"unexpected insert response: {result!r}")
        return int(result["id"])

    def insert_engrams_batch(
        self,
        items: Sequence[dict],
    ) -> int:
        """POST /engrams/batch — bulk API-side insertion in one transaction (v0.0.17+).

        items: list of dicts, each with keys surface (required), payload (required),
               ts (optional, unix nanoseconds, defaults server-now), meta (optional).
        Returns the number of engrams inserted (same as len(items) on success).
        Any validation failure rolls back the entire batch.

        Raises ValueError if items is empty or any item is missing surface/payload.
        Raises DaemonError on transport failure or 4xx/5xx from daemon.
        """
        if not items:
            raise ValueError("items must be non-empty")
        for i, item in enumerate(items):
            if not item.get("surface"):
                raise ValueError(f"items[{i}]: surface required")
            if not item.get("payload"):
                raise ValueError(f"items[{i}]: payload required")
        result = self._post_json("/engrams/batch", list(items))
        if not isinstance(result, dict) or "inserted" not in result:
            raise DaemonError(f"unexpected batch response: {result!r}")
        return int(result["inserted"])


    def count_engrams(
        self,
        surface: str = "",
        since: int = 0,
    ) -> int:
        """GET /engrams/count — return the count of engrams matching the given filters (v0.0.20+).

        surface: filter to a specific surface (empty = all surfaces).
        since: Unix nanoseconds; only count engrams with ts > since (0 = all time).
        Returns the integer count. Raises DaemonError on transport failure.
        """
        params: dict[str, str] = {}
        if surface:
            params["surface"] = surface
        if since > 0:
            params["since"] = str(since)
        qs = f"?{urlencode(params)}" if params else ""
        body = self._get_json(f"/engrams/count{qs}")
        if not isinstance(body, dict) or "count" not in body:
            raise DaemonError(f"unexpected count response: {body!r}")
        return int(body["count"])

    def delete_engram_by_id(self, id: int) -> bool:
        """DELETE /engrams/{id} — remove a single engram by primary key (v0.0.19+).

        Returns True on success (deleted=1). Raises DaemonError with status 404
        if no engram has that ID. Raises ValueError if id is not a positive integer.
        """
        if not isinstance(id, int) or id <= 0:
            raise ValueError(f"id must be a positive integer, got {id!r}")
        conn = self._conn()
        try:
            headers: dict[str, str] = {}
            if self._auth_token:
                headers["Authorization"] = f"Bearer {self._auth_token}"
            conn.request("DELETE", f"/engrams/{id}", headers=headers)
            resp = conn.getresponse()
            body = resp.read().decode("utf-8")
            if resp.status == 404:
                raise DaemonError(f"daemon returned 404: {body}")
            if resp.status != 200:
                raise DaemonError(f"daemon returned {resp.status}: {body}")
            result = json.loads(body)
            return int(result.get("deleted", 0)) == 1
        except (OSError, json.JSONDecodeError) as exc:
            raise DaemonError(f"daemon transport / parse error: {exc}") from exc
        finally:
            conn.close()

    def get_engram_by_id(self, id: int) -> Engram:
        """GET /engrams/{id} — fetch a single engram by primary key (v0.0.18+).

        Returns the matching Engram. Raises DaemonError with status 404 if no
        engram has that ID, or on any transport / parse failure.
        Raises ValueError if id is not a positive integer.
        """
        if not isinstance(id, int) or id <= 0:
            raise ValueError(f"id must be a positive integer, got {id!r}")
        body = self._get_json(f"/engrams/{id}")
        return _parse_engram(body)

    def digest(self, window: str = "7d") -> dict:
        """GET /digest — windowed activity recap (v0.0.47+).

        Returns the daemon's /digest JSON verbatim as a dict, with fields
        like window, since, total_engrams, by_surface, top_hours, top_terms,
        sample_engrams, and instructions (for the host LLM to render).

        window must be one of {"24h", "7d", "30d"}; validated client-side
        to avoid a round-trip on bad input. Raises ValueError on a bad
        window. Raises DaemonError on transport failure or daemons
        predating v0.0.47.
        """
        if window not in {"24h", "7d", "30d"}:
            raise ValueError(
                f"window must be one of '24h', '7d', '30d', got {window!r}"
            )
        body = self._get_json(f"/digest?{urlencode({'window': window})}")
        if not isinstance(body, dict):
            raise DaemonError(f"expected object, got {type(body).__name__}")
        return body

    def timeline(
        self,
        since: int = 0,
        before: int = 0,
        surfaces: Sequence[str] = (),
        limit: int = 200,
    ) -> dict:
        """GET /timeline — cross-surface chronological engram stream (v0.0.47+).

        Returns the daemon's /timeline JSON verbatim as a dict with keys
        ``engrams`` (list of engram dicts ordered by ts asc, interleaved
        across the requested surfaces), ``count`` (int), and ``surfaces``
        (the list of surfaces requested; may be empty when no filter was
        applied).

        Args:
            since: Unix nanoseconds lower bound (exclusive). 0 = no bound.
            before: Unix nanoseconds upper bound (exclusive). 0 = no bound.
            surfaces: surfaces to interleave (empty = all surfaces).
            limit: max engrams to return. Client-validated to 1..1000;
                daemon also caps at 1000.

        Raises ValueError when limit is outside 1..1000. Raises DaemonError
        on transport failure or daemons predating v0.0.47.
        """
        if not isinstance(limit, int) or limit < 1 or limit > 1000:
            raise ValueError(
                f"limit must be in 1..1000, got {limit!r}"
            )
        params: list[tuple[str, str]] = []
        if since > 0:
            params.append(("since", str(since)))
        if before > 0:
            params.append(("before", str(before)))
        if surfaces:
            # Daemon expects comma-separated surfaces in a single query param.
            joined = ",".join(s for s in surfaces if s)
            if joined:
                params.append(("surfaces", joined))
        params.append(("limit", str(limit)))
        body = self._get_json(f"/timeline?{urlencode(params)}")
        if not isinstance(body, dict):
            raise DaemonError(f"expected object, got {type(body).__name__}")
        return body


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
            snippet=str(row.get("snippet", "")),
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
