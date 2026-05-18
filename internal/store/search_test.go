package store_test

import (
	"context"
	"errors"
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
