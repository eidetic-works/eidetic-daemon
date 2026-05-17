package capture

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/eidetic-works/eidetic-daemon/internal/engram"
	"github.com/eidetic-works/eidetic-daemon/internal/store"
)

// memSink records every batch into a slice. Thread-safe for concurrent
// inserts from the watcher's parseAndCommit goroutine.
type memSink struct {
	mu sync.Mutex
	in []engram.Engram
}

func (m *memSink) InsertBatch(_ context.Context, b []engram.Engram) error {
	m.mu.Lock()
	m.in = append(m.in, b...)
	m.mu.Unlock()
	return nil
}

func (m *memSink) all() []engram.Engram {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]engram.Engram, len(m.in))
	copy(out, m.in)
	return out
}

func newWatcherForSurface(t *testing.T, sink Sink, surface, dir, glob string, parser Parser, debounce time.Duration) (*Watcher, chan ParseDone, context.CancelFunc) {
	t.Helper()
	state, err := LoadState(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := []SurfaceConfig{{Surface: surface, Root: dir, Glob: glob, Parser: parser}}
	w := NewWatcher(sink, state, cfg, debounce)
	doneCh := make(chan ParseDone, 64)
	w.SetParseDoneChannel(doneCh)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = w.Run(ctx) }()
	// Allow fsnotify to attach.
	time.Sleep(50 * time.Millisecond)
	return w, doneCh, cancel
}

func waitFor(t *testing.T, ch chan ParseDone, want int, timeout time.Duration) []ParseDone {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	got := make([]ParseDone, 0, want)
	totalCount := 0
	for totalCount < want {
		select {
		case ev := <-ch:
			got = append(got, ev)
			totalCount += ev.Count
		case <-deadline.C:
			t.Fatalf("timeout waiting for %d engrams; got %d events: %+v", want, totalCount, got)
		}
	}
	return got
}

// drainExtra drains any spurious initial-scan parse events for the given file.
// fsnotify on macOS often coalesces creation events with later writes, so a
// test that creates+appends may see the file twice.
func drainExtra(ch chan ParseDone, settle time.Duration) {
	t := time.NewTimer(settle)
	defer t.Stop()
	for {
		select {
		case <-ch:
		case <-t.C:
			return
		}
	}
}

func TestWatcherEndToEndJSONLAppend(t *testing.T) {
	dir := t.TempDir()
	sink := &memSink{}
	_, doneCh, cancel := newWatcherForSurface(t, sink, "claude_code", dir, "*.jsonl", NewJSONLParser("claude_code"), 5*time.Millisecond)
	defer cancel()

	p := filepath.Join(dir, "session.jsonl")
	start := time.Now()
	if err := os.WriteFile(p, []byte(`{"a":1}`+"\n"+`{"b":2}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	events := waitFor(t, doneCh, 2, 2*time.Second)
	elapsed := time.Since(start)

	all := sink.all()
	if len(all) != 2 {
		t.Errorf("got %d engrams in sink, want 2", len(all))
	}
	// Spec section 2.3: <50ms target. Build-tag split: tight 100ms margin
	// without -race (CI gate); 500ms with -race (detector overhead 2-20×).
	// Closes PR#1 review hole-poke #3 — prior 500ms-everywhere margin masked
	// any regression up to 200ms.
	margin := 100 * time.Millisecond
	if isRaceMode {
		margin = 500 * time.Millisecond
	}
	if elapsed > margin {
		t.Errorf("end-to-end latency %v exceeded %v margin (race=%v; spec target 50ms)",
			elapsed, margin, isRaceMode)
	}
	// Confirm at least one event was emitted with non-zero count.
	any := false
	for _, e := range events {
		if e.Count > 0 {
			any = true
		}
	}
	if !any {
		t.Errorf("no parse events with Count>0 in %+v", events)
	}
}

func TestWatcherIncrementalAppendUsesOffset(t *testing.T) {
	dir := t.TempDir()
	sink := &memSink{}
	_, doneCh, cancel := newWatcherForSurface(t, sink, "claude_code", dir, "*.jsonl", NewJSONLParser("claude_code"), 5*time.Millisecond)
	defer cancel()

	p := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(p, []byte(`{"a":1}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, doneCh, 1, 2*time.Second)
	drainExtra(doneCh, 100*time.Millisecond)

	// Append a second record.
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte(`{"b":2}` + "\n")); err != nil {
		t.Fatal(err)
	}
	f.Close()

	waitFor(t, doneCh, 1, 2*time.Second)
	all := sink.all()
	if len(all) != 2 {
		t.Errorf("got %d engrams, want 2 (no re-ingest of first record)", len(all))
	}
	if all[1].Payload != `{"b":2}` {
		t.Errorf("second engram payload = %q, want {b:2}", all[1].Payload)
	}
}

