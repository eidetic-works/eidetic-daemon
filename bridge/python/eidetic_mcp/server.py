"""MCP stdio server exposing the eidetic-daemon as MCP tools.

Spec § 7 Open Q #5 fulfillment: thin Python wrapper over the daemon's
HTTP-over-UDS API. Speaks MCP stdio so any MCP client (Cursor, Claude Code,
Cline, etc.) can list + call tools.

Tools exposed:

  query_engrams(surface="", limit=50, since=0, before=0, asc=False)
    Returns a list of engrams from the daemon's local store. surface is
    optional (v0.0.23+); omit to retrieve across all surfaces. Use surface
    ∈ {claude_code, cowork, cursor, ...} to filter to one surface.
    `since`/`before` are unix epoch nanoseconds; 0 = no bound.

  daemon_status()
    Returns {'healthy': bool} via /healthz round-trip. Useful as a
    diagnostic before invoking query_engrams.

  daemon_metrics()
    Returns the daemon's /metrics JSON (v0.0.7+): version, uptime_seconds,
    engram_total, engram_by_surface, capture_skipped, db_path,
    db_size_bytes. Schema additive-only across versions. Daemons
    predating v0.0.7 return error 'metrics not configured'.

  list_surfaces()
    Returns a map of surface name → engram count for every surface the
    daemon has seen (v0.0.13+). Empty dict when store is empty.

  purge_engrams(surface, before=0)
    Delete engrams for a surface (v0.0.13+). `before` is unix epoch
    nanoseconds; 0 (default) removes all engrams for that surface.
    Returns {"deleted": N}. Irreversible — use with care.

  search_engrams(q, surface="", limit=50)
    Full-text search over engram payloads (v0.0.14+). `q` is an FTS5
    match expression — bare keywords, phrase queries in double quotes
    ("benchmark result"), OR/AND/NOT boolean operators. Results ordered
    by relevance rank. Optional `surface` filter narrows to one surface.

  recent_engrams(since=0, limit=50)
    Return newest engrams across all surfaces, ordered newest-first
    (v0.0.15+). `since` is unix epoch nanoseconds; 0 = no lower bound.
    Useful for a cross-surface activity snapshot without a keyword query.

  insert_engram(surface, payload, ts=0, meta="")
    Directly insert an engram into the daemon store (v0.0.16+). Bypasses
    the fsnotify capture path. surface + payload required; ts defaults to
    server-side now; meta is optional. Returns {"id": N}.

  insert_engrams_batch(items)
    Bulk insert a list of engram dicts in one atomic transaction (v0.0.17+).
    Each item needs surface + payload; ts + meta optional. Returns {"inserted": N}.

  get_engram_by_id(id)
    Fetch a single engram by its primary-key ID (v0.0.18+). Returns the full
    Engram JSON. Error when ID does not exist or is not a positive integer.

  count_engrams(surface="", since=0)
    Return the count of engrams matching optional surface and since filters
    (v0.0.20+). surface="" counts across all surfaces. since=0 counts all time.
    Useful for monitoring badges and health checks without fetching rows.

  delete_engram_by_id(id)
    Remove a single engram by its primary-key ID (v0.0.19+). Returns
    {"deleted": 1} on success. Error when ID does not exist or is not a
    positive integer. Irreversible — use with care.

  nucleus_digest(window="7d")
    Windowed activity recap (v0.0.6+). Calls daemon's /digest endpoint
    (v0.0.47+). window ∈ {"24h", "7d", "30d"}. Returns the daemon JSON
    verbatim with `instructions` field promoted to the top of the
    payload so the host LLM renders the recap correctly.

  nucleus_timeline(window="7d", surfaces=[], limit=200)
    Cross-surface chronological engram stream (v0.0.7+). Calls daemon's
    /timeline endpoint (v0.0.47+). window ∈ {"24h", "7d", "30d"} maps
    to a since= lower bound. Optional `surfaces` filter restricts to a
    set of surfaces; empty = all surfaces. Returns the daemon JSON
    (engrams interleaved by ts asc) plus an `instructions` field
    promoted to the top of the payload telling the host LLM to render
    the result as a brief activity narrative.

  nucleus_link(engram_id, window_minutes=30, limit=20)
    "What else was happening when I wrote this?" surface (v0.0.8+).
    Composes /engrams/{id} + /timeline?since=ts-window&before=ts+window
    to return the anchor engram plus adjacent engrams across all
    surfaces in the surrounding time window. Returns
    {anchor_engram, adjacent_engrams, window_minutes, instructions}
    with the anchor itself filtered out of adjacent_engrams (the host
    LLM gets it once via anchor_engram).

Run:

    eideticd &                          # daemon listens on UDS
    python -m eidetic_mcp.server        # MCP stdio server attached to client

Configure MCP client (e.g. Cursor's mcp.json / Claude Code's
~/.claude/mcp.json) with:

    {
      "eidetic": {
        "command": "python",
        "args": ["-m", "eidetic_mcp.server"],
        "env": {}
      }
    }

Set EIDETIC_TCP=1 to use TCP loopback (127.0.0.1:9876) instead of UDS.
Set EIDETIC_UDS_PATH to override the default UDS path.
"""

