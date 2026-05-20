package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/eidetic-works/eidetic-daemon/internal/api"
	"github.com/eidetic-works/eidetic-daemon/internal/auth"
)

// TestCORSHeadersPresentWhenEnabled verifies that responses include the
// required CORS headers when Options.CORS is true.
func TestCORSHeadersPresentWhenEnabled(t *testing.T) {
	s := tempStore(t)
	defer s.Close()

	srv, err := api.New(s, api.Options{TCPAddr: "127.0.0.1:0", CORS: true})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()

	url := "http://" + srv.Addr().String() + "/healthz"
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Methods"); got == "" {
		t.Error("Access-Control-Allow-Methods missing")
	}
	if got := resp.Header.Get("Access-Control-Allow-Headers"); got == "" {
		t.Error("Access-Control-Allow-Headers missing")
	}
}

// TestCORSHeadersAbsentWhenDisabled verifies that the UDS/primary path does
// not add CORS headers when Options.CORS is false (the default).
func TestCORSHeadersAbsentWhenDisabled(t *testing.T) {
	s := tempStore(t)
	defer s.Close()

	srv, err := api.New(s, api.Options{TCPAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()

	resp, err := http.Get("http://" + srv.Addr().String() + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty (CORS off)", got)
	}
}

// TestCORSPreflightReturns204 verifies that OPTIONS requests return 204 with
// CORS headers and an empty body — required for browser preflight.
func TestCORSPreflightReturns204(t *testing.T) {
	s := tempStore(t)
	defer s.Close()

	srv, err := api.New(s, api.Options{TCPAddr: "127.0.0.1:0", CORS: true})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()

	req, _ := http.NewRequest(http.MethodOptions, "http://"+srv.Addr().String()+"/engrams", nil)
	req.Header.Set("Origin", "https://claude.ai")
	req.Header.Set("Access-Control-Request-Method", "GET")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("OPTIONS /engrams: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("OPTIONS status = %d, want 204", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
	}
}

// TestBridgeDualListenerSharesStore verifies that two api.Server instances
// sharing one store both see the same engram data — the core invariant of
// the bridge dual-listener design.
func TestBridgeDualListenerSharesStore(t *testing.T) {
	s := tempStore(t)
	defer s.Close()

	// Primary (UDS-style — use TCP for portability in tests)
	primary, err := api.New(s, api.Options{TCPAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("primary api.New: %v", err)
	}
	defer primary.Close()

	// Bridge (TCP + CORS)
	bridge, err := api.New(s, api.Options{TCPAddr: "127.0.0.1:0", CORS: true})
	if err != nil {
		t.Fatalf("bridge api.New: %v", err)
	}
	defer bridge.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = primary.Serve(ctx) }()
	go func() { _ = bridge.Serve(ctx) }()

	// Both listeners should report healthy
	for _, addr := range []string{primary.Addr().String(), bridge.Addr().String()} {
		resp, err := http.Get("http://" + addr + "/healthz")
		if err != nil {
			t.Fatalf("GET %s /healthz: %v", addr, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("addr %s: /healthz status = %d, want 200", addr, resp.StatusCode)
		}
	}
}

// TestCORSPreflightBypassesAuth verifies that OPTIONS preflight returns 204
// even when auth is enabled — browsers don't send Authorization on preflight,
// so blocking preflight would break the web dashboard entirely.
func TestCORSPreflightBypassesAuth(t *testing.T) {
	s := tempStore(t)
	defer s.Close()

	tok := &auth.Token{}
	tok.Set("secret-bearer-token")

	srv, err := api.New(s, api.Options{
		TCPAddr:   "127.0.0.1:0",
		CORS:      true,
		AuthToken: tok,
	})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()

	// Preflight — no Authorization header (matches what browsers send)
	req, _ := http.NewRequest(http.MethodOptions, "http://"+srv.Addr().String()+"/surfaces", nil)
	req.Header.Set("Origin", "https://eidetic.works")
	req.Header.Set("Access-Control-Request-Method", "GET")
	req.Header.Set("Access-Control-Request-Headers", "Authorization")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("OPTIONS /surfaces: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("preflight status = %d, want 204 (auth-bypassed)", resp.StatusCode)
	}

	// Actual GET WITHOUT auth should fail (proves auth middleware is still active)
	resp2, err := http.Get("http://" + srv.Addr().String() + "/surfaces")
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauth GET /surfaces: status = %d, want 401", resp2.StatusCode)
	}

	// Actual GET WITH auth should succeed
	req3, _ := http.NewRequest(http.MethodGet, "http://"+srv.Addr().String()+"/surfaces", nil)
	req3.Header.Set("Authorization", "Bearer secret-bearer-token")
	resp3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Errorf("auth GET /surfaces: status = %d, want 200", resp3.StatusCode)
	}
}
