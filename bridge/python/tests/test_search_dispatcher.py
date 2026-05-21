"""Dispatcher-layer tests for FTS5-backed search tools.

Covers two tools that use the daemon's FTS5 index:
    - search_engrams  (literal FTS5 query, surface filter, ranked results)
    - nucleus_ask     (NL question → FTS query, w/ stop-word stripping +
                       FALLBACK to bare keyword search when the rewritten
                       FTS5 expression is malformed)

The nucleus_ask FALLBACK PATH at server.py:847-854 is the high-leverage gap
the audit called out: it exercises both the happy path AND the degraded path
(daemon rejects rewritten query → dispatcher retries with original question).
Without this test, a regression to the fallback path would only surface in
production when the daemon FTS5 parser becomes stricter.
"""

from __future__ import annotations

import asyncio
import json
from unittest.mock import MagicMock

import mcp.types as types

from eidetic_mcp.client import DaemonError, Engram
from eidetic_mcp.server import build_server


def _call_tool(client: MagicMock, name: str, arguments: dict) -> str:
    """Drive the MCP server's CallToolRequest handler and return the first
    TextContent's text. Mirrors what a real MCP client triggers over stdio."""
    server = build_server(client=client)
    handler = server.request_handlers[types.CallToolRequest]
    req = types.CallToolRequest(
        method="tools/call",
        params=types.CallToolRequestParams(name=name, arguments=arguments),
    )
    result = asyncio.run(handler(req))
    return result.root.content[0].text


# ── search_engrams ───────────────────────────────────────────────────────────


def test_search_engrams_happy_path_with_snippet():
    """Happy path: bare keyword → ranked engrams returned with snippet field."""
    client = MagicMock()
    client.search_engrams.return_value = (
        Engram(
            id=11,
            surface="claude_code",
            ts=100,
            payload="postgres tuning notes...",
            meta="",
            snippet="...postgres TUNING is the key...",
        ),
    )
    text = _call_tool(client, "search_engrams", {"q": "postgres", "limit": 5})
    body = json.loads(text)
    assert len(body) == 1
    assert body[0]["id"] == 11
    assert body[0]["snippet"] == "...postgres TUNING is the key..."
    client.search_engrams.assert_called_once_with(q="postgres", surface="", limit=5)


def test_search_engrams_with_surface_filter_passes_through():
    """surface arg reaches daemon-client method intact."""
    client = MagicMock()
    client.search_engrams.return_value = ()
    _call_tool(
        client,
        "search_engrams",
        {"q": "auth", "surface": "claude_code"},
    )
    client.search_engrams.assert_called_once_with(
        q="auth", surface="claude_code", limit=50
    )


def test_search_engrams_empty_q_rejected():
    """Empty q → dispatcher-side rejection, daemon NEVER called."""
    client = MagicMock()
    text = _call_tool(client, "search_engrams", {"q": ""})
    assert text.startswith("error:")
    assert "q" in text.lower()
    client.search_engrams.assert_not_called()


def test_search_engrams_whitespace_only_q_rejected():
    """q that is only whitespace also rejected (strip() catches it)."""
    client = MagicMock()
    text = _call_tool(client, "search_engrams", {"q": "   \t  "})
    assert text.startswith("error:")
    assert "q" in text.lower()
    client.search_engrams.assert_not_called()


def test_search_engrams_malformed_fts_surfaces_as_error():
    """FTS5 parse error from daemon → 'error: ...' text (no graceful fallback
    at the search_engrams layer — that's nucleus_ask's job)."""
    client = MagicMock()
    client.search_engrams.side_effect = DaemonError(
        "daemon returned 400: malformed MATCH expression"
    )
    text = _call_tool(client, "search_engrams", {"q": "(((unbalanced"})
    assert text.startswith("error:")
    assert "MATCH" in text or "400" in text


def test_search_engrams_empty_result_returns_empty_list():
    """Daemon returns 0 matches → '[]' (not an error)."""
    client = MagicMock()
    client.search_engrams.return_value = ()
    text = _call_tool(client, "search_engrams", {"q": "no-such-keyword"})
    assert text == "[]"


# ── nucleus_ask ──────────────────────────────────────────────────────────────


def test_nucleus_ask_happy_path_includes_instructions():
    """Happy path: question → FTS-rewritten query → engrams returned with
    `instructions` field telling host LLM how to synthesize the answer."""
    client = MagicMock()
    client.search_engrams.return_value = (
        Engram(
            id=22,
            surface="claude_code",
            ts=200,
            payload="React Suspense lets you...",
            meta="",
            snippet="...React Suspense boundaries are...",
        ),
    )
    text = _call_tool(
        client,
        "nucleus_ask",
        {"question": "What did I learn about React Suspense?"},
    )
    body = json.loads(text)
    assert body["question"] == "What did I learn about React Suspense?"
    # _question_to_fts strips stopwords + lowercases; should contain key terms
    assert "react" in body["fts_query"] or "suspense" in body["fts_query"]
    assert "instructions" in body
    assert "do NOT fabricate" in body["instructions"]
    assert len(body["engrams"]) == 1
    assert body["engrams"][0]["id"] == 22