from __future__ import annotations

import json
import time
from dataclasses import asdict
from typing import Any

from .client import DaemonClient, DaemonError
from .reassemble import reassemble_chunks


# Stop-words filtered out of nucleus_ask questions before they hit FTS5.
# Aggressive but not exhaustive — FTS5 is forgiving on residual noise tokens.
_STOPWORDS = frozenset({
    "a", "an", "the", "is", "was", "were", "are", "be", "been", "being",
    "i", "me", "my", "we", "our", "you", "your", "they", "them", "their",
    "it", "its", "this", "that", "these", "those",
    "what", "when", "where", "why", "how", "who", "which",
    "do", "did", "does", "have", "had", "has", "having",
    "and", "or", "but", "if", "then", "else", "of", "to", "in", "on",
    "for", "with", "from", "by", "at", "as", "about", "into", "over",
    "out", "up", "down", "again", "anything", "something", "find", "tell",
    "show", "give", "ask", "any", "some", "all", "each", "every",
})


def _question_to_fts(question: str) -> str:
    """Turn a natural-language question into a permissive FTS5 query.

    Strategy: tokenize on non-word chars, drop stop-words and very short tokens,
    keep the survivors as a bare OR-joined keyword list. FTS5 ranks by relevance,
    so over-matching is OK — under-matching is the failure mode we avoid.
    """
    import re

    tokens = re.findall(r"\w+", question.lower())
    keywords = [t for t in tokens if len(t) >= 3 and t not in _STOPWORDS]
    if not keywords:
        # Stripping was too aggressive; fall back to the original question.
        return question
    # Bare keywords joined by space = implicit AND in FTS5 default config,
    # but most stores prefer OR semantics for recall over precision. We use
    # OR explicitly so a 5-word question doesn't require all 5 to land in
    # the same engram.
    return " OR ".join(keywords)


# Window strings → seconds; mirrors the daemon's /digest window vocabulary so
# nucleus_timeline can map a friendly window arg to a since= lower bound.
_WINDOW_SECONDS = {
    "24h": 24 * 3600,
    "7d": 7 * 24 * 3600,
    "30d": 30 * 24 * 3600,
}


def _window_to_since_ns(window: str) -> int:
    """Map {'24h','7d','30d'} → since-nanoseconds = now - window.

    Raises ValueError for any other window string. Used by nucleus_timeline
    so the host LLM can pass a friendly string instead of computing a
    nanosecond timestamp.
    """
    if window not in _WINDOW_SECONDS:
        raise ValueError(
            f"window must be one of '24h', '7d', '30d', got {window!r}"
        )
    return time.time_ns() - _WINDOW_SECONDS[window] * 1_000_000_000


# Lazy import of mcp SDK so client.py + tests can be exercised without it.
def _mcp_imports() -> tuple[Any, Any, Any, Any]:
    """Import mcp SDK lazily — raises ImportError with a clear install hint."""
    try:
        import mcp.server  # type: ignore
        import mcp.server.stdio  # type: ignore
        import mcp.types  # type: ignore
    except ImportError as exc:  # pragma: no cover
        raise ImportError(
            "mcp SDK not installed. Run: pip install 'mcp>=1.0' (see bridge/python/README.md)"
        ) from exc
    return mcp.server.Server, mcp.server.stdio.stdio_server, mcp.types.Tool, mcp.types.TextContent


