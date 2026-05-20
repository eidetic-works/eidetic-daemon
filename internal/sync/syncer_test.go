package sync_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	eidetic_sync "github.com/eidetic-works/eidetic-daemon/internal/sync"
)

func TestLoadConfig_Missing(t *testing.T) {
	dir := t.TempDir()
	cfg, err := eidetic_sync.LoadConfig(dir)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if cfg != nil {
		t.Fatal("expected nil config when sync.json absent")
	}
}

func TestLoadConfig_Valid(t *testing.T) {
	dir := t.TempDir()
	b, _ := json.Marshal(map[string]interface{}{
		"worker_url": "https://example.workers.dev",
		"api_key":    "test-key",
		"device_id":  "test-device",
	})
	if err := os.WriteFile(filepath.Join(dir, "sync.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := eidetic_sync.LoadConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.WorkerURL != "https://example.workers.dev" {
		t.Errorf("worker_url mismatch: %q", cfg.WorkerURL)
	}
}

func TestLoadConfig_MissingFields(t *testing.T) {
	dir := t.TempDir()
	b, _ := json.Marshal(map[string]interface{}{
		"worker_url": "https://example.workers.dev",
		// missing api_key + device_id
	})
	os.WriteFile(filepath.Join(dir, "sync.json"), b, 0600)
	_, err := eidetic_sync.LoadConfig(dir)
	if err == nil {
		t.Fatal("expected error for missing fields")
	}
}

func TestLoadConfig_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "sync.json"), []byte("not json"), 0600)
	_, err := eidetic_sync.LoadConfig(dir)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestNewReturnsNilForNilConfig(t *testing.T) {
	s := eidetic_sync.New(nil, "", "", nil)
	if s != nil {
		t.Fatal("expected nil Syncer for nil config")
	}
}

func TestSyncNowWithNilSyncer(t *testing.T) {
	var s *eidetic_sync.Syncer
	err := s.SyncNow()
	if err == nil {
		t.Fatal("expected error from nil Syncer.SyncNow()")
	}
}

func TestTriggerIfDueWithNilSyncer(t *testing.T) {
	var s *eidetic_sync.Syncer
	if err := s.TriggerIfDue(); err != nil {
		t.Fatalf("expected nil error from nil Syncer.TriggerIfDue(), got %v", err)
	}
}