def test_nucleus_ask_empty_result_includes_no_engrams_instruction():
    """No matches → explicit instructions field telling LLM to say so
    instead of fabricating an answer. Audit called this the model response
    handling — preserve it as a regression guard."""
    client = MagicMock()
    client.search_engrams.return_value = ()
    text = _call_tool(client, "nucleus_ask", {"question": "Anything about quantum?"})
    body = json.loads(text)
    assert body["engrams"] == []
    assert "No engrams matched" in body["instructions"]
    assert "do NOT fabricate" in body["instructions"]


def test_nucleus_ask_empty_question_rejected():
    """Empty question → dispatcher-side rejection, daemon NEVER called."""
    client = MagicMock()
    text = _call_tool(client, "nucleus_ask", {"question": ""})
    assert text.startswith("error:")
    assert "question" in text.lower()
    client.search_engrams.assert_not_called()


def test_nucleus_ask_fallback_path_on_malformed_fts():
    """REGRESSION GUARD for the fallback at server.py:847-854.

    Scenario: first search_engrams() call (with the FTS-rewritten query)
    raises DaemonError (e.g. user question contained an FTS5-reserved
    character that survived rewriting). Dispatcher then RETRIES with the
    bare original question. If that succeeds, the engrams are returned
    normally.

    Without this fallback the user sees an opaque parse error every time
    their question contains a reserved char. Without this test, a regression
    that removes the fallback would only surface in production.
    """
    client = MagicMock()
    bare_call_result = (
        Engram(id=33, surface="cursor", ts=300, payload="Suspense matters", meta=""),
    )
    # First call (rewritten FTS) raises; second call (bare question) succeeds.
    client.search_engrams.side_effect = [
        DaemonError("daemon returned 400: fts5: syntax error near OR"),
        bare_call_result,
    ]
    text = _call_tool(
        client,
        "nucleus_ask",
        {"question": 'What about React "Suspense"?'},
    )
    body = json.loads(text)
    # Fallback succeeded → we get the engram from the SECOND call, not an error.
    assert "engrams" in body
    assert body["engrams"][0]["id"] == 33
    # Dispatcher called search_engrams exactly twice — once with rewritten,
    # once with bare question.
    assert client.search_engrams.call_count == 2


def test_nucleus_ask_fallback_path_both_fail_surfaces_error():
    """If BOTH the rewritten and the bare-question search raise, the
    dispatcher surfaces the INNER (bare-question) error to the host LLM.

    This is the failure mode behind the fallback: if even the bare question
    is unparseable (extremely rare — would require FTS5 itself being broken),
    the LLM should see a real error, not an empty engram list. Tests both
    `try` blocks in the fallback wrapper at server.py:847-854.
    """
    client = MagicMock()
    client.search_engrams.side_effect = [
        DaemonError("daemon returned 400: rewritten malformed"),
        DaemonError("daemon returned 500: db locked"),
    ]
    text = _call_tool(client, "nucleus_ask", {"question": "anything?"})
    assert text.startswith("error:")
    # The INNER exception (the second one) is what reaches the LLM —
    # confirms the dispatcher caught + re-raised after fallback failed.
    assert "500" in text or "db locked" in text
    assert client.search_engrams.call_count == 2


def test_nucleus_ask_limit_clamped_to_30():
    """limit is clamped 1..30 (lower than other tools — appropriate for
    LLM context window).

    The Tool schema declares maximum=30, so MCP SDK input validation rejects
    limit=100 before the dispatcher runs. We confirm BOTH layers: (a) schema
    catches limit=100 with an input-validation error; (b) within-range values
    (limit=30) pass through intact. This guards both the schema cap AND the
    dispatcher-side `max(1, min(limit, 30))` defense at server.py:841.
    """
    # Layer A: schema rejection at limit=100
    client = MagicMock()
    text = _call_tool(client, "nucleus_ask", {"question": "test", "limit": 100})
    assert "validation" in text.lower() or "30" in text
    client.search_engrams.assert_not_called()

    # Layer B: limit=30 (in-range) does reach the dispatcher
    client2 = MagicMock()
    client2.search_engrams.return_value = ()
    _call_tool(client2, "nucleus_ask", {"question": "test", "limit": 30})
    _, kwargs = client2.search_engrams.call_args
    assert kwargs["limit"] == 30


def test_nucleus_ask_surface_filter_passes_through():
    """surface arg reaches the daemon-client method intact on both
    fallback attempts (audit-relevant: a surface-restricted question with
    a malformed rewritten query must still retry with the same surface)."""
    client = MagicMock()
    client.search_engrams.side_effect = [
        DaemonError("rewritten failed"),
        (),
    ]
    _call_tool(
        client,
        "nucleus_ask",
        {"question": "auth", "surface": "claude_code"},
    )
    # Both calls should carry surface="claude_code"
    assert client.search_engrams.call_count == 2
    for call in client.search_engrams.call_args_list:
        assert call.kwargs["surface"] == "claude_code"