def build_server(client: DaemonClient | None = None) -> Any:
    """Build the MCP server with two tools registered. Caller-supplied
    `client` lets tests inject a fake DaemonClient without spinning up a
    real daemon."""
    Server, _stdio_server, Tool, TextContent = _mcp_imports()

    if client is None:
        client = DaemonClient()
    daemon = client

    server = Server("eidetic-daemon-bridge")

    @server.list_tools()
    async def _list_tools() -> list:  # type: ignore[misc]
        return [
            Tool(
                name="query_engrams",
                description=(
                    "Query the local eidetic-daemon engram store for recent records. "
                    "Returns up to `limit` rows ordered by timestamp descending. "
                    "Surfaces are tool-specific tags like 'claude_code', 'cowork', "
                    "'cursor'. Omit `surface` to retrieve across all surfaces (v0.0.23+). "
                    "Use `since` (unix ns) to page. P95 retrieval latency on a "
                    "10K-row store is ~0.27 ms.\n\n"
                    "By default, chunked records (per ADR-018: lines >7 MiB split "
                    "into N chunks tagged with chunk_id/seq/total in meta) are "
                    "reassembled into single rows before return. Set `raw_chunks=true` "
                    "to disable reassembly + see chunks as separate engrams (useful "
                    "for debugging or surface-aware consumers that handle chunking)."
                ),
                inputSchema={
                    "type": "object",
                    "properties": {
                        "surface": {
                            "type": "string",
                            "description": "Surface tag, e.g. claude_code | cowork | cursor. Omit to retrieve across all surfaces (v0.0.23+).",
                        },
                        "limit": {
                            "type": "integer",
                            "description": "Max rows to return (daemon-side default 50, cap 500)",
                            "minimum": 1,
                            "maximum": 500,
                        },
                        "since": {
                            "type": "integer",
                            "description": "Unix nanoseconds lower bound (0 = no bound)",
                            "minimum": 0,
                        },
                        "before": {
                            "type": "integer",
                            "description": "Unix nanoseconds upper bound exclusive (0 = no bound, v0.0.21+)",
                            "minimum": 0,
                        },
                        "asc": {
                            "type": "boolean",
                            "description": "If true, return oldest-first (default false = newest-first, v0.0.22+)",
                        },
                        "raw_chunks": {
                            "type": "boolean",
                            "description": "If true, skip chunk-reassembly + return chunks as separate engrams (default false)",
                        },
                    },
                    "required": [],
                },
            ),
            Tool(
                name="daemon_status",
                description=(
                    "Check whether the eidetic-daemon is reachable and healthy. "
                    "Returns {'healthy': bool}. Use as a pre-flight before query_engrams."
                ),
                inputSchema={"type": "object", "properties": {}, "required": []},
            ),
            Tool(
                name="daemon_metrics",
                description=(
                    "Read live observability counters from the eidetic-daemon "
                    "(v0.0.7+). Returns the daemon's /metrics JSON: version, "
                    "uptime_seconds, engram_total, engram_by_surface, "
                    "capture_skipped, db_path, db_size_bytes. Schema is "
                    "additive-only across versions. Daemons predating v0.0.7 "
                    "return error 'metrics not configured'."
                ),
                inputSchema={"type": "object", "properties": {}, "required": []},
            ),
            Tool(
                name="list_surfaces",
                description=(
                    "List every surface the eidetic-daemon has seen, with its "
                    "engram count (v0.0.13+). Returns a JSON object mapping "
                    "surface name to count, e.g. {'claude_code': 1234, 'cursor': 567}. "
                    "Use as a discovery step before querying a specific surface."
                ),
                inputSchema={"type": "object", "properties": {}, "required": []},
            ),
            Tool(
                name="purge_engrams",
                description=(
                    "[DESTRUCTIVE — DO NOT INVOKE WITHOUT EXPLICIT USER CONSENT] "
                    "Permanently delete engrams from the daemon's store for a "
                    "given surface (v0.0.13+). Irreversible — no undo. "
                    "Returns {'deleted': N}.\n\n"
                    "With `before` (unix epoch nanoseconds): only removes engrams "
                    "older than that timestamp, leaving newer ones intact.\n"
                    "Without `before` (or before=0): removes ALL engrams for the "
                    "surface — wipes history.\n\n"
                    "Before calling: ALWAYS confirm with the user which surface + "
                    "timeframe they intend to purge. Prefer `list_surfaces` + "
                    "`count_engrams` first to scope the impact. The `confirm` "
                    "field is REQUIRED and must be true — server-side defense "
                    "against autonomous LLM invocation."
                ),
                inputSchema={
                    "type": "object",
                    "properties": {
                        "surface": {
                            "type": "string",
                            "description": "Surface tag to purge, e.g. claude_code | cursor",
                        },
                        "before": {
                            "type": "integer",
                            "description": "Unix epoch nanoseconds cutoff. Purges ts < before. 0 = purge all.",
                            "minimum": 0,
                        },
                        "confirm": {
                            "type": "boolean",
                            "description": "Must be true. Server rejects calls without confirm=true to prevent autonomous purges. The LLM should only set this true after explicit user consent.",
                        },
                    },
                    "required": ["surface", "confirm"],
                },
            ),
            Tool(
                name="search_engrams",
                description=(
                    "Full-text search over engram payloads (v0.0.14+). Results are "
                    "ordered by relevance rank (best match first). Each result includes "
                    "a `snippet` field — a ~200-char FTS5 context window around the "
                    "match (v0.0.28+) — so you can read the hit without parsing the full payload.\n\n"
                    "`q` is an FTS5 match expression:\n"
                    "  - bare keywords: benchmark latency\n"
                    '  - phrase query: "benchmark result"\n'
                    "  - boolean: benchmark AND NOT cursor\n\n"
                    "Optional `surface` filter restricts to one surface. Optional "
                    "`limit` (default 50, max 500)."
                ),
                inputSchema={
                    "type": "object",
                    "properties": {
                        "q": {
                            "type": "string",
                            "description": "FTS5 match expression. Bare keywords or quoted phrase.",
                        },
                        "surface": {
                            "type": "string",
                            "description": "Restrict to one surface, e.g. claude_code. Empty = search all.",
                        },
                        "limit": {
                            "type": "integer",
                            "description": "Max results (default 50, max 500).",
                            "minimum": 1,
                            "maximum": 500,
                        },
                    },
                    "required": ["q"],
                },
            ),
            Tool(
                name="recent_engrams",
                description=(
                    "Return the most recent engrams across all surfaces, newest first "
                    "(v0.0.15+). Useful for getting a quick snapshot of recent activity "
                    "without a surface filter or keyword query.\n\n"
                    "Optional `since`: Unix nanoseconds; only return engrams with "
                    "ts > since (0 or omit = all). Optional `before` (v0.0.21+): Unix "
                    "nanoseconds upper bound exclusive. Optional `limit` (default 50, max 500)."
                ),
                inputSchema={
                    "type": "object",
                    "properties": {
                        "since": {
                            "type": "integer",
                            "description": "Unix nanoseconds lower bound (exclusive). 0 = no filter.",
                        },
                        "before": {
                            "type": "integer",
                            "description": "Unix nanoseconds upper bound (exclusive). 0 = no filter. (v0.0.21+)",
                        },
                        "limit": {
                            "type": "integer",
                            "description": "Max results (default 50, max 500).",
                            "minimum": 1,
                            "maximum": 500,
                        },
                    },
                    "required": [],
                },
            ),
            Tool(
                name="insert_engram",
                description=(
                    "Directly insert an engram into the daemon store (v0.0.16+). "
                    "Bypasses the fsnotify capture path — use this to inject engrams "
                    "from sources the daemon doesn't watch (mobile, webhooks, API calls, "
                    "manual annotations).\n\n"
                    "`surface` and `payload` are required. `ts` defaults to now "
                    "(Unix nanoseconds). `meta` is optional free-form JSON string.\n\n"
                    "Returns the assigned engram ID. The engram is immediately "
                    "searchable via search_engrams and retrievable via query_engrams."
                ),
                inputSchema={
                    "type": "object",
                    "properties": {
                        "surface": {
                            "type": "string",
                            "description": "Surface tag, e.g. claude_code, cursor, mobile.",
                        },
                        "payload": {
                            "type": "string",
                            "description": "The engram content — text, JSON, markdown, etc.",
                        },
                        "ts": {
                            "type": "integer",
                            "description": "Timestamp in Unix nanoseconds. 0 or omit = server now.",
                        },
                        "meta": {
                            "type": "string",
                            "description": "Optional free-form JSON metadata string.",
                        },
                    },
                    "required": ["surface", "payload"],
                },
            ),
            Tool(
                name="insert_engrams_batch",
                description=(
                    "Bulk insert a list of engrams in one atomic transaction (v0.0.17+). "
                    "All items succeed or none do. Use when you have multiple engrams to "
                    "inject at once — relay sync, bulk import, session replay.\n\n"
                    "Each item requires `surface` and `payload`; `ts` and `meta` are "
                    "optional. Returns the count of inserted engrams."
                ),
                inputSchema={
                    "type": "object",
                    "properties": {
                        "items": {
                            "type": "array",
                            "description": "Array of engram objects.",
                            "items": {
                                "type": "object",
                                "properties": {
                                    "surface": {"type": "string"},
                                    "payload": {"type": "string"},
                                    "ts": {"type": "integer"},
                                    "meta": {"type": "string"},
                                },
                                "required": ["surface", "payload"],
                            },
                        },
                    },
                    "required": ["items"],
                },
            ),
            Tool(
                name="get_engram_by_id",
                description=(
                    "Fetch a single engram by its primary-key ID (v0.0.18+). "
                    "Returns the full Engram JSON on success. "
                    "Returns an error string when the ID does not exist or is invalid."
                ),
                inputSchema={
                    "type": "object",
                    "properties": {
                        "id": {
                            "type": "integer",
                            "description": "Positive integer primary key of the engram.",
                        },
                    },
                    "required": ["id"],
                },
            ),
            Tool(
                name="count_engrams",
                description=(
                    "Return the count of engrams matching optional filters (v0.0.20+). "
                    "surface (optional): count only engrams on that surface; omit to count all. "
                    "since (optional): Unix epoch nanoseconds — count only engrams with ts > since. "
                    "Returns {\"count\": N}. "
                    "Use for monitoring badges or health checks without fetching rows."
                ),
                inputSchema={
                    "type": "object",
                    "properties": {
                        "surface": {
                            "type": "string",
                            "description": "Surface name to filter on; omit to count across all surfaces.",
                        },
                        "since": {
                            "type": "integer",
                            "description": "Unix epoch nanoseconds; count only engrams with ts > since. 0 = all time.",
                        },
                    },
                },
            ),
            Tool(
                name="delete_engram_by_id",
                description=(
                    "Remove a single engram by its primary-key ID (v0.0.19+). "
                    "Returns {\"deleted\": 1} on success. "
                    "Returns an error string when the ID does not exist or is invalid. "
                    "Irreversible — use with care."
                ),
                inputSchema={
                    "type": "object",
                    "properties": {
                        "id": {
                            "type": "integer",
                            "description": "Positive integer primary key of the engram to delete.",
                        },
                    },
                    "required": ["id"],
                },
            ),
            Tool(
                name="nucleus_ask",
                description=(
                    "Ask a natural-language question about your engrams (v0.0.5+). "
                    "This is RAG over your local engram store: the tool extracts "
                    "keywords from your question, retrieves the top-K most-relevant "
                    "engrams via FTS5, and returns them wrapped in answer-scaffolding "
                    "for the host LLM to read.\n\n"
                    "Example questions:\n"
                    "  - 'What was that Postgres tuning trick I learned last week?'\n"
                    "  - 'Find anything I wrote about React Suspense boundaries'\n"
                    "  - 'What did I decide about the auth middleware?'\n\n"
                    "The host LLM (Claude Code / Cursor / etc.) reads the returned "
                    "engrams and synthesizes the answer. The tool does NOT call any "
                    "external LLM — your engrams never leave the local daemon."
                ),
                inputSchema={
                    "type": "object",
                    "properties": {
                        "question": {
                            "type": "string",
                            "description": "Natural-language question to recall from your engrams.",
                        },
                        "surface": {
                            "type": "string",
                            "description": "Restrict to one surface (claude_code | cursor | cowork). Empty = all.",
                        },
                        "limit": {
                            "type": "integer",
                            "description": "Max engrams to retrieve (default 10, max 30).",
                            "minimum": 1,
                            "maximum": 30,
                        },
                    },
                    "required": ["question"],
                },
            ),
            Tool(
                name="nucleus_digest",
                description=(
                    "Render a windowed activity recap from your engram store (v0.0.6+). "
                    "Calls the daemon's /digest endpoint (v0.0.47+) and returns the JSON "
                    "verbatim: window, since, total_engrams, by_surface, top_hours, "
                    "top_terms, sample_engrams, and `instructions` for the host LLM to "
                    "compose the recap.\n\n"
                    "Use this for weekly/monthly/daily reviews. The `instructions` field "
                    "tells the host LLM exactly how to present the digest — read it "
                    "before generating the user-facing summary.\n\n"
                    "`window` must be one of '24h', '7d' (default), or '30d'. The tool "
                    "does NOT call any external LLM — your engrams never leave the local daemon."
                ),
                inputSchema={
                    "type": "object",
                    "properties": {
                        "window": {
                            "type": "string",
                            "description": "Time window for the digest. One of '24h', '7d', '30d'. Default '7d'.",
                            "enum": ["24h", "7d", "30d"],
                        },
                    },
                    "required": [],
                },
            ),
            Tool(
                name="nucleus_timeline",
                description=(
                    "Cross-surface chronological engram stream (v0.0.7+). Calls the "
                    "daemon's /timeline endpoint (v0.0.47+) and returns the JSON: "
                    "engrams (interleaved by ts ascending across the requested "
                    "surfaces), count, surfaces, plus an `instructions` field "
                    "(promoted to the top of the payload) telling the host LLM to "
                    "render the result as a brief activity narrative.\n\n"
                    "Use this to answer 'what was I doing on a given day across "
                    "every tool at once?' — pairs naturally with `nucleus_digest` "
                    "for a quick stats-then-narrative recap.\n\n"
                    "`window` ∈ {'24h', '7d' (default), '30d'} sets a `since=` "
                    "lower bound. Optional `surfaces` filter restricts to a list "
                    "of surfaces (e.g. ['claude_code', 'cursor']); empty = all "
                    "surfaces. `limit` defaults to 200, capped at 1000. The tool "
                    "does NOT call any external LLM — your engrams never leave the local daemon."
                ),
                inputSchema={
                    "type": "object",
                    "properties": {
                        "window": {
                            "type": "string",
                            "description": "Time window for the timeline. One of '24h', '7d', '30d'. Default '7d'.",
                            "enum": ["24h", "7d", "30d"],
                        },
                        "surfaces": {
                            "type": "array",
                            "description": "Optional list of surfaces to interleave. Empty = all surfaces.",
                            "items": {"type": "string"},
                        },
                        "limit": {
                            "type": "integer",
                            "description": "Max engrams to return (default 200, max 1000).",
                            "minimum": 1,
                            "maximum": 1000,
                        },
                    },
                    "required": [],
                },
            ),
            Tool(
                name="nucleus_link",
                description=(
                    "Given an anchor engram, return adjacent engrams across all "
                    "surfaces in the surrounding time window (v0.0.8+). Composes "
                    "GET /engrams/{id} + GET /timeline?since=<ts-window>&before="
                    "<ts+window>&limit=<limit> with no daemon-side changes.\n\n"
                    "Use this to answer 'what else was happening when I wrote "
                    "this?' — pull cross-surface context around a specific "
                    "moment without manually computing the time window. Pairs "
                    "naturally with `nucleus_ask` (find the anchor) then "
                    "`nucleus_link` (expand the context).\n\n"
                    "`engram_id` is required (positive integer). `window_minutes` "
                    "defaults to 30 = ±30 min around the anchor (range 1..1440 "
                    "= up to ±24h). `limit` defaults to 20 (cap 1000). Returns "
                    "{anchor_engram, adjacent_engrams, window_minutes, "
                    "instructions} with the anchor itself filtered out of "
                    "adjacent_engrams. The tool does NOT call any external LLM "
                    "— your engrams never leave the local daemon."
                ),
                inputSchema={
                    "type": "object",
                    "properties": {
                        "engram_id": {
                            "type": "integer",
                            "description": "Positive integer primary key of the anchor engram.",
                            "minimum": 1,
                        },
                        "window_minutes": {
                            "type": "integer",
                            "description": "Half-width of the time window in minutes (1..1440). Default 30.",
                            "minimum": 1,
                            "maximum": 1440,
                        },
                        "limit": {
                            "type": "integer",
                            "description": "Max adjacent engrams to return (default 20, max 1000).",
                            "minimum": 1,
                            "maximum": 1000,
                        },
                    },
                    "required": ["engram_id"],
                },
            ),
            Tool(
                name="nucleus_curate",
                description=(
                    "Mark an engram as 'canonical' (most authoritative), 'demote' "
                    "(downweight in future recall), or 'archive' (hide from default "
                    "views). Non-destructive: creates a curation overlay engram "
                    "pointing at the target; original is never modified. Use this "
                    "after the user explicitly says 'this engram is the right "
                    "answer' or 'ignore that noisy one'. Note: overlay-aware "
                    "recall is rolling out — see release notes for which tools "
                    "currently consume curation overlays."
                ),
                inputSchema={
                    "type": "object",
                    "properties": {
                        "engram_id": {
                            "type": "integer",
                            "description": "Positive integer primary key of the engram being curated.",
                            "minimum": 1,
                        },
                        "action": {
                            "type": "string",
                            "description": "What to do with the target engram. 'canonical' = surface-first in recall; 'demote' = downweight; 'archive' = exclude from default recall.",
                            "enum": ["canonical", "demote", "archive"],
                        },
                        "note": {
                            "type": "string",
                            "description": "Optional human-readable rationale (<=2000 chars). Stored in the curation engram's meta.note.",
                            "maxLength": 2000,
                        },
                    },
                    "required": ["engram_id", "action"],
                },
            ),
        ]

    @server.call_tool()
    async def _call_tool(name: str, arguments: dict) -> list:  # type: ignore[misc]
        if name == "query_engrams":
            surface = str(arguments.get("surface", "")).strip()
            limit = int(arguments.get("limit", 50))
            since = int(arguments.get("since", 0))
            before = int(arguments.get("before", 0))
            asc = bool(arguments.get("asc", False))
            raw_chunks = bool(arguments.get("raw_chunks", False))
            try:
                rows = daemon.query_engrams(surface=surface, limit=limit, since=since, before=before, asc=asc)
            except (DaemonError, ValueError) as exc:
                return [TextContent(type="text", text=f"error: {exc}")]
            if not raw_chunks:
                rows = reassemble_chunks(rows)
            payload = [asdict(r) for r in rows]
            return [TextContent(type="text", text=json.dumps(payload, indent=2))]

        if name == "daemon_status":
            return [TextContent(type="text", text=json.dumps({"healthy": daemon.healthy()}))]

        if name == "daemon_metrics":
            try:
                m = daemon.metrics()
            except DaemonError as exc:
                return [TextContent(type="text", text=f"error: {exc}")]
            return [TextContent(type="text", text=json.dumps(m, indent=2))]

        if name == "list_surfaces":
            try:
                counts = daemon.surfaces()
            except DaemonError as exc:
                return [TextContent(type="text", text=f"error: {exc}")]
            return [TextContent(type="text", text=json.dumps(counts, indent=2))]

        if name == "purge_engrams":
            surface = str(arguments.get("surface", "")).strip()
            if not surface:
                return [TextContent(type="text", text="error: surface required")]
            if not arguments.get("confirm"):
                return [TextContent(type="text", text="error: purge_engrams requires confirm=true to prevent autonomous destruction")]
            before = int(arguments.get("before", 0))
            try:
                deleted = daemon.purge_engrams(surface=surface, before=before)
            except (DaemonError, ValueError) as exc:
                return [TextContent(type="text", text=f"error: {exc}")]
            return [TextContent(type="text", text=json.dumps({"deleted": deleted}))]

        if name == "search_engrams":
            q = str(arguments.get("q", "")).strip()
            if not q:
                return [TextContent(type="text", text="error: q required")]
            surface = str(arguments.get("surface", "")).strip()
            limit = int(arguments.get("limit", 50))
            try:
                rows = daemon.search_engrams(q=q, surface=surface, limit=limit)
            except (DaemonError, ValueError) as exc:
                return [TextContent(type="text", text=f"error: {exc}")]
            payload = [asdict(r) for r in rows]
            return [TextContent(type="text", text=json.dumps(payload, indent=2))]

        if name == "recent_engrams":
            since = int(arguments.get("since", 0))
            before = int(arguments.get("before", 0))
            limit = int(arguments.get("limit", 50))
            try:
                rows = daemon.recent_engrams(since=since, before=before, limit=limit)
            except DaemonError as exc:
                return [TextContent(type="text", text=f"error: {exc}")]
            payload = [asdict(r) for r in rows]
            return [TextContent(type="text", text=json.dumps(payload, indent=2))]

        if name == "insert_engram":
            surface = str(arguments.get("surface", "")).strip()
            payload_text = str(arguments.get("payload", ""))
            if not surface:
                return [TextContent(type="text", text="error: surface required")]
            if not payload_text.strip():
                return [TextContent(type="text", text="error: payload required")]
            ts = int(arguments.get("ts", 0))
            meta = str(arguments.get("meta", ""))
            try:
                engram_id = daemon.insert_engram(
                    surface=surface, payload=payload_text, ts=ts, meta=meta
                )
            except (DaemonError, ValueError) as exc:
                return [TextContent(type="text", text=f"error: {exc}")]
            return [TextContent(type="text", text=json.dumps({"id": engram_id}))]

        if name == "insert_engrams_batch":
            items = arguments.get("items", [])
            if not isinstance(items, list) or not items:
                return [TextContent(type="text", text="error: items must be non-empty array")]
            try:
                n = daemon.insert_engrams_batch(items)
            except (DaemonError, ValueError) as exc:
                return [TextContent(type="text", text=f"error: {exc}")]
            return [TextContent(type="text", text=json.dumps({"inserted": n}))]

        if name == "get_engram_by_id":
            raw_id = arguments.get("id")
            try:
                engram_id = int(raw_id)
            except (TypeError, ValueError):
                return [TextContent(type="text", text=f"error: id must be a positive integer")]
            try:
                e = daemon.get_engram_by_id(engram_id)
            except (DaemonError, ValueError) as exc:
                return [TextContent(type="text", text=f"error: {exc}")]
            return [TextContent(type="text", text=json.dumps(asdict(e), indent=2))]

        if name == "count_engrams":
            surface = str(arguments.get("surface", "")).strip()
            since = int(arguments.get("since", 0))
            try:
                n = daemon.count_engrams(surface=surface, since=since)
            except DaemonError as exc:
                return [TextContent(type="text", text=f"error: {exc}")]
            return [TextContent(type="text", text=json.dumps({"count": n}))]

        if name == "delete_engram_by_id":
            raw_id = arguments.get("id")
            try:
                engram_id = int(raw_id)
            except (TypeError, ValueError):
                return [TextContent(type="text", text=f"error: id must be a positive integer")]
            try:
                ok = daemon.delete_engram_by_id(engram_id)
            except (DaemonError, ValueError) as exc:
                return [TextContent(type="text", text=f"error: {exc}")]
            return [TextContent(type="text", text=json.dumps({"deleted": 1 if ok else 0}))]

        if name == "nucleus_ask":
            question = str(arguments.get("question", "")).strip()
            if not question:
                return [TextContent(type="text", text="error: question required")]
            surface = str(arguments.get("surface", "")).strip()
            limit = int(arguments.get("limit", 10))
            limit = max(1, min(limit, 30))

            # FTS5 is permissive on natural-language input — it tokenizes and ranks.
            # For tighter retrieval we strip stop-words and quote multi-word phrases.
            fts_query = _question_to_fts(question)

            try:
                rows = daemon.search_engrams(q=fts_query, surface=surface, limit=limit)
            except (DaemonError, ValueError) as exc:
                # Fall back to bare keyword search if the FTS expr is malformed.
                try:
                    rows = daemon.search_engrams(q=question, surface=surface, limit=limit)
                except (DaemonError, ValueError) as inner:
                    return [TextContent(type="text", text=f"error: {inner}")]

            if not rows:
                payload = {
                    "question": question,
                    "fts_query": fts_query,
                    "instructions": (
                        "No engrams matched. Tell the user no relevant engrams were found; "
                        "do NOT fabricate an answer. Suggest broader keywords or check "
                        "if the surface filter is too restrictive."
                    ),
                    "engrams": [],
                }
                return [TextContent(type="text", text=json.dumps(payload, indent=2))]

            payload = {
                "question": question,
                "fts_query": fts_query,
                "instructions": (
                    f"You are answering the question above using ONLY the {len(rows)} engram "
                    f"excerpts below. Each engram is a snapshot from the user's past work "
                    f"(coding sessions, notes, conversations). Cite the surface + timestamp "
                    f"when you reference one. If the engrams don't answer the question, say "
                    f"so honestly — do NOT fabricate. Prefer recent engrams when relevance ties."
                ),
                "engrams": [asdict(r) for r in rows],
            }
            return [TextContent(type="text", text=json.dumps(payload, indent=2))]

        if name == "nucleus_digest":
            window = str(arguments.get("window", "7d")).strip() or "7d"
            try:
                body = daemon.digest(window=window)
            except (DaemonError, ValueError) as exc:
                return [TextContent(type="text", text=f"error: {exc}")]
            # Return the daemon's JSON verbatim — the `instructions` field is
            # already authored daemon-side and tells the host LLM how to render
            # the recap. We preserve key ordering so `instructions` (when present)
            # stays prominent at the top of the dict by reinjecting it first.
            if isinstance(body, dict) and "instructions" in body:
                ordered = {"instructions": body["instructions"]}
                for k, v in body.items():
                    if k != "instructions":
                        ordered[k] = v
                body = ordered
            return [TextContent(type="text", text=json.dumps(body, indent=2))]

        if name == "nucleus_timeline":
            window = str(arguments.get("window", "7d")).strip() or "7d"
            raw_surfaces = arguments.get("surfaces", []) or []
            if not isinstance(raw_surfaces, list):
                return [TextContent(type="text", text="error: surfaces must be a list of strings")]
            surfaces = [str(s).strip() for s in raw_surfaces if str(s).strip()]
            limit = int(arguments.get("limit", 200))
            try:
                since = _window_to_since_ns(window)
            except ValueError as exc:
                return [TextContent(type="text", text=f"error: {exc}")]
            try:
                body = daemon.timeline(since=since, surfaces=surfaces, limit=limit)
            except (DaemonError, ValueError) as exc:
                return [TextContent(type="text", text=f"error: {exc}")]
            # Promote `instructions` to the top of the payload so the host LLM
            # reads it before parsing the engram stream. Daemon's /timeline
            # does not author one daemon-side; we inject a static instruction
            # tailored to the cross-tool narrative use case.
            instructions = (
                "These are cross-tool engrams in chronological order. "
                "Render as a brief activity narrative."
            )
            if isinstance(body, dict):
                ordered: dict[str, Any] = {"instructions": instructions}
                for k, v in body.items():
                    if k != "instructions":
                        ordered[k] = v
                body = ordered
            return [TextContent(type="text", text=json.dumps(body, indent=2))]

        if name == "nucleus_link":
            raw_id = arguments.get("engram_id")
            try:
                engram_id = int(raw_id)
            except (TypeError, ValueError):
                return [TextContent(type="text", text="error: engram_id must be a positive integer")]
            try:
                window_minutes = int(arguments.get("window_minutes", 30))
            except (TypeError, ValueError):
                return [TextContent(type="text", text="error: window_minutes must be an integer")]
            try:
                limit = int(arguments.get("limit", 20))
            except (TypeError, ValueError):
                return [TextContent(type="text", text="error: limit must be an integer")]
            try:
                body = daemon.link(
                    engram_id=engram_id,
                    window_minutes=window_minutes,
                    limit=limit,
                )
            except (DaemonError, ValueError) as exc:
                return [TextContent(type="text", text=f"error: {exc}")]
            # body is already shaped with `instructions` first — no reorder
            # needed; client.link() builds the dict in the canonical order.
            return [TextContent(type="text", text=json.dumps(body, indent=2))]

        if name == "nucleus_curate":
            raw_id = arguments.get("engram_id")
            try:
                engram_id = int(raw_id)
            except (TypeError, ValueError):
                return [TextContent(type="text", text="error: engram_id must be a positive integer")]
            action = str(arguments.get("action", "")).strip()
            note = str(arguments.get("note", "")).strip()
            try:
                body = daemon.curate(engram_id=engram_id, action=action, note=note)
            except (DaemonError, ValueError) as exc:
                return [TextContent(type="text", text=f"error: {exc}")]
            return [TextContent(type="text", text=json.dumps(body, indent=2))]

        return [TextContent(type="text", text=f"error: unknown tool {name!r}")]

    return server


async def main() -> None:  # pragma: no cover
    """Entry point for `python -m eidetic_mcp.server`."""
    Server, stdio_server, _Tool, _TextContent = _mcp_imports()
    server = build_server()
    async with stdio_server() as (read, write):
        # InitializationOptions vary across mcp SDK versions; keep minimal.
        await server.run(read, write, server.create_initialization_options())


if __name__ == "__main__":  # pragma: no cover
    import asyncio

    asyncio.run(main())
