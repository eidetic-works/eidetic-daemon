package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/eidetic-works/eidetic-daemon/internal/engram"
	"github.com/eidetic-works/eidetic-daemon/internal/store"
)

func TestCounterDeleteByIDSync(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	var firstA int64
	for _, surface := range []string{"a", "a", "b"} {
		id, err := s.Insert(ctx, engram.Engram{Surface: surface, TS: 1, Payload: "x"})
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
		if surface == "a" && firstA == 0 {
			firstA = id
		}
	}
	if err := s.DeleteByID(ctx, firstA); err != nil {
		t.Fatalf("delete: %v", err)
	}
	n, err := s.Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Errorf("Count: want 2, got %d", n)
	}
	by, err := s.CountBySurface(ctx)
	if err != nil {
		t.Fatalf("count by surface: %v", err)
	}
	if by["a"] != 1 || by["b"] != 1 {
		t.Errorf("CountBySurface: want a=1 b=1, got %v", by)
	}
	na, err := s.CountEngrams(ctx, "a", 0)
	if err != nil {
		t.Fatalf("count engrams: %v", err)
	}
	if na != 1 {
		t.Errorf("CountEngrams(a): want 1, got %d", na)
	}
}

func TestCounterPurgeSync(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	data := []struct {
		surface string
		ts      int64
	}{
		{"a", 100}, {"a", 200}, {"a", 300}, {"b", 100}, {"b", 200},
	}
	for _, d := range data {
		if _, err := s.Insert(ctx, engram.Engram{Surface: d.surface, TS: d.ts, Payload: "x"}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// Partial purge: a rows with ts < 250 (two rows).
	deleted, err := s.Purge(ctx, "a", 250)
	if err != nil {
		t.Fatalf("purge partial: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("purge partial: want 2 deleted, got %d", deleted)
	}
	by, err := s.CountBySurface(ctx)
	if err != nil {
		t.Fatalf("count by surface: %v", err)
	}
	if by["a"] != 1 || by["b"] != 2 {
		t.Errorf("after partial purge: want a=1 b=2, got %v", by)
	}

	// Full purge of b: surface must disappear from CountBySurface (n=0 filtered).
	if _, err := s.Purge(ctx, "b", 0); err != nil {
		t.Fatalf("purge full: %v", err)
	}
	by, err = s.CountBySurface(ctx)
	if err != nil {
		t.Fatalf("count by surface: %v", err)
	}
	if _, ok := by["b"]; ok {
		t.Errorf("after full purge: surface b should be omitted, got %v", by)
	}
	n, err := s.Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("Count after purges: want 1, got %d", n)
	}
}

func TestCounterBackfillOnReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engrams.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctx := context.Background()
	for _, surface := range []string{"a", "a", "b"} {
		if _, err := s.Insert(ctx, engram.Engram{Surface: surface, TS: 1, Payload: "x"}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Simulate a pre-ADR-022 store: engrams populated, counter table empty.
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	if _, err := raw.Exec(`DELETE FROM engram_counts`); err != nil {
		t.Fatalf("clear counters: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("raw close: %v", err)
	}

	s2, err := store.Open(path)
	if err != nil {
		t.Fatalf("re-open: %v", err)
	}
	defer s2.Close()
	n, err := s2.Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 3 {
		t.Errorf("Count after backfill: want 3, got %d", n)
	}
	by, err := s2.CountBySurface(ctx)
	if err != nil {
		t.Fatalf("count by surface: %v", err)
	}
	if by["a"] != 2 || by["b"] != 1 {
		t.Errorf("CountBySurface after backfill: want a=2 b=1, got %v", by)
	}
}

func TestCounterEmptyAfterDeleteAll(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	var ids []int64
	for range 2 {
		id, err := s.Insert(ctx, engram.Engram{Surface: "a", TS: 1, Payload: "x"})
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
		ids = append(ids, id)
	}
	for _, id := range ids {
		if err := s.DeleteByID(ctx, id); err != nil {
			t.Fatalf("delete: %v", err)
		}
	}
	n, err := s.Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("Count: want 0, got %d", n)
	}
	by, err := s.CountBySurface(ctx)
	if err != nil {
		t.Fatalf("count by surface: %v", err)
	}
	if len(by) != 0 {
		t.Errorf("CountBySurface: want empty, got %v", by)
	}
}
