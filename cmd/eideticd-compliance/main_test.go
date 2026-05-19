package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/eidetic-works/eidetic-daemon/internal/engram"
	"github.com/eidetic-works/eidetic-daemon/internal/store"
)

func TestLoadPolicy_valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "retention-policy.json")
	data := `{"surfaces":{"claude_code":30,"cursor":90,"cowork":0}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := loadPolicy(path)
	if err != nil {
		t.Fatalf("loadPolicy: %v", err)
	}
	if p.Surfaces["claude_code"] != 30 {
		t.Errorf("claude_code: got %d want 30", p.Surfaces["claude_code"])
	}
	if p.Surfaces["cursor"] != 90 {
		t.Errorf("cursor: got %d want 90", p.Surfaces["cursor"])
	}
	if p.Surfaces["cowork"] != 0 {
		t.Errorf("cowork: got %d want 0 (infinite)", p.Surfaces["cowork"])
	}
}

func TestLoadPolicy_notExist(t *testing.T) {
	_, err := loadPolicy("/nonexistent/retention-policy.json")
	if !os.IsNotExist(err) {
		t.Errorf("want ErrNotExist, got %v", err)
	}
}

func TestLoadPolicy_invalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("{bad json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := loadPolicy(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestResolveDataPath_envOverride(t *testing.T) {
	t.Setenv("EIDETIC_DATA_DIR", "/tmp/eidetic-test")
	got, err := resolveDataPath("compliance.log", "")
	if err != nil {
		t.Fatal(err)
	}
	want := "/tmp/eidetic-test/compliance.log"
	if got != want {
		t.Errorf("got %s want %s", got, want)
	}
}

func TestResolveDataPath_dbOverride(t *testing.T) {
	got, err := resolveDataPath("compliance.log", "/custom/dir/engrams.db")
	if err != nil {
		t.Fatal(err)
	}
	want := "/custom/dir/compliance.log"
	if got != want {
		t.Errorf("got %s want %s", got, want)
	}
}

// TestPurgeRetention is an integration test against a real store.
// It inserts engrams with old and fresh timestamps, then calls store.Purge
// directly to validate the retention cutoff math, mirroring what the daemon does.
func TestPurgeRetention(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "engrams.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	now := time.Now()

	// Insert 3 old engrams (45 days old) and 2 fresh ones.
	oldTS := now.Add(-45 * 24 * time.Hour).UnixNano()
	freshTS := now.Add(-5 * 24 * time.Hour).UnixNano()

	for i := 0; i < 3; i++ {
		_, err := st.Insert(ctx, engram.Engram{
			Surface: "cursor",
			TS:      oldTS - int64(i*1000),
			Payload: "old engram",
		})
		if err != nil {
			t.Fatalf("insert old: %v", err)
		}
	}
	for i := 0; i < 2; i++ {
		_, err := st.Insert(ctx, engram.Engram{
			Surface: "cursor",
			TS:      freshTS + int64(i*1000),
			Payload: "fresh engram",
		})
		if err != nil {
			t.Fatalf("insert fresh: %v", err)
		}
	}

	// Cutoff = 30 days ago — should delete the 3 old rows, keep the 2 fresh.
	cutoff := now.Add(-30 * 24 * time.Hour).UnixNano()
	deleted, err := st.Purge(ctx, "cursor", cutoff)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if deleted != 3 {
		t.Errorf("deleted=%d want 3", deleted)
	}

	remaining, err := st.Retrieve(ctx, "cursor", 0, 0, 10, false)
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(remaining) != 2 {
		t.Errorf("remaining=%d want 2", len(remaining))
	}
}

// TestPolicyRoundtrip verifies Policy marshals and unmarshals cleanly.
func TestPolicyRoundtrip(t *testing.T) {
	original := Policy{
		Surfaces: map[string]int{
			"claude_code": 30,
			"cursor":      90,
		},
	}
	b, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Policy
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Surfaces["claude_code"] != 30 {
		t.Errorf("claude_code: got %d want 30", decoded.Surfaces["claude_code"])
	}
}