// TestUploadToWorker verifies the HTTP contract against a mock server.
func TestUploadToWorker(t *testing.T) {
	const fakeKey = "test-bearer-key"
	const fakeDevice = "test-device-01"

	var gotAuth, gotDevice, gotContentType string
	var gotBodyLen int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sync" || r.Method != http.MethodPost {
			http.Error(w, "unexpected", 404)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		gotDevice = r.Header.Get("X-Device-ID")
		gotContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		gotBodyLen = len(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"key":"engrams/test/db.db","byteLength":10}`))
	}))
	defer srv.Close()

	// Write a fake SQLite file
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "engrams.db")
	if err := os.WriteFile(dbPath, []byte("SQLITE3"), 0600); err != nil {
		t.Fatal(err)
	}

	cfg := &eidetic_sync.Config{
		WorkerURL: srv.URL,
		APIKey:    fakeKey,
		DeviceID:  fakeDevice,
	}
	s := eidetic_sync.New(cfg, dbPath, dir, nil)
	if err := s.SyncNow(); err != nil {
		t.Fatalf("SyncNow() error: %v", err)
	}

	if gotAuth != "Bearer "+fakeKey {
		t.Errorf("Authorization header: got %q, want %q", gotAuth, "Bearer "+fakeKey)
	}
	if gotDevice != fakeDevice {
		t.Errorf("X-Device-ID: got %q, want %q", gotDevice, fakeDevice)
	}
	if gotContentType != "application/x-sqlite3" {
		t.Errorf("Content-Type: got %q, want %q", gotContentType, "application/x-sqlite3")
	}
	if gotBodyLen != len("SQLITE3") {
		t.Errorf("body length: got %d, want %d", gotBodyLen, len("SQLITE3"))
	}
}

// TestUploadRejectsNon201 verifies that a non-201 response from the Worker
// surfaces as an error (not silently swallowed).
func TestUploadRejectsNon201(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("worker error"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "engrams.db")
	os.WriteFile(dbPath, []byte("SQLITE3"), 0600)

	cfg := &eidetic_sync.Config{
		WorkerURL: srv.URL,
		APIKey:    "key",
		DeviceID:  "dev",
	}
	s := eidetic_sync.New(cfg, dbPath, dir, nil)
	err := s.SyncNow()
	if err == nil {
		t.Fatal("expected error for non-201 response")
	}
	if err.Error() == "" {
		t.Error("expected non-empty error message")
	}
}

// TestUploadMissingDB verifies that a missing database file surfaces as error.
func TestUploadMissingDB(t *testing.T) {
	cfg := &eidetic_sync.Config{
		WorkerURL: "http://localhost:9999",
		APIKey:    "key",
		DeviceID:  "dev",
	}
	s := eidetic_sync.New(cfg, "/nonexistent/path/engrams.db", t.TempDir(), nil)
	err := s.SyncNow()
	if err == nil {
		t.Fatal("expected error for missing db file")
	}
}

// TestRestoreFromConfig verifies the download → atomic replace flow.
func TestRestoreFromConfig(t *testing.T) {
	const fakeKey = "restore-bearer"
	const fakeDevice = "restore-device"
	const fakeDB = "SQLite format 3\x00fake-restored-db-content"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/download" || r.Method != http.MethodGet {
			http.Error(w, "unexpected", 404)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+fakeKey {
			http.Error(w, "unauthorized", 401)
			return
		}
		if r.Header.Get("X-Device-ID") != fakeDevice {
			http.Error(w, "bad device", 400)
			return
		}
		w.Header().Set("Content-Type", "application/x-sqlite3")
		w.Header().Set("X-Backup-Key", "engrams/"+fakeDevice+"/engrams-9999.db")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(fakeDB))
	}))
	defer srv.Close()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "engrams.db")
	// Write an existing DB so we can verify it gets backed up.
	os.WriteFile(dbPath, []byte("old-db-content"), 0600)

	cfg := &eidetic_sync.Config{
		WorkerURL: srv.URL,
		APIKey:    fakeKey,
		DeviceID:  fakeDevice,
	}
	if err := eidetic_sync.RestoreFromConfig(cfg, dbPath); err != nil {
		t.Fatalf("RestoreFromConfig() error: %v", err)
	}

	// Restored DB should contain the server's payload.
	got, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("read restored db: %v", err)
	}
	if string(got) != fakeDB {
		t.Errorf("restored content mismatch: got %q, want %q", got, fakeDB)
	}

	// Backup of old DB should exist.
	bak, err := os.ReadFile(dbPath + ".bak")
	if err != nil {
		t.Fatalf("read backup db: %v", err)
	}
	if string(bak) != "old-db-content" {
		t.Errorf("backup content mismatch: got %q", bak)
	}
}

// TestRestoreFromConfig_NilConfig verifies that nil config returns an error.
func TestRestoreFromConfig_NilConfig(t *testing.T) {
	err := eidetic_sync.RestoreFromConfig(nil, "/tmp/db.db")
	if err == nil {
		t.Fatal("expected error for nil config")
	}
}

// TestRestoreFromConfig_NoBackup verifies that a 404 from the Worker surfaces as error.
func TestRestoreFromConfig_NoBackup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"no backup found"}`))
	}))
	defer srv.Close()

	cfg := &eidetic_sync.Config{
		WorkerURL: srv.URL,
		APIKey:    "key",
		DeviceID:  "dev",
	}
	err := eidetic_sync.RestoreFromConfig(cfg, filepath.Join(t.TempDir(), "engrams.db"))
	if err == nil {
		t.Fatal("expected error for 404 response")
	}
}

