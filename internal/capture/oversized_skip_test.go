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

// TestWatcherOversizedPayloadCounted asserts the per-path serializer + the
// pre-filter against store.MaxPayloadBytes both fire: oversized records are
// NOT inserted (would poison the whole batch via InsertBatch validation) BUT
// the rest of the batch lands, and the watcher's atomic skip-counter
// increments. Closes cc-tb SPIKE-RESULT finding #2 (relay 20260513_152711).
func TestWatcherOversizedPayloadCounted(t *testing.T) {
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

	// One in-bounds record + one oversized record (just over the cap).
	huge := strings.Repeat("x", store.MaxPayloadBytes+1)
	p := filepath.Join(dir, "burst.jsonl")
	body := fmt.Sprintf(`{"i":0,"payload":"small"}`+"\n"+`{"i":1,"payload":%q}`+"\n", huge)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	waitFor(t, doneCh, 1, 2*time.Second)
	drainExtra(doneCh, 500*time.Millisecond)

	// Counter should be 1 (the oversized record).
	if got := w.SkippedPayloadTooLarge(); got != 1 {
		t.Errorf("SkippedPayloadTooLarge() = %d, want 1", got)
	}

	// Store should hold the small record (NOT the oversized one). If the
	// pre-filter were missing, InsertBatch's pre-tx validation would have
	// failed the WHOLE batch and 0 rows would be present.
	rows, err := st.Retrieve(context.Background(), "claude_code", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Errorf("got %d rows, want exactly 1 (small record only; oversized skipped not failed-whole-batch)", len(rows))
	}
	if len(rows) > 0 && !strings.Contains(rows[0].Payload, "small") {
		t.Errorf("expected small-payload row, got %q", rows[0].Payload[:50])
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
