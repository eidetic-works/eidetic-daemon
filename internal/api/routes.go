package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/eidetic-works/eidetic-daemon/internal/store"
)

// handleHealthz serves GET /healthz. Returns 200 + {"status":"ok"}. Used by
// the README quickstart + service managers (launchd / systemd) for liveness
// detection. No store touch — answers from the listener thread, so a stuck
// writer pool doesn't tip the readiness signal.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleSurfaces serves GET /surfaces. Returns 200 + JSON object mapping
// surface name → engram count for all surfaces with at least one engram.
// Empty store returns {}. 405 on non-GET, 500 on store error. (v0.0.13+)
func (s *Server) handleSurfaces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	counts, err := s.store.CountBySurface(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if counts == nil {
		counts = map[string]int64{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(counts)
}

// handleEngrams dispatches /engrams by method:
//   - GET  → handleEngramsGET  (retrieve)
//   - DELETE → handleEngramsDELETE (purge)
func (s *Server) handleEngrams(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleEngramsGET(w, r)
	case http.MethodDelete:
		s.handleEngramsDELETE(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleEngramsGET serves GET /engrams?surface=X&limit=N&since=unix-ns.
// Returns 200 + JSON array on success, 400 on missing surface, 500 on store
// error. Param parse failures map to store defaults ("forgiving param parse").
func (s *Server) handleEngramsGET(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	surface := q.Get("surface")
	if surface == "" {
		http.Error(w, "surface required", http.StatusBadRequest)
		return
	}

	limit, _ := strconv.Atoi(q.Get("limit"))
	since, _ := strconv.ParseInt(q.Get("since"), 10, 64)

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	start := time.Now()
	rows, err := s.store.Retrieve(ctx, surface, since, limit)
	if s.queryLatency != nil {
		s.queryLatency.Record(time.Since(start))
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rows)
}

// handleEngramsDELETE serves DELETE /engrams?surface=X[&before=unix-ns].
// Purges engrams for the given surface. When before is provided and > 0, only
// rows with ts < before are deleted; otherwise all rows for the surface are
// purged. Returns 200 + {"deleted": N} on success, 400 on missing surface,
// 500 on store error. (v0.0.13+)
func (s *Server) handleEngramsDELETE(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	surface := q.Get("surface")
	if surface == "" {
		http.Error(w, "surface required", http.StatusBadRequest)
		return
	}

	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	n, err := s.store.Purge(ctx, surface, before)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]int64{"deleted": n})
}

// handleSearch serves GET /search?q=<fts5-query>[&surface=X][&limit=N].
// Runs an FTS5 full-text search over engram payloads and returns results
// ordered by relevance rank (best match first). Returns the same JSON-array
// shape as GET /engrams for client compatibility.
//
// q is an FTS5 match expression: bare keywords, phrase queries in double
// quotes ("what did the benchmark say"), OR/AND/NOT boolean operators.
// 400 when q is missing or empty. 500 on store error. (v0.0.14+)
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	query := q.Get("q")
	if query == "" {
		http.Error(w, "q required", http.StatusBadRequest)
		return
	}

	surface := q.Get("surface")
	limit, _ := strconv.Atoi(q.Get("limit"))

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	rows, err := s.store.Search(ctx, query, surface, limit)
	if err != nil {
		if errors.Is(err, store.ErrEmptyQuery) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rows)
}
