package capture

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/eidetic-works/eidetic-daemon/internal/engram"
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

	buf, err := io.ReadAll(f)
	if err != nil {
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
	e := engram.Engram{
		Surface: p.surface,
		TS:      stat.ModTime().UnixNano(),
		Payload: string(buf),
		Meta:    meta,
	}
	return []engram.Engram{e}, hashAsOffset, nil
}
