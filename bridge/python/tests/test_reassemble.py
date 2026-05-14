"""Tests for eidetic_mcp.reassemble.reassemble_chunks().

Mirror of the daemon-side ADR-018 contract: chunks share chunk_id,
sort by chunk_seq, concat to original. Non-chunked rows pass through.
"""

from __future__ import annotations

import json
import warnings

import pytest

from eidetic_mcp.client import Engram
from eidetic_mcp.reassemble import reassemble_chunks


def _engram(id_: int, payload: str, meta: dict | None = None) -> Engram:
    return Engram(
        id=id_,
        surface="claude_code",
        ts=id_ * 100,
        payload=payload,
        meta=json.dumps(meta) if meta is not None else "",
    )


def _chunk(id_: int, payload: str, chunk_id: str, seq: int, total: int) -> Engram:
    return _engram(id_, payload, {
        "path": "/x.jsonl", "offset_end": 0, "parser": "jsonl/v1",
        "chunk_id": chunk_id, "chunk_seq": seq, "chunk_total": total,
    })


# ── Non-chunked: pass-through ───────────────────────────────────────────────

def test_no_chunked_rows_passthrough():
    rows = [_engram(1, "hello"), _engram(2, "world")]
    out = reassemble_chunks(rows)
    assert list(out) == rows


def test_empty_meta_passthrough():
    rows = [Engram(id=1, surface="x", ts=1, payload="p", meta="")]
    out = reassemble_chunks(rows)
    assert list(out) == rows


def test_meta_without_chunk_id_passthrough():
    rows = [_engram(1, "hi", {"path": "/x", "parser": "jsonl/v1"})]  # no chunk_id
    out = reassemble_chunks(rows)
    assert list(out) == rows


def test_malformed_meta_treated_as_non_chunked():
    rows = [Engram(id=1, surface="x", ts=1, payload="p", meta="{not json")]
    with warnings.catch_warnings(record=True) as w:
        warnings.simplefilter("always")
        out = reassemble_chunks(rows)
    assert list(out) == rows
    assert any("not JSON" in str(wi.message) for wi in w)


# ── Chunked: merge ───────────────────────────────────────────────────────────

def test_two_chunks_merge_in_seq_order():
    rows = [
        _chunk(1, "Hello, ", chunk_id="abc123", seq=0, total=2),
        _chunk(2, "world!", chunk_id="abc123", seq=1, total=2),
    ]
    out = list(reassemble_chunks(rows))
    assert len(out) == 1
    assert out[0].payload == "Hello, world!"


def test_chunks_returned_out_of_order_still_merge_correctly():
    # chunk_seq=1 comes BEFORE chunk_seq=0 in input — reassembler must sort.
    rows = [
        _chunk(2, "world!", chunk_id="abc123", seq=1, total=2),
        _chunk(1, "Hello, ", chunk_id="abc123", seq=0, total=2),
    ]
    out = list(reassemble_chunks(rows))
    assert len(out) == 1
    assert out[0].payload == "Hello, world!"


def test_three_chunks_merge():
    rows = [
        _chunk(1, "AAA", chunk_id="x1", seq=0, total=3),
        _chunk(2, "BBB", chunk_id="x1", seq=1, total=3),
        _chunk(3, "CCC", chunk_id="x1", seq=2, total=3),
    ]
    out = list(reassemble_chunks(rows))
    assert len(out) == 1
    assert out[0].payload == "AAABBBCCC"


def test_mixed_chunked_and_non_chunked_in_one_call():
    rows = [
        _engram(1, "small1"),
        _chunk(2, "big-pt0", chunk_id="big1", seq=0, total=2),
        _chunk(3, "big-pt1", chunk_id="big1", seq=1, total=2),
        _engram(4, "small2"),
    ]
    out = list(reassemble_chunks(rows))
    assert len(out) == 3
    assert out[0].payload == "small1"
    assert out[1].payload == "big-pt0big-pt1"
    assert out[2].payload == "small2"


def test_two_distinct_chunk_groups_merge_independently():
    rows = [
        _chunk(1, "groupA-0", chunk_id="A", seq=0, total=2),
        _chunk(2, "groupB-0", chunk_id="B", seq=0, total=2),
        _chunk(3, "groupA-1", chunk_id="A", seq=1, total=2),
        _chunk(4, "groupB-1", chunk_id="B", seq=1, total=2),
    ]
    out = list(reassemble_chunks(rows))
    assert len(out) == 2
    payloads = {r.payload for r in out}
    assert payloads == {"groupA-0groupA-1", "groupB-0groupB-1"}


def test_input_order_preserved_for_first_occurrence():
    # If chunked group's first row is INPUT-INDEX 0, the reassembled
    # row appears at OUTPUT-INDEX 0. Caller relies on this for ts-DESC.
    rows = [
        _chunk(1, "head", chunk_id="x", seq=0, total=1),  # 1-chunk group
        _engram(2, "later"),
    ]
    out = list(reassemble_chunks(rows))
    assert len(out) == 2
    assert out[0].payload == "head"
    assert out[1].payload == "later"


def test_merged_engram_carries_first_chunks_id_and_ts():
    rows = [
        _chunk(7, "hello-", chunk_id="x", seq=0, total=2),
        _chunk(8, "world", chunk_id="x", seq=1, total=2),
    ]
    out = list(reassemble_chunks(rows))
    assert len(out) == 1
    assert out[0].id == 7  # first chunk's id (id=7, ts=700)
    assert out[0].ts == 700


# ── Edge cases (warn + best-effort) ─────────────────────────────────────────

def test_missing_chunk_in_group_warns_and_emits_partial():
    # chunk_total=3 but only 2 chunks present.
    rows = [
        _chunk(1, "AAA", chunk_id="g", seq=0, total=3),
        _chunk(3, "CCC", chunk_id="g", seq=2, total=3),  # gap at seq=1
    ]
    with warnings.catch_warnings(record=True) as w:
        warnings.simplefilter("always")
        out = list(reassemble_chunks(rows))
    assert len(out) == 1
    assert out[0].payload == "AAACCC"  # partial; not crash
    assert any("incomplete" in str(wi.message) or "gap" in str(wi.message) for wi in w)


def test_chunk_total_mismatch_within_group_warns():
    rows = [
        _chunk(1, "AAA", chunk_id="g", seq=0, total=2),
        _chunk(2, "BBB", chunk_id="g", seq=1, total=99),  # mismatched total
    ]
    with warnings.catch_warnings(record=True) as w:
        warnings.simplefilter("always")
        out = list(reassemble_chunks(rows))
    assert len(out) == 1
    assert out[0].payload == "AAABBB"
    assert any("chunk_total mismatch" in str(wi.message) for wi in w)


def test_idempotent_on_already_reassembled_input():
    # Running reassemble twice produces same output (already-merged rows
    # are non-chunked + pass through).
    rows = [
        _chunk(1, "Hello, ", chunk_id="x", seq=0, total=2),
        _chunk(2, "world!", chunk_id="x", seq=1, total=2),
    ]
    once = list(reassemble_chunks(rows))
    twice = list(reassemble_chunks(once))
    assert once == twice
