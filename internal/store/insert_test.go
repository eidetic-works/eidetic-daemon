package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/eidetic-works/eidetic-daemon/internal/engram"
	"github.com/eidetic-works/eidetic-daemon/internal/store"
)

func TestInsertReturnsID(t *testing.T) {
	s := tempStore(t)
	id, err := s.Insert(context.Background(), engram.Engram{
		Surface: "claude_code", TS: 1_000_000_000, Payload: "hello",
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if id <= 0 {
		t.Errorf("want positive id, got %d", id)
	}
}

func TestInsertEmptySurfaceReturnsErrInvalidEngram(t *testing.T) {
	s := tempStore(t)
	_, err := s.Insert(context.Background(), engram.Engram{
		Surface: "", TS: 1, Payload: "x",
	})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !errors.Is(err, store.ErrInvalidEngram) {
		t.Errorf("want ErrInvalidEngram, got %v", err)
	}
}

func TestInsertZeroTSReturnsErrInvalidEngram(t *testing.T) {
	s := tempStore(t)
	_, err := s.Insert(context.Background(), engram.Engram{
		Surface: "cursor", TS: 0, Payload: "x",
	})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !errors.Is(err, store.ErrInvalidEngram) {
		t.Errorf("want ErrInvalidEngram, got %v", err)
	}
}

func TestInsertEmptyPayloadReturnsErrInvalidEngram(t *testing.T) {
	s := tempStore(t)
	_, err := s.Insert(context.Background(), engram.Engram{
		Surface: "vim", TS: 1, Payload: "",
	})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !errors.Is(err, store.ErrInvalidEngram) {
		t.Errorf("want ErrInvalidEngram, got %v", err)
	}
}

func TestInsertIsRetrievable(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	_, err := s.Insert(ctx, engram.Engram{
		Surface: "cowork", TS: 42, Payload: "persisted",
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	rows, err := s.Retrieve(ctx, "cowork", 0, 10)
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	if rows[0].Payload != "persisted" {
		t.Errorf("want 'persisted', got %q", rows[0].Payload)
	}
}
