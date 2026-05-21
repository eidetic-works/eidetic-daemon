package capture

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/eidetic-works/eidetic-daemon/internal/engram"
)

// jsonlMaxFileBytes caps the unconsumed (size - fromOffset) byte count for
// a single Parse pass so a 5 GB legacy Claude Code session JSONL discovered
// on first startup can't OOM the daemon. Above this cap we skip + WARN; the
// operator can split the file into the `.archive/` subdir to capture it
// piece-meal. 500 MiB is generous — real session JSONLs observed in the
// wild top out around 100 MiB.
const jsonlMaxFileBytes = 500 << 20 // 500 MiB

// JSONLParser tails an append-only JSONL file. Each newline-terminated record
// becomes one engram with payload = the raw line (no internal JSON parsing
// in W1; payload is opaque text).
//
// Used for surfaces "claude_code" and "cowork".
type JSONLParser struct {
	surface string
}

// NewJSONLParser returns a JSONL parser for the given surface name.
func NewJSONLParser(surface string) *JSONLParser {
	return &JSONLParser{surface: surface}
}

// Surface returns the surface name.
func (p *JSONLParser) Surface() string { return p.surface }

// Parse reads from `fromOffset`, splits on newline, drops a trailing partial
// record if the file does not end in '\n', and returns one engram per
// complete line.
//
// Edge cases handled per spec section 5:
//   - Empty file → 0 engrams, offset unchanged
//   - Partial line at EOF → consumed up to the last '\n', partial deferred
//   - Multi-record append → 1 engram per line in order
//   - Malformed line → still emitted (payload is the raw bytes); the consumer
//     decides what to do with non-JSON
//   - File truncated/replaced (size < fromOffset) → reset to 0, re-read all
//   - UTF-8 BOM at file start → stripped from the first record only
func (p *JSONLParser) Parse(path string, fromOffset int64) ([]engram.Engram, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fromOffset, err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return nil, fromOffset, err
	}
	size := stat.Size()
	if size == 0 {
		return nil, 0, nil
	}

	if fromOffset > size {
		// File was truncated or replaced. Re-read from start.
		fromOffset = 0
	}

	// Audit ref: CRITICAL `internal/capture/parser_jsonl.go:70` — pre-fix
	// `io.ReadAll` on (size - fromOffset) → a 5 GB legacy session JSONL at
	// first-startup scan → 5 GB allocation → daemon OOM at boot. Bound the
	// per-pass read; on the first scan, advance the offset past the file
	// so subsequent polls don't re-trip the same WARN.
	if size-fromOffset > jsonlMaxFileBytes {
		log.Printf("jsonl: WARN skipping %s (unconsumed %d bytes > %d cap) — "+
			"legacy oversized session; please split or move to .archive/ to capture its contents",
			path, size-fromOffset, jsonlMaxFileBytes)
		// Advance to EOF so we don't keep re-warning every poll. If the
		// file is later truncated (size < fromOffset) the truncation branch
		// above kicks in and we re-start from 0.
		return nil, size, nil
	}

	if _, err := f.Seek(fromOffset, io.SeekStart); err != nil {
		return nil, fromOffset, err
	}

	// Bounded read instead of io.ReadAll. We know exactly how many bytes
	// remain (size - fromOffset, already capped above) — read no more, even
	// if the file grows mid-Parse. Late-arriving bytes get picked up next
	// poll via the updated offset.
	remaining := size - fromOffset
	buf := make([]byte, remaining)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF {
		return nil, fromOffset, err
	}
	buf = buf[:n]
	if len(buf) == 0 {
		return nil, fromOffset, nil
	}

	// Strip UTF-8 BOM only if we're at the very start of the file.
	if fromOffset == 0 && len(buf) >= 3 && buf[0] == 0xEF && buf[1] == 0xBB && buf[2] == 0xBF {
		buf = buf[3:]
		fromOffset += 3
	}

	// Trim a trailing partial record (no '\n' yet). Keep its bytes for next call.
	consumable := buf
	if !endsWithNewline(buf) {
		idx := bytes.LastIndexByte(buf, '\n')
		if idx < 0 {
			// No complete record yet.
			return nil, fromOffset, nil
		}
		consumable = buf[:idx+1]
	}

	now := time.Now().UnixNano()
	lines := bytes.Split(bytes.TrimRight(consumable, "\n"), []byte("\n"))
	engrams := make([]engram.Engram, 0, len(lines))
	cumOffset := fromOffset
	for _, line := range lines {
		// Each consumed record advances the offset by len(line) + 1 (the '\n').
		cumOffset += int64(len(line)) + 1

		raw := strings.TrimRight(string(line), "\r")
		if raw == "" {
			// Skip blank lines but DO advance offset (we consumed the '\n').
			continue
		}

		ts := extractTimestamp(raw, now)
		// Chunked-capture: lines exceeding the per-engram cap are split into
		// N chunks and tagged with chunk_id (sha256-prefix of full payload —
		// idempotent on resume) + chunk_seq (0..N-1) + chunk_total in meta.
		// Consumers reassemble via meta filter. Records ≤ cap → 1 engram
		// (backward-compat: no chunk_* fields in meta). Per CHANGELOG W2+
		// candidate "Chunked-capture for arbitrarily-large records (replaces
		// the 8 MiB cap as a hard wall)" pulled forward Day 4.
		if len(raw) > chunkPayloadBudget {
			engrams = append(engrams, splitOversized(p.surface, ts, path, cumOffset, raw)...)
		} else {
			meta := fmt.Sprintf(
				`{"path":%q,"offset_end":%d,"parser":"jsonl/v1"}`,
				path, cumOffset,
			)
			engrams = append(engrams, engram.Engram{
				Surface: p.surface,
				TS:      ts,
				Payload: raw,
				Meta:    meta,
			})
		}
	}

	return engrams, cumOffset, nil
}