func TestWatcherMultiSurface(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	sink := &memSink{}
	state, err := LoadState(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfgs := []SurfaceConfig{
		{Surface: "claude_code", Root: dir1, Glob: "*.jsonl", Parser: NewJSONLParser("claude_code")},
		{Surface: "cowork", Root: dir2, Glob: "*.json", Parser: NewJSONLParser("cowork")},
	}
	w := NewWatcher(sink, state, cfgs, 5*time.Millisecond)
	doneCh := make(chan ParseDone, 64)
	w.SetParseDoneChannel(doneCh)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)

	if err := os.WriteFile(filepath.Join(dir1, "a.jsonl"), []byte(`{"a":1}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir2, "b.json"), []byte(`{"b":2}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, doneCh, 2, 2*time.Second)

	all := sink.all()
	if len(all) != 2 {
		t.Errorf("got %d engrams, want 2", len(all))
	}
	surfaces := map[string]bool{}
	for _, e := range all {
		surfaces[e.Surface] = true
	}
	if !surfaces["claude_code"] || !surfaces["cowork"] {
		t.Errorf("missing surfaces: got %v", surfaces)
	}
}

func TestWatcherRespectsGlob(t *testing.T) {
	dir := t.TempDir()
	sink := &memSink{}
	_, doneCh, cancel := newWatcherForSurface(t, sink, "claude_code", dir, "*.jsonl", NewJSONLParser("claude_code"), 5*time.Millisecond)
	defer cancel()

	if err := os.WriteFile(filepath.Join(dir, "ignored.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "kept.jsonl"), []byte(`{"a":1}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, doneCh, 1, 2*time.Second)
	drainExtra(doneCh, 200*time.Millisecond)

	all := sink.all()
	if len(all) != 1 || all[0].Payload != `{"a":1}` {
		t.Errorf("glob filter failed: %+v", all)
	}
}

func TestWatcherSkipsMissingRoot(t *testing.T) {
	sink := &memSink{}
	state, _ := LoadState(filepath.Join(t.TempDir(), "state.json"))
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	w := NewWatcher(sink, state, []SurfaceConfig{
		{Surface: "cowork", Root: missing, Glob: "*.json", Parser: NewJSONLParser("cowork")},
	}, 5*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := w.Run(ctx); err != nil {
		t.Errorf("Run errored on missing root: %v", err)
	}
}

func TestWatcherIntegrationWithRealStore(t *testing.T) {
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
	doneCh := make(chan ParseDone, 16)
	w.SetParseDoneChannel(doneCh)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)

	if err := os.WriteFile(filepath.Join(dir, "x.jsonl"), []byte(`{"hello":"world"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, doneCh, 1, 2*time.Second)

	rows, err := st.Retrieve(context.Background(), "claude_code", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Payload != `{"hello":"world"}` {
		t.Errorf("real-store integration failed: %+v", rows)
	}
}

func TestWatcherConcurrentSurfaces(t *testing.T) {
	t.Setenv("EIDETIC_DATA_DIR", t.TempDir())
	st, err := store.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	d1 := t.TempDir()
	d2 := t.TempDir()
	d3 := t.TempDir()
	state, _ := LoadState(filepath.Join(t.TempDir(), "state.json"))
	w := NewWatcher(st, state, []SurfaceConfig{
		{Surface: "claude_code", Root: d1, Glob: "*.jsonl", Parser: NewJSONLParser("claude_code")},
		{Surface: "cowork", Root: d2, Glob: "*.json", Parser: NewJSONLParser("cowork")},
		{Surface: "cursor", Root: d3, Glob: "*.json", Parser: NewCursorParser("cursor")},
	}, 5*time.Millisecond)
	doneCh := make(chan ParseDone, 64)
	w.SetParseDoneChannel(doneCh)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)

	const perSurface = 5
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < perSurface; i++ {
			os.WriteFile(filepath.Join(d1, "a.jsonl"), []byte(`{"i":`+itoa(i)+`}`+"\n"), 0o644)
			time.Sleep(10 * time.Millisecond)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < perSurface; i++ {
			os.WriteFile(filepath.Join(d2, "b.json"), []byte(`{"j":`+itoa(i)+`}`+"\n"), 0o644)
			time.Sleep(10 * time.Millisecond)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < perSurface; i++ {
			os.WriteFile(filepath.Join(d3, "c.json"), []byte(`{"k":`+itoa(i)+`}`), 0o644)
			time.Sleep(10 * time.Millisecond)
		}
	}()
	wg.Wait()
	// Allow watcher to drain.
	drainExtra(doneCh, 500*time.Millisecond)

	// Each surface should have produced ≥1 row (debounce may collapse some).
	for _, s := range []string{"claude_code", "cowork", "cursor"} {
		rows, err := st.Retrieve(context.Background(), s, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 0 {
			t.Errorf("surface %q: no rows captured", s)
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := []byte{}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}
