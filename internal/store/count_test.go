package store_test

import (
	"context"
	"testing"

	"github.com/eidetic-works/eidetic-daemon/internal/engram"
)

// TestCountEmpty: fresh store reports 0 total + empty by-surface map.
func TestCountEmpty(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	n, err := s.Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("Count empty: got %d, want 0", n)
	}
	bySurface, err := s.CountBySurface(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(bySurface) != 0 {
		t.Errorf("CountBySurface empty: got %d entries, want 0", len(bySurface))
	}
}

// TestCountAndCountBySurfaceAfterInsert: counts reflect inserted rows;
// per-surface map keys/values match the actual ingest pattern.
func TestCountAndCountBySurfaceAfterInsert(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	batch := []engram.Engram{
		{Surface: "claude_code", TS: 1, Payload: "a"},
		{Surface: "claude_code", TS: 2, Payload: "b"},
		{Surface: "claude_code", TS: 3, Payload: "c"},
		{Surface: "cursor", TS: 4, Payload: "d"},
		{Surface: "cowork", TS: 5, Payload: "e"},
		{Surface: "cowork", TS: 6, Payload: "f"},
	}
	if err := s.InsertBatch(ctx, batch); err != nil {
		t.Fatal(err)
	}
	n, err := s.Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 6 {
		t.Errorf("Count: got %d, want 6", n)
	}
	bySurface, err := s.CountBySurface(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{"claude_code": 3, "cursor": 1, "cowork": 2}
	if len(bySurface) != len(want) {
		t.Errorf("CountBySurface key count: got %d, want %d (got=%v)", len(bySurface), len(want), bySurface)
	}
	for k, v := range want {
		if bySurface[k] != v {
			t.Errorf("CountBySurface[%q]: got %d, want %d", k, bySurface[k], v)
		}
	}
}