// TestLoadSyncState_Missing verifies that a missing state file returns zero-value (not error).
func TestLoadSyncState_Missing(t *testing.T) {
	state, err := eidetic_sync.LoadSyncState(t.TempDir())
	if err != nil {
		t.Fatalf("expected nil error for missing state file, got %v", err)
	}
	if !state.LastSync.IsZero() {
		t.Error("expected zero-value LastSync for missing state file")
	}
}

// TestUploadWritesSyncState verifies that a successful upload persists sync-state.json.
func TestUploadWritesSyncState(t *testing.T) {
	const backupKey = "engrams/dev01/2026-05-19T000000Z.db"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backup-Key", backupKey)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"key":"` + backupKey + `"}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "engrams.db")
	os.WriteFile(dbPath, []byte("SQLITE3fake"), 0600)

	cfg := &eidetic_sync.Config{
		WorkerURL: srv.URL,
		APIKey:    "key",
		DeviceID:  "dev01",
	}
	s := eidetic_sync.New(cfg, dbPath, dir, nil)
	if err := s.SyncNow(); err != nil {
		t.Fatalf("SyncNow() error: %v", err)
	}

	state, err := eidetic_sync.LoadSyncState(dir)
	if err != nil {
		t.Fatalf("LoadSyncState error: %v", err)
	}
	if state.LastSync.IsZero() {
		t.Error("expected LastSync to be set after upload")
	}
	if state.LastKey != backupKey {
		t.Errorf("LastKey: got %q, want %q", state.LastKey, backupKey)
	}
	if state.LastBytes != int64(len("SQLITE3fake")) {
		t.Errorf("LastBytes: got %d, want %d", state.LastBytes, len("SQLITE3fake"))
	}
}

// TestCheckConfig_NilConfig verifies CheckConfig handles missing sync.json gracefully.
func TestCheckConfig_NilConfig(t *testing.T) {
	err := eidetic_sync.CheckConfig(nil, t.TempDir())
	if err == nil {
		t.Fatal("expected error for nil config")
	}
}

// TestCheckConfig_WorkerOK verifies CheckConfig reports healthy when Worker returns 200.
func TestCheckConfig_WorkerOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"ok"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	cfg := &eidetic_sync.Config{
		WorkerURL: srv.URL,
		APIKey:    "key",
		DeviceID:  "dev01",
	}
	err := eidetic_sync.CheckConfig(cfg, t.TempDir())
	if err != nil {
		t.Fatalf("CheckConfig returned error for healthy Worker: %v", err)
	}
}

// TestUploadSendsXTeamIDWhenSet verifies that the X-Team-ID header is included
// when Config.TeamID is set, and omitted when it isn't.
func TestUploadSendsXTeamIDWhenSet(t *testing.T) {
	var gotTeamID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTeamID = r.Header.Get("X-Team-ID")
		w.Header().Set("X-Backup-Key", "k")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "engrams.db")
	os.WriteFile(dbPath, []byte("SQLITE3"), 0600)

	// Without team_id — header should be empty
	cfgSolo := &eidetic_sync.Config{WorkerURL: srv.URL, APIKey: "k", DeviceID: "dev"}
	s1 := eidetic_sync.New(cfgSolo, dbPath, dir, nil)
	if err := s1.SyncNow(); err != nil {
		t.Fatal(err)
	}
	if gotTeamID != "" {
		t.Errorf("solo upload: X-Team-ID = %q, want empty", gotTeamID)
	}

	// With team_id — header should be set
	cfgTeam := &eidetic_sync.Config{WorkerURL: srv.URL, APIKey: "k", DeviceID: "dev", TeamID: "acme-eng"}
	s2 := eidetic_sync.New(cfgTeam, dbPath, dir, nil)
	if err := s2.SyncNow(); err != nil {
		t.Fatal(err)
	}
	if gotTeamID != "acme-eng" {
		t.Errorf("team upload: X-Team-ID = %q, want acme-eng", gotTeamID)
	}
}

