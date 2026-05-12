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
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/eidetic-works/eidetic-daemon/internal/api"
	"github.com/eidetic-works/eidetic-daemon/internal/store"
)

const (
	defaultUDSPath = "/tmp/eidetic-daemon.sock"
	defaultTCPAddr = "127.0.0.1:9876"
)

func main() {
	udsPath := flag.String("uds", "", "Unix domain socket path (overrides default)")
	tcpAddr := flag.String("tcp", "", "TCP listen address (overrides default; opt-in via EIDETIC_TCP=1)")
	flag.Parse()

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

	srv, err := api.New(s, opts)
	if err != nil {
		log.Fatalf("api new: %v", err)
	}
	defer srv.Close()

	log.Printf("eideticd listening at %s (db=%s)", srv.Addr(), dbPath)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := srv.Serve(ctx); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
