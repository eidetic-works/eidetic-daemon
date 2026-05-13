package capture

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, dir, name, contents string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestJSONLParserEmptyFile(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "x.jsonl", "")
	parser := NewJSONLParser("claude_code")
	got, off, err := parser.Parse(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %d engrams, want 0", len(got))
	}
	if off != 0 {
		t.Errorf("offset=%d, want 0", off)
	}
}

func TestJSONLParserMultipleRecords(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "x.jsonl", `{"a":1}
{"b":2}
{"c":3}
`)
	parser := NewJSONLParser("claude_code")
	got, off, err := parser.Parse(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d engrams, want 3", len(got))
	}
	wants := []string{`{"a":1}`, `{"b":2}`, `{"c":3}`}
	for i, w := range wants {
		if got[i].Payload != w {
			t.Errorf("payload[%d] = %q, want %q", i, got[i].Payload, w)
		}
		if got[i].Surface != "claude_code" {
			t.Errorf("surface[%d] = %q", i, got[i].Surface)
		}
	}
	stat, _ := os.Stat(p)
	if off != stat.Size() {
		t.Errorf("offset=%d, want file size %d", off, stat.Size())
	}
}

func TestJSONLParserPartialLineDeferred(t *testing.T) {
	dir := t.TempDir()
	// Last record has no trailing newline — should NOT be consumed.
	p := writeFile(t, dir, "x.jsonl", `{"a":1}
{"b":2}
{"c":3 unterminated`)
	parser := NewJSONLParser("claude_code")
	got, off, err := parser.Parse(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("got %d engrams, want 2 (a and b only)", len(got))
	}
	// Offset should land at end-of-second-record (after second '\n').
	want := int64(len(`{"a":1}` + "\n" + `{"b":2}` + "\n"))
	if off != want {
		t.Errorf("offset=%d, want %d", off, want)
	}
}

func TestJSONLParserResumeFromOffset(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "x.jsonl", `{"a":1}
{"b":2}
{"c":3}
`)
	parser := NewJSONLParser("claude_code")
	first, off, _ := parser.Parse(p, 0)
	if len(first) != 3 {
		t.Fatalf("first parse: got %d, want 3", len(first))
	}
	// Append a new record, parse again from off.
	if err := os.WriteFile(p, append(read(t, p), []byte(`{"d":4}`+"\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _, err := parser.Parse(p, off)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Payload != `{"d":4}` {
		t.Errorf("resume produced %+v, want one {d:4} engram", got)
	}
}

func read(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestJSONLParserMalformedRecordStillEmitted(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "x.jsonl", "not json but a line\n")
	parser := NewJSONLParser("claude_code")
	got, _, err := parser.Parse(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Payload != "not json but a line" {
		t.Errorf("got %+v", got)
	}
}

func TestJSONLParserCRLF(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "x.jsonl", "{\"a\":1}\r\n{\"b\":2}\r\n")
	parser := NewJSONLParser("claude_code")
	got, _, _ := parser.Parse(p, 0)
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	for i, e := range got {
		if strings.Contains(e.Payload, "\r") {
			t.Errorf("payload[%d] retained \\r: %q", i, e.Payload)
		}
	}
}

func TestJSONLParserBOMStripped(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bom.jsonl")
	if err := os.WriteFile(p, append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"a":1}`+"\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	parser := NewJSONLParser("claude_code")
	got, _, err := parser.Parse(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Payload != `{"a":1}` {
		t.Errorf("got %+v, want one {a:1}", got)
	}
}

func TestJSONLParserExtractsTimestamp(t *testing.T) {
	const tsStr = "2026-04-11T01:10:46.644Z"
	wantTime, err := time.Parse(time.RFC3339Nano, tsStr)
	if err != nil {
		t.Fatal(err)
	}
	want := wantTime.UnixNano()

	dir := t.TempDir()
	p := writeFile(t, dir, "x.jsonl", `{"timestamp":"`+tsStr+`","x":1}`+"\n")
	parser := NewJSONLParser("claude_code")
	got, _, _ := parser.Parse(p, 0)
	if len(got) != 1 {
		t.Fatalf("got %d", len(got))
	}
	if got[0].TS != want {
		t.Errorf("ts=%d, want %d", got[0].TS, want)
	}
}

func TestJSONLParserTruncationResetsOffset(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "x.jsonl", `{"a":1}`+"\n"+`{"b":2}`+"\n")
	parser := NewJSONLParser("claude_code")
	_, off, _ := parser.Parse(p, 0)
	// Truncate file (replace with smaller content)
	if err := os.WriteFile(p, []byte(`{"reset":1}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, newOff, err := parser.Parse(p, off)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Payload != `{"reset":1}` {
		t.Errorf("post-truncation got %+v", got)
	}
	stat, _ := os.Stat(p)
	if newOff != stat.Size() {
		t.Errorf("offset=%d, want %d", newOff, stat.Size())
	}
}

func TestJSONLParserMetaContainsPath(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "x.jsonl", `{"a":1}`+"\n")
	parser := NewJSONLParser("claude_code")
	got, _, _ := parser.Parse(p, 0)
	if len(got) != 1 {
		t.Fatal("expected 1 engram")
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(got[0].Meta), &m); err != nil {
		t.Fatalf("meta not json: %v (raw=%s)", err, got[0].Meta)
	}
	if m["parser"] != "jsonl/v1" {
		t.Errorf("meta.parser = %v", m["parser"])
	}
	if m["path"] != p {
		t.Errorf("meta.path = %v, want %s", m["path"], p)
	}
}

