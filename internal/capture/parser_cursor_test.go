package capture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eidetic-works/eidetic-daemon/internal/store"
)

// TestCursorParserSkipsOversizedFile is the regression test for the CRITICAL
// audit finding (`internal/capture/parser_cursor.go:45`): pre-fix `io.ReadAll`
// on an oversized file allocated the entire file size in RAM (OOM-prone),
// then silently dropped the result because store.MaxPayloadBytes rejected it.
//
// Post-fix: files larger than cursorMaxFileBytes (100 MiB) are skipped with
// a WARN log; nothing is allocated for their body.
func TestCursorParserSkipsOversizedFile(t *testing.T) {
	dir := t.TempDir()
	// Create a sparse file > cursorMaxFileBytes without actually allocating
	// disk: open + Truncate. Stat returns the apparent size; the parser
	// must NOT call ReadAll on it.
	path := filepath.Join(dir, "huge.json")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(cursorMaxFileBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	parser := NewCursorParser("cursor")
	got, newOff, err := parser.Parse(path, 0)
	if err != nil {
		t.Fatalf("Parse should NOT error on oversized file; got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("oversized file: got %d engrams, want 0 (skip semantics)", len(got))
	}
	if newOff >= 0 {
		t.Errorf("oversized file: new offset = %d, expected negative sentinel "+
			"(so equal-size repeats stay skipped)", newOff)
	}
}

// TestCursorParserChunksLargeButUndercapFile — file between
// chunkPayloadBudget (7 MiB) and cursorMaxFileBytes (100 MiB) is chunked
// via splitOversized so no payload is silently dropped on store insert.
func TestCursorParserChunksLargeButUndercapFile(t *testing.T) {
	dir := t.TempDir()
	// 12 MiB → > chunkPayloadBudget (7 MiB), < cursorMaxFileBytes (100 MiB)
	// → expect ⌈12/7⌉ = 2 chunks.
	body := strings.Repeat("Z", 12<<20)
	path := writeFile(t, dir, "big.json", body)

	parser := NewCursorParser("cursor")
	got, _, err := parser.Parse(path, 0)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) < 2 {
		t.Fatalf("expected ≥ 2 chunks for 12 MiB file at 7 MiB budget; got %d", len(got))
	}
	// All chunks must fit under store.MaxPayloadBytes.
	for i, e := range got {
		if int64(len(e.Payload)) > store.MaxPayloadBytes {
			t.Errorf("chunk %d payload %d > MaxPayloadBytes %d",
				i, len(e.Payload), store.MaxPayloadBytes)
		}
		if !strings.Contains(e.Meta, `"chunk_id":`) {
			t.Errorf("chunk %d missing chunk_id in meta: %s", i, e.Meta)
		}
		if !strings.Contains(e.Meta, `"parser":"cursor/v1"`) {
			t.Errorf("chunk %d missing cursor parser tag in meta: %s", i, e.Meta)
		}
	}
	// Concatenated payloads should reconstruct the original body.
	var sb strings.Builder
	for _, e := range got {
		sb.WriteString(e.Payload)
	}
	if sb.String() != body {
		t.Errorf("chunked reassembly mismatch: len(got)=%d, len(want)=%d",
			sb.Len(), len(body))
	}
}

// TestCursorParserSmallFileSingleEngram preserves the original
// single-engram-per-file contract for files at or below chunkPayloadBudget.
func TestCursorParserSmallFileSingleEngram(t *testing.T) {
	dir := t.TempDir()
	body := `{"hello":"world"}`
	path := writeFile(t, dir, "small.json", body)

	parser := NewCursorParser("cursor")
	got, _, err := parser.Parse(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("small file: got %d engrams, want 1", len(got))
	}
	if got[0].Payload != body {
		t.Errorf("payload mismatch: got %q, want %q", got[0].Payload, body)
	}
	if strings.Contains(got[0].Meta, `"chunk_id":`) {
		t.Errorf("small file should NOT have chunk_id in meta: %s", got[0].Meta)
	}
}
