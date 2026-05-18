// Package sync uploads the local engrams SQLite database to Cloudflare R2
// via the eidetic-sync Worker (bridge/cloudflare/worker.js).
//
// Config is read from $EIDETIC_DATA_DIR/sync.json (or ~/.eidetic/sync.json).
// If the config file is absent, sync is disabled — zero runtime cost for
// users who haven't opted in to cloud sync.
//
// Sync is triggered by the Syncer.TriggerIfDue() call, which the main loop
// calls every 60 seconds. Actual upload fires when:
//   - at least SyncInterval has elapsed since the last upload, AND
//   - no new engrams have arrived in the last IdleSeconds (avoids splitting
//     an active session's context across two backups)
package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/eidetic-works/eidetic-daemon/internal/store"
)

const (
	defaultSyncInterval = 60 * time.Minute
	defaultIdleSeconds  = 30
	maxDBSize           = 500 * 1024 * 1024 // 500 MB guard matches Worker limit
)

// Config is deserialized from sync.json.
type Config struct {
	WorkerURL    string `json:"worker_url"`    // e.g. "https://eidetic-sync.your-acct.workers.dev"
	APIKey       string `json:"api_key"`       // Bearer token (matches EIDETIC_API_KEY secret)
	DeviceID     string `json:"device_id"`     // 4-64 lowercase alphanum/-/_ (e.g. "macbook-m2")
	SyncInterval int    `json:"sync_interval"` // minutes; default 60
}

// Syncer wraps sync config + last-upload state.
type Syncer struct {
	cfg      Config
	dbPath   string
	store    *store.Store
	mu       sync.Mutex
	lastSync time.Time
	lastRow  int64 // store row-count at last sync, used for idle check
}

// LoadConfig reads sync.json from dataDir. Returns nil + nil if the file
// does not exist (sync disabled). Returns non-nil error on parse failure.
func LoadConfig(dataDir string) (*Config, error) {
	p := filepath.Join(dataDir, "sync.json")
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sync: read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("sync: parse config: %w", err)
	}
	if cfg.WorkerURL == "" || cfg.APIKey == "" || cfg.DeviceID == "" {
		return nil, fmt.Errorf("sync: config missing required fields (worker_url, api_key, device_id)")
	}
	return &cfg, nil
}

// New constructs a Syncer. Returns nil if cfg is nil (sync disabled).
func New(cfg *Config, dbPath string, s *store.Store) *Syncer {
	if cfg == nil {
		return nil
	}
	interval := defaultSyncInterval
	if cfg.SyncInterval > 0 {
		interval = time.Duration(cfg.SyncInterval) * time.Minute
	}
	syn := &Syncer{
		cfg:    *cfg,
		dbPath: dbPath,
		store:  s,
	}
	_ = interval
	return syn
}

// TriggerIfDue uploads the database if syncInterval has elapsed and the
// daemon has been idle (no new rows) for at least idleSeconds.
// Safe to call concurrently; a mutex prevents double-upload.
// Returns nil if no upload was needed, or a non-fatal upload error.
func (s *Syncer) TriggerIfDue() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	interval := defaultSyncInterval
	if s.cfg.SyncInterval > 0 {
		interval = time.Duration(s.cfg.SyncInterval) * time.Minute
	}

	if time.Since(s.lastSync) < interval {
		return nil
	}

	count, err := s.store.Count(context.Background())
	if err != nil {
		return fmt.Errorf("sync: row count: %w", err)
	}

	// Idle check: if rowcount grew in the last poll, defer until it stabilises
	if count != s.lastRow {
		s.lastRow = count
		return nil // not idle yet
	}

	if err := s.upload(); err != nil {
		return err
	}
	s.lastSync = time.Now()
	s.lastRow = count
	return nil
}

// SyncNow uploads immediately, ignoring the interval and idle checks.
// Used by the `eideticd --sync-now` flag.
func (s *Syncer) SyncNow() error {
	if s == nil {
		return fmt.Errorf("sync: not configured (missing ~/.eidetic/sync.json)")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.upload()
}

func (s *Syncer) upload() error {
	f, err := os.Open(s.dbPath)
	if err != nil {
		return fmt.Errorf("sync: open db: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("sync: stat db: %w", err)
	}
	if info.Size() > maxDBSize {
		return fmt.Errorf("sync: db too large (%d bytes > %d limit)", info.Size(), maxDBSize)
	}

	body, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("sync: read db: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, s.cfg.WorkerURL+"/sync", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("sync: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.APIKey)
	req.Header.Set("X-Device-ID", s.cfg.DeviceID)
	req.Header.Set("Content-Type", "application/x-sqlite3")
	req.ContentLength = int64(len(body))

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("sync: upload: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		rb, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("sync: worker returned %d: %s", resp.StatusCode, rb)
	}
	return nil
}
