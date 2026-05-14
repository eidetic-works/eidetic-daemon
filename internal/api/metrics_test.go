package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/eidetic-works/eidetic-daemon/internal/store"
)

// metricsTestServer spins up a TCP-bound server (random port) with the
// given MetricsProvider so HTTP tests don't have to negotiate UDS path
// limits. Caller closes the returned cleanup func.
func metricsTestServer(t *testing.T, provider MetricsProvider) (string, func()) {
	t.Helper()
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "engrams.db"))
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(st, Options{TCPAddr: "127.0.0.1:0", Timeout: 2 * time.Second, Metrics: provider})
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.Serve(ctx) }()
	// Allow listener to settle.
	time.Sleep(20 * time.Millisecond)
	url := fmt.Sprintf("http://%s/metrics", srv.Addr().String())
	cleanup := func() {
		cancel()
		srv.Close()
		st.Close()
	}
	return url, cleanup
}

// TestMetricsHappyPath: GET /metrics with a configured provider returns
// 200 + JSON body matching the Metrics schema. Schema is contract — any
// new field is additive; existing fields must not silently drop.
func TestMetricsHappyPath(t *testing.T) {
	provider := func(_ context.Context) (Metrics, error) {
		return Metrics{
			Version:         "v0.0.7-test",
			UptimeSeconds:   42,
			EngramTotal:     1000,
			EngramBySurface: map[string]int64{"claude_code": 750, "cursor": 250},
			CaptureSkipped:  3,
			DBPath:          "/tmp/test.db",
			DBSizeBytes:     12345,
		}, nil
	}
	url, cleanup := metricsTestServer(t, provider)
	defer cleanup()

	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	var got Metrics
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Version != "v0.0.7-test" {
		t.Errorf("Version: got %q, want %q", got.Version, "v0.0.7-test")
	}
	if got.EngramTotal != 1000 {
		t.Errorf("EngramTotal: got %d, want 1000", got.EngramTotal)
	}
	if got.EngramBySurface["claude_code"] != 750 {
		t.Errorf("EngramBySurface[claude_code]: got %d, want 750", got.EngramBySurface["claude_code"])
	}
	if got.CaptureSkipped != 3 {
		t.Errorf("CaptureSkipped: got %d, want 3", got.CaptureSkipped)
	}
	if got.DBSizeBytes != 12345 {
		t.Errorf("DBSizeBytes: got %d, want 12345", got.DBSizeBytes)
	}
}

// TestMetricsNoProviderReturns503: nil-provider fallback. Useful for
// callers detecting "this daemon predates v0.0.7 metrics wiring".
func TestMetricsNoProviderReturns503(t *testing.T) {
	url, cleanup := metricsTestServer(t, nil)
	defer cleanup()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d, want 503", resp.StatusCode)
	}
}

// TestMetricsProviderError: provider returns error → 500. Caller sees the
// error message in the body for debugging.
func TestMetricsProviderError(t *testing.T) {
	provider := func(_ context.Context) (Metrics, error) {
		return Metrics{}, errors.New("simulated provider failure")
	}
	url, cleanup := metricsTestServer(t, provider)
	defer cleanup()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500", resp.StatusCode)
	}
}

// TestMetricsMethodNotAllowed: POST etc. → 405.
func TestMetricsMethodNotAllowed(t *testing.T) {
	provider := func(_ context.Context) (Metrics, error) { return Metrics{}, nil }
	url, cleanup := metricsTestServer(t, provider)
	defer cleanup()
	resp, err := http.Post(url, "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status: got %d, want 405", resp.StatusCode)
	}
}
