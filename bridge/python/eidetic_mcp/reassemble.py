"""Consumer-side reassembly for chunked engrams (ADR-018).

The daemon's JSONL parser splits records exceeding chunkPayloadBudget
(7 MiB) into N chunks, each tagged with chunk_id (sha256-prefix of the
full payload, idempotent on resume) + chunk_seq (0-indexed) +
chunk_total in meta JSON. Records ≤ budget emit 1 engram with no
chunk_* fields (backward-compat).

reassemble_chunks() consumes raw daemon output + returns engrams with
chunked records merged back into single rows. The output engram for a
reassembled record carries the FIRST chunk's id/surface/ts (oldest chunk
of the logical record) and concatenated payload across all chunks.

Idempotent + safe to call on already-reassembled output (non-chunked
rows pass through unchanged).

Edge cases:
  - Missing chunks (gap in chunk_seq) — emit best-effort partial + log
    via warnings (NOT silent dropping)
  - chunk_total mismatch within a group — emit partial + warn
  - Malformed meta JSON — pass row through as if non-chunked + warn
  - Mixed chunked + non-chunked in same input list — both handled
"""

from __future__ import annotations

import json
import warnings
from collections import defaultdict
from dataclasses import replace
from typing import Sequence

from .client import Engram


def reassemble_chunks(rows: Sequence[Engram]) -> Sequence[Engram]:
    """Group chunked rows by meta.chunk_id, concat payloads in chunk_seq order,
    return one engram per logical record.

    Non-chunked rows (no chunk_id in meta, or unparseable meta) pass through
    unchanged. Order of returned engrams matches order of FIRST occurrence in
    the input list (so callers can rely on ts-DESC ordering being preserved
    for the head-row of each chunk group).
    """
    # Build chunk groups + collect non-chunked passthroughs in input order.
    # Each entry in `output_order` is either a non-chunked Engram (immediately
    # emit) or a chunk_id (emit reassembled when group complete).
    output_order: list[object] = []
    chunk_groups: dict[str, list[Engram]] = defaultdict(list)
    seen_chunk_ids: set[str] = set()

    for row in rows:
        chunk_id, _seq, _total = _parse_chunk_meta(row.meta)
        if chunk_id is None:
            output_order.append(row)
            continue
        chunk_groups[chunk_id].append(row)
        if chunk_id not in seen_chunk_ids:
            seen_chunk_ids.add(chunk_id)
            output_order.append(chunk_id)

    out: list[Engram] = []
    for item in output_order:
        if isinstance(item, Engram):
            out.append(item)
            continue
        chunk_id = item  # str
        group = chunk_groups[chunk_id]
        merged = _merge_chunk_group(chunk_id, group)
        if merged is not None:
            out.append(merged)
    return tuple(out)


def _parse_chunk_meta(raw_meta: str) -> tuple[str | None, int | None, int | None]:
    """Return (chunk_id, chunk_seq, chunk_total) or (None, None, None) for
    non-chunked rows. Malformed meta → (None, None, None) + warning."""
    if not raw_meta:
        return None, None, None
    try:
        meta = json.loads(raw_meta)
    except (json.JSONDecodeError, TypeError):
        warnings.warn(f"reassemble: meta is not JSON (treating as non-chunked): {raw_meta[:80]!r}")
        return None, None, None
    if not isinstance(meta, dict):
        return None, None, None
    chunk_id = meta.get("chunk_id")
    if chunk_id is None:
        return None, None, None
    if not isinstance(chunk_id, str) or not chunk_id:
        warnings.warn(f"reassemble: chunk_id present but invalid (treating as non-chunked): {chunk_id!r}")
        return None, None, None
    seq = meta.get("chunk_seq")
    total = meta.get("chunk_total")
    if not isinstance(seq, int) or not isinstance(total, int):
        warnings.warn(f"reassemble: chunk_id={chunk_id!r} but seq/total not int: seq={seq!r} total={total!r}")
        return chunk_id, None, None
    return chunk_id, seq, total


def _merge_chunk_group(chunk_id: str, group: list[Engram]) -> Engram | None:
    """Sort group by chunk_seq, validate completeness, concatenate payloads.
    Returns merged engram. None if group is empty."""
    if not group:
        return None

    # Sort by chunk_seq (parsed from each row's meta — recompute since we
    # already validated chunk_id presence in _parse_chunk_meta but not seq).
    indexed: list[tuple[int, Engram]] = []
    expected_total = None
    for row in group:
        _id, seq, total = _parse_chunk_meta(row.meta)
        if seq is None:
            warnings.warn(f"reassemble: chunk_id={chunk_id!r} row missing chunk_seq; skipping row in merge")
            continue
        if expected_total is None:
            expected_total = total
        elif total != expected_total and total is not None:
            warnings.warn(
                f"reassemble: chunk_id={chunk_id!r} chunk_total mismatch: "
                f"expected {expected_total}, got {total} on seq {seq}"
            )
        indexed.append((seq, row))
    indexed.sort(key=lambda p: p[0])

    if not indexed:
        return None

    # Detect gaps + total mismatch (best-effort; emit partial with warning).
    seen_seqs = [s for s, _ in indexed]
    if expected_total is not None and len(seen_seqs) != expected_total:
        warnings.warn(
            f"reassemble: chunk_id={chunk_id!r} incomplete: have {len(seen_seqs)}/{expected_total} "
            f"chunks (seqs {seen_seqs}); emitting partial reassembly"
        )
    for i, s in enumerate(seen_seqs):
        if s != i:
            warnings.warn(
                f"reassemble: chunk_id={chunk_id!r} sequence gap at index {i} "
                f"(got seq {s}); emitting partial reassembly"
            )
            break

    head = indexed[0][1]
    merged_payload = "".join(row.payload for _, row in indexed)
    # Strip chunk_* from merged meta so re-running reassemble_chunks on
    # already-merged output is a true no-op (idempotent without spurious
    # "incomplete" warnings on single-row replay). Replace meta with a
    # version that records the merge provenance for debugging.
    merged_meta = _stripped_chunk_meta(head.meta, len(indexed))
    return replace(head, payload=merged_payload, meta=merged_meta)


def _stripped_chunk_meta(raw_meta: str, chunks_merged: int) -> str:
    """Return raw_meta with chunk_id/chunk_seq/chunk_total removed +
    a `reassembled_from` field added for debugging. Idempotent."""
    if not raw_meta:
        return raw_meta
    try:
        meta = json.loads(raw_meta)
    except (json.JSONDecodeError, TypeError):
        return raw_meta
    if not isinstance(meta, dict):
        return raw_meta
    for k in ("chunk_id", "chunk_seq", "chunk_total"):
        meta.pop(k, None)
    meta["reassembled_from"] = chunks_merged
    return json.dumps(meta)
