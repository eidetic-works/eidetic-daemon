package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
)

// Prometheus exposition + OpenMetrics format constants.
// Prometheus: https://prometheus.io/docs/instrumenting/exposition_formats/
// OpenMetrics: https://github.com/OpenObservability/OpenMetrics/blob/main/specification/OpenMetrics.md
const (
	prometheusContentType  = "text/plain; version=0.0.4; charset=utf-8"
	openMetricsContentType = "application/openmetrics-text; version=1.0.0; charset=utf-8"
	jsonContentType        = "application/json"
)

// Metrics is the JSON body returned by GET /metrics. v0.0.7 schema —
// additive-only across versions (callers can rely on existing fields
// continuing to exist; new fields may appear).
type Metrics struct {
	Version         string           `json:"version"`
	UptimeSeconds   int64            `json:"uptime_seconds"`
	EngramTotal     int64            `json:"engram_total"`
	EngramBySurface map[string]int64 `json:"engram_by_surface"`
	CaptureSkipped  uint64           `json:"capture_skipped"`
	DBPath          string           `json:"db_path"`
	DBSizeBytes     int64            `json:"db_size_bytes"`

	// Query latency percentiles (v0.0.12+). Nil when fewer than 2 samples
	// have been recorded (daemon just started or no /engrams calls yet).
	// Values in microseconds. QueryCount is the reservoir sample count
	// (capped at tracker capacity, not total request count).
	QueryP50Us  *float64 `json:"query_p50_us,omitempty"`
	QueryP95Us  *float64 `json:"query_p95_us,omitempty"`
	QueryP99Us  *float64 `json:"query_p99_us,omitempty"`
	QueryCount  int      `json:"query_count,omitempty"`
}

// MetricsProvider is supplied by main() so the api package stays decoupled
// from cmd-side state (Watcher pointer, process start time, version
// string injected via -ldflags). The handler calls it on every request;
// providers should be cheap (no heavy IO inside the closure).
type MetricsProvider func(ctx context.Context) (Metrics, error)

