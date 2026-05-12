package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
)

// handleEngramsGET serves GET /engrams?surface=X&limit=N&since=unix-ns.
// Returns 200 + JSON array on success, 400 on missing surface, 405 on
// non-GET, 500 on store error. Param parse failures map to store defaults
// (per docs/PHASE_2_DESIGN.md "forgiving param parse" decision).
func (s *Server) handleEngramsGET(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	surface := q.Get("surface")
	if surface == "" {
		http.Error(w, "surface required", http.StatusBadRequest)
		return
	}

	limit, _ := strconv.Atoi(q.Get("limit"))      // 0 / invalid → store default (50)
	since, _ := strconv.ParseInt(q.Get("since"), 10, 64)

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	rows, err := s.store.Retrieve(ctx, surface, since, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rows)
}
