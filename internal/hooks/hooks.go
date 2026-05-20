// Package hooks fires outbound webhook events when engrams matching
// configured patterns arrive. Lets users wire engram capture into Slack /
// Discord / PagerDuty / their own infra without external middleware.
//
// Privacy (ADR-020): hooks are explicit user-side opt-in. No webhook fires
// without ~/.eidetic/hooks.json. The payload sent to webhooks is whatever
// the user configured per-hook (default: no engram content, just a "match
// fired" event with surface + timestamp + match keyword).
//
// Config (~/.eidetic/hooks.json):
//
//	{
//	  "hooks": [
//	    {
//	      "name": "deploy-mentions-pager",
//	      "url": "https://events.pagerduty.com/v2/enqueue",
//	      "method": "POST",
//	      "headers": {"Authorization": "Token x"},
//	      "match_pattern": "deploy failed",
//	      "match_surface": "claude_code",
//	      "include_payload": false,
//	      "min_interval_sec": 300
//	    }
//	  ]
//	}
//
// match_pattern is a substring match on engram payload (case-insensitive).
// match_surface filters to one surface (empty = all).
// include_payload=true sends the full engram in the webhook body
// (DANGEROUS for sensitive engrams — opt-in only).
// min_interval_sec rate-limits per-hook firing.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/eidetic-works/eidetic-daemon/internal/engram"
)

// Config is the on-disk hook configuration shape.
type Config struct {
	Hooks []HookSpec `json:"hooks"`
}

// HookSpec is one user-configured outbound webhook.
type HookSpec struct {
	Name           string            `json:"name"`
	URL            string            `json:"url"`
	Method         string            `json:"method,omitempty"` // default POST
	Headers        map[string]string `json:"headers,omitempty"`
	MatchPattern   string            `json:"match_pattern,omitempty"`
	MatchSurface   string            `json:"match_surface,omitempty"`
	IncludePayload bool              `json:"include_payload,omitempty"`
	MinIntervalSec int               `json:"min_interval_sec,omitempty"`
}

// Dispatcher fires webhooks for matching engrams.
type Dispatcher struct {
	mu     sync.Mutex
	hooks  []HookSpec
	lastAt map[string]time.Time // hook name → last fired time (for rate-limit)
	client *http.Client
}

// LoadConfig reads ~/.eidetic/hooks.json (or returns nil if absent).
func LoadConfig(dataDir string) (*Config, error) {
	p := filepath.Join(dataDir, "hooks.json")
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("hooks: read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("hooks: parse config: %w", err)
	}
	return &cfg, nil
}

// NewDispatcher builds a Dispatcher from a Config. Returns nil if cfg is nil
// or has zero hooks (no overhead when feature is off).
func NewDispatcher(cfg *Config) *Dispatcher {
	if cfg == nil || len(cfg.Hooks) == 0 {
		return nil
	}
	return &Dispatcher{
		hooks:  cfg.Hooks,
		lastAt: make(map[string]time.Time, len(cfg.Hooks)),
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// Maybe runs all hooks against the engram; fires HTTP requests asynchronously
// in goroutines so capture path isn't blocked. Safe to call concurrently.
// No-op when d is nil.
func (d *Dispatcher) Maybe(ctx context.Context, e engram.Engram) {
	if d == nil {
		return
	}
	for _, h := range d.hooks {
		if !d.matches(e, h) {
			continue
		}
		if !d.canFire(h) {
			continue
		}
		go d.fire(ctx, h, e)
	}
}

func (d *Dispatcher) matches(e engram.Engram, h HookSpec) bool {
	if h.MatchSurface != "" && h.MatchSurface != e.Surface {
		return false
	}
	if h.MatchPattern != "" {
		if !strings.Contains(strings.ToLower(e.Payload), strings.ToLower(h.MatchPattern)) {
			return false
		}
	}
	return true
}

func (d *Dispatcher) canFire(h HookSpec) bool {
	if h.MinIntervalSec <= 0 {
		return true
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	last, ok := d.lastAt[h.Name]
	now := time.Now()
	if !ok || now.Sub(last) >= time.Duration(h.MinIntervalSec)*time.Second {
		d.lastAt[h.Name] = now
		return true
	}
	return false
}

func (d *Dispatcher) fire(ctx context.Context, h HookSpec, e engram.Engram) {
	body := map[string]any{
		"hook":      h.Name,
		"surface":   e.Surface,
		"timestamp": time.Unix(0, e.TS).UTC().Format(time.RFC3339),
		"pattern":   h.MatchPattern,
	}
	if h.IncludePayload {
		body["payload"] = e.Payload
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return
	}

	method := h.Method
	if method == "" {
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, h.URL, bytes.NewReader(raw))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range h.Headers {
		req.Header.Set(k, v)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return // best-effort; don't block capture, don't log noise
	}
	resp.Body.Close()
}

// SinkInterface is the subset of internal/capture.Sink we wrap.
type SinkInterface interface {
	InsertBatch(ctx context.Context, batch []engram.Engram) error
}

// WrapSink returns a SinkInterface that delegates InsertBatch to the inner sink
// and ALSO fires matching hooks after each successful insert. No-op when d
// is nil — returns the inner sink unchanged. Lets main.go plug hooks into
// the capture path with one line.
func (d *Dispatcher) WrapSink(inner SinkInterface) SinkInterface {
	if d == nil {
		return inner
	}
	return &hookedSink{inner: inner, dispatcher: d}
}

type hookedSink struct {
	inner      SinkInterface
	dispatcher *Dispatcher
}

func (h *hookedSink) InsertBatch(ctx context.Context, batch []engram.Engram) error {
	if err := h.inner.InsertBatch(ctx, batch); err != nil {
		return err
	}
	for _, e := range batch {
		h.dispatcher.Maybe(ctx, e)
	}
	return nil
}

// Names returns the hook names currently configured (for /metrics + --check).
func (d *Dispatcher) Names() []string {
	if d == nil {
		return nil
	}
	names := make([]string, len(d.hooks))
	for i, h := range d.hooks {
		names[i] = h.Name
	}
	return names
}
