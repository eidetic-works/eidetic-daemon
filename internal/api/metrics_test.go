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

// TestMetricsAcceptMultipleWithoutOpenMetricsHonorsTextPlain: legacy
// scraper Accept (no openmetrics-text clause) honors text/plain →
// Prometheus. Distinct from TestMetricsOpenMetricsTakesPrecedenceOverPrometheus
// which covers the modern-scraper case (both clauses → OpenMetrics).
func TestMetricsAcceptMultipleWithoutOpenMetricsHonorsTextPlain(t *testing.T) {
	provider := func(_ context.Context) (Metrics, error) {
		return Metrics{Version: "v0.0.11"}, nil
	}
	url, cleanup := metricsTestServer(t, provider)
	defer cleanup()
	req, _ := http.NewRequest("GET", url, nil)
	// Pre-OpenMetrics scrapers / curl-with-explicit-flag:
	req.Header.Set("Accept", "text/plain;version=0.0.4,*/*;q=0.1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("multi-Accept w/ text/plain (no openmetrics): got %q, want text/plain prefix", ct)
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

// TestMetricsOpenMetricsFormat: Accept: application/openmetrics-text →
// OpenMetrics 1.0.0 exposition. Asserts content-type header, EOF
// trailer, UNIT comments, counter naming convention.
func TestMetricsOpenMetricsFormat(t *testing.T) {
	provider := func(_ context.Context) (Metrics, error) {
		return Metrics{
			Version:         "v0.0.11-test",
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
	req.Header.Set("Accept", "application/openmetrics-text")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/openmetrics-text") {
		t.Errorf("content-type: got %q, want application/openmetrics-text prefix", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	mustContain := []string{
		// UNIT comments — distinguishing OpenMetrics feature
		"# UNIT eidetic_uptime_seconds seconds",
		"# UNIT eidetic_db_size_bytes bytes",
		// Counter spec compliance: declared name lacks _total; value line has it
		"# TYPE eidetic_capture_skipped counter",
		"eidetic_capture_skipped_total 3",
		// Per-surface metric — note OpenMetrics uses `eidetic_engrams_by_surface` (no _total suffix on gauge)
		`eidetic_engrams_by_surface{surface="claude_code"} 750`,
		`eidetic_engrams_by_surface{surface="cursor"} 250`,
		// Build info
		`eidetic_build_info{version="v0.0.11-test"} 1`,
		// Mandatory EOF trailer
		"# EOF",
	}
	for _, want := range mustContain {
		if !strings.Contains(bodyStr, want) {
			t.Errorf("OpenMetrics body missing %q. Full body:\n%s", want, bodyStr)
		}
	}
	// EOF must be the LAST line (modulo trailing newline)
	if !strings.HasSuffix(strings.TrimRight(bodyStr, "\n"), "# EOF") {
		t.Errorf("OpenMetrics body must end with `# EOF`. Last 100 chars: %q", bodyStr[max(0, len(bodyStr)-100):])
	}
}

// TestMetricsOpenMetricsTakesPrecedenceOverPrometheus: when Accept
// includes both `application/openmetrics-text` and `text/plain`,
// OpenMetrics wins (matches real Prometheus scraper behavior — they
// prefer OpenMetrics if the server supports it).
func TestMetricsOpenMetricsTakesPrecedenceOverPrometheus(t *testing.T) {
	provider := func(_ context.Context) (Metrics, error) {
		return Metrics{Version: "v0.0.11"}, nil
	}
	url, cleanup := metricsTestServer(t, provider)
	defer cleanup()
	req, _ := http.NewRequest("GET", url, nil)
	// Real Prometheus scraper Accept header (sends both):
	req.Header.Set("Accept", "application/openmetrics-text;version=1.0.0,text/plain;version=0.0.4;q=0.5,*/*;q=0.1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/openmetrics-text") {
		t.Errorf("scraper-style Accept w/ both: got %q, want application/openmetrics-text prefix (OpenMetrics precedence)", ct)
	}
}

// TestMetricsPlainTextStillReturnsPrometheus: regression — Accept:
// text/plain alone (no openmetrics-text clause) still returns
// Prometheus format, not OpenMetrics. Preserves v0.0.10 contract.
func TestMetricsPlainTextStillReturnsPrometheus(t *testing.T) {
	provider := func(_ context.Context) (Metrics, error) {
		return Metrics{Version: "v0.0.11"}, nil
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
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("text/plain alone: got %q, want text/plain (Prometheus, not OpenMetrics)", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "# EOF") {
		t.Error("Prometheus format must NOT have # EOF trailer (that's OpenMetrics)")
	}
}

// TestMarshalOpenMetricsCounterNaming: spec compliance — declared
// counter name MUST NOT have _total suffix; value line MUST have it.
func TestMarshalOpenMetricsCounterNaming(t *testing.T) {
	m := Metrics{Version: "v1.0", CaptureSkipped: 7}
	out := m.MarshalOpenMetrics()
	if !strings.Contains(out, "# TYPE eidetic_capture_skipped counter") {
		t.Errorf("declared name should be eidetic_capture_skipped (no _total). Body:\n%s", out)
	}
	if !strings.Contains(out, "eidetic_capture_skipped_total 7") {
		t.Errorf("value line should be eidetic_capture_skipped_total 7. Body:\n%s", out)
	}
	if strings.Contains(out, "# TYPE eidetic_capture_skipped_total") {
		t.Errorf("must NOT declare TYPE on _total-suffixed name (OpenMetrics spec violation). Body:\n%s", out)
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// TestMetricsQueryLatencyJSONOmitEmpty: QueryP50Us/P95Us/P99Us/QueryCount are
// absent in JSON when nil (omitempty). Present when set.
func TestMetricsQueryLatencyJSONOmitEmpty(t *testing.T) {
	// No latency data — fields must be absent.
	m := Metrics{Version: "v0.0.12", EngramTotal: 5}
	b, _ := json.Marshal(m)
	for _, key := range []string{"query_p50_us", "query_p95_us", "query_p99_us", "query_count"} {
		if strings.Contains(string(b), key) {
			t.Errorf("nil latency: JSON must not contain %q. Got: %s", key, b)
		}
	}

	// With latency data — fields must be present.
	p50, p95, p99 := 12.3, 45.6, 78.9
	m2 := Metrics{
		Version:    "v0.0.12",
		QueryP50Us: &p50,
		QueryP95Us: &p95,
		QueryP99Us: &p99,
		QueryCount: 42,
	}
	b2, _ := json.Marshal(m2)
	s2 := string(b2)
	for _, key := range []string{"query_p50_us", "query_p95_us", "query_p99_us", "query_count"} {
		if !strings.Contains(s2, key) {
			t.Errorf("set latency: JSON must contain %q. Got: %s", key, s2)
		}
	}
}

// TestMetricsPrometheusQueryLatencySummary: summary block absent when nil,
// present with correct quantile lines and _count when set.
func TestMetricsPrometheusQueryLatencySummary(t *testing.T) {
	// Absent when nil.
	m := Metrics{Version: "v0.0.12"}
	out := m.MarshalPrometheus()
	if strings.Contains(out, "eidetic_query_duration_microseconds") {
		t.Errorf("nil latency: Prometheus must not emit query_duration block. Got:\n%s", out)
	}

	// Present with correct format.
	p50, p95, p99 := 10.5, 95.1, 99.9
	m2 := Metrics{
		Version:    "v0.0.12",
		QueryP50Us: &p50,
		QueryP95Us: &p95,
		QueryP99Us: &p99,
		QueryCount: 1000,
	}
	out2 := m2.MarshalPrometheus()

	wantLines := []string{
		"# HELP eidetic_query_duration_microseconds",
		"# TYPE eidetic_query_duration_microseconds summary",
		`eidetic_query_duration_microseconds{quantile="0.5"} 10.500`,
		`eidetic_query_duration_microseconds{quantile="0.95"} 95.100`,
		`eidetic_query_duration_microseconds{quantile="0.99"} 99.900`,
		"eidetic_query_duration_microseconds_count 1000",
	}
	for _, want := range wantLines {
		if !strings.Contains(out2, want) {
			t.Errorf("Prometheus latency summary: want line %q.\nGot:\n%s", want, out2)
		}
	}
	// Must NOT have # EOF (Prometheus format, not OpenMetrics).
	if strings.Contains(out2, "# EOF") {
		t.Error("Prometheus format must not contain # EOF")
	}
}

// TestMetricsOpenMetricsQueryLatencySummary: summary block absent when nil,
// present with UNIT comment and correct quantile lines when set.
func TestMetricsOpenMetricsQueryLatencySummary(t *testing.T) {
	// Absent when nil.
	m := Metrics{Version: "v0.0.12"}
	out := m.MarshalOpenMetrics()
	if strings.Contains(out, "eidetic_query_duration{") {
		t.Errorf("nil latency: OpenMetrics must not emit query_duration block. Got:\n%s", out)
	}
	// Must still end with # EOF even when no latency.
	if !strings.HasSuffix(strings.TrimRight(out, "\n"), "# EOF") {
		t.Errorf("OpenMetrics must end with # EOF regardless of latency. Got:\n%s", out)
	}

	// Present with correct format.
	p50, p95, p99 := 10.5, 95.1, 99.9
	m2 := Metrics{
		Version:    "v0.0.12",
		QueryP50Us: &p50,
		QueryP95Us: &p95,
		QueryP99Us: &p99,
		QueryCount: 500,
	}
	out2 := m2.MarshalOpenMetrics()

	wantLines := []string{
		"# HELP eidetic_query_duration",
		"# TYPE eidetic_query_duration summary",
		"# UNIT eidetic_query_duration microseconds",
		`eidetic_query_duration{quantile="0.5"} 10.500`,
		`eidetic_query_duration{quantile="0.95"} 95.100`,
		`eidetic_query_duration{quantile="0.99"} 99.900`,
		"eidetic_query_duration_count 500",
		"# EOF",
	}
	for _, want := range wantLines {
		if !strings.Contains(out2, want) {
			t.Errorf("OpenMetrics latency summary: want line %q.\nGot:\n%s", want, out2)
		}
	}
}
