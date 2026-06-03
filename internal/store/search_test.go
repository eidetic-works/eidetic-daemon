package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/eidetic-works/eidetic-daemon/internal/engram"
	"github.com/eidetic-works/eidetic-daemon/internal/store"
)

func insertForSearch(t *testing.T, s *store.Store, surface, payload string) {
	t.Helper()
	ctx := context.Background()
	_, err := s.Insert(ctx, engram.Engram{
		Surface: surface, TS: 1_000_000_000, Payload: payload,
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
}

func TestSearchEmptyQueryReturnsError(t *testing.T) {
	s := tempStore(t)
	_, err := s.Search(context.Background(), "", "", 10)
	if err == nil {
		t.Fatal("expected error for empty query")
	}
	if !errors.Is(err, store.ErrEmptyQuery) {
		t.Fatalf("want ErrEmptyQuery, got %v", err)
	}
}

func TestSearchFindsKeyword(t *testing.T) {
	s := tempStore(t)
	insertForSearch(t, s, "claude_code", `{"role":"user","payload":"What did the last benchmark say?"}`)
	insertForSearch(t, s, "claude_code", `{"role":"assistant","payload":"I found the test results in the log."}`)

	rows, err := s.Search(context.Background(), "benchmark", "", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 result, got %d", len(rows))
	}
	if rows[0].Surface != "claude_code" {
		t.Errorf("wrong surface: %s", rows[0].Surface)
	}
	if rows[0].Snippet == "" {
		t.Error("expected non-empty Snippet from FTS5 snippet()")
	}
}

func TestSearchPhraseQuery(t *testing.T) {
	s := tempStore(t)
	insertForSearch(t, s, "cursor", `Anjali meetup tomorrow at 5pm`)
	insertForSearch(t, s, "cursor", `tomorrow we should review the PR`)

	// Phrase query — only the first engram matches "meetup tomorrow"
	rows, err := s.Search(context.Background(), `"meetup tomorrow"`, "", 10)
	if err != nil {
		t.Fatalf("search phrase: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 result for phrase, got %d", len(rows))
	}
}

func TestSearchFiltersBySurface(t *testing.T) {
	s := tempStore(t)
	insertForSearch(t, s, "claude_code", "benchmark latency result")
	insertForSearch(t, s, "cursor", "benchmark latency result")

	rows, err := s.Search(context.Background(), "benchmark", "cursor", 10)
	if err != nil {
		t.Fatalf("search with surface filter: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 result (cursor only), got %d", len(rows))
	}
	if rows[0].Surface != "cursor" {
		t.Errorf("wrong surface: %s", rows[0].Surface)
	}
}

func TestSearchLimitClamped(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	// Insert 10 engrams all matching "needle"
	for i := range 10 {
		_, err := s.Insert(ctx, engram.Engram{
			Surface: "cowork", TS: int64(i + 1), Payload: "needle in haystack",
		})
		if err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	rows, err := s.Search(ctx, "needle", "", 3)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(rows) > 3 {
		t.Errorf("limit 3 not honored: got %d", len(rows))
	}
}

func TestSearchNoResults(t *testing.T) {
	s := tempStore(t)
	insertForSearch(t, s, "claude_code", "nothing interesting here")

	rows, err := s.Search(context.Background(), "xyzzy", "", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("want 0 results for impossible query, got %d", len(rows))
	}
}

// TestSearchResponseBounded reproduces the v0.0.X+ regression caught via
// MCP dogfood-loop 2026-06-02: search_engrams("scrubber consolidation
// HARD_BLOCK_FLOOR brand_identity_routing", limit=5) returned 71,272 chars
// across 42 results when the underlying corpus had large payloads, blowing
// the MCP tool-result token budget on the consumer side.
//
// Root cause was that SELECT pulled BOTH e.payload (unbounded, 1KB-100KB per
// row) AND snippet() (~200 chars/row), so callers received full payload bodies
// even though the snippet was intended to replace them for /search.
//
// Fix: drop e.payload from Search()'s SELECT. Payload remains "" on returned
// Engrams (still emitted as "payload": "" — 14 bytes/row — for JSON-shape
// stability with /engrams response, but bounded).
//
// This test seeds a synthetic corpus with deliberately large payloads
// (matching real-world cc-main session_jsonl + cowork_archive surface
// payload sizes), runs Search, and asserts the encoded JSON response stays
// within a bounded budget.
func TestSearchResponseBounded(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()

	const numResults = 50
	const largePayloadBytes = 8 * 1024 // 8KB per row — realistic upper bound

	largeBody := strings.Repeat("syntheticneedle ", largePayloadBytes/16) // ~8KB
	for i := range numResults {
		_, err := s.Insert(ctx, engram.Engram{
			Surface: "synthetic_corpus",
			TS:      int64(i + 1),
			Payload: largeBody,
		})
		if err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	rows, err := s.Search(ctx, "syntheticneedle", "", numResults)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(rows) != numResults {
		t.Fatalf("want %d results, got %d", numResults, len(rows))
	}

	// Pre-fix: 50 rows × 8KB = ~400KB. Bound: 50 rows × ~300 bytes (id + surface
	// + ts + meta + ~200-char snippet + "payload": "" + framing JSON) = ~15KB.
	// Budget 20KB to allow for snippet-length variance + JSON framing overhead.
	const responseSizeBudget = 20 * 1024

	encoded, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("json encode: %v", err)
	}
	if len(encoded) > responseSizeBudget {
		t.Errorf("search response %d bytes exceeds budget %d bytes (regression: payload not dropped from SELECT?)",
			len(encoded), responseSizeBudget)
	}

	// Payload field MUST be "" on Search() returned engrams (drop-from-SELECT contract).
	// Callers needing full payload use GetByID(id).
	for i, r := range rows {
		if r.Payload != "" {
			t.Errorf("row %d Payload should be empty on Search result (snippet replaces it); got %d bytes",
				i, len(r.Payload))
		}
		if r.Snippet == "" {
			t.Errorf("row %d Snippet should be populated on Search result; got empty", i)
		}
	}
}

func TestSearchBackfillOnExistingDB(t *testing.T) {
	// Simulate an existing pre-v0.0.14 database: insert rows via Insert
	// (triggers fire so FTS stays in sync in normal flow). Then verify
	// a fresh Open() on the same path can still search — this exercises
	// the backfill path for real on first open after migration.
	path := t.TempDir() + "/engrams.db"
	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	insertForSearch(t, s, "claude_code", "first session important concept")
	s.Close()

	// Reopen — backfillFTS should detect FTS already populated (triggers
	// fired on Insert above) and skip the full backfill.
	s2, err := store.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()

	rows, err := s2.Search(context.Background(), "concept", "", 10)
	if err != nil {
		t.Fatalf("search after reopen: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 result after reopen, got %d", len(rows))
	}
}

// TestSearchHandlesLiteralDot covers the Bug #3 regression: literal-input
// callers passing tokens FTS5 parses as syntax (e.g. `tb.py`, `feat/foo`)
// previously returned `fts5: syntax error`. Search() now retries with
// phrase-quoting on syntax error so the caller sees zero or more results,
// not a daemon-internal SQL error.
func TestSearchHandlesLiteralDot(t *testing.T) {
	s := tempStore(t)
	insertForSearch(t, s, "claude_code", `editing tb.py for the benchmark`)
	insertForSearch(t, s, "claude_code", `unrelated session about cooking`)

	rows, err := s.Search(context.Background(), "tb.py", "", 10)
	if err != nil {
		t.Fatalf("literal-dot query must not surface fts5 syntax error: %v", err)
	}
	// Result count is not asserted (depends on FTS5 tokenizer behavior on the
	// phrase-quoted retry); the guarantee is "no SQL error escapes to caller".
	if rows == nil {
		t.Fatal("rows should never be nil on success")
	}
}

// TestSearchPreservesPhraseQuerySemantics ensures the try-then-retry refactor
// does not regress callers who already pass FTS5-syntax-aware phrase queries.
// This is the regression that an earlier always-phrase-quote-at-entry shape
// would have introduced — double-wrapping a phrase-quoted input changes its
// FTS5 semantics from "phrase match" to "literal-text-including-quote-chars".
func TestSearchPreservesPhraseQuerySemantics(t *testing.T) {
	s := tempStore(t)
	insertForSearch(t, s, "cursor", `Anjali meetup tomorrow at 5pm`)
	insertForSearch(t, s, "cursor", `tomorrow we should review the PR`)

	// First-try succeeds (phrase query is valid FTS5 syntax); no retry path
	// hit. The phrase `"meetup tomorrow"` matches only the first engram.
	rows, err := s.Search(context.Background(), `"meetup tomorrow"`, "", 10)
	if err != nil {
		t.Fatalf("phrase query must not be touched by retry path: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 result for phrase query (try-then-retry preserves semantics), got %d", len(rows))
	}
}

// TestSearchPropagatesNonSyntaxErrors ensures the try-then-retry path only
// retries on `fts5: syntax error`. Other DB errors (cancelled context, lock
// contention, etc.) must propagate to the caller unchanged so they can be
// mapped to the right HTTP status / surfaced for diagnosis.
func TestSearchPropagatesNonSyntaxErrors(t *testing.T) {
	s := tempStore(t)
	insertForSearch(t, s, "claude_code", "any payload")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the call — QueryContext returns context.Canceled

	_, err := s.Search(ctx, "any", "", 10)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled to propagate; got %v", err)
	}
	if strings.Contains(err.Error(), "fts5: syntax error") {
		t.Fatal("non-syntax error should not be reframed as fts5 syntax error")
	}
}
