package capture

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eidetic-works/eidetic-daemon/internal/engram"
	"github.com/eidetic-works/eidetic-daemon/internal/store"
	"github.com/fsnotify/fsnotify"
)

// Sink is the subset of *store.Store the watcher needs. Defined as an
// interface so tests can supply an in-memory recorder.
type Sink interface {
	InsertBatch(ctx context.Context, batch []engram.Engram) error
}

// Watcher multiplexes fsnotify events across configured surfaces, debounces
// them per-file, runs the per-surface parser, batches the resulting engrams
// into the writer pool, and persists offset state.
type Watcher struct {
	sink     Sink
	state    *State
	configs  []SurfaceConfig
	bySurface map[string]Parser

	debounce time.Duration
	clock    func() time.Time

	// debounceMu guards pending; pending maps file path → timer firing the parse.
	debounceMu sync.Mutex
	pending    map[string]*time.Timer

	// parseMu serializes parseAndCommit per-path. Without this, two
	// debounce timers can fire `parseAndCommit` for the same path
	// concurrently, and the second can read state.Get BEFORE the first
	// writes state.Set, causing double-insert of the same bytes. See
	// TestWatcherBurstNoDoubleInsert (regression test for the burst-flood
	// hole-poke from PR#1 review).
	//
	// Map values are *sync.Mutex; we use sync.Map for lock-free reads of
	// the per-path mutex itself.
	parseMu sync.Map

	// skippedPayloadTooLarge counts engrams dropped because their payload
	// exceeded store.MaxPayloadBytes. Updated atomically by parseAndCommit;
	// read via SkippedPayloadTooLarge() for telemetry/test assertions.
	// Per a 2026-05-13 runtime spike: real Claude Code session JSONLs
	// produce chunks up to 2.41 MiB; cap raised to 8 MiB but skips still
	// possible on outliers — surface count so users see data loss.
	skippedPayloadTooLarge uint64

	// inflight tracks parseAndCommit goroutines spawned via scheduleParse
	// AfterFunc and synchronous scanInitial calls. Run blocks on this
	// before returning so the caller (main()) can safely close the store
	// without racing in-flight InsertBatch calls. See issue #17 (v0.0.5
	// shutdown race: ~30 "database is closed" errors per stop).
	inflight sync.WaitGroup

	// instrumentation for tests / observability
	parseDoneCh chan ParseDone
}

// SkippedPayloadTooLarge returns the count of engrams skipped this session
// due to payload exceeding store.MaxPayloadBytes. Atomic read.
func (w *Watcher) SkippedPayloadTooLarge() uint64 {
	return atomic.LoadUint64(&w.skippedPayloadTooLarge)
}

// ParseDone is emitted on parseDoneCh after each successful parse + commit
// pair. Useful for integration tests asserting end-to-end latency.
type ParseDone struct {
	Surface string
	Path    string
	Count   int
	Latency time.Duration
}

// NewWatcher constructs a Watcher. debounce defaults to 10ms (per spec
// section 2.3 + ADR-013 #2 fsnotify event coalescing concern).
func NewWatcher(sink Sink, state *State, configs []SurfaceConfig, debounce time.Duration) *Watcher {
	if debounce <= 0 {
		debounce = 10 * time.Millisecond
	}
	bySurface := make(map[string]Parser, len(configs))
	for _, c := range configs {
		bySurface[c.Surface] = c.Parser
	}
	return &Watcher{
		sink:      sink,
		state:     state,
		configs:   configs,
		bySurface: bySurface,
		debounce:  debounce,
		clock:     time.Now,
		pending:   map[string]*time.Timer{},
	}
}

// SetParseDoneChannel enables emission of ParseDone events. Tests use this
// to wait for the watcher to finish processing without sleeping.
//
// Caller is responsible for draining the channel (it is NOT buffered for
// production usage to avoid silent backpressure).
func (w *Watcher) SetParseDoneChannel(ch chan ParseDone) {
	w.parseDoneCh = ch
}

