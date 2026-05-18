package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/eidetic-works/eidetic-daemon/internal/engram"
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
//   - GET    → handleEngramsGET    (retrieve)
//   - POST   → handleEngramsPOST   (direct insert, v0.0.16+)
//   - DELETE → handleEngramsDELETE (purge)
func (s *Server) handleEngrams(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleEngramsGET(w, r)
	case http.MethodPost:
		s.handleEngramsPOST(w, r)
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

// handleEngramsPOST serves POST /engrams — direct API-side engram insertion
// (v0.0.16+). Accepts a single JSON object:
//
//	{"surface":"claude_code","payload":"...","ts":unix-ns,"meta":"..."}
//
// surface and payload are required. ts defaults to time.Now().UnixNano() when
// omitted or zero — callers that set their own ts must supply unix nanoseconds.
// meta is optional. Returns 201 + {"id":N} on success.
func (s *Server) handleEngramsPOST(w http.ResponseWriter, r *http.Request) {
	// Guard against oversized bodies (same cap as store.MaxPayloadBytes + JSON envelope).
	r.Body = http.MaxBytesReader(w, r.Body, store.MaxPayloadBytes+4096)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "request body too large or unreadable", http.StatusBadRequest)
		return
	}

	var e engram.Engram
	if err := json.Unmarshal(body, &e); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if e.TS == 0 {
		e.TS = time.Now().UnixNano()
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	id, err := s.store.Insert(ctx, e)
	if err != nil {
		if errors.Is(err, store.ErrInvalidEngram) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]int64{"id": id})
}

// handleEngramsCount serves GET /engrams/count?[surface=X][&since=unix-ns].
// Returns 200 + {"count": N}. surface is optional; omit to count across all
// surfaces. since (optional) filters to engrams with ts > since. 405 on
// non-GET. (v0.0.20+)
func (s *Server) handleEngramsCount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	surface := q.Get("surface")
	since, _ := strconv.ParseInt(q.Get("since"), 10, 64)

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	n, err := s.store.CountEngrams(ctx, surface, since)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]int64{"count": n})
}

// handleEngramsByID dispatches /engrams/{id} by method:
//   - GET    → fetch a single engram by primary key (v0.0.18+)
//   - DELETE → remove a single engram by primary key (v0.0.19+)
func (s *Server) handleEngramsByID(w http.ResponseWriter, r *http.Request) {
	rawID := r.PathValue("id")
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "id must be a positive integer", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	switch r.Method {
	case http.MethodGet:
		e, err := s.store.GetByID(ctx, id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(e)

	case http.MethodDelete:
		if err := s.store.DeleteByID(ctx, id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int{"deleted": 1})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleEngramsBatch serves POST /engrams/batch — bulk API-side insertion
// (v0.0.17+). Accepts a JSON array of engram objects:
//
//	[{"surface":"...","payload":"...","ts":unix-ns,"meta":"..."}, ...]
//
// All items are inserted in a single transaction via InsertBatch. Any
// validation failure rolls back the entire batch and returns 400.
// ts defaults to time.Now().UnixNano() per-item when omitted or zero.
// Body is capped at 32 MiB. Returns 201 + {"inserted": N} on success.
func (s *Server) handleEngramsBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	const batchBodyCap = 32 << 20 // 32 MiB
	r.Body = http.MaxBytesReader(w, r.Body, batchBodyCap)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "request body too large or unreadable", http.StatusBadRequest)
		return
	}

	var items []engram.Engram
	if err := json.Unmarshal(body, &items); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(items) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]int{"inserted": 0})
		return
	}

	now := time.Now().UnixNano()
	for i := range items {
		if items[i].TS == 0 {
			items[i].TS = now
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	if err := s.store.InsertBatch(ctx, items); err != nil {
		if errors.Is(err, store.ErrInvalidEngram) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]int{"inserted": len(items)})
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

// handleRecent serves GET /recent?since=unix-ns&limit=N.
// Returns up to limit engrams ordered newest-first across all surfaces.
// since (optional): Unix nanoseconds — only return engrams with ts > since.
// limit: 1-500, default 50.
func (s *Server) handleRecent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	since, _ := strconv.ParseInt(q.Get("since"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	rows, err := s.store.Recent(ctx, since, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rows)
}
