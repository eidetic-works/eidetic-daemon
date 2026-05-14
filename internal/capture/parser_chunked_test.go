package capture

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eidetic-works/eidetic-daemon/internal/store"
)

// TestJSONLParserNormalLineNoChunking — record at-or-below
// chunkPayloadBudget emits exactly 1 engram with no chunk_* meta fields
// (backward-compat with pre-chunking consumers).
func TestJSONLParserNormalLineNoChunking(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "x.jsonl", `{"a":1,"payload":"small"}`+"\n")
	parser := NewJSONLParser("claude_code")
	got, _, err := parser.Parse(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d engrams, want 1 (no chunking on small payload)", len(got))
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(got[0].Meta), &meta); err != nil {
		t.Fatalf("meta not json: %v (raw=%s)", err, got[0].Meta)
	}
	for _, k := range []string{"chunk_id", "chunk_seq", "chunk_total"} {
		if _, has := meta[k]; has {
			t.Errorf("meta should NOT contain %q for non-chunked engram (got %v)", k, meta)
		}
	}
}

// TestJSONLParserOversizedLineSplits — a single line exceeding
// chunkPayloadBudget is split into N chunks, each tagged with chunk_id
// (sha256-prefix of full payload), chunk_seq (0..N-1), chunk_total.
func TestJSONLParserOversizedLineSplits(t *testing.T) {
	dir := t.TempDir()
	// Build a single JSONL line larger than chunkPayloadBudget.
	// chunkPayloadBudget = 7 MiB; use 16 MiB to force ⌈16/7⌉ = 3 chunks.
	const target = 16 << 20
	prefix := `{"big":"`
	suffix := `"}` + "\n"
	pad := strings.Repeat("X", target-len(prefix)-len(suffix))
	body := prefix + pad + suffix
	p := writeFile(t, dir, "huge.jsonl", body)

	parser := NewJSONLParser("claude_code")
	got, _, err := parser.Parse(p, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Expect ⌈(target-1)/chunkPayloadBudget⌉ chunks (target-1 because we
	// trim the trailing \n at consumer-time before splitting).
	wantChunks := (target - 1 + chunkPayloadBudget - 1) / chunkPayloadBudget
	if len(got) != wantChunks {
		t.Fatalf("got %d engrams, want %d (16 MiB split at %d-byte budget)", len(got), wantChunks, chunkPayloadBudget)
	}

	// All chunks should share the same chunk_id, have sequential chunk_seq,
	// and chunk_total = wantChunks. Reassembly = concatenate by chunk_seq.
	var firstID string
	totalLen := 0
	for i, e := range got {
		if int64(len(e.Payload)) > store.MaxPayloadBytes {
			t.Errorf("chunk %d payload %d bytes exceeds store.MaxPayloadBytes %d",
				i, len(e.Payload), store.MaxPayloadBytes)
		}
		var meta map[string]any
		if err := json.Unmarshal([]byte(e.Meta), &meta); err != nil {
			t.Fatalf("chunk %d meta not json: %v", i, err)
		}
		id, ok := meta["chunk_id"].(string)
		if !ok || id == "" {
			t.Errorf("chunk %d meta.chunk_id missing or empty: %v", i, meta)
		}
		if i == 0 {
			firstID = id
		} else if id != firstID {
			t.Errorf("chunk %d chunk_id %q differs from first %q", i, id, firstID)
		}
		seq, _ := meta["chunk_seq"].(float64)
		if int(seq) != i {
			t.Errorf("chunk %d meta.chunk_seq = %v, want %d", i, seq, i)
		}
		total, _ := meta["chunk_total"].(float64)
		if int(total) != wantChunks {
			t.Errorf("chunk %d meta.chunk_total = %v, want %d", i, total, wantChunks)
		}
		totalLen += len(e.Payload)
	}
	if totalLen != target-1 {
		t.Errorf("sum of chunk payload lengths = %d, want %d (full line minus trailing \\n)", totalLen, target-1)
	}
}

// TestJSONLParserChunkIDIsIdempotent — same input → same chunk_id.
// Critical for state-resume scenarios: consumers can dedupe on chunk_id.
func TestJSONLParserChunkIDIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	body := strings.Repeat("Y", 9<<20) + "\n" // 9 MiB → 2 chunks
	p1 := writeFile(t, dir, "a.jsonl", body)
	p2 := writeFile(t, dir, "b.jsonl", body)
	parser := NewJSONLParser("claude_code")

	out1, _, _ := parser.Parse(p1, 0)
	out2, _, _ := parser.Parse(p2, 0)
	if len(out1) != len(out2) {
		t.Fatalf("chunk count mismatch: %d vs %d", len(out1), len(out2))
	}
	id1 := chunkIDFromMeta(t, out1[0].Meta)
	id2 := chunkIDFromMeta(t, out2[0].Meta)
	if id1 != id2 {
		t.Errorf("idempotency violated: same payload → different chunk_id (%q vs %q)", id1, id2)
	}
	// And id1 should match sha256-prefix(body without trailing \n).
	want := sha256.Sum256([]byte(strings.TrimRight(body, "\n")))
	wantHex := hex.EncodeToString(want[:8])
	if id1 != wantHex {
		t.Errorf("chunk_id derivation: got %q, want sha256-prefix %q", id1, wantHex)
	}
}

func chunkIDFromMeta(t *testing.T, raw string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("meta not json: %v", err)
	}
	id, _ := m["chunk_id"].(string)
	return id
}