func TestCursorParserHashDedup(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "state.json", `{"hello":"world"}`)
	parser := NewCursorParser("cursor")
	first, off, err := parser.Parse(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 {
		t.Errorf("first: got %d, want 1", len(first))
	}
	// Re-parse with same content + previous offset should emit nothing.
	got, sameOff, err := parser.Parse(p, off)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("dedup failed: got %+v", got)
	}
	if sameOff != off {
		t.Errorf("offset changed without content change: %d → %d", off, sameOff)
	}
}

func TestCursorParserContentChangeFires(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "state.json", `{"v":1}`)
	parser := NewCursorParser("cursor")
	_, off, _ := parser.Parse(p, 0)
	if err := os.WriteFile(p, []byte(`{"v":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, newOff, _ := parser.Parse(p, off)
	if len(got) != 1 {
		t.Errorf("change should emit: got %d", len(got))
	}
	if newOff == off {
		t.Errorf("offset (hash) should change with content")
	}
}

func TestCursorParserMetaCarriesHash(t *testing.T) {
	dir := t.TempDir()
	contents := `{"v":42}`
	p := writeFile(t, dir, "state.json", contents)
	parser := NewCursorParser("cursor")
	got, _, _ := parser.Parse(p, 0)
	if len(got) != 1 {
		t.Fatal("expected 1 engram")
	}
	want := sha256.Sum256([]byte(contents))
	var m map[string]any
	if err := json.Unmarshal([]byte(got[0].Meta), &m); err != nil {
		t.Fatalf("meta not json: %v (raw=%s)", err, got[0].Meta)
	}
	if got, _ := m["hash"].(string); got == "" || len(got) != 64 {
		t.Errorf("meta.hash missing/wrong-length: %v", m["hash"])
	}
	_ = want
}

func TestStateRoundtrip(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	s, err := LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	s.Set("claude_code", "/path/a", 100)
	s.Set("cursor", "/path/b", 200)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s2, err := LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.Get("claude_code", "/path/a"); got != 100 {
		t.Errorf("a offset=%d, want 100", got)
	}
	if got := s2.Get("cursor", "/path/b"); got != 200 {
		t.Errorf("b offset=%d, want 200", got)
	}
}

func TestStateMissingFileLoadsEmpty(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadState(filepath.Join(dir, "nonexistent.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Get("claude_code", "anything"); got != 0 {
		t.Errorf("empty state should return 0, got %d", got)
	}
}

func TestStateNoOpSaveIfNotDirty(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	s, _ := LoadState(statePath)
	// Save without setting anything → no file should be created.
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(statePath); err == nil {
		t.Errorf("clean Save should not create file")
	}
}

func TestStateSetSameOffsetIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	s, _ := LoadState(filepath.Join(dir, "state.json"))
	s.Set("a", "/p", 5)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	// Setting the same offset should not re-dirty.
	s.Set("a", "/p", 5)
	stat, _ := os.Stat(filepath.Join(dir, "state.json"))
	mtime1 := stat.ModTime()
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	stat2, _ := os.Stat(filepath.Join(dir, "state.json"))
	if !stat2.ModTime().Equal(mtime1) {
		t.Errorf("idempotent set caused write: mtimes %v vs %v", mtime1, stat2.ModTime())
	}
}
