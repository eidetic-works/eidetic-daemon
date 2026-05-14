// Package api serves engram retrieval over a local Unix domain socket
// (default) or TCP loopback (testing/CI). UDS trust boundary is the
// socket file permission (0600); no auth, no TLS in W1 per
// docs/SPEC.md § 2.4 + docs/PHASE_2_DESIGN.md.
package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/eidetic-works/eidetic-daemon/internal/store"
)

// Options configures a Server. Exactly one of UDSPath or TCPAddr must be
// non-empty; New returns an error if the configuration is ambiguous.
type Options struct {
	UDSPath string        // e.g. "/tmp/eidetic-daemon.sock"; if set, takes precedence
	TCPAddr string        // e.g. "127.0.0.1:9876"; used only when UDSPath is empty
	Timeout time.Duration // per-request timeout; default 5s if zero

	// Metrics is an optional provider for the GET /metrics endpoint
	// (v0.0.7+). Supplied by main() so the api package stays decoupled
	// from cmd-side state (Watcher, process start time, build version).
	// If nil, /metrics returns 503 "metrics not configured".
	Metrics MetricsProvider
}

// Server wraps an http.Server bound to a local listener (UDS or TCP).
// One Server owns one Store reference; multiple Servers can share a Store.
type Server struct {
	httpSrv  *http.Server
	listener net.Listener
	store    *store.Store
	udsPath  string // empty for TCP; set so Close can unlink the file
	timeout  time.Duration
	metrics  MetricsProvider // may be nil; /metrics returns 503 in that case
}

// New constructs a server bound per opts. Cleans up a stale UDS file at
// UDSPath before binding. Returns an error if both UDSPath and TCPAddr
// are empty.
func New(s *store.Store, opts Options) (*Server, error) {
	if s == nil {
		return nil, errors.New("store is required")
	}
	if opts.UDSPath == "" && opts.TCPAddr == "" {
		return nil, errors.New("Options requires UDSPath or TCPAddr")
	}
	if opts.Timeout == 0 {
		opts.Timeout = 5 * time.Second
	}

	srv := &Server{store: s, timeout: opts.Timeout, metrics: opts.Metrics}

	var (
		listener net.Listener
		err      error
	)
	if opts.UDSPath != "" {
		// Clean up stale socket file from a prior crashed run. ListenUnix
		// returns EADDRINUSE if a file exists at the path even when no
		// process is bound, so unconditional remove is the correct shape.
		_ = os.Remove(opts.UDSPath)
		listener, err = net.Listen("unix", opts.UDSPath)
		if err != nil {
			return nil, err
		}
		// UDS trust boundary: 0600 = owner-only. Defense against multi-user
		// systems accidentally exposing engram reads.
		if err := os.Chmod(opts.UDSPath, 0o600); err != nil {
			listener.Close()
			_ = os.Remove(opts.UDSPath)
			return nil, err
		}
		srv.udsPath = opts.UDSPath
	} else {
		listener, err = net.Listen("tcp", opts.TCPAddr)
		if err != nil {
			return nil, err
		}
	}
	srv.listener = listener

	mux := http.NewServeMux()
	mux.HandleFunc("/engrams", srv.handleEngramsGET)
	mux.HandleFunc("/healthz", srv.handleHealthz)
	mux.HandleFunc("/metrics", srv.handleMetrics)

	srv.httpSrv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: opts.Timeout,
	}

	return srv, nil
}

// Addr returns the listener's address. Useful for tests that bind to
// a random TCP port via "127.0.0.1:0".
func (s *Server) Addr() net.Addr { return s.listener.Addr() }

// Serve blocks until ctx is cancelled OR the underlying http.Server returns
// an error other than http.ErrServerClosed. On ctx cancel, performs a
// timeout-bounded graceful shutdown.
func (s *Server) Serve(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() { errCh <- s.httpSrv.Serve(s.listener) }()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), s.timeout)
		defer cancel()
		// Shutdown closes the listener; drain any started request up to timeout.
		_ = s.httpSrv.Shutdown(shutCtx)
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// Close releases the listener and unlinks the UDS file (if applicable).
// Idempotent; safe to call after Serve returns.
func (s *Server) Close() error {
	var first error
	if s.httpSrv != nil {
		if err := s.httpSrv.Close(); err != nil {
			first = err
		}
	}
	if s.udsPath != "" {
		_ = os.Remove(s.udsPath)
	}
	return first
}