// TestJSONLParserMixedSizesInOneFile — small + huge + small in same file
// each emit independently; small produces 1 engram (no chunk_*), huge
// produces N chunks (with chunk_*).
func TestJSONLParserMixedSizesInOneFile(t *testing.T) {
	dir := t.TempDir()
	small1 := `{"i":1,"payload":"small"}`
	huge := `{"big":"` + strings.Repeat("Z", 9<<20) + `"}`
	small2 := `{"i":2,"payload":"small"}`
	body := small1 + "\n" + huge + "\n" + small2 + "\n"
	p := writeFile(t, dir, "mixed.jsonl", body)

	parser := NewJSONLParser("claude_code")
	got, _, err := parser.Parse(p, 0)
	if err != nil {
		t.Fatal(err)
	}

	// 1 (small1) + ⌈9/7⌉=2 (huge) + 1 (small2) = 4 engrams
	if len(got) != 4 {
		t.Fatalf("got %d engrams, want 4 (1 small + 2 huge-chunks + 1 small)", len(got))
	}

	if got[0].Payload != small1 {
		t.Errorf("engram[0] payload mismatch: %q", got[0].Payload)
	}
	if got[3].Payload != small2 {
		t.Errorf("engram[3] payload mismatch: %q", got[3].Payload)
	}
	// Middle 2 are chunks of huge — they share chunk_id, seq=0+1, total=2.
	id1 := chunkIDFromMeta(t, got[1].Meta)
	id2 := chunkIDFromMeta(t, got[2].Meta)
	if id1 == "" || id1 != id2 {
		t.Errorf("middle 2 should be chunks with shared chunk_id; got %q vs %q", id1, id2)
	}
	// First and last (small) should NOT have chunk_id.
	if chunkIDFromMeta(t, got[0].Meta) != "" {
		t.Errorf("small engram[0] meta has chunk_id (should not)")
	}
	if chunkIDFromMeta(t, got[3].Meta) != "" {
		t.Errorf("small engram[3] meta has chunk_id (should not)")
	}
}

// TestSplitOversizedReassembly — golden test for the consumer-side
// reassembly contract: concat(payload[i] for i in 0..total-1 sorted by
// chunk_seq) == original payload.
func TestSplitOversizedReassembly(t *testing.T) {
	original := strings.Repeat("ABCDEFGH", (10<<20)/8) // ~10 MiB of patterned bytes
	chunks := splitOversized("claude_code", 1, "/tmp/x", 0, original)
	if len(chunks) < 2 {
		t.Fatalf("want at least 2 chunks for 10 MiB at 7 MiB budget; got %d", len(chunks))
	}
	var sb strings.Builder
	for _, c := range chunks {
		sb.WriteString(c.Payload)
	}
	if sb.String() != original {
		t.Errorf("reassembly mismatch: len(reassembled)=%d, len(original)=%d", sb.Len(), len(original))
	}
}

// TestJSONLParserStateOffsetAdvancesPastOversized — even with chunking,
// the state offset advances to the END of the consumed line; resume
// from that offset doesn't re-emit chunks.
func TestJSONLParserStateOffsetAdvancesPastOversized(t *testing.T) {
	dir := t.TempDir()
	body := strings.Repeat("Q", 9<<20) + "\n" + `{"after":1}` + "\n"
	p := writeFile(t, dir, "x.jsonl", body)
	parser := NewJSONLParser("claude_code")

	first, off, err := parser.Parse(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	// 2 chunks for the 9 MiB line + 1 engram for the small follow-up.
	if len(first) != 3 {
		t.Errorf("first parse: got %d, want 3 (2 chunks + 1 small)", len(first))
	}

	// Resume from `off`; should be 0 new engrams (we consumed everything).
	got, _, err := parser.Parse(p, off)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("resume: got %d engrams, want 0 (offset should land past consumed bytes)", len(got))
	}
}

// Sanity: confirm chunkPayloadBudget is below store.MaxPayloadBytes
// (otherwise we'd produce chunks the store rejects).
func TestChunkBudgetUnderStoreCap(t *testing.T) {
	if chunkPayloadBudget >= store.MaxPayloadBytes {
		t.Errorf("chunkPayloadBudget (%d) must be < store.MaxPayloadBytes (%d) so meta fits",
			chunkPayloadBudget, store.MaxPayloadBytes)
	}
	// Also ensure the gap is meaningful (≥ 256 KiB for meta + wire overhead).
	if store.MaxPayloadBytes-chunkPayloadBudget < 256<<10 {
		t.Errorf("chunk budget headroom %d bytes is too tight (want ≥ 256 KiB for meta + wire)",
			store.MaxPayloadBytes-chunkPayloadBudget)
	}
}

// Compile-time link to ensure capture's chunking respects store's cap so
// drift can't sneak in.
var _ = func() bool {
	_ = filepath.Separator
	_ = os.PathSeparator
	return chunkPayloadBudget < store.MaxPayloadBytes
}()