// handleMetrics serves GET /metrics. Content-type negotiation per Accept
// header (v0.0.10+):
//   - Accept: application/json → JSON Metrics body (v0.0.7 contract; default for backward-compat)
//   - Accept: text/plain → Prometheus exposition format (v0.0.10+)
//   - Accept: */* or missing → JSON (preserves v0.0.7 default for callers
//     that don't negotiate; switch to Prometheus-default at v1.0 ADR if
//     scraper-share warrants)
//
// Returns 200 on success, 503 if no provider is configured, 500 if the
// provider errors, 405 on non-GET. Per-request timeout from Server.timeout.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.metrics == nil {
		http.Error(w, "metrics not configured", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()
	m, err := s.metrics(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	accept := r.Header.Get("Accept")
	// OpenMetrics takes precedence over Prometheus when both are accepted
	// (real Prometheus scrapers do this — they prefer OpenMetrics if the
	// server supports it). v0.0.11+.
	if wantsOpenMetrics(accept) {
		w.Header().Set("Content-Type", openMetricsContentType)
		_, _ = w.Write([]byte(m.MarshalOpenMetrics()))
		return
	}
	if wantsPrometheus(accept) {
		w.Header().Set("Content-Type", prometheusContentType)
		_, _ = w.Write([]byte(m.MarshalPrometheus()))
		return
	}
	w.Header().Set("Content-Type", jsonContentType)
	_ = json.NewEncoder(w).Encode(m)
}

// wantsOpenMetrics returns true when the Accept header explicitly
// requests OpenMetrics (application/openmetrics-text). Triggered only
// on the explicit MIME type — not on text/plain (that's Prometheus
// exposition).
func wantsOpenMetrics(accept string) bool {
	if accept == "" {
		return false
	}
	for _, part := range strings.Split(accept, ",") {
		mediaType := strings.TrimSpace(part)
		if i := strings.IndexByte(mediaType, ';'); i >= 0 {
			mediaType = strings.TrimSpace(mediaType[:i])
		}
		if mediaType == "application/openmetrics-text" {
			return true
		}
	}
	return false
}

// wantsPrometheus returns true when the Accept header indicates the
// client wants Prometheus exposition format. Only triggers on explicit
// text/plain (with optional version=0.0.4 parameter); */* and missing
// Accept default to JSON for v0.0.7 backward-compat.
func wantsPrometheus(accept string) bool {
	if accept == "" {
		return false
	}
	for _, part := range strings.Split(accept, ",") {
		mediaType := strings.TrimSpace(part)
		// Strip any q= or version= parameters; we only care about the type.
		if i := strings.IndexByte(mediaType, ';'); i >= 0 {
			mediaType = strings.TrimSpace(mediaType[:i])
		}
		if mediaType == "text/plain" {
			return true
		}
	}
	return false
}

// MarshalPrometheus renders a Metrics value as a Prometheus exposition-format
// string per https://prometheus.io/docs/instrumenting/exposition_formats/.
//
// Schema (additive-only; new metric names may be added across versions):
//   eidetic_uptime_seconds            (gauge) — daemon uptime
//   eidetic_engrams_total             (gauge) — total engrams in store
//   eidetic_engrams_by_surface_total  (gauge, label: surface=<name>) — per-surface count
//   eidetic_capture_skipped_total     (counter) — engrams dropped due to size
//   eidetic_db_size_bytes             (gauge) — store DB file size
//   eidetic_build_info                (gauge, label: version=<v>) — value 1, version label
//
// All gauges/counters use snake_case suffix per Prometheus convention.
// engram counts use _total per counter naming convention even where the
// underlying value is a gauge (matches Prometheus client_golang norms for
// "current count" patterns).
func (m Metrics) MarshalPrometheus() string {
	var b strings.Builder

	writeMetric := func(name, help, mtype string, value interface{}, labels ...string) {
		fmt.Fprintf(&b, "# HELP %s %s\n", name, help)
		fmt.Fprintf(&b, "# TYPE %s %s\n", name, mtype)
		labelStr := ""
		if len(labels) > 0 {
			labelStr = "{" + strings.Join(labels, ",") + "}"
		}
		fmt.Fprintf(&b, "%s%s %v\n", name, labelStr, value)
	}

	writeMetric("eidetic_uptime_seconds", "Daemon uptime in seconds since process start.", "gauge", m.UptimeSeconds)
	writeMetric("eidetic_engrams_total", "Total engrams in the local store across all surfaces.", "gauge", m.EngramTotal)

	// Per-surface engram counts as a single metric with surface label.
	// Sort surfaces for deterministic output (test stability + diff-friendly).
	if len(m.EngramBySurface) > 0 {
		fmt.Fprintln(&b, "# HELP eidetic_engrams_by_surface_total Engram count grouped by capture surface.")
		fmt.Fprintln(&b, "# TYPE eidetic_engrams_by_surface_total gauge")
		surfaces := make([]string, 0, len(m.EngramBySurface))
		for s := range m.EngramBySurface {
			surfaces = append(surfaces, s)
		}
		sort.Strings(surfaces)
		for _, s := range surfaces {
			fmt.Fprintf(&b, `eidetic_engrams_by_surface_total{surface=%q} %d`+"\n", s, m.EngramBySurface[s])
		}
	}

	writeMetric("eidetic_capture_skipped_total", "Engrams dropped at capture due to payload exceeding store.MaxPayloadBytes.", "counter", m.CaptureSkipped)
	writeMetric("eidetic_db_size_bytes", "On-disk size of the engram SQLite database file.", "gauge", m.DBSizeBytes)
	writeMetric("eidetic_build_info", "Daemon build version. Value is always 1; version is in the label.", "gauge", 1, fmt.Sprintf("version=%q", m.Version))

	// v0.0.12+: query latency summary. Omitted when no data yet.
	if m.QueryP50Us != nil && !math.IsNaN(*m.QueryP50Us) {
		fmt.Fprintln(&b, "# HELP eidetic_query_duration_microseconds /engrams query latency percentiles (reservoir, last ~1000 samples).")
		fmt.Fprintln(&b, "# TYPE eidetic_query_duration_microseconds summary")
		fmt.Fprintf(&b, `eidetic_query_duration_microseconds{quantile="0.5"} %.3f`+"\n", *m.QueryP50Us)
		fmt.Fprintf(&b, `eidetic_query_duration_microseconds{quantile="0.95"} %.3f`+"\n", *m.QueryP95Us)
		fmt.Fprintf(&b, `eidetic_query_duration_microseconds{quantile="0.99"} %.3f`+"\n", *m.QueryP99Us)
		fmt.Fprintf(&b, "eidetic_query_duration_microseconds_count %d\n", m.QueryCount)
	}

	return b.String()
}

// MarshalOpenMetrics renders Metrics in OpenMetrics 1.0.0 exposition
// format per https://github.com/OpenObservability/OpenMetrics/blob/main/specification/OpenMetrics.md.
//
// Differences from Prometheus exposition (v0.0.10):
//   - Counter metric names MUST end in `_total` (the v0.0.10
//     `eidetic_capture_skipped_total` already conforms; gauges keep their
//     names); for OpenMetrics we ALSO drop the `_total` suffix from the
//     declared metric name and append `_total` only on the value line per
//     spec § "Counter Metric type"
//   - UNIT comment per metric type (e.g., `# UNIT eidetic_uptime seconds`)
//   - Stricter HELP escaping (we don't currently emit special chars; safe
//     no-op until that changes)
//   - MUST end with `# EOF` line
//   - Counter values MAY include `_created` timestamps (we don't track
//     creation time per metric; omitted)
//
// Schema mirrors MarshalPrometheus to make Grafana/Prometheus dashboards
// portable across the two formats with minimal config tweaks.
func (m Metrics) MarshalOpenMetrics() string {
	var b strings.Builder

	writeGauge := func(name, help, unit string, value interface{}, labels ...string) {
		fmt.Fprintf(&b, "# HELP %s %s\n", name, help)
		fmt.Fprintf(&b, "# TYPE %s gauge\n", name)
		if unit != "" {
			fmt.Fprintf(&b, "# UNIT %s %s\n", name, unit)
		}
		labelStr := ""
		if len(labels) > 0 {
			labelStr = "{" + strings.Join(labels, ",") + "}"
		}
		fmt.Fprintf(&b, "%s%s %v\n", name, labelStr, value)
	}

	writeGauge("eidetic_uptime_seconds", "Daemon uptime in seconds since process start.", "seconds", m.UptimeSeconds)
	writeGauge("eidetic_engrams", "Total engrams in the local store across all surfaces.", "", m.EngramTotal)

	if len(m.EngramBySurface) > 0 {
		fmt.Fprintln(&b, "# HELP eidetic_engrams_by_surface Engram count grouped by capture surface.")
		fmt.Fprintln(&b, "# TYPE eidetic_engrams_by_surface gauge")
		surfaces := make([]string, 0, len(m.EngramBySurface))
		for s := range m.EngramBySurface {
			surfaces = append(surfaces, s)
		}
		sort.Strings(surfaces)
		for _, s := range surfaces {
			fmt.Fprintf(&b, `eidetic_engrams_by_surface{surface=%q} %d`+"\n", s, m.EngramBySurface[s])
		}
	}

	// Counter per OpenMetrics § Counter Metric type: declared name has NO
	// _total suffix; value line appends `_total`.
	fmt.Fprintln(&b, "# HELP eidetic_capture_skipped Engrams dropped at capture due to payload exceeding store.MaxPayloadBytes.")
	fmt.Fprintln(&b, "# TYPE eidetic_capture_skipped counter")
	fmt.Fprintf(&b, "eidetic_capture_skipped_total %d\n", m.CaptureSkipped)

	writeGauge("eidetic_db_size_bytes", "On-disk size of the engram SQLite database file.", "bytes", m.DBSizeBytes)
	writeGauge("eidetic_build_info", "Daemon build version. Value is always 1; version is in the label.", "", 1, fmt.Sprintf("version=%q", m.Version))

	// v0.0.12+: query latency summary. Omitted when no data yet.
	if m.QueryP50Us != nil && !math.IsNaN(*m.QueryP50Us) {
		fmt.Fprintln(&b, "# HELP eidetic_query_duration /engrams query latency percentiles (reservoir, last ~1000 samples).")
		fmt.Fprintln(&b, "# TYPE eidetic_query_duration summary")
		fmt.Fprintln(&b, "# UNIT eidetic_query_duration microseconds")
		fmt.Fprintf(&b, `eidetic_query_duration{quantile="0.5"} %.3f`+"\n", *m.QueryP50Us)
		fmt.Fprintf(&b, `eidetic_query_duration{quantile="0.95"} %.3f`+"\n", *m.QueryP95Us)
		fmt.Fprintf(&b, `eidetic_query_duration{quantile="0.99"} %.3f`+"\n", *m.QueryP99Us)
		fmt.Fprintf(&b, "eidetic_query_duration_count %d\n", m.QueryCount)
	}

	// Mandatory EOF line per spec.
	fmt.Fprintln(&b, "# EOF")

	return b.String()
}
