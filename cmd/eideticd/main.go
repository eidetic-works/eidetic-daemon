// Command eideticd is the local daemon: opens the engram store and serves
// retrieval over a local Unix socket. Per docs/SPEC.md, the daemon is
// started by a service manager (launchd / systemd-user) at user login.
//
// Spawn-at-app-startup is mandatory per ADR-016: modernc.org/sqlite has
// a ~1.75s init cost on first open, which would be perceptually disruptive
// if amortized into a user request. The service manager swallows it
// behind login.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/eidetic-works/eidetic-daemon/internal/api"
	"github.com/eidetic-works/eidetic-daemon/internal/auth"
	"github.com/eidetic-works/eidetic-daemon/internal/capture"
	"github.com/eidetic-works/eidetic-daemon/internal/engram"
	"github.com/eidetic-works/eidetic-daemon/internal/hooks"
	eidetic_sync "github.com/eidetic-works/eidetic-daemon/internal/sync"
	"github.com/eidetic-works/eidetic-daemon/internal/store"
	"github.com/eidetic-works/eidetic-daemon/internal/textsearch"
	"github.com/eidetic-works/eidetic-daemon/internal/versioncheck"
)

const (
	defaultUDSPath = "/tmp/eidetic-daemon.sock"
	defaultTCPAddr = "127.0.0.1:9876"
)

// Version is set at build time via -ldflags "-X main.Version=vX.Y.Z" or
// defaults to "dev" for unreleased local builds. Single source of truth for
// the `-version` flag + any future telemetry/log-line that needs to identify
// the running binary.
var Version = "dev"

