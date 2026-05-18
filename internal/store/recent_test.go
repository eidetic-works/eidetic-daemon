package store_test

import (
	"context"
	"testing"

	"github.com/eidetic-works/eidetic-daemon/internal/engram"
)

func insertRecent(t *testing.T, s interface {
	Insert(context.Context, engram.Engram) (int64, error)
}, surface string, ts int64, payload string) {
	t.Helper()
	ctx := context.Background()
	_, err := s.Insert(ctx, engram.Engram{Surface: surface, TS: ts, Payload: payload})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
}

func TestRecentDefaultLimit(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()

	for i := range 5 {
		_, err := s.Insert(ctx, engram.Engram{
			Surface: "claude_code", TS: int64(i + 1), Payload: "payload",
		})
		if err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	rows, err := s.Recent(ctx, 0, 0)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("want 5 rows, got %d", len(rows))
	}
	// newest-first
	if rows[0].TS < rows[len(rows)-1].TS {
		t.Errorf("want descending TS order; got rows[0].TS=%d rows[-1].TS=%d", rows[0].TS, rows[len(rows)-1].TS)
	}
}

func TestRecentLimitClamped(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()

	for i := range 10 {
		_, err := s.Insert(ctx, engram.Engram{
			Surface: "cursor", TS: int64(i + 1), Payload: "x",
		})
		if err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	rows, err := s.Recent(ctx, 0, 3)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(rows) > 3 {
		t.Errorf("limit 3 not honored: got %d", len(rows))
	}
}

func TestRecentSinceFilter(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()

	// ts=1,2,3 — we'll filter since=2 so only ts=3 returns
	for _, ts := range []int64{1, 2, 3} {
		_, err := s.Insert(ctx, engram.Engram{
			Surface: "claude_code", TS: ts, Payload: "payload",
		})
		if err != nil {
			t.Fatalf("insert ts=%d: %v", ts, err)
		}
	}

	rows, err := s.Recent(ctx, 2, 50)
	if err != nil {
		t.Fatalf("recent with since: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row (ts>2), got %d", len(rows))
	}
	if rows[0].TS != 3 {
		t.Errorf("want TS=3, got %d", rows[0].TS)
	}
}

func TestRecentCrossSurface(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()

	for _, surf := range []string{"claude_code", "cursor", "vim"} {
		_, err := s.Insert(ctx, engram.Engram{
			Surface: surf, TS: 1, Payload: "x",
		})
		if err != nil {
			t.Fatalf("insert %s: %v", surf, err)
		}
	}

	rows, err := s.Recent(ctx, 0, 50)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("want 3 rows (all surfaces), got %d", len(rows))
	}
}

func TestRecentEmptyDB(t *testing.T) {
	s := tempStore(t)

	rows, err := s.Recent(context.Background(), 0, 50)
	if err != nil {
		t.Fatalf("recent on empty db: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("want 0 rows on empty db, got %d", len(rows))
	}
}

func TestRecentLimitMax500(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()

	for i := range 10 {
		_, err := s.Insert(ctx, engram.Engram{
			Surface: "x", TS: int64(i + 1), Payload: "x",
		})
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// limit > 500 should be clamped; still returns only 10 available
	rows, err := s.Recent(ctx, 0, 9999)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(rows) != 10 {
		t.Fatalf("want 10 rows, got %d", len(rows))
	}
}
