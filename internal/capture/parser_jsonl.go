package capture

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/eidetic-works/eidetic-daemon/internal/engram"
)

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

	if _, err := f.Seek(fromOffset, io.SeekStart); err != nil {
		return nil, fromOffset, err
	}

	buf, err := io.ReadAll(f)
	if err != nil {
		return nil, fromOffset, err
	}
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

	return engrams, cumOffset, nil
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
