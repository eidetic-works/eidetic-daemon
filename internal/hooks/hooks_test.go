package hooks

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eidetic-works/eidetic-daemon/internal/engram"
)

func TestLoadConfig_Missing(t *testing.T) {
	cfg, err := LoadConfig(t.TempDir())
	if err != nil {
		t.Fatalf("missing config should not error, got %v", err)
	}
	if cfg != nil {
		t.Errorf("missing config: got non-nil, want nil")
	}
}

func TestLoadConfig_Valid(t *testing.T) {
	dir := t.TempDir()
	body := `{"hooks":[{"name":"test","url":"https://example.com","match_pattern":"deploy"}]}`
	if err := os.WriteFile(filepath.Join(dir, "hooks.json"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Hooks) != 1 {
		t.Fatalf("want 1 hook, got %d", len(cfg.Hooks))
	}
	if cfg.Hooks[0].Name != "test" {
		t.Errorf("hook name: got %q, want test", cfg.Hooks[0].Name)
	}
}

func TestNewDispatcher_NoConfig(t *testing.T) {
	if d := NewDispatcher(nil); d != nil {
		t.Error("nil config: want nil dispatcher")
	}
	if d := NewDispatcher(&Config{}); d != nil {
		t.Error("empty config: want nil dispatcher")
	}
}

func TestDispatcher_FiresOnMatch(t *testing.T) {
	var fired atomic.Int32
	var gotBody string
	var bodyMu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fired.Add(1)
		body, _ := io.ReadAll(r.Body)
		bodyMu.Lock()
		gotBody = string(body)
		bodyMu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &Config{Hooks: []HookSpec{{
		Name:         "deploy-alert",
		URL:          srv.URL,
		MatchPattern: "deploy failed",
	}}}
	d := NewDispatcher(cfg)

	d.Maybe(context.Background(), engram.Engram{
		Surface: "claude_code",
		TS:      time.Now().UnixNano(),
		Payload: "the deploy failed at step 3 due to flaky test",
	})

	// Wait for async goroutine
	for i := 0; i < 50 && fired.Load() == 0; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if fired.Load() != 1 {
		t.Fatalf("expected 1 fire, got %d", fired.Load())
	}

	bodyMu.Lock()
	var parsed map[string]any
	json.Unmarshal([]byte(gotBody), &parsed)
	bodyMu.Unlock()
	if parsed["surface"] != "claude_code" {
		t.Errorf("surface in body: got %v, want claude_code", parsed["surface"])
	}
	if _, hasPayload := parsed["payload"]; hasPayload {
		t.Error("include_payload defaults false; payload should NOT be in body")
	}
}

func TestDispatcher_NoMatchNoFire(t *testing.T) {
	var fired atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fired.Add(1)
	}))
	defer srv.Close()

	cfg := &Config{Hooks: []HookSpec{{Name: "x", URL: srv.URL, MatchPattern: "production incident"}}}
	d := NewDispatcher(cfg)
	d.Maybe(context.Background(), engram.Engram{Surface: "claude_code", Payload: "nothing to see here"})
	time.Sleep(100 * time.Millisecond)
	if fired.Load() != 0 {
		t.Errorf("non-matching engram: should not fire, fired %d", fired.Load())
	}
}

func TestDispatcher_SurfaceFilter(t *testing.T) {
	var fired atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fired.Add(1)
	}))
	defer srv.Close()

	cfg := &Config{Hooks: []HookSpec{{Name: "x", URL: srv.URL, MatchSurface: "cursor", MatchPattern: "x"}}}
	d := NewDispatcher(cfg)
	// Wrong surface — should not fire
	d.Maybe(context.Background(), engram.Engram{Surface: "claude_code", Payload: "x"})
	time.Sleep(100 * time.Millisecond)
	if fired.Load() != 0 {
		t.Errorf("surface filter leaked: fired %d", fired.Load())
	}
	// Right surface — should fire
	d.Maybe(context.Background(), engram.Engram{Surface: "cursor", Payload: "x"})
	for i := 0; i < 50 && fired.Load() == 0; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if fired.Load() != 1 {
		t.Errorf("expected fire for cursor: got %d", fired.Load())
	}
}

func TestDispatcher_RateLimit(t *testing.T) {
	var fired atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fired.Add(1)
	}))
	defer srv.Close()

	cfg := &Config{Hooks: []HookSpec{{
		Name:           "rate-limited",
		URL:            srv.URL,
		MatchPattern:   "x",
		MinIntervalSec: 60,
	}}}
	d := NewDispatcher(cfg)

	// Two matching engrams back-to-back — second should be suppressed
	d.Maybe(context.Background(), engram.Engram{Surface: "any", Payload: "x"})
	d.Maybe(context.Background(), engram.Engram{Surface: "any", Payload: "x"})

	time.Sleep(150 * time.Millisecond)
	if fired.Load() != 1 {
		t.Errorf("rate limit not enforced: got %d fires, want 1", fired.Load())
	}
}

func TestDispatcher_IncludePayloadOptIn(t *testing.T) {
	var got map[string]any
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		json.Unmarshal(body, &got)
		mu.Unlock()
	}))
	defer srv.Close()

	cfg := &Config{Hooks: []HookSpec{{
		Name:           "with-payload",
		URL:            srv.URL,
		MatchPattern:   "x",
		IncludePayload: true,
	}}}
	d := NewDispatcher(cfg)
	d.Maybe(context.Background(), engram.Engram{Surface: "any", Payload: "this is x in the payload"})

	for i := 0; i < 50; i++ {
		mu.Lock()
		hit := got != nil
		mu.Unlock()
		if hit {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if got["payload"] != "this is x in the payload" {
		t.Errorf("payload should be in body when include_payload=true; got %v", got["payload"])
	}
}

func TestDispatcher_Names(t *testing.T) {
	var d *Dispatcher
	if names := d.Names(); names != nil {
		t.Error("nil dispatcher: Names() should be nil")
	}
	d = NewDispatcher(&Config{Hooks: []HookSpec{{Name: "a"}, {Name: "b"}}})
	names := d.Names()
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Errorf("Names(): got %v, want [a b]", names)
	}
}
