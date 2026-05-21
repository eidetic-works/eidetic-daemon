package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/eidetic-works/eidetic-daemon/internal/engram"
	"github.com/eidetic-works/eidetic-daemon/internal/store"
	"github.com/eidetic-works/eidetic-daemon/internal/textsearch"
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

// handleEngramsGET serves GET /engrams?[surface=X]&[limit=N]&[since=unix-ns]&[before=unix-ns]&[order=asc].
// surface is optional (v0.0.23+); omit to retrieve across all surfaces using idx_ts.
// Returns 200 + JSON array on success, 500 on store error.
// Param parse failures map to store defaults ("forgiving param parse").
func (s *Server) handleEngramsGET(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	surface := q.Get("surface")

	limit, _ := strconv.Atoi(q.Get("limit"))
	since, _ := strconv.ParseInt(q.Get("since"), 10, 64)
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	asc := q.Get("order") == "asc"

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	start := time.Now()
	rows, err := s.store.Retrieve(ctx, surface, since, before, limit, asc)
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

// askCacheInstance is the per-Server LRU cache for /ask responses (v0.0.45+).
// 5-minute TTL is short enough that newly-arrived engrams surface quickly;
// 64-entry cap bounds memory even under dashboard polling.
var askCacheInstance = newAskCache(64, 5*time.Minute)

// AskCacheStats exposes the /ask cache's hit/miss counters for /metrics
// emission. Safe to call concurrently. (v0.0.49+)
func AskCacheStats() (hits, misses uint64, size int) {
	return askCacheInstance.Stats()
}

// handleAsk serves GET /ask?question=<text>[&surface=X][&limit=N].
// Wraps the question→FTS keyword extraction + RAG-format flow that the
// eidetic-mcp nucleus_ask tool provides — but accessible to non-MCP clients
// (web dashboard, mobile, plain curl).
//
// Response shape:
//
//	{
//	  "question":     "What was that Postgres trick I learned?",
//	  "fts_query":    "postgres OR trick OR learned",
//	  "instructions": "Use these engrams to answer...",
//	  "engrams":      [...]
//	}
//
// 400 if question missing. 500 on store error. (v0.0.38+)
func (s *Server) handleAsk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	question := strings.TrimSpace(q.Get("question"))
	if question == "" {
		http.Error(w, "question required", http.StatusBadRequest)
		return
	}

	surface := q.Get("surface")
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 10
	}
	if limit > 30 {
		limit = 30
	}

	// v0.0.45+: cache by canonical signature so dashboard polling + repeat
	// questions don't re-hit FTS5. TTL is short (5 min) so newly-written
	// engrams surface quickly.
	cacheKey := question + "\x00" + surface + "\x00" + strconv.Itoa(limit)
	if cached, ok := askCacheInstance.Get(cacheKey); ok {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Ask-Cache", "hit")
		_, _ = w.Write(cached)
		return
	}

	fts := questionToFTS(question)

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	rows, err := s.store.Search(ctx, fts, surface, limit)
	if err != nil {
		// Fall back to the raw question if the FTS expression was rejected.
		rows, err = s.store.Search(ctx, question, surface, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	var instructions string
	if len(rows) == 0 {
		instructions = "No engrams matched. Tell the user no relevant engrams " +
			"were found; do NOT fabricate an answer."
	} else {
		instructions = "You are answering the question above using ONLY the " +
			"engram excerpts below. Each engram is a snapshot from the user's " +
			"past work. Cite the surface + timestamp when you reference one. " +
			"If the engrams don't answer the question, say so honestly — do " +
			"NOT fabricate. Prefer recent engrams when relevance ties."
	}

	resp := map[string]any{
		"question":     question,
		"fts_query":    fts,
		"instructions": instructions,
		"engrams":      rows,
	}
	// Marshal once; serve + cache the same bytes.
	body, mErr := json.Marshal(resp)
	if mErr != nil {
		http.Error(w, mErr.Error(), http.StatusInternalServerError)
		return
	}
	askCacheInstance.Put(cacheKey, body)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Ask-Cache", "miss")
	_, _ = w.Write(body)
}

// askStopwords is kept as a re-export of textsearch.Stopwords for the /digest
// tokenizer (which also strips stop-words but uses its own per-engram token
// loop, not a one-shot question). Single source of truth lives in
// internal/textsearch.
var askStopwords = textsearch.Stopwords

// questionToFTS delegates to the shared internal/textsearch package so
// HTTP /ask, cmd/eideticd --ask, and (by mirror) the Python MCP tool all
// give identical retrieval behavior. (v0.0.53+)
func questionToFTS(question string) string {
	return textsearch.QuestionToFTS(question)
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
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	rows, err := s.store.Recent(ctx, since, before, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rows)
}

// handleExport serves GET /export[?surface=X][&since=ns][&before=ns].
// Streams every engram in the local store as newline-delimited JSON (NDJSON),
// one engram per line, chronological order (oldest first). Designed for
// right-to-export use cases: backup before uninstall, migrate to another
// store, audit / compliance dump.
//
// Memory-bounded via paginated RetrieveAfter (1000 rows per page) using a
// compound (ts, id) cursor. Safe to call against a 10M-row store; the
// response streams as it generates.
//
// Why compound cursor: chunked-capture splitOversized() emits N chunks at
// identical ts, and handleEngramsBatch assigns one `time.Now()` per call to
// every item with TS==0. The prior `ts > since` cursor dropped every row
// sharing the boundary ts — N-1 chunks per oversized record, N-1 rows per
// API batch insert. The (ts > c.ts) OR (ts = c.ts AND id > c.id) shape
// covers shared-ts records.
//
// Audit ref: CRITICAL `internal/api/routes.go:537`.
//
// Content-Type: application/x-ndjson (so curl users can pipe through `jq`).
// (v0.0.42+)
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	surface := q.Get("surface")
	since, _ := strconv.ParseInt(q.Get("since"), 10, 64)
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Hint to curl: filename suggestion when saving with -O.
	w.Header().Set("Content-Disposition", `attachment; filename="engrams-export.ndjson"`)

	flusher, _ := w.(http.Flusher)
	enc := json.NewEncoder(w)

	const pageSize = 1000
	// Compound cursor (ts, id). Start position: since acts as initial ts
	// bound, id=0 so the first page includes any row at exactly that ts.
	// If since == 0, both fields are 0 → no after-filter, full export.
	cursorTS := since
	var cursorID int64
	total := 0

	for {
		ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
		rows, err := s.store.RetrieveAfter(ctx, surface, cursorTS, cursorID, before, pageSize)
		cancel()
		if err != nil {
			// Mid-stream errors can't change HTTP status — already sent 200.
			// Log via response body as a final NDJSON line so callers can detect.
			_ = enc.Encode(map[string]any{"_export_error": err.Error()})
			return
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			if err := enc.Encode(row); err != nil {
				return // client likely disconnected; nothing useful to do
			}
		}
		total += len(rows)
		// Advance compound cursor past the last row. Asc order → last row
		// in the page is the new lower bound; id breaks shared-ts ties.
		last := rows[len(rows)-1]
		cursorTS = last.TS
		cursorID = last.ID
		if len(rows) < pageSize {
			break
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	// Final summary line so clients can verify completion + count.
	_ = enc.Encode(map[string]any{"_export_complete": true, "_count": total})
}

// handleTimeline serves GET /timeline?[since=ns][&before=ns][&surfaces=a,b,c].
// Returns engrams across the named surfaces interleaved by timestamp, oldest
// first — answers "what was I doing on a given day across every tool at once?"
// Default surfaces: all configured surfaces.
// (v0.0.47+)
func (s *Server) handleTimeline(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	since, _ := strconv.ParseInt(q.Get("since"), 10, 64)
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}

	var surfaces []string
	if raw := q.Get("surfaces"); raw != "" {
		for _, s := range strings.Split(raw, ",") {
			if s = strings.TrimSpace(s); s != "" {
				surfaces = append(surfaces, s)
			}
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	// If no surface filter, just call Retrieve with empty surface — store's
	// cross-surface path handles it. If filtered, fetch per surface, merge, sort.
	if len(surfaces) == 0 {
		rows, err := s.store.Retrieve(ctx, "", since, before, limit, true)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeTimelineResponse(w, rows, surfaces)
		return
	}

	type fetchResult struct {
		engrams []engram.Engram
		err     error
	}
	results := make([]engram.Engram, 0, limit)
	for _, surface := range surfaces {
		rows, err := s.store.Retrieve(ctx, surface, since, before, limit, true)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		results = append(results, rows...)
	}
	// Sort merged result by ts asc; cap to limit.
	sortEngramsByTS(results)
	if len(results) > limit {
		results = results[:limit]
	}
	writeTimelineResponse(w, results, surfaces)
}

func writeTimelineResponse(w http.ResponseWriter, rows []engram.Engram, surfaces []string) {
	resp := map[string]any{
		"engrams":  rows,
		"count":    len(rows),
		"surfaces": surfaces,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// sortEngramsByTS is a small in-place sort. Avoids pulling in sort.Slice
// dependency overhead — engrams are typically <=1000 rows for /timeline.
func sortEngramsByTS(rows []engram.Engram) {
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j-1].TS > rows[j].TS; j-- {
			rows[j-1], rows[j] = rows[j], rows[j-1]
		}
	}
}

// handleDigest serves GET /digest?[window=24h|7d|30d].
// Returns a structured summary the host LLM can render into prose:
//   - Per-surface engram counts in the window
//   - Top 5 most-active hours
//   - Top 10 most-FTS-rankable terms (heuristic: most-frequent 4+-char words)
//   - 20 sampled engram payloads (head/middle/tail for context)
//
// Designed to back a `nucleus_digest` MCP tool or a weekly recap email.
// (v0.0.47+)
func (s *Server) handleDigest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	window := r.URL.Query().Get("window")
	if window == "" {
		window = "7d"
	}
	var dur time.Duration
	switch window {
	case "24h":
		dur = 24 * time.Hour
	case "7d":
		dur = 7 * 24 * time.Hour
	case "30d":
		dur = 30 * 24 * time.Hour
	default:
		http.Error(w, "window must be 24h | 7d | 30d", http.StatusBadRequest)
		return
	}
	since := time.Now().Add(-dur).UnixNano()

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	// Pull up to 2000 engrams in the window — enough for stats, cap on memory.
	rows, err := s.store.Retrieve(ctx, "", since, 0, 2000, true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	bySurface := make(map[string]int, 8)
	byHour := make(map[int]int, 24)
	wordCount := make(map[string]int, 256)
	for _, row := range rows {
		bySurface[row.Surface]++
		hr := time.Unix(0, row.TS).Hour()
		byHour[hr]++
		// Crude tokenizer — split on non-alnum, lowercase, keep 4+ chars.
		var cur []rune
		for _, ch := range row.Payload {
			if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
				cur = append(cur, ch)
			} else {
				if len(cur) >= 4 {
					t := strings.ToLower(string(cur))
					if _, stop := askStopwords[t]; !stop {
						wordCount[t]++
					}
				}
				cur = cur[:0]
			}
		}
		if len(cur) >= 4 {
			t := strings.ToLower(string(cur))
			if _, stop := askStopwords[t]; !stop {
				wordCount[t]++
			}
		}
	}

	type kv struct {
		Key   string `json:"key"`
		Count int    `json:"count"`
	}
	topWords := topN(wordCount, 10)
	topHours := topNInt(byHour, 5)

	// Sample 20 engrams: 5 head, 10 middle, 5 tail
	var samples []engram.Engram
	if len(rows) <= 20 {
		samples = rows
	} else {
		samples = append(samples, rows[:5]...)
		mid := len(rows) / 2
		samples = append(samples, rows[mid-5:mid+5]...)
		samples = append(samples, rows[len(rows)-5:]...)
	}

	resp := map[string]any{
		"window":         window,
		"since":          since,
		"total_engrams":  len(rows),
		"by_surface":     bySurface,
		"top_hours":      topHours,
		"top_terms":      topWords,
		"sample_engrams": samples,
		"instructions": "Render this digest as a 5-7 sentence recap for the user. " +
			"Lead with the most-active surface + the dominant theme (from top_terms). " +
			"Mention any unusual hour pattern. Reference sampled engrams briefly. " +
			"If total_engrams is 0, say so plainly — do not fabricate.",
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func topN(m map[string]int, n int) []map[string]any {
	type kv struct {
		Key   string
		Count int
	}
	list := make([]kv, 0, len(m))
	for k, v := range m {
		list = append(list, kv{k, v})
	}
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j-1].Count < list[j].Count; j-- {
			list[j-1], list[j] = list[j], list[j-1]
		}
	}
	if len(list) > n {
		list = list[:n]
	}
	out := make([]map[string]any, len(list))
	for i, kv := range list {
		out[i] = map[string]any{"term": kv.Key, "count": kv.Count}
	}
	return out
}

func topNInt(m map[int]int, n int) []map[string]any {
	type kv struct {
		Key   int
		Count int
	}
	list := make([]kv, 0, len(m))
	for k, v := range m {
		list = append(list, kv{k, v})
	}
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j-1].Count < list[j].Count; j-- {
			list[j-1], list[j] = list[j], list[j-1]
		}
	}
	if len(list) > n {
		list = list[:n]
	}
	out := make([]map[string]any, len(list))
	for i, kv := range list {
		out[i] = map[string]any{"hour": kv.Key, "count": kv.Count}
	}
	return out
}

// handleHooks serves GET /hooks. Returns per-hook config snapshot + fire counts
// from the configured Dispatcher (v0.0.56+). 503 when hooks are not wired up
// (no ~/.eidetic/hooks.json). Useful for dashboard/observability tooling.
func (s *Server) handleHooks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.hookStatusFn == nil {
		http.Error(w, "hooks not configured (drop ~/.eidetic/hooks.json to enable)", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.hookStatusFn())
}
