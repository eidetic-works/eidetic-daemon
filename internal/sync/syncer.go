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
	"github.com/fsnotify/fsnotify"
)

const (
	defaultSyncInterval = 60 * time.Minute
	defaultIdleSeconds  = 30
	maxDBSize           = 500 * 1024 * 1024 // 500 MB guard matches Worker limit
	stateFile           = "sync-state.json"
)

// SyncState is persisted to <dataDir>/sync-state.json after each successful upload.
// It survives daemon restarts so --stats / --backups can show cloud backup history.
type SyncState struct {
	LastSync  time.Time     `json:"last_sync"`
	LastKey   string        `json:"last_key"`
	LastBytes int64         `json:"last_bytes"`
	History   []BackupEntry `json:"history,omitempty"` // ring buffer, newest first, capped at maxBackupHistory
}

// BackupEntry is one row in the SyncState.History ring buffer.
type BackupEntry struct {
	SyncedAt time.Time `json:"synced_at"`
	Key      string    `json:"key"`
	Bytes    int64     `json:"bytes"`
}

const maxBackupHistory = 10

// LoadSyncState reads the persisted sync state from dataDir. Returns zero-value
// SyncState (not an error) if the file does not exist yet.
func LoadSyncState(dataDir string) (SyncState, error) {
	var s SyncState
	b, err := os.ReadFile(filepath.Join(dataDir, stateFile))
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("sync: read state: %w", err)
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("sync: parse state: %w", err)
	}
	return s, nil
}

// Config is deserialized from sync.json.
type Config struct {
	WorkerURL    string `json:"worker_url"`    // e.g. "https://eidetic-sync.your-acct.workers.dev"
	APIKey       string `json:"api_key"`       // Bearer token (matches EIDETIC_API_KEY secret)
	DeviceID     string `json:"device_id"`     // 4-64 lowercase alphanum/-/_ (e.g. "macbook-m2")
	SyncInterval int    `json:"sync_interval"` // minutes; default 60

	// TeamID (v0.0.39+) — optional team identifier for shared-team engrams.
	// When set, every upload includes X-Team-ID header. The Worker uses this
	// to bucket uploads under engrams/team/<team_id>/<device_id>/... so any
	// seat can query the shared team prefix. Solo Pro subscribers omit this.
	// Format: 4-32 lowercase alphanum/-/_, e.g. "acme-engineering".
	TeamID string `json:"team_id,omitempty"`
}

// Syncer wraps sync config + last-upload state.
type Syncer struct {
	cfg      Config
	dbPath   string
	dataDir  string
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
func New(cfg *Config, dbPath, dataDir string, s *store.Store) *Syncer {
	if cfg == nil {
		return nil
	}
	interval := defaultSyncInterval
	if cfg.SyncInterval > 0 {
		interval = time.Duration(cfg.SyncInterval) * time.Minute
	}
	syn := &Syncer{
		cfg:     *cfg,
		dbPath:  dbPath,
		dataDir: dataDir,
		store:   s,
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

// WatchConfig watches dataDir for sync.json create/modify/remove events and
// invokes onChange with the freshly-loaded config (nil if file removed or
// invalid). Debounces rapid events (300ms). Blocks until ctx is canceled.
//
// Use case: hot-reload — Pro customer drops sync.json into ~/.eidetic/ and
// sync starts within seconds without restarting the daemon.
func WatchConfig(ctx context.Context, dataDir string, onChange func(*Config)) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("sync watch: create watcher: %w", err)
	}
	defer w.Close()

	// Watch the parent dir — fsnotify can't watch a file that doesn't exist yet.
	if err := w.Add(dataDir); err != nil {
		return fmt.Errorf("sync watch: add %s: %w", dataDir, err)
	}

	target := filepath.Join(dataDir, "sync.json")
	var (
		debounceMu sync.Mutex
		pending    *time.Timer
	)

	fire := func() {
		debounceMu.Lock()
		if pending != nil {
			pending.Stop()
		}
		pending = time.AfterFunc(300*time.Millisecond, func() {
			cfg, err := LoadConfig(dataDir)
			if err != nil {
				onChange(nil)
				return
			}
			onChange(cfg)
		})
		debounceMu.Unlock()
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if filepath.Clean(ev.Name) != target {
				continue
			}
			if ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Rename|fsnotify.Remove) != 0 {
				fire()
			}
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			return fmt.Errorf("sync watch: %w", err)
		}
	}
}

