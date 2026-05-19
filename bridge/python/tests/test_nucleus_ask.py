"""Tests for nucleus_ask question→FTS rewriting (pure function, no daemon).

End-to-end MCP test (Tool dispatched, daemon returns matches) lives in
scripts/demo-smoke.sh — requires a live daemon and mcp SDK installed.
"""

from __future__ import annotations

from eidetic_mcp.server import _question_to_fts


def test_strips_stopwords():
    # "what", "was", "that", "I" all in stop-word list
    assert _question_to_fts("What was that Postgres trick I learned?") == \
        "postgres OR trick OR learned"


def test_keeps_keyword_order():
    assert _question_to_fts("Find anything about React Suspense boundaries") == \
        "react OR suspense OR boundaries"


def test_or_joined_for_recall():
    # OR rather than AND so a 5-keyword question doesn't require all 5 in one engram
    q = _question_to_fts("decide auth middleware refactor scope")
    assert " OR " in q
    assert "AND" not in q


def test_short_tokens_dropped():
    # "is", "of", "an" too short / stopword
    assert _question_to_fts("Is React an alternative") == "react OR alternative"


def test_empty_after_strip_falls_back():
    # All stop-words → fall back to the original question so FTS still runs
    out = _question_to_fts("what is the why")
    assert out == "what is the why"


def test_lowercases_for_consistency():
    out = _question_to_fts("POSTGRES tuning")
    assert "postgres" in out
    assert "POSTGRES" not in out


def test_punctuation_split():
    # word-boundary regex strips punctuation rather than emitting compound tokens
    out = _question_to_fts("middleware.auth, refactor!")
    assert "middleware" in out
    assert "auth" in out
    assert "refactor" in out
    assert "middleware.auth" not in out
