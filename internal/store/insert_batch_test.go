package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/eidetic-works/eidetic-daemon/internal/engram"
)

func TestInsertBatchTransactional(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	batch := []engram.Engram{
		{Surface: "cursor", TS: 1, Payload: "a"},
		{Surface: "cursor", TS: 2, Payload: "b"},
		{Surface: "cursor", TS: 3, Payload: "c"},
	}
	if err := s.InsertBatch(ctx, batch); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	got, err := s.Retrieve(ctx, "cursor", 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("got %d rows, want 3", len(got))
	}
}

func TestInsertBatchEmpty(t *testing.T) {
	s := tempStore(t)
	if err := s.InsertBatch(context.Background(), nil); err != nil {
		t.Errorf("empty batch should be no-op, got %v", err)
	}
	if err := s.InsertBatch(context.Background(), []engram.Engram{}); err != nil {
		t.Errorf("zero-len batch should be no-op, got %v", err)
	}
}

func TestInsertBatchValidationRejectsAtomically(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	batch := []engram.Engram{
		{Surface: "cursor", TS: 1, Payload: "ok-1"},
		{Surface: "", TS: 2, Payload: "missing-surface"},
		{Surface: "cursor", TS: 3, Payload: "ok-3"},
	}
	err := s.InsertBatch(ctx, batch)
	if err == nil {
		t.Fatal("expected validation error on bad batch")
	}
	if !strings.Contains(err.Error(), "batch[1]") {
		t.Errorf("error should name batch index 1: %v", err)
	}
	// Zero rows committed — pre-flight validation runs before tx open.
	got, _ := s.Retrieve(ctx, "cursor", 0, 50)
	if len(got) != 0 {
		t.Errorf("got %d rows, want 0 (atomic rejection)", len(got))
	}
}

func TestInsertBatchMaxPayloadEnforced(t *testing.T) {
	s := tempStore(t)
	huge := strings.Repeat("x", (1<<20)+1)
	batch := []engram.Engram{{Surface: "cursor", TS: 1, Payload: huge}}
	if err := s.InsertBatch(context.Background(), batch); err == nil {
		t.Errorf("expected MaxPayloadBytes rejection")
	}
}

func TestExplainQueryHitsIndex(t *testing.T) {
	s := tempStore(t)
	plan, err := s.ExplainQuery(context.Background())
	if err != nil {
		t.Fatalf("ExplainQuery: %v", err)
	}
	if !strings.Contains(plan, "idx_surface_ts") {
		t.Errorf("EXPLAIN missing idx_surface_ts:\n%s", plan)
	}
	// Negative-shape: a SCAN-without-USING-INDEX line indicates the index
	// would not be hit and the hot path would degrade.
	for _, line := range strings.Split(plan, "\n") {
		if strings.Contains(line, "SCAN engrams") && !strings.Contains(line, "USING INDEX") {
			t.Errorf("EXPLAIN shows table scan: %s", line)
		}
	}
}
