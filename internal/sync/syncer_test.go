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
	s := eidetic_sync.New(nil, "", nil)
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
	s := eidetic_sync.New(cfg, dbPath, nil)
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
	s := eidetic_sync.New(cfg, dbPath, nil)
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
	s := eidetic_sync.New(cfg, "/nonexistent/path/engrams.db", nil)
	err := s.SyncNow()
	if err == nil {
		t.Fatal("expected error for missing db file")
	}
}
