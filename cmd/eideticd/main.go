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
	"github.com/eidetic-works/eidetic-daemon/internal/capture"
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
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("eideticd", Version)
		return
	}

	dataDir := os.Getenv("EIDETIC_DATA_DIR")
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("resolve home: %v", err)
		}
		dataDir = filepath.Join(home, ".eidetic")
	}
	dbPath := filepath.Join(dataDir, "engrams.db")

	s, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("store open: %v", err)
	}
	defer s.Close()

	opts := api.Options{Timeout: 5 * time.Second}
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