// CheckConfig validates sync.json configuration and tests Worker connectivity.
// Prints a human-readable report to stdout. Returns non-nil if config is missing
// or the Worker is unreachable. Designed to run before store.Open.
func CheckConfig(cfg *Config, dataDir string) error {
	if cfg == nil {
		fmt.Printf("  sync.json:  not found at %s\n", filepath.Join(dataDir, "sync.json"))
		fmt.Printf("  sync:       disabled (drop sync.json to enable cloud backup)\n")
		return fmt.Errorf("sync not configured")
	}

	fmt.Printf("  worker_url: %s\n", cfg.WorkerURL)
	fmt.Printf("  device_id:  %s\n", cfg.DeviceID)
	if cfg.TeamID != "" {
		fmt.Printf("  team_id:    %s (shared-team mode)\n", cfg.TeamID)
	}
	if cfg.SyncInterval > 0 {
		fmt.Printf("  interval:   %d min\n", cfg.SyncInterval)
	} else {
		fmt.Printf("  interval:   60 min (default)\n")
	}

	// Ping the Worker /healthz endpoint.
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, cfg.WorkerURL+"/healthz", nil)
	if err != nil {
		fmt.Printf("  worker:     ✗ build request: %v\n", err)
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	req.Header.Set("X-Device-ID", cfg.DeviceID)

	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("  worker:     ✗ unreachable: %v\n", err)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		fmt.Printf("  worker:     ✓ reachable (200 OK)\n")
	} else if resp.StatusCode == http.StatusUnauthorized {
		fmt.Printf("  worker:     ✗ auth failed (401) — check api_key in sync.json\n")
		return fmt.Errorf("worker auth failed")
	} else {
		fmt.Printf("  worker:     ✗ unexpected status %d\n", resp.StatusCode)
		return fmt.Errorf("worker returned %d", resp.StatusCode)
	}

	// Check persisted sync state.
	state, err := LoadSyncState(dataDir)
	if err != nil {
		fmt.Printf("  last sync:  (error reading state: %v)\n", err)
	} else if state.LastSync.IsZero() {
		fmt.Printf("  last sync:  never (run eideticd --sync-now to upload immediately)\n")
	} else {
		ago := time.Since(state.LastSync).Round(time.Minute)
		fmt.Printf("  last sync:  %s (%s ago)\n", state.LastSync.Local().Format("2006-01-02 15:04"), ago)
		fmt.Printf("  last key:   %s\n", state.LastKey)
		fmt.Printf("  last size:  %.1f MB\n", float64(state.LastBytes)/1e6)
	}
	return nil
}

// RestoreFromConfig downloads the most recent backup for the configured device
// from Cloudflare R2 via the /download endpoint and atomically replaces dbPath.
// The existing file (if any) is renamed to dbPath+".bak" before replacement.
// Designed to run before store.Open — does not require a live Syncer instance.
func RestoreFromConfig(cfg *Config, dbPath string) error {
	if cfg == nil {
		return fmt.Errorf("restore: not configured (missing sync.json)")
	}

	req, err := http.NewRequest(http.MethodGet, cfg.WorkerURL+"/download", nil)
	if err != nil {
		return fmt.Errorf("restore: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	req.Header.Set("X-Device-ID", cfg.DeviceID)
	if cfg.TeamID != "" {
		req.Header.Set("X-Team-ID", cfg.TeamID)
	}

	client := &http.Client{Timeout: 300 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("restore: download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("restore: no backup found for device %q", cfg.DeviceID)
	}
	if resp.StatusCode != http.StatusOK {
		rb, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("restore: worker returned %d: %s", resp.StatusCode, rb)
	}

	// Stream to a temp file alongside the target DB so rename is atomic.
	tmpPath := dbPath + ".restore-tmp"
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("restore: create temp file: %w", err)
	}
	n, copyErr := io.Copy(f, resp.Body)
	f.Close()
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("restore: write temp file: %w", copyErr)
	}

	// Back up existing DB before replacing.
	bakPath := dbPath + ".bak"
	if _, statErr := os.Stat(dbPath); statErr == nil {
		if err := os.Rename(dbPath, bakPath); err != nil {
			_ = os.Remove(tmpPath)
			return fmt.Errorf("restore: backup existing db: %w", err)
		}
	}

	if err := os.Rename(tmpPath, dbPath); err != nil {
		_ = os.Rename(bakPath, dbPath) // attempt to restore backup on failure
		return fmt.Errorf("restore: replace db: %w", err)
	}

	backupKey := resp.Header.Get("X-Backup-Key")
	fmt.Printf("✓ Downloaded %.1f MB engrams.db from cloud backup\n", float64(n)/1e6)
	if backupKey != "" {
		fmt.Printf("  key: %s\n", backupKey)
	}
	if _, statErr := os.Stat(bakPath); statErr == nil {
		fmt.Printf("  previous db saved to %s\n", bakPath)
	}
	fmt.Printf("  restart eideticd to use the restored database\n")
	return nil
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
	if s.cfg.TeamID != "" {
		req.Header.Set("X-Team-ID", s.cfg.TeamID)
	}
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

	backupKey := resp.Header.Get("X-Backup-Key")
	now := time.Now()
	prev, _ := LoadSyncState(s.dataDir)
	entry := BackupEntry{SyncedAt: now, Key: backupKey, Bytes: int64(len(body))}
	history := append([]BackupEntry{entry}, prev.History...)
	if len(history) > maxBackupHistory {
		history = history[:maxBackupHistory]
	}
	state := SyncState{
		LastSync:  now,
		LastKey:   backupKey,
		LastBytes: int64(len(body)),
		History:   history,
	}
	_ = saveSyncState(s.dataDir, state) // best-effort; don't fail the upload
	return nil
}

func saveSyncState(dataDir string, state SyncState) error {
	b, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("sync: marshal state: %w", err)
	}
	tmp := filepath.Join(dataDir, stateFile+".tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("sync: write state: %w", err)
	}
	return os.Rename(tmp, filepath.Join(dataDir, stateFile))
}
