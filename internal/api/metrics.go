package api

import (
	"context"
	"encoding/json"
	"net/http"
)

// Metrics is the JSON body returned by GET /metrics. v0.0.7 schema —
// additive-only across versions (callers can rely on existing fields
// continuing to exist; new fields may appear).
type Metrics struct {
	Version          string           `json:"version"`
	UptimeSeconds    int64            `json:"uptime_seconds"`
	EngramTotal      int64            `json:"engram_total"`
	EngramBySurface  map[string]int64 `json:"engram_by_surface"`
	CaptureSkipped   uint64           `json:"capture_skipped"`
	DBPath           string           `json:"db_path"`
	DBSizeBytes      int64            `json:"db_size_bytes"`
}

// MetricsProvider is supplied by main() so the api package stays decoupled
// from cmd-side state (Watcher pointer, process start time, version
// string injected via -ldflags). The handler calls it on every request;
// providers should be cheap (no heavy IO inside the closure).
type MetricsProvider func(ctx context.Context) (Metrics, error)

// handleMetrics serves GET /metrics. Returns 200 + Metrics JSON on
// success, 503 if no provider is configured (provider is optional;
// daemons started before v0.0.7 wiring could be detected this way),
// 500 if the provider errors. Per-request timeout from Server.timeout.
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
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(m)
}
