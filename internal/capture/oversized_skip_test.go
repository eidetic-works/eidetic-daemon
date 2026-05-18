package capture

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eidetic-works/eidetic-daemon/internal/engram"
	"github.com/eidetic-works/eidetic-daemon/internal/store"
)

// TestWatcherOversizedPayloadChunked — UPDATED contract per chunked-capture
// (post-PR-N): oversized records are no longer dropped + counted as skipped;
// instead the JSONL parser splits them into chunks (each ≤ chunk budget)
// tagged with chunk_id/chunk_seq/chunk_total in meta. The watcher's
// SkippedPayloadTooLarge() counter remains as defense-in-depth (any chunk
// somehow still exceeding cap → skipped + counted), but on normal chunked
// records it stays at 0 because chunks fit by construction.
//
// This supersedes the v0.0.3 TestWatcherOversizedPayloadCounted shape
// where oversized records were dropped + counted.
func TestWatcherOversizedPayloadChunked(t *testing.T) {
	t.Setenv("EIDETIC_DATA_DIR", t.TempDir())
	st, err := store.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	dir := t.TempDir()
	state, _ := LoadState(filepath.Join(t.TempDir(), "state.json"))
	w := NewWatcher(st, state, []SurfaceConfig{
		{Surface: "claude_code", Root: dir, Glob: "*.jsonl", Parser: NewJSONLParser("claude_code")},
	}, 5*time.Millisecond)
	doneCh := make(chan ParseDone, 16)
	w.SetParseDoneChannel(doneCh)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)

	// One in-bounds record + one oversized record (just over the per-engram
	// cap). The oversized line will be split into ⌈(MaxPayloadBytes+1) /
	// chunkPayloadBudget⌉ chunks by the JSONL parser.
	huge := strings.Repeat("x", store.MaxPayloadBytes+1)
	p := filepath.Join(dir, "burst.jsonl")
	body := fmt.Sprintf(`{"i":0,"payload":"small"}`+"\n"+`{"i":1,"payload":%q}`+"\n", huge)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	// Wait for at least 3 engrams: 1 small + 2 chunks (8 MiB+ over a 7 MiB
	// budget → 2 chunks) at minimum.
	waitFor(t, doneCh, 3, 4*time.Second)
	drainExtra(doneCh, 500*time.Millisecond)

	// Defense-in-depth counter should remain 0 — chunks fit by construction.
	if got := w.SkippedPayloadTooLarge(); got != 0 {
		t.Errorf("SkippedPayloadTooLarge() = %d, want 0 (chunking handles oversized; pre-filter is defense-only)", got)
	}

	rows, err := st.Retrieve(context.Background(), "claude_code", 0, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	// Expect: 1 small + 2 chunks (the JSON-encoded huge line is slightly
	// over MaxPayloadBytes due to the wrapping `{"i":1,"payload":"..."}`,
	// so split into 2 chunks at 7 MiB each).
	if len(rows) < 3 {
		t.Errorf("got %d rows, want ≥ 3 (1 small + chunked oversized)", len(rows))
	}

	// Find the small + chunk rows by scanning meta.
	var smallCount, chunkCount int
	chunkIDs := map[string]int{}
	for _, r := range rows {
		if strings.Contains(r.Meta, `"chunk_id":`) {
			chunkCount++
			// Extract chunk_id for collision check.
			i := strings.Index(r.Meta, `"chunk_id":"`)
			if i >= 0 {
				rest := r.Meta[i+12:]
				j := strings.IndexByte(rest, '"')
				if j > 0 {
					chunkIDs[rest[:j]]++
				}
			}
		} else if strings.Contains(r.Payload, "small") {
			smallCount++
		}
	}
	if smallCount != 1 {
		t.Errorf("smallCount = %d, want 1", smallCount)
	}
	if chunkCount < 2 {
		t.Errorf("chunkCount = %d, want ≥ 2", chunkCount)
	}
	if len(chunkIDs) != 1 {
		t.Errorf("expected exactly 1 distinct chunk_id (all chunks share one); got %d (%v)", len(chunkIDs), chunkIDs)
	}
}

// Compile-time assertion: capture imports store, so the cap value is
// single-source-of-truth. Drift between capture's pre-filter and store's
// validateEngram is impossible if both reference store.MaxPayloadBytes.
var _ = func() bool {
	_ = engram.Engram{}
	_ = store.MaxPayloadBytes
	return true
}()
