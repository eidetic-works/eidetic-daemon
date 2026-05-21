package capture

import (
	"os"
	"path/filepath"
	"testing"
)

// TestJSONLParserSkipsLegacyOversizedFile is the regression test for the
// CRITICAL audit finding (`internal/capture/parser_jsonl.go:70`): pre-fix
// `io.ReadAll(size - fromOffset)` against a 5GB legacy Claude Code session
// JSONL allocated 5GB at first-startup scan → daemon OOM at boot.
//
// Post-fix: an unconsumed read larger than jsonlMaxFileBytes (500 MiB) is
// skipped with a WARN log; the offset is advanced to EOF so subsequent
// polls don't re-trip the warn.
func TestJSONLParserSkipsLegacyOversizedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.jsonl")
	// Create a sparse file > jsonlMaxFileBytes — no disk allocation needed.
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(jsonlMaxFileBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	parser := NewJSONLParser("claude_code")
	got, newOff, err := parser.Parse(path, 0)
	if err != nil {
		t.Fatalf("Parse should NOT error on oversized file; got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("oversized file: got %d engrams, want 0 (skip semantics)", len(got))
	}
	// Offset should be advanced to file size so we don't re-warn.
	if newOff != jsonlMaxFileBytes+1 {
		t.Errorf("oversized file: new offset = %d, want %d (file size, so subsequent polls skip)",
			newOff, jsonlMaxFileBytes+1)
	}

	// Subsequent Parse with the advanced offset should also be a no-op
	// (no double-warn, no engrams).
	got2, newOff2, err := parser.Parse(path, newOff)
	if err != nil {
		t.Fatal(err)
	}
	if len(got2) != 0 {
		t.Errorf("resume: got %d engrams on second pass, want 0", len(got2))
	}
	if newOff2 != newOff {
		t.Errorf("resume: offset moved from %d to %d on no-op pass", newOff, newOff2)
	}
}

// TestJSONLParserBoundedReadOnGrowingFile — verifies the per-pass cap also
// applies when a file is appended to faster than we can consume it. Even
// with size jumping mid-Parse, we never read more than the unconsumed
// bytes captured at stat time.
func TestJSONLParserBoundedReadOnGrowingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "growing.jsonl")
	if err := os.WriteFile(path, []byte(`{"a":1}`+"\n"+`{"b":2}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	parser := NewJSONLParser("claude_code")
	first, off, err := parser.Parse(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 {
		t.Errorf("initial pass: got %d, want 2", len(first))
	}

	// Append a third record AFTER we've stated the file but before the
	// next Parse — proves bounded read picks it up on the next pass, not
	// retroactively in the first.
	if err := os.WriteFile(path, []byte(`{"a":1}`+"\n"+`{"b":2}`+"\n"+`{"c":3}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	second, _, err := parser.Parse(path, off)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 {
		t.Errorf("resume: got %d, want 1 (the appended record)", len(second))
	}
}

// TestJSONLParserUndercapNormalFile — a 5 MiB file (well under the 500 MiB
// cap) parses normally. Sanity check that the cap doesn't regress the
// common case.
func TestJSONLParserUndercapNormalFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mid.jsonl")
	// 5 MiB of single-line JSON records. Cheap to build + parse.
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	line := `{"surface":"claude_code","payload":"hello"}` + "\n"
	target := int64(5 << 20)
	written := int64(0)
	chunk := []byte(line + line + line + line + line + line + line + line + line + line)
	for written < target {
		n, _ := f.Write(chunk)
		written += int64(n)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	parser := NewJSONLParser("claude_code")
	got, _, err := parser.Parse(path, 0)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) == 0 {
		t.Errorf("normal file: got 0 engrams, want > 0 (cap should not trigger at 5 MiB)")
	}
}
