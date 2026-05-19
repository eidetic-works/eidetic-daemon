package sync_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

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