// Run starts an fsnotify watcher, registers each existing surface root, walks
// each root recursively to seed initial-state parsing, and blocks on the
// event loop until ctx is done.
//
// Shutdown contract (issue #17): Run does NOT return until all in-flight
// parseAndCommit goroutines have completed. Callers (main()) may safely
// close the underlying store immediately after Run returns without racing
// pending InsertBatch calls.
func (w *Watcher) Run(ctx context.Context) error {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer fw.Close()
	// Drain in-flight parse goroutines before returning. This pairs with
	// inflight.Add(1) in scheduleParse + scanInitial so the store survives
	// past the last InsertBatch. See issue #17.
	defer w.inflight.Wait()

	for _, c := range w.configs {
		if ctx.Err() != nil {
			// SIGTERM during initial walk: bail before adding more work.
			break
		}
		if err := w.addRoot(fw, c); err != nil {
			// Per spec section 2.3 + audit: missing surface dir on this host is
			// expected (e.g., Cowork dir absent). Log and continue.
			log.Printf("capture: skip surface %q (root %s): %v", c.Surface, c.Root, err)
			continue
		}
		w.scanInitial(ctx, c)
	}

	for {
		select {
		case <-ctx.Done():
			w.flushAll(ctx)
			return nil
		case err, ok := <-fw.Errors:
			if !ok {
				return nil
			}
			log.Printf("capture: fsnotify error: %v", err)
		case ev, ok := <-fw.Events:
			if !ok {
				return nil
			}
			w.onEvent(ctx, fw, ev)
		}
	}
}

func (w *Watcher) addRoot(fw *fsnotify.Watcher, c SurfaceConfig) error {
	stat, err := os.Stat(c.Root)
	if err != nil {
		return err
	}
	if !stat.IsDir() {
		return errors.New("not a directory")
	}
	// Recursive walk: fsnotify on macOS does not auto-recurse.
	return filepath.Walk(c.Root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if info.IsDir() {
			return fw.Add(path)
		}
		return nil
	})
}

// scanInitial does a one-shot parse of all matching files at startup. This
// catches writes that happened while the daemon was down.
//
// Bails on ctx cancellation (issue #17) so SIGTERM during a hot ~/.claude/
// projects walk doesn't keep firing parseAndCommit after main() has begun
// shutdown. Each parseAndCommit call is bracketed by inflight.Add/Done so
// Run.defer can drain them.
func (w *Watcher) scanInitial(ctx context.Context, c SurfaceConfig) {
	_ = filepath.Walk(c.Root, func(path string, info os.FileInfo, walkErr error) error {
		if ctx.Err() != nil {
			return filepath.SkipAll
		}
		if walkErr != nil || info.IsDir() {
			return nil
		}
		if !w.matches(c, path) {
			return nil
		}
		w.inflight.Add(1)
		func() {
			defer w.inflight.Done()
			w.parseAndCommit(ctx, c.Surface, path)
		}()
		return nil
	})
}

func (w *Watcher) matches(c SurfaceConfig, path string) bool {
	if c.Glob == "" {
		return true
	}
	ok, err := filepath.Match(c.Glob, filepath.Base(path))
	return err == nil && ok
}

func (w *Watcher) onEvent(ctx context.Context, fw *fsnotify.Watcher, ev fsnotify.Event) {
	if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Chmod) == 0 {
		return
	}
	// New directory: register so we receive its child events too.
	if ev.Op&fsnotify.Create != 0 {
		if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
			_ = fw.Add(ev.Name)
			return
		}
	}

	// Find the surface this path belongs to.
	for _, c := range w.configs {
		if !strings.HasPrefix(ev.Name, c.Root) || !w.matches(c, ev.Name) {
			continue
		}
		w.scheduleParse(ctx, c.Surface, ev.Name)
		return
	}
}

// scheduleParse coalesces bursts: if multiple events arrive for the same
// path within w.debounce, only one parseAndCommit fires. Per ADR-013 #2.
//
// inflight.Add(1) reserves a slot at SCHEDULE time, not fire time, so a
// debounce timer that fires concurrently with shutdown is still tracked.
// Stopping the timer (via flushAll) decrements the slot if the AfterFunc
// did not run; otherwise the AfterFunc decrements via defer.
func (w *Watcher) scheduleParse(ctx context.Context, surface, path string) {
	w.debounceMu.Lock()
	if t, ok := w.pending[path]; ok {
		// If we successfully Stop() the prior timer, its AfterFunc never
		// runs and we own its inflight slot — release it so totals balance.
		if t.Stop() {
			w.inflight.Done()
		}
	}
	w.inflight.Add(1)
	w.pending[path] = time.AfterFunc(w.debounce, func() {
		defer w.inflight.Done()
		w.debounceMu.Lock()
		delete(w.pending, path)
		w.debounceMu.Unlock()
		w.parseAndCommit(ctx, surface, path)
	})
	w.debounceMu.Unlock()
}