// chunkPayloadBudget is the per-chunk payload size budget — kept below
// store.MaxPayloadBytes to leave room for the meta JSON which travels
// alongside payload through the same writer.ExecContext call. 7 MiB
// (vs the 8 MiB MaxPayloadBytes cap) gives ~1 MiB of headroom for
// meta + any wire-protocol overhead.
const chunkPayloadBudget = 7 << 20

// splitOversized chops `raw` into ⌈len(raw)/chunkPayloadBudget⌉ chunks,
// each emitted as its own engram with a shared chunk_id (sha256-prefix
// of the full payload) + chunk_seq + chunk_total in meta. Idempotent
// on resume: same input bytes → same chunk_id, so duplicate detection
// at the consumer side is straightforward (group by chunk_id, sort by
// chunk_seq, concatenate payload).
func splitOversized(surface string, ts int64, path string, offsetEnd int64, raw string) []engram.Engram {
	hash := sha256.Sum256([]byte(raw))
	chunkID := hex.EncodeToString(hash[:8]) // 16 hex chars = 64 bits, enough for collision-free per-file usage
	total := (len(raw) + chunkPayloadBudget - 1) / chunkPayloadBudget
	out := make([]engram.Engram, 0, total)
	for seq := 0; seq < total; seq++ {
		start := seq * chunkPayloadBudget
		end := start + chunkPayloadBudget
		if end > len(raw) {
			end = len(raw)
		}
		meta := fmt.Sprintf(
			`{"path":%q,"offset_end":%d,"parser":"jsonl/v1","chunk_id":%q,"chunk_seq":%d,"chunk_total":%d}`,
			path, offsetEnd, chunkID, seq, total,
		)
		out = append(out, engram.Engram{
			Surface: surface,
			TS:      ts,
			Payload: raw[start:end],
			Meta:    meta,
		})
	}
	return out
}

func endsWithNewline(b []byte) bool {
	return len(b) > 0 && b[len(b)-1] == '\n'
}

// extractTimestamp pulls a unix-nanosecond ts from the JSON record if a known
// "timestamp" or "ts" field is present and ISO-8601-parseable. Falls back to
// the supplied default (typically file mtime or wall clock).
//
// Pragmatic, not exhaustive — payload's authoritative timestamp source is
// surface-dependent and W2+ enrichment work.
func extractTimestamp(raw string, fallback int64) int64 {
	for _, key := range []string{`"timestamp":"`, `"ts":"`} {
		idx := strings.Index(raw, key)
		if idx < 0 {
			continue
		}
		start := idx + len(key)
		end := strings.IndexByte(raw[start:], '"')
		if end < 0 {
			continue
		}
		s := raw[start : start+end]
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t.UnixNano()
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t.UnixNano()
		}
	}
	return fallback
}
