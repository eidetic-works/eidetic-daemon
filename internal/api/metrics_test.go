package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
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

// TestMetricsPrometheusFormat: Accept: text/plain → Prometheus exposition.
// Asserts content-type header, presence of all 6 metric families with
// HELP+TYPE comments, label format on multi-value metrics.
func TestMetricsPrometheusFormat(t *testing.T) {
	provider := func(_ context.Context) (Metrics, error) {
		return Metrics{
			Version:         "v0.0.10-test",
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

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Accept", "text/plain")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content-type: got %q, want text/plain prefix", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	// Each metric family must have HELP + TYPE + at least one value line.
	mustContain := []string{
		"# HELP eidetic_uptime_seconds",
		"# TYPE eidetic_uptime_seconds gauge",
		"eidetic_uptime_seconds 42",
		"# HELP eidetic_engrams_total",
		"# TYPE eidetic_engrams_total gauge",
		"eidetic_engrams_total 1000",
		"# HELP eidetic_engrams_by_surface_total",
		"# TYPE eidetic_engrams_by_surface_total gauge",
		`eidetic_engrams_by_surface_total{surface="claude_code"} 750`,
		`eidetic_engrams_by_surface_total{surface="cursor"} 250`,
		"# HELP eidetic_capture_skipped_total",
		"# TYPE eidetic_capture_skipped_total counter",
		"eidetic_capture_skipped_total 3",
		"# HELP eidetic_db_size_bytes",
		"# TYPE eidetic_db_size_bytes gauge",
		"eidetic_db_size_bytes 12345",
		"# HELP eidetic_build_info",
		"# TYPE eidetic_build_info gauge",
		`eidetic_build_info{version="v0.0.10-test"} 1`,
	}
	for _, want := range mustContain {
		if !strings.Contains(bodyStr, want) {
			t.Errorf("Prometheus body missing %q. Full body:\n%s", want, bodyStr)
		}
	}
}

// TestMetricsAcceptDefaultsJSON: missing Accept header → JSON
// (preserves v0.0.7 default for backward-compat).
func TestMetricsAcceptDefaultsJSON(t *testing.T) {
	provider := func(_ context.Context) (Metrics, error) {
		return Metrics{Version: "v0.0.10", EngramTotal: 5}, nil
	}
	url, cleanup := metricsTestServer(t, provider)
	defer cleanup()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("default content-type: got %q, want application/json prefix", ct)
	}
}

// TestMetricsAcceptStarStarReturnsJSON: Accept: */* → JSON
// (the */* wildcard doesn't trigger Prometheus; only explicit text/plain does).
func TestMetricsAcceptStarStarReturnsJSON(t *testing.T) {
	provider := func(_ context.Context) (Metrics, error) {
		return Metrics{Version: "v0.0.10"}, nil
	}
	url, cleanup := metricsTestServer(t, provider)
	defer cleanup()
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Accept", "*/*")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Accept */* content-type: got %q, want application/json prefix", ct)
	}
}

// TestMetricsAcceptMultipleHonorsTextPlain: Accept with multiple types
// including text/plain → Prometheus.
func TestMetricsAcceptMultipleHonorsTextPlain(t *testing.T) {
	provider := func(_ context.Context) (Metrics, error) {
		return Metrics{Version: "v0.0.10"}, nil
	}
	url, cleanup := metricsTestServer(t, provider)
	defer cleanup()
	req, _ := http.NewRequest("GET", url, nil)
	// Prometheus scrapers commonly send this:
	req.Header.Set("Accept", "application/openmetrics-text;version=1.0.0,text/plain;version=0.0.4;q=0.5,*/*;q=0.1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("multi-Accept w/ text/plain: got %q, want text/plain prefix", ct)
	}
}

// TestMarshalPrometheusEmptyBySurface: empty EngramBySurface → no
// per-surface metric block emitted (don't emit dangling HELP/TYPE
// without a value).
func TestMarshalPrometheusEmptyBySurface(t *testing.T) {
	m := Metrics{Version: "v0.0.10", EngramBySurface: nil}
	out := m.MarshalPrometheus()
	if strings.Contains(out, "eidetic_engrams_by_surface_total") {
		t.Errorf("empty by-surface should suppress block; got:\n%s", out)
	}
}

// TestMarshalPrometheusDeterministicSurfaceOrder: surfaces sorted
// alphabetically for stable diffs.
func TestMarshalPrometheusDeterministicSurfaceOrder(t *testing.T) {
	m := Metrics{
		Version:         "v0.0.10",
		EngramBySurface: map[string]int64{"cursor": 1, "claude_code": 2, "cowork": 3},
	}
	out := m.MarshalPrometheus()
	cIdx := strings.Index(out, `surface="claude_code"`)
	wIdx := strings.Index(out, `surface="cowork"`)
	rIdx := strings.Index(out, `surface="cursor"`)
	if !(cIdx < wIdx && wIdx < rIdx) {
		t.Errorf("surfaces not alphabetical: claude_code@%d, cowork@%d, cursor@%d. Body:\n%s", cIdx, wIdx, rIdx, out)
	}
}
