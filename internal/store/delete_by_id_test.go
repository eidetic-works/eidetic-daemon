package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/eidetic-works/eidetic-daemon/internal/engram"
	"github.com/eidetic-works/eidetic-daemon/internal/store"
)

func TestDeleteByIDRemovesEngram(t *testing.T) {
	s := tempStore(t)
	ctx := context.Background()
	id, err := s.Insert(ctx, engram.Engram{
		Surface: "claude_code", TS: 42, Payload: "delete me",
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	if err := s.DeleteByID(ctx, id); err != nil {
		t.Fatalf("delete by id: %v", err)
	}

	_, err = s.GetByID(ctx, id)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("want ErrNotFound after delete, got %v", err)
	}
}

func TestDeleteByIDNotFoundReturnsErrNotFound(t *testing.T) {
	s := tempStore(t)
	err := s.DeleteByID(context.Background(), 999999)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestDeleteByIDZeroIDReturnsErrNotFound(t *testing.T) {
	s := tempStore(t)
	err := s.DeleteByID(context.Background(), 0)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}