func (w *Watcher) parseAndCommit(ctx context.Context, surface, path string) {
	// Per-path mutex serializes concurrent parseAndCommit calls for the
	// same path. See parseMu doc on the Watcher struct for the race this
	// guards against.
	muIfaceNew := &sync.Mutex{}
	muIface, _ := w.parseMu.LoadOrStore(path, muIfaceNew)
	mu := muIface.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	start := w.clock()
	parser, ok := w.bySurface[surface]
	if !ok {
		return
	}
	from := w.state.Get(surface, path)
	engrams, newOffset, err := parser.Parse(path, from)
	if err != nil {
		log.Printf("capture: parse %s %s: %v", surface, path, err)
		return
	}
	if len(engrams) > 0 {
		// Pre-filter oversized payloads so one too-large record doesn't fail
		// the entire batch (InsertBatch validates pre-tx and rolls back on any
		// row violation). Surface the skip count via log + Watcher counter so
		// users see the data loss they're getting. Cap value tracked in
		// store.MaxPayloadBytes; capture mirrors the gate to avoid one-row
		// batch poisoning. See ADR-017 (2026-05-13 runtime spike).
		filtered := engrams[:0]
		skipped := 0
		for _, e := range engrams {
			if len(e.Payload) > store.MaxPayloadBytes {
				skipped++
				continue
			}
			filtered = append(filtered, e)
		}
		if skipped > 0 {
			atomic.AddUint64(&w.skippedPayloadTooLarge, uint64(skipped))
			log.Printf("capture: %s %s: skipped %d oversized engrams (>%d bytes); total skipped this session: %d",
				surface, path, skipped, store.MaxPayloadBytes,
				atomic.LoadUint64(&w.skippedPayloadTooLarge))
		}
		if len(filtered) > 0 {
			if err := w.sink.InsertBatch(ctx, filtered); err != nil {
				log.Printf("capture: insert %s: %v", surface, err)
				return
			}
		}
	}
	if newOffset != from {
		w.state.Set(surface, path, newOffset)
		if err := w.state.Save(); err != nil {
			log.Printf("capture: state save: %v", err)
		}
	}
	if w.parseDoneCh != nil {
		w.parseDoneCh <- ParseDone{
			Surface: surface,
			Path:    path,
			Count:   len(engrams),
			Latency: w.clock().Sub(start),
		}
	}
}

// flushAll is called on ctx cancellation. It stops all pending debounce
// timers (so future fires are cancelled) and decrements inflight for each
// timer it successfully stopped before it could fire. Run.defer waits on
// the remaining inflight (already-fired AfterFunc goroutines) before
// returning. See issue #17.
func (w *Watcher) flushAll(ctx context.Context) {
	w.debounceMu.Lock()
	pendings := make([]*time.Timer, 0, len(w.pending))
	for _, t := range w.pending {
		pendings = append(pendings, t)
	}
	w.pending = map[string]*time.Timer{}
	w.debounceMu.Unlock()
	for _, t := range pendings {
		if t.Stop() {
			// AfterFunc never ran — its inflight slot is ours to release.
			w.inflight.Done()
		}
	}
	_ = w.state.Save()
	_ = ctx
}

// DefaultSurfaces returns the spec section 2.3 surface list with paths
// resolved against the user home directory. Missing dirs are NOT pruned
// here (Run logs and skips them).
func DefaultSurfaces() []SurfaceConfig {
	home, _ := os.UserHomeDir()
	return []SurfaceConfig{
		{
			Surface: "claude_code",
			Root:    claudeRoot(home),
			Glob:    "*.jsonl",
			Parser:  NewJSONLParser("claude_code"),
		},
		{
			Surface: "cowork",
			Root:    filepath.Join(home, ".cowork", "sessions"),
			Glob:    "*.json",
			Parser:  NewJSONLParser("cowork"),
		},
		{
			Surface: "cursor",
			Root:    cursorRoot(home),
			Glob:    "*.json",
			Parser:  NewCursorParser("cursor"),
		},
	}
}

// Compile-time assertion that *store.Store satisfies Sink.
var _ Sink = (*store.Store)(nil)
