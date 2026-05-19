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
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/eidetic-works/eidetic-daemon/internal/api"
	"github.com/eidetic-works/eidetic-daemon/internal/auth"
	"github.com/eidetic-works/eidetic-daemon/internal/capture"
	eidetic_sync "github.com/eidetic-works/eidetic-daemon/internal/sync"
	"github.com/eidetic-works/eidetic-daemon/internal/store"
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
	installSvc := flag.Bool("install", false, "register eideticd as a login-time service (launchd on macOS, systemd-user on Linux) and exit")
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
	syncCfg, err := eidetic_sync.LoadConfig(dataDir)
	if err != nil {
		log.Printf("sync: config error (sync disabled): %v", err)
	}
	syncer := eidetic_sync.New(syncCfg, dbPath, dataDir, s)

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
		return
	}

	// --sync-now: upload immediately and exit (no daemon loop needed)
	if *syncNow {
		if err := syncer.SyncNow(); err != nil {
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

	// /metrics provider (v0.0.7+). Closes over watcher (for capture
	// skip-counter), store (for engram counts + DB path), and process
	// start time (for uptime). Provider is built BEFORE watcher is
	// constructed below — so we forward-declare via a *capture.Watcher
	// pointer set later. nil-safe inside the closure.
	var watcherPtr *capture.Watcher
	startTime := time.Now()
	opts.Metrics = func(ctx context.Context) (api.Metrics, error) {
		m := api.Metrics{
			Version:       Version,
			UptimeSeconds: int64(time.Since(startTime).Seconds()),
			DBPath:        dbPath,
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
	watcher := capture.NewWatcher(s, state, capture.DefaultSurfaces(), 0)
	watcherPtr = watcher // satisfy /metrics provider closure forward-decl
	captureDone := make(chan struct{})
	go func() {
		defer close(captureDone)
		if err := watcher.Run(ctx); err != nil {
			log.Printf("capture: %v", err)
		}
	}()

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
				if err := syncer.TriggerIfDue(); err != nil {
					log.Printf("sync: %v", err)
				}
			}
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
