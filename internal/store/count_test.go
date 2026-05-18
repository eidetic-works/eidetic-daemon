package store_test

import (
	"context"
	"testing"

	"github.com/eidetic-works/eidetic-daemon/internal/engram"
)

func TestCountEmptyStore(t *testing.T) {
	s := tempStore(t)
	n, err := s.CountEngrams(context.Background(), "", 0)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("want 0, got %d", n)
	}
}

func TestCountAllSurfaces(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	for _, surface := range []string{"a", "a", "b"} {
		if _, err := s.Insert(ctx, engram.Engram{Surface: surface, TS: 1, Payload: "x"}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	n, err := s.CountEngrams(ctx, "", 0)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 3 {
		t.Errorf("want 3, got %d", n)
	}
}

func TestCountBySurface(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	for _, surface := range []string{"a", "a", "b"} {
		if _, err := s.Insert(ctx, engram.Engram{Surface: surface, TS: 1, Payload: "x"}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	n, err := s.CountEngrams(ctx, "a", 0)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Errorf("want 2, got %d", n)
	}
}

func TestCountSince(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	for _, ts := range []int64{100, 200, 300} {
		if _, err := s.Insert(ctx, engram.Engram{Surface: "a", TS: ts, Payload: "x"}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	n, err := s.CountEngrams(ctx, "", 150)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Errorf("want 2, got %d", n)
	}
}

func TestCountSurfaceAndSince(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	data := []struct {
		surface string
		ts      int64
	}{
		{"a", 100}, {"a", 200}, {"b", 300},
	}
	for _, d := range data {
		if _, err := s.Insert(ctx, engram.Engram{Surface: d.surface, TS: d.ts, Payload: "x"}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	n, err := s.CountEngrams(ctx, "a", 150)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("want 1, got %d", n)
	}
}
