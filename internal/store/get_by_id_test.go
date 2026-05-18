package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/eidetic-works/eidetic-daemon/internal/engram"
	"github.com/eidetic-works/eidetic-daemon/internal/store"
)

func TestGetByIDReturnsEngram(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	id, err := s.Insert(ctx, engram.Engram{
		Surface: "claude_code", TS: 42, Payload: "find me",
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	e, err := s.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if e.ID != id {
		t.Errorf("want id=%d, got %d", id, e.ID)
	}
	if e.Payload != "find me" {
		t.Errorf("want payload='find me', got %q", e.Payload)
	}
	if e.Surface != "claude_code" {
		t.Errorf("want surface='claude_code', got %q", e.Surface)
	}
}

func TestGetByIDNotFoundReturnsErrNotFound(t *testing.T) {
	s := tempStore(t)
	_, err := s.GetByID(context.Background(), 999999)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestGetByIDZeroIDReturnsErrNotFound(t *testing.T) {
	s := tempStore(t)
	_, err := s.GetByID(context.Background(), 0)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	// id=0 doesn't exist (SQLite rowid starts at 1) → ErrNotFound
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}