// TestUploadAppendsHistory verifies that successive uploads push onto the ring
// buffer and the buffer is capped (no unbounded growth).
func TestUploadAppendsHistory(t *testing.T) {
	var uploadCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uploadCount++
		w.Header().Set("X-Backup-Key", fmt.Sprintf("engrams/dev/upload-%d.db", uploadCount))
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "engrams.db")
	os.WriteFile(dbPath, []byte("SQLITE3"), 0600)

	cfg := &eidetic_sync.Config{WorkerURL: srv.URL, APIKey: "k", DeviceID: "dev"}
	s := eidetic_sync.New(cfg, dbPath, dir, nil)

	// Push 12 uploads — ring buffer cap is 10.
	for i := 0; i < 12; i++ {
		if err := s.SyncNow(); err != nil {
			t.Fatalf("SyncNow #%d: %v", i, err)
		}
	}

	state, _ := eidetic_sync.LoadSyncState(dir)
	if len(state.History) != 10 {
		t.Errorf("History length: got %d, want 10 (cap)", len(state.History))
	}
	// Newest first: the 12th upload should be at index 0.
	if state.History[0].Key != "engrams/dev/upload-12.db" {
		t.Errorf("newest entry: got %q, want engrams/dev/upload-12.db", state.History[0].Key)
	}
	// Oldest in the buffer should be upload-3 (12 - 10 + 1 = 3).
	if state.History[9].Key != "engrams/dev/upload-3.db" {
		t.Errorf("oldest entry: got %q, want engrams/dev/upload-3.db", state.History[9].Key)
	}
}

// TestWatchConfig_CreateAndRemove verifies the hot-reload watcher fires onChange
// with a non-nil config when sync.json is created, and nil when removed.
func TestWatchConfig_CreateAndRemove(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events := make(chan *eidetic_sync.Config, 4)
	go func() {
		eidetic_sync.WatchConfig(ctx, dir, func(c *eidetic_sync.Config) {
			events <- c
		})
	}()

	// Give the watcher time to attach.
	time.Sleep(100 * time.Millisecond)

	// Create a valid sync.json.
	cfg := `{"worker_url":"https://example.com","api_key":"key","device_id":"dev01","sync_interval":60}`
	cfgPath := filepath.Join(dir, "sync.json")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-events:
		if got == nil {
			t.Fatal("expected non-nil config after create")
		}
		if got.DeviceID != "dev01" {
			t.Errorf("DeviceID: got %q, want dev01", got.DeviceID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for create event")
	}

	// Remove sync.json.
	if err := os.Remove(cfgPath); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-events:
		if got != nil {
			t.Errorf("expected nil config after remove, got %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for remove event")
	}
}

// TestWatchConfig_InvalidJSON verifies that a malformed sync.json triggers
// onChange(nil) rather than crashing the watcher.
func TestWatchConfig_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events := make(chan *eidetic_sync.Config, 2)
	go func() {
		eidetic_sync.WatchConfig(ctx, dir, func(c *eidetic_sync.Config) {
			events <- c
		})
	}()

	time.Sleep(100 * time.Millisecond)

	if err := os.WriteFile(filepath.Join(dir, "sync.json"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-events:
		if got != nil {
			t.Errorf("expected nil for invalid JSON, got %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for invalid-json event")
	}
}

// TestCheckConfig_WorkerUnauth verifies CheckConfig surfaces auth failure.
func TestCheckConfig_WorkerUnauth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	cfg := &eidetic_sync.Config{
		WorkerURL: srv.URL,
		APIKey:    "wrong-key",
		DeviceID:  "dev01",
	}
	err := eidetic_sync.CheckConfig(cfg, t.TempDir())
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
}