func main() {
	udsPath := flag.String("uds", "", "Unix domain socket path (overrides default)")
	tcpAddr := flag.String("tcp", "", "TCP listen address (overrides default; opt-in via EIDETIC_TCP=1)")
	bridgeAddr := flag.String("bridge", "", "TCP listen address for Cloudflare tunnel / Bridge (e.g. :8420); auth always-on + CORS enabled; runs alongside UDS server")
	authFlag := flag.Bool("auth", false, "enable Bearer-token caller authentication (also EIDETIC_AUTH=1); writes <dataDir>/auth-token (0600)")
	showVersion := flag.Bool("version", false, "print version and exit")
	syncNow := flag.Bool("sync-now", false, "upload engrams.db to Cloudflare R2 immediately (requires sync.json in dataDir) and exit")
	restoreFlag := flag.Bool("restore", false, "download latest engrams.db backup from Cloudflare R2 (requires sync.json in dataDir) and exit")
	showStats := flag.Bool("stats", false, "print engram database statistics and exit")
	checkSync := flag.Bool("check", false, "validate sync.json config and test Worker connectivity, then exit")
	showBackups := flag.Bool("backups", false, "list recent cloud backups from local sync history, then exit")
	showDigest := flag.String("digest", "", "print a recap of recent engrams (window: 24h | 7d | 30d), then exit. Reads the store directly — no daemon required.")
	askQuery := flag.String("ask", "", "ask a natural-language question, retrieve top engrams via FTS5, print answer-scaffolding to stdout. Reads the store directly — no daemon required.")
	captureFlag := flag.Bool("capture", false, "read stdin as an engram and insert into the local store; pair with -surface NAME. Reads the store directly — no daemon required.")
	captureSurface := flag.String("surface", "", "with -capture: surface tag for the engram (e.g. kubernetes, clipboard, browser). Required.")
	vacuumFlag := flag.Bool("vacuum", false, "run SQLite VACUUM on engrams.db to compact + reclaim space. Requires daemon to be down (write-lock). Reports before/after size.")
	installSvc := flag.Bool("install", false, "register eideticd as a login-time service (launchd on macOS, systemd-user on Linux) and exit")
	uninstallSvc := flag.Bool("uninstall", false, "stop + unregister the login-time service and optionally delete local data; inverse of -install")
	uninstallPurge := flag.Bool("purge", false, "with -uninstall: skip interactive confirm and delete <dataDir> (engrams.db, state, tokens)")
	initFlag := flag.Bool("init", false, "first-run interactive setup wizard — creates dataDir, detects surfaces, optionally registers service + pastes Pro sync.json, smoke-tests /healthz")
	initYes := flag.Bool("yes", false, "with -init: skip prompts and accept defaults (non-interactive setup)")
	flag.Parse()

	if *showVersion {
		fmt.Println("eideticd", Version)
		return
	}

	if *installSvc {
		if err := installService(); err != nil {
			log.Fatalf("install: %v", err)
		}
		return
	}

	if *initFlag {
		dd := os.Getenv("EIDETIC_DATA_DIR")
		if dd == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				log.Fatalf("init: resolve home: %v", err)
			}
			dd = filepath.Join(home, ".eidetic")
		}
		if err := initWizard(dd, *initYes); err != nil {
			log.Fatalf("init: %v", err)
		}
		return
	}

	if *uninstallSvc {
		// Resolve dataDir the same way the main loop does — env override + ~/.eidetic fallback.
		dd := os.Getenv("EIDETIC_DATA_DIR")
		if dd == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				log.Fatalf("uninstall: resolve home: %v", err)
			}
			dd = filepath.Join(home, ".eidetic")
		}
		if err := uninstallService(dd, *uninstallPurge); err != nil {
			log.Fatalf("uninstall: %v", err)
		}
		return
	}

	_ = syncNow // used below after store is open

	dataDir := os.Getenv("EIDETIC_DATA_DIR")
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("resolve home: %v", err)
		}
		dataDir = filepath.Join(home, ".eidetic")
	}
	dbPath := filepath.Join(dataDir, "engrams.db")

	// Ensure dataDir exists with 0700 perms BEFORE any file write
	// (auth-token, state.json, engrams.db all live under it).
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		log.Fatalf("mkdir dataDir %s: %v", dataDir, err)
	}

	// --vacuum: SQLite compaction. Daemon must be down (acquires write-lock).
	// Reports before/after size; safe to run periodically. v0.0.54+.
	if *vacuumFlag {
		fi, statErr := os.Stat(dbPath)
		var before int64
		if statErr == nil {
			before = fi.Size()
		}
		s, err := store.Open(dbPath)
		if err != nil {
			log.Fatalf("vacuum: open store: %v (is the daemon running? stop it first)", err)
		}
		fmt.Printf("vacuum: starting on %s (current: %.1f MB)\n", dbPath, float64(before)/1e6)
		if err := s.Vacuum(context.Background()); err != nil {
			s.Close()
			log.Fatalf("vacuum: %v", err)
		}
		s.Close()
		fi, _ = os.Stat(dbPath)
		after := fi.Size()
		saved := before - after
		fmt.Printf("vacuum: complete\n")
		fmt.Printf("  before: %.2f MB\n", float64(before)/1e6)
		fmt.Printf("  after:  %.2f MB\n", float64(after)/1e6)
		fmt.Printf("  saved:  %.2f MB (%.1f%%)\n", float64(saved)/1e6, 100.0*float64(saved)/float64(before))
		return
	}

	// --ask: query the store via FTS5, print top engrams + answer-scaffolding.
	// Works without a running daemon (read-only store open). v0.0.51+.
	if *askQuery != "" {
		s, err := store.Open(dbPath)
		if err != nil {
			log.Fatalf("ask: open store: %v", err)
		}
		defer s.Close()
		// Shared question→FTS heuristic (same as HTTP /ask + nucleus_ask).
		fts := textsearch.QuestionToFTS(*askQuery)
		ctx := context.Background()
		rows, err := s.Search(ctx, fts, "", 10)
		if err != nil {
			// fall back to bare query
			rows, err = s.Search(ctx, *askQuery, "", 10)
			if err != nil {
				log.Fatalf("ask: %v", err)
			}
		}
		fmt.Printf("Question: %s\n", *askQuery)
		fmt.Printf("FTS query: %s\n\n", fts)
		if len(rows) == 0 {
			fmt.Println("No engrams matched. Try broader keywords.")
			return
		}
		fmt.Printf("Top %d matching engrams (host LLM: synthesize an answer from these):\n\n", len(rows))
		for i, r := range rows {
			ts := time.Unix(0, r.TS).UTC().Format("2006-01-02 15:04")
			payload := r.Payload
			if len(payload) > 300 {
				payload = payload[:300] + "..."
			}
			payload = strings.ReplaceAll(payload, "\n", " ")
			fmt.Printf("[%d] [%s @ %s]\n  %s\n\n", i+1, r.Surface, ts, payload)
		}
		return
	}

	// --capture: read stdin as an engram and insert directly. v0.0.52+.
	if *captureFlag {
		if *captureSurface == "" {
			log.Fatal("capture: -surface required (e.g. -surface kubernetes)")
		}
		payload, err := io.ReadAll(os.Stdin)
		if err != nil {
			log.Fatalf("capture: read stdin: %v", err)
		}
		if len(payload) == 0 {
			log.Fatal("capture: stdin was empty")
		}
		s, err := store.Open(dbPath)
		if err != nil {
			log.Fatalf("capture: open store: %v", err)
		}
		defer s.Close()
		e := engram.Engram{
			Surface: *captureSurface,
			TS:      time.Now().UnixNano(),
			Payload: string(payload),
			Meta:    `{"source":"cli-capture"}`,
		}
		id, err := s.Insert(context.Background(), e)
		if err != nil {
			log.Fatalf("capture: insert: %v", err)
		}
		fmt.Printf("captured engram id=%d surface=%s bytes=%d\n", id, *captureSurface, len(payload))
		return
	}

	// --digest: read store + render the same recap shape as GET /digest.
	// Works without a running daemon (opens engrams.db read-only). v0.0.50+.
	if *showDigest != "" {
		var dur time.Duration
		switch *showDigest {
		case "24h":
			dur = 24 * time.Hour
		case "7d":
			dur = 7 * 24 * time.Hour
		case "30d":
			dur = 30 * 24 * time.Hour
		default:
			log.Fatalf("digest: window must be 24h | 7d | 30d (got %q)", *showDigest)
		}
		// Use a separate read-only opener: even if the daemon is running,
		// SQLite WAL allows concurrent readers without contention.
		s, err := store.Open(dbPath)
		if err != nil {
			log.Fatalf("digest: open store: %v", err)
		}
		defer s.Close()
		since := time.Now().Add(-dur).UnixNano()
		ctx := context.Background()
		rows, err := s.Retrieve(ctx, "", since, 0, 2000, true)
		if err != nil {
			log.Fatalf("digest: %v", err)
		}
		fmt.Printf("eideticd %s — %s recap\n", Version, *showDigest)
		fmt.Println("=============================================")
		fmt.Println()
		fmt.Printf("Total engrams: %d\n\n", len(rows))
		if len(rows) == 0 {
			fmt.Println("No engrams in window. Nothing to recap.")
			return
		}
		// Per-surface counts
		bySurface := make(map[string]int)
		for _, r := range rows {
			bySurface[r.Surface]++
		}
		fmt.Println("By surface:")
		for s, n := range bySurface {
			fmt.Printf("  %-20s %d\n", s, n)
		}
		fmt.Println()
		// Tail samples
		fmt.Println("Most recent engrams:")
		for _, r := range rows[max(0, len(rows)-5):] {
			payload := r.Payload
			if len(payload) > 100 {
				payload = payload[:100] + "..."
			}
			payload = strings.ReplaceAll(payload, "\n", " ")
			fmt.Printf("  [%s] %s\n", r.Surface, payload)
		}
		return
	}

	// --backups: list last N cloud uploads from local history file. No DB needed.
	if *showBackups {
		state, err := eidetic_sync.LoadSyncState(dataDir)
		if err != nil {
			log.Fatalf("backups: %v", err)
		}
		fmt.Printf("eideticd %s — cloud backup history\n\n", Version)
		if len(state.History) == 0 {
			if state.LastSync.IsZero() {
				fmt.Println("  no backups yet")
			} else {
				// Pre-v0.0.36 daemon: only the last sync was recorded.
				fmt.Printf("  %s  %s  (%.1f MB)\n",
					state.LastSync.Local().Format("2006-01-02 15:04"),
					state.LastKey, float64(state.LastBytes)/1e6)
			}
			return
		}
		for _, e := range state.History {
			fmt.Printf("  %s  %s  (%.1f MB)\n",
				e.SyncedAt.Local().Format("2006-01-02 15:04"),
				e.Key, float64(e.Bytes)/1e6)
		}
		return
	}

	// --check: validate sync.json + test Worker. Runs before store.Open (no DB needed).
	if *checkSync {
		cfg, err := eidetic_sync.LoadConfig(dataDir)
		if err != nil {
			log.Fatalf("check: load sync config: %v", err)
		}
		fmt.Printf("eideticd %s — sync check\n\n", Version)
		if checkErr := eidetic_sync.CheckConfig(cfg, dataDir); checkErr != nil {
			fmt.Printf("\n  status: ✗ sync not healthy\n")
			os.Exit(1)
		}
		fmt.Printf("\n  status: ✓ sync healthy\n")
		return
	}

	// --restore: download latest backup and replace local DB.
	// Must run before store.Open to avoid a write-lock conflict on engrams.db.
	if *restoreFlag {
		cfg, err := eidetic_sync.LoadConfig(dataDir)
		if err != nil {
			log.Fatalf("restore: load sync config: %v", err)
		}
		if err := eidetic_sync.RestoreFromConfig(cfg, dbPath); err != nil {
			log.Fatalf("restore: %v", err)
		}
		return
	}

	s, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("store open: %v", err)
	}
	defer s.Close()

	// v0.0.24+: optional Cloudflare R2 sync (ADR-019). Opt-in via sync.json.
	// v0.0.35+: sync.json is hot-reloaded — dropping a new file is detected via
	// fsnotify and the Syncer is recreated without daemon restart.
	syncCfg, err := eidetic_sync.LoadConfig(dataDir)
	if err != nil {
		log.Printf("sync: config error (sync disabled): %v", err)
	}
	var (
		syncerMu sync.RWMutex
		syncer   = eidetic_sync.New(syncCfg, dbPath, dataDir, s)
	)
	getSyncer := func() *eidetic_sync.Syncer {
		syncerMu.RLock()
		defer syncerMu.RUnlock()
		return syncer
	}

	// --stats: print database statistics and exit.
	if *showStats {
		snap, err := s.Stats(context.Background())
		if err != nil {
			log.Fatalf("stats: %v", err)
		}
		fmt.Printf("eideticd %s — engram statistics\n\n", Version)
		fmt.Printf("  engrams:    %d\n", snap.Total)
		for surf, n := range snap.BySurface {
			fmt.Printf("    %-20s %d\n", surf, n)
		}
		if snap.OldestNs > 0 {
			oldest := time.Unix(0, snap.OldestNs).UTC()
			newest := time.Unix(0, snap.NewestNs).UTC()
			fmt.Printf("  oldest:     %s\n", oldest.Format("2006-01-02"))
			fmt.Printf("  newest:     %s\n", newest.Format("2006-01-02"))
		}
		fmt.Printf("  db size:    %.1f MB\n", float64(snap.DBBytes)/1e6)
		if snap.P95LatNs > 0 {
			fmt.Printf("  P95 fetch:  %.2f ms\n", float64(snap.P95LatNs)/1e6)
		}
		if syncState, err := eidetic_sync.LoadSyncState(dataDir); err == nil && !syncState.LastSync.IsZero() {
			fmt.Printf("\n  cloud sync:\n")
			fmt.Printf("    last sync:  %s\n", syncState.LastSync.Local().Format("2006-01-02 15:04:05"))
			fmt.Printf("    last key:   %s\n", syncState.LastKey)
			fmt.Printf("    last size:  %.1f MB\n", float64(syncState.LastBytes)/1e6)
		}
		// v0.0.37+: surface upgrade hint from cached release check.
		ck := versioncheck.New(dataDir)
		if ck.UpdateAvailable(Version) {
			fmt.Printf("\n  ⬆ update available: %s → %s\n", Version, ck.Latest())
			fmt.Printf("    brew upgrade eideticd  (or re-run install.sh)\n")
		}
		return
	}

	// --sync-now: upload immediately and exit (no daemon loop needed)
	if *syncNow {
		if err := getSyncer().SyncNow(); err != nil {
			log.Fatalf("sync-now: %v", err)
		}
		log.Printf("sync-now: upload complete")
		return
	}

	// v0.0.9+: opt-in caller authentication. Off by default — preserves
	// W1 single-user UDS-trust model (SECURITY.md). On via -auth flag OR
	// EIDETIC_AUTH=1 env var. Token rotates every startup.
	authEnabled := *authFlag || os.Getenv("EIDETIC_AUTH") == "1"
	authToken := &auth.Token{}
	if authEnabled {
		tok, err := auth.Generate()
		if err != nil {
			log.Fatalf("auth: generate: %v", err)
		}
		path, err := auth.WriteFile(dataDir, tok)
		if err != nil {
			log.Fatalf("auth: write file: %v", err)
		}
		authToken.Set(tok)
		log.Printf("auth: enabled — token written to %s (0600), rotates each restart", path)
	}

	// v0.0.12+: query latency tracker (1000-sample reservoir; ~8 KB).
	queryTracker := api.NewLatencyTracker(1000)

	opts := api.Options{Timeout: 5 * time.Second, AuthToken: authToken, QueryLatency: queryTracker}
	switch {
	case *udsPath != "":
		opts.UDSPath = *udsPath
	case *tcpAddr != "":
		opts.TCPAddr = *tcpAddr
	case os.Getenv("EIDETIC_TCP") == "1":
		opts.TCPAddr = defaultTCPAddr
	default:
		opts.UDSPath = defaultUDSPath
	}

	// v0.0.37+: background poll of GitHub releases API (24h period) so /metrics
	// can report update_available. Best-effort; offline daemons get empty fields.
	verCheck := versioncheck.New(dataDir)

	// /metrics provider (v0.0.7+). Closes over watcher (for capture
	// skip-counter), store (for engram counts + DB path), and process
	// start time (for uptime). Provider is built BEFORE watcher is
	// constructed below — so we forward-declare via a *capture.Watcher
	// pointer set later. nil-safe inside the closure.
	var watcherPtr *capture.Watcher
	startTime := time.Now()
	opts.Metrics = func(ctx context.Context) (api.Metrics, error) {
		askHits, askMisses, askSize := api.AskCacheStats()
		m := api.Metrics{
			Version:         Version,
			UptimeSeconds:   int64(time.Since(startTime).Seconds()),
			DBPath:          dbPath,
			LatestVersion:   verCheck.Latest(),
			UpdateAvailable: verCheck.UpdateAvailable(Version),
			AskCacheHits:    askHits,
			AskCacheMisses:  askMisses,
			AskCacheSize:    askSize,
		}
		if total, err := s.Count(ctx); err == nil {
			m.EngramTotal = total
		}
		if bySurface, err := s.CountBySurface(ctx); err == nil {
			m.EngramBySurface = bySurface
		}
		if watcherPtr != nil {
			m.CaptureSkipped = watcherPtr.SkippedPayloadTooLarge()
		}
		if fi, err := os.Stat(dbPath); err == nil {
			m.DBSizeBytes = fi.Size()
		}
		// v0.0.12+: query latency percentiles. Nil when < 2 samples.
		if p50, p95, p99 := queryTracker.Percentiles(); p50 == p50 { // NaN != NaN
			m.QueryP50Us = &p50
			m.QueryP95Us = &p95
			m.QueryP99Us = &p99
			m.QueryCount = queryTracker.Count()
		}
		return m, nil
	}

	srv, err := api.New(s, opts)
	if err != nil {
		log.Fatalf("api new: %v", err)
	}
	defer srv.Close()

	log.Printf("eideticd listening at %s (db=%s)", srv.Addr(), dbPath)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// v0.0.31+: optional Bridge server — TCP listener for Cloudflare tunnel.
	// Runs alongside the primary UDS server (shared store, separate listener).
	// Auth is always-on; CORS enabled for web-based AI clients.
	// Token written to <dataDir>/bridge-token (0600).
	if *bridgeAddr != "" {
		bridgeToken := &auth.Token{}
		tok, err := auth.Generate()
		if err != nil {
			log.Fatalf("bridge auth generate: %v", err)
		}
		btPath := filepath.Join(dataDir, "bridge-token")
		if err := os.WriteFile(btPath, []byte(tok), 0o600); err != nil {
			log.Fatalf("bridge auth write: %v", err)
		}
		bridgeToken.Set(tok)
		log.Printf("bridge: auth token → %s (0600)", btPath)

		bridgeOpts := api.Options{
			TCPAddr:      *bridgeAddr,
			Timeout:      10 * time.Second,
			AuthToken:    bridgeToken,
			Metrics:      opts.Metrics,
			QueryLatency: queryTracker,
			CORS:         true,
		}
		bridgeSrv, err := api.New(s, bridgeOpts)
		if err != nil {
			log.Fatalf("bridge server: %v", err)
		}
		defer bridgeSrv.Close()
		go func() {
			if err := bridgeSrv.Serve(ctx); err != nil {
				log.Printf("bridge serve: %v", err)
			}
		}()
		log.Printf("bridge listening on %s (auth=on, cors=on)", bridgeSrv.Addr())
	}

	// Capture-side: fsnotify watchers across the 3 default surface roots.
	// Loads/persists per-file offsets at <dataDir>/state.json. Missing
	// surface dirs (e.g., Cowork on hosts without it) are graceful-skip
	// per capture.Watcher.Run.
	state, err := capture.LoadState(filepath.Join(dataDir, "state.json"))
	if err != nil {
		log.Fatalf("capture state load: %v", err)
	}
	// v0.0.55+: outbound webhook hooks. If ~/.eidetic/hooks.json exists,
	// the dispatcher wraps the sink so matching engrams fire user-configured
	// webhooks AFTER InsertBatch succeeds. nil-safe when config absent.
	hookCfg, hookErr := hooks.LoadConfig(dataDir)
	if hookErr != nil {
		log.Printf("hooks: config error (hooks disabled): %v", hookErr)
	}
	hookDispatcher := hooks.NewDispatcher(hookCfg)
	if names := hookDispatcher.Names(); len(names) > 0 {
		log.Printf("hooks: %d webhook(s) registered: %v", len(names), names)
	}
	sink := hookDispatcher.WrapSink(s)

	watcher := capture.NewWatcher(sink, state, capture.DefaultSurfaces(), 0)
	watcherPtr = watcher // satisfy /metrics provider closure forward-decl
	captureDone := make(chan struct{})
	go func() {
		defer close(captureDone)
		if err := watcher.Run(ctx); err != nil {
			log.Printf("capture: %v", err)
		}
	}()

	// v0.0.37+: background poll of GitHub releases API (24h period).
	verStop := make(chan struct{})
	go verCheck.Run(verStop)
	defer close(verStop)

	// v0.0.24+: periodic R2 sync poll — fires TriggerIfDue every 60s.
	// No-ops if syncer is nil (sync.json absent → opt-out).
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := getSyncer().TriggerIfDue(); err != nil {
					log.Printf("sync: %v", err)
				}
			}
		}
	}()

	// v0.0.35+: hot-reload sync.json. When a Pro customer drops their sync.json
	// into dataDir, the daemon detects it and starts syncing within ~1 second
	// without requiring a restart.
	go func() {
		err := eidetic_sync.WatchConfig(ctx, dataDir, func(newCfg *eidetic_sync.Config) {
			newSyncer := eidetic_sync.New(newCfg, dbPath, dataDir, s)
			syncerMu.Lock()
			syncer = newSyncer
			syncerMu.Unlock()
			if newSyncer != nil {
				log.Printf("sync: hot-reload — config applied (worker=%s device=%s)",
					newCfg.WorkerURL, newCfg.DeviceID)
				// Kick an immediate sync so the customer sees confirmation quickly.
				go func() {
					if err := newSyncer.SyncNow(); err != nil {
						log.Printf("sync: hot-reload initial upload: %v", err)
					} else {
						log.Printf("sync: hot-reload initial upload complete")
					}
				}()
			} else {
				log.Printf("sync: hot-reload — config removed or invalid; sync disabled")
			}
		})
		if err != nil {
			log.Printf("sync watch: %v", err)
		}
	}()

	if err := srv.Serve(ctx); err != nil {
		// Demoted from Fatalf to Printf so the capture-drain below still
		// runs on serve errors (e.g., listener already-bound), preserving
		// the issue #17 invariant: store closes only after capture drains.
		log.Printf("serve: %v", err)
	}

	// Drain capture before letting `defer s.Close()` fire. Without this,
	// in-flight parseAndCommit goroutines race store close and emit ~30
	// "begin batch tx: sql: database is closed" errors per shutdown
	// (issue #17). watcher.Run defers inflight.Wait() so no pending
	// InsertBatch remains when this returns.
	<-captureDone
}

