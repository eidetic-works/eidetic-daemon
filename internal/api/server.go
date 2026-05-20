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

	"github.com/eidetic-works/eidetic-daemon/internal/auth"
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

	// AuthToken is an optional opt-in caller-authentication token (v0.0.9+).
	// When the token's Enabled() returns true, /engrams and /metrics
	// require Authorization: Bearer <token> headers; /healthz stays open.
	// When nil OR disabled, all routes pass through (preserves W1
	// single-user UDS-trust behavior). main() supplies this; api stays
	// decoupled from token-file path resolution.
	AuthToken *auth.Token

	// CORS enables permissive CORS headers on all responses (v0.0.31+).
	// Intended for the Bridge TCP server exposed via Cloudflare tunnel so
	// web-based AI clients (Claude.ai, ChatGPT web) can call the daemon
	// from a browser context. Never set on the UDS server (trust boundary
	// is the socket file, not Origin headers).
	CORS bool

	// QueryLatency is an optional LatencyTracker for /engrams query timing
	// (v0.0.12+). When non-nil, every handleEngramsGET records the
	// store.Retrieve round-trip duration; percentiles surface via /metrics.
	// When nil, no timing overhead is incurred.
	QueryLatency *LatencyTracker
}

// Server wraps an http.Server bound to a local listener (UDS or TCP).
// One Server owns one Store reference; multiple Servers can share a Store.
type Server struct {
	httpSrv      *http.Server
	listener     net.Listener
	store        *store.Store
	udsPath      string          // empty for TCP; set so Close can unlink the file
	timeout      time.Duration
	metrics      MetricsProvider // may be nil; /metrics returns 503 in that case
	auth         *auth.Token     // may be nil OR disabled; middleware passes through
	queryLatency *LatencyTracker // may be nil; no timing overhead when absent
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

	srv := &Server{store: s, timeout: opts.Timeout, metrics: opts.Metrics, auth: opts.AuthToken, queryLatency: opts.QueryLatency}

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
	mux.HandleFunc("/engrams", srv.handleEngrams)
	mux.HandleFunc("/engrams/batch", srv.handleEngramsBatch)
	mux.HandleFunc("/engrams/count", srv.handleEngramsCount)
	mux.HandleFunc("/engrams/{id}", srv.handleEngramsByID)
	mux.HandleFunc("/surfaces", srv.handleSurfaces)
	mux.HandleFunc("/search", srv.handleSearch)
	mux.HandleFunc("/recent", srv.handleRecent)
	mux.HandleFunc("/ask", srv.handleAsk)
	mux.HandleFunc("/export", srv.handleExport)
	mux.HandleFunc("/healthz", srv.handleHealthz)
	mux.HandleFunc("/metrics", srv.handleMetrics)

	// v0.0.9+: optional Bearer-token middleware. /healthz stays open
	// (liveness probe, no sensitive data leaked). When opts.AuthToken
	// is nil OR disabled, Middleware passes through transparently —
	// preserves backward-compat for callers that don't set EIDETIC_AUTH=1.
	var handler http.Handler = mux
	if opts.AuthToken != nil {
		handler = opts.AuthToken.Middleware(mux, "/healthz")
	}

	// v0.0.31+: CORS middleware for Bridge TCP server. Wraps after auth so
	// preflight OPTIONS requests still pass through auth (token required).
	// Permissive origin (*) is intentional: the token is the auth boundary.
	if opts.CORS {
		inner := handler
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			inner.ServeHTTP(w, r)
		})
	}

	srv.httpSrv = &http.Server{
		Handler:           handler,
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
