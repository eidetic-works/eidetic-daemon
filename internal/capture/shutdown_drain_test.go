package capture

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eidetic-works/eidetic-daemon/internal/engram"
)

// closableSink mimics the production *store.Store from a shutdown-race
// perspective: InsertBatch can be called concurrently with Close, and
// post-Close calls return a "closed" error analogous to modernc.org/sqlite's
// "sql: database is closed". The post-Close call counter feeds the
// regression assertion in TestWatcherDrainsBeforeShutdown.
type closableSink struct {
	mu             sync.Mutex
	closed         bool
	postCloseCalls atomic.Int64
	insertDelay    time.Duration
	totalCalls     atomic.Int64
}

func (s *closableSink) InsertBatch(_ context.Context, b []engram.Engram) error {
	s.totalCalls.Add(1)
	// Hold the call long enough that a hostile shutdown sequence can race
	// the close. 5ms is well above scheduler jitter and far below test
	// timeouts; without the bug fix, Close runs while we're sleeping here.
	if s.insertDelay > 0 {
		time.Sleep(s.insertDelay)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		s.postCloseCalls.Add(1)
		return errors.New("sink closed (analog of modernc 'database is closed')")
	}
	_ = b
	return nil
}

func (s *closableSink) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
}

// TestWatcherDrainsBeforeShutdown reproduces issue #17 (the v0.0.5 shutdown
// race that fired ~30 "database is closed" errors per stop) and asserts
// the v0.0.6 fix:
//
//   - Watcher.Run does NOT return while in-flight parseAndCommit goroutines
//     are still calling sink.InsertBatch.
//   - Therefore the caller can safely close the sink immediately after Run
//     returns, with zero racing calls landing on the closed sink.
//
// The test arranges the worst case: many JSONL files queued for the
// initial scan, plus a slow sink so each InsertBatch is in-flight for
// 5ms. SIGTERM equivalent (ctx cancel) fires while scanInitial is mid-walk.
// Pre-fix: post-close calls > 0; post-fix: post-close calls == 0.
func TestWatcherDrainsBeforeShutdown(t *testing.T) {
	dir := t.TempDir()
	// 40 JSONL files keeps the walk busy long enough that ctx cancel
	// during scanInitial is virtually guaranteed to land mid-loop.
	for i := 0; i < 40; i++ {
		path := filepath.Join(dir, fmt.Sprintf("file_%03d.jsonl", i))
		// One small JSON record per file — exercises parseAndCommit
		// without exercising chunked-capture paths.
		content := `{"timestamp":"2026-05-14T16:00:00Z","sessionId":"drain-test","payload":"x"}` + "\n"
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	sink := &closableSink{insertDelay: 5 * time.Millisecond}
	state, err := LoadState(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := []SurfaceConfig{{
		Surface: "claude_code",
		Root:    dir,
		Glob:    "*.jsonl",
		Parser:  NewJSONLParser("claude_code"),
	}}
	w := NewWatcher(sink, state, cfg, 10*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = w.Run(ctx)
	}()

	// Cancel context just early enough that scanInitial is virtually
	// certain to be mid-walk. 20ms > 50ms-debounce-attach but ≪ time
	// needed to walk + insert all 40 files at 5ms/insert (~200ms minimum).
	time.Sleep(20 * time.Millisecond)
	cancel()

	// Run must return only after all in-flight InsertBatch calls drain.
	select {
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("watcher.Run did not return within 5s of ctx cancel — drain deadlock?")
	}

	// CRITICAL POST-CONDITION (issue #17 contract): close the sink
	// immediately after Run returns, then verify nothing further calls
	// InsertBatch on the now-closed sink. Pre-fix this would catch the
	// in-flight goroutines and post-close calls would be > 0.
	sink.Close()
	// Generous settle window — any goroutine that escaped Run.defer would
	// land here. Without the fix, we observe non-zero PostCloseCalls.
	time.Sleep(100 * time.Millisecond)

	if got := sink.postCloseCalls.Load(); got > 0 {
		t.Fatalf("issue #17 regression: %d InsertBatch calls landed on closed sink (expected 0). Total calls: %d",
			got, sink.totalCalls.Load())
	}
	t.Logf("drain ok: %d total InsertBatch calls, 0 post-close races", sink.totalCalls.Load())
}

// TestWatcherDrainBalancesInflight asserts the inflight WaitGroup arithmetic
// does not under/overflow across many scheduleParse cycles where some timers
// are stopped (replaced) before firing and others fire to completion.
//
// Without the fix, the symptoms would be:
//
//   - Negative WaitGroup counter (panic) if Done is called more often than Add
//   - Hung Run.defer wait if Add outpaces Done
//
// Both manifest as test deadlock or panic; passing this test is the
// contract that scheduleParse + flushAll + AfterFunc balance.
func TestWatcherDrainBalancesInflight(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "burst.jsonl")
	if err := os.WriteFile(path, []byte(`{"sessionId":"x","payload":"y"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	sink := &closableSink{}
	state, err := LoadState(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := []SurfaceConfig{{
		Surface: "claude_code",
		Root:    dir,
		Glob:    "*.jsonl",
		Parser:  NewJSONLParser("claude_code"),
	}}
	w := NewWatcher(sink, state, cfg, 50*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = w.Run(ctx)
	}()

	// Synthetic burst: many scheduleParse calls for the same path within
	// the debounce window. Each one Stops the previous timer (releasing
	// its inflight slot) and Adds a fresh one. Total +N Add, -N Done from
	// scheduleParse itself, plus +1 Add / -1 Done from the final firing.
	time.Sleep(60 * time.Millisecond) // let scanInitial settle
	for i := 0; i < 50; i++ {
		w.scheduleParse(ctx, "claude_code", path)
	}

	cancel()
	select {
	case <-runDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return — inflight WG likely Wait()-deadlocked (Add > Done)")
	}
	// If we reached here without panic ("negative WaitGroup counter") and
	// without deadlock, the inflight arithmetic is balanced. The Run.defer
	// drain succeeded; nothing more to assert.
}
