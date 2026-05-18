package store_test

import (
	"context"
	"testing"

	"github.com/eidetic-works/eidetic-daemon/internal/engram"
)

func TestStatsEmptyStore(t *testing.T) {
	s := tempStore(t)
	snap, err := s.Stats(context.Background())
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if snap.Total != 0 {
		t.Errorf("total: want 0, got %d", snap.Total)
	}
	if snap.OldestNs != 0 || snap.NewestNs != 0 {
		t.Errorf("timestamps: want 0/0, got %d/%d", snap.OldestNs, snap.NewestNs)
	}
}

func TestStatsWithEngrams(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	rows := []engram.Engram{
		{Surface: "claude_code", TS: 1000, Payload: "first"},
		{Surface: "claude_code", TS: 2000, Payload: "second"},
		{Surface: "cursor", TS: 3000, Payload: "third"},
	}
	for _, e := range rows {
		if _, err := s.Insert(ctx, e); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	snap, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}

	if snap.Total != 3 {
		t.Errorf("total: want 3, got %d", snap.Total)
	}
	if snap.BySurface["claude_code"] != 2 {
		t.Errorf("claude_code: want 2, got %d", snap.BySurface["claude_code"])
	}
	if snap.BySurface["cursor"] != 1 {
		t.Errorf("cursor: want 1, got %d", snap.BySurface["cursor"])
	}
	if snap.OldestNs != 1000 {
		t.Errorf("oldest: want 1000, got %d", snap.OldestNs)
	}
	if snap.NewestNs != 3000 {
		t.Errorf("newest: want 3000, got %d", snap.NewestNs)
	}
	if snap.P95LatNs <= 0 {
		t.Errorf("P95LatNs: want > 0, got %d", snap.P95LatNs)
	}
}
