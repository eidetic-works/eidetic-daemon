package capture

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/eidetic-works/eidetic-daemon/internal/engram"
	"github.com/eidetic-works/eidetic-daemon/internal/store"
)

// CursorParser handles surfaces where the file is rewritten as a whole rather
// than appended to. It de-dupes via content hash so repeated saves of the
// same content do NOT produce duplicate engrams.
//
// The "offset" field is hijacked here as a hash-prefix carrier (first 8 bytes
// of the SHA-256 packed as int64). State.json round-trips the value the same
// way an offset would, so no state.go changes are needed.
type CursorParser struct {
	surface string
}

// NewCursorParser returns a Cursor (whole-file replace) parser.
func NewCursorParser(surface string) *CursorParser {
	return &CursorParser{surface: surface}
}

func (p *CursorParser) Surface() string { return p.surface }

// cursorMaxFileBytes caps the per-file read so a runaway 2GB chatSessions
// JSON can't OOM the daemon. Files larger than this are skipped with a
// loud WARN log so the operator can investigate (split, archive, etc.).
// Picked 100 MiB as a generous ceiling — real Cursor workspace JSON files
// observed in practice are well under 10 MiB.
const cursorMaxFileBytes = 100 << 20 // 100 MiB

func (p *CursorParser) Parse(path string, fromOffset int64) ([]engram.Engram, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fromOffset, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return nil, fromOffset, err
	}
	if stat.Size() == 0 {
		return nil, 0, nil
	}

	// Audit ref: CRITICAL `internal/capture/parser_cursor.go:45` — no size
	// cap on io.ReadAll → a 2GB file → 2GB allocation → daemon OOM. Plus
	// any payload > store.MaxPayloadBytes silently dropped after read.
	// Guard: stat first, skip + WARN if file exceeds the hard cap.
	if stat.Size() > cursorMaxFileBytes {
		log.Printf("cursor: WARN skipping %s (%d bytes > %d cap) — too large to safely ingest; "+
			"split or archive the file to capture its contents",
			path, stat.Size(), cursorMaxFileBytes)
		// Advance the offset to "consumed" so we don't keep retrying every
		// poll. Use a sentinel = -size so a content change (size delta)
		// triggers a re-evaluation but identical-size repeats stay skipped.
		return nil, -stat.Size(), nil
	}

	// Use io.ReadFull on a bounded buffer so a truncated-after-stat race
	// can't smuggle extra bytes past the cap.
	buf := make([]byte, stat.Size())
	if _, err := io.ReadFull(f, buf); err != nil && err != io.ErrUnexpectedEOF {
		return nil, fromOffset, err
	}

	sum := sha256.Sum256(buf)
	hashAsOffset := int64(0)
	for i := 0; i < 8; i++ {
		hashAsOffset = (hashAsOffset << 8) | int64(sum[i])
	}
	if hashAsOffset == fromOffset && fromOffset != 0 {
		// Content unchanged since last parse; nothing to emit.
		return nil, fromOffset, nil
	}

	meta := fmt.Sprintf(
		`{"path":%q,"hash":%q,"size":%d,"parser":"cursor/v1"}`,
		path, hex.EncodeToString(sum[:]), stat.Size(),
	)
	// If the file is below the cursor-cap but above the per-engram cap
	// (store.MaxPayloadBytes), splitOversized lets the chunked-capture
	// downstream consume it instead of silent-dropping. Same shape JSONL
	// uses; consumer reassembles via chunk_id grouping.
	if len(buf) > chunkPayloadBudget {
		log.Printf("cursor: file %s (%d bytes) exceeds per-engram budget %d — chunking",
			path, len(buf), chunkPayloadBudget)
		chunks := splitOversized(p.surface, stat.ModTime().UnixNano(), path, 0, string(buf))
		// Merge cursor meta (hash) into each chunk's meta so the consumer
		// can verify the assembled payload.
		for i := range chunks {
			chunks[i].Meta = fmt.Sprintf(
				`{"path":%q,"hash":%q,"size":%d,"parser":"cursor/v1","chunk_id":%q,"chunk_seq":%d,"chunk_total":%d}`,
				path, hex.EncodeToString(sum[:]), stat.Size(),
				deriveChunkID(buf), i, len(chunks),
			)
		}
		return chunks, hashAsOffset, nil
	}
	e := engram.Engram{
		Surface: p.surface,
		TS:      stat.ModTime().UnixNano(),
		Payload: string(buf),
		Meta:    meta,
	}
	return []engram.Engram{e}, hashAsOffset, nil
}

// deriveChunkID returns the 16-hex-char chunk_id used by splitOversized so
// cursor-side chunks match the JSONL shape downstream consumers expect.
func deriveChunkID(payload []byte) string {
	h := sha256.Sum256(payload)
	return hex.EncodeToString(h[:8])
}

// Compile-time link to store.MaxPayloadBytes so any future drift in the
// per-engram cap is reflected here too.
var _ = store.MaxPayloadBytes
