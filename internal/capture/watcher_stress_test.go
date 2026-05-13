package capture

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/eidetic-works/eidetic-daemon/internal/store"
)

// TestWatcherBurstNoDoubleInsert exercises peer hole-poke #1
// (relay_20260513_072250_eee4993e §1):
//
//	"the real fsnotify-event-flood pattern under burst sessions could collapse
//	the headroom. Look at internal/capture/watcher.go scheduleParse debounce
//	— does its 10ms timer-stack handle 100+ events/sec without leaking
//	timers? Check the pending map under contention."
//
// We APPEND N=200 lines to one JSONL file, one line per ~5ms, while the
// watcher is running. Expected: store row count = exactly N (one per line).
// Pre-fix: parseAndCommit can race with state.Get and double-insert lines
// already consumed by an in-flight prior parse. Post-fix: per-path serial
// execution eliminates the race.
//
// This test is the regression gate for the per-path serializer.
func TestWatcherBurstNoDoubleInsert(t *testing.T) {
	t.Setenv("EIDETIC_DATA_DIR", t.TempDir())
	st, err := store.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	dir := t.TempDir()
	state, err := LoadState(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	w := NewWatcher(st, state, []SurfaceConfig{
		{Surface: "claude_code", Root: dir, Glob: "*.jsonl", Parser: NewJSONLParser("claude_code")},
	}, 5*time.Millisecond)
	doneCh := make(chan ParseDone, 1024)
	w.SetParseDoneChannel(doneCh)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)

	const N = 200
	p := filepath.Join(dir, "burst.jsonl")
	// Touch the file once so fsnotify is watching.
	if err := os.WriteFile(p, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	// Drain the empty-file event before starting the burst.
	drainExtra(doneCh, 100*time.Millisecond)

	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	for i := 0; i < N; i++ {
		// Each line is unique so we can detect duplicates by content.
		if _, err := fmt.Fprintf(f, `{"i":%d,"payload":"unique-%06d"}`+"\n", i, i); err != nil {
			t.Fatal(err)
		}
		// 5ms ≈ 200/sec — well above the 100/sec spec target for ADR-014 gap A.
		time.Sleep(5 * time.Millisecond)
	}

	// Allow the watcher to fully drain before the assertion.
	drainExtra(doneCh, 1*time.Second)

	rows, err := st.Retrieve(context.Background(), "claude_code", 0, N*4)
	if err != nil {
		t.Fatal(err)
	}

	if len(rows) != N {
		t.Errorf("got %d rows in store, want exactly %d (no double-insert from race)", len(rows), N)
	}

	// Strong assertion: no duplicate payloads.
	seen := make(map[string]int, N)
	for _, r := range rows {
		seen[r.Payload]++
	}
	dupes := 0
	for payload, count := range seen {
		if count > 1 {
			dupes++
			if dupes <= 3 {
				t.Errorf("payload duplicated %d times: %s", count, payload)
			}
		}
	}
	if dupes > 3 {
		t.Errorf("(...%d more duplicate payloads — race is real, fix incomplete)", dupes-3)
	}
}

// TestWatcherPendingMapBounded asserts the debounce map size never exceeds
// the count of distinct watched files, even under burst. A timer leak would
// cause unbounded growth.
func TestWatcherPendingMapBounded(t *testing.T) {
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
	doneCh := make(chan ParseDone, 1024)
	w.SetParseDoneChannel(doneCh)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)

	const files = 3
	const eventsPerFile = 100
	paths := make([]string, files)
	for i := 0; i < files; i++ {
		paths[i] = filepath.Join(dir, fmt.Sprintf("f%d.jsonl", i))
		if err := os.WriteFile(paths[i], []byte{}, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	drainExtra(doneCh, 100*time.Millisecond)

	var wg sync.WaitGroup
	wg.Add(files)
	for i := 0; i < files; i++ {
		go func(idx int) {
			defer wg.Done()
			f, err := os.OpenFile(paths[idx], os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				return
			}
			defer f.Close()
			for j := 0; j < eventsPerFile; j++ {
				fmt.Fprintf(f, `{"file":%d,"i":%d}`+"\n", idx, j)
				time.Sleep(2 * time.Millisecond)
			}
		}(i)
	}
	wg.Wait()
	drainExtra(doneCh, 1*time.Second)

	w.debounceMu.Lock()
	pendingSize := len(w.pending)
	w.debounceMu.Unlock()

	if pendingSize > files {
		t.Errorf("pending map size %d exceeds watched-file count %d (timer leak)", pendingSize, files)
	}

	rows, _ := st.Retrieve(context.Background(), "claude_code", 0, files*eventsPerFile*2)
	if len(rows) != files*eventsPerFile {
		t.Errorf("got %d rows, want %d (file_count*events_per_file)", len(rows), files*eventsPerFile)
	}
}
