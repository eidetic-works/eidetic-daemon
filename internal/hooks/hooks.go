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
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
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
	mu      sync.Mutex
	hooks   []HookSpec
	lastAt  map[string]time.Time // hook name → last fired time (for rate-limit)
	regexes map[string]*regexp.Regexp // compiled regex per hook (only for "regex:" patterns); nil for substring
	counts  map[string]*atomic.Uint64 // per-hook fire counts; surfaced by /hooks endpoint
	client  *http.Client
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
//
// Patterns prefixed with "regex:" are compiled once at startup; non-compileable
// regexes log a warning and the hook silently never fires. Plain string
// patterns use case-insensitive substring matching (v0.0.55 behavior).
// (Regex support added in v0.0.56)
func NewDispatcher(cfg *Config) *Dispatcher {
	if cfg == nil || len(cfg.Hooks) == 0 {
		return nil
	}
	d := &Dispatcher{
		hooks:   cfg.Hooks,
		lastAt:  make(map[string]time.Time, len(cfg.Hooks)),
		regexes: make(map[string]*regexp.Regexp, len(cfg.Hooks)),
		counts:  make(map[string]*atomic.Uint64, len(cfg.Hooks)),
		client:  &http.Client{Timeout: 10 * time.Second},
	}
	for _, h := range cfg.Hooks {
		d.counts[h.Name] = new(atomic.Uint64)
		if strings.HasPrefix(h.MatchPattern, "regex:") {
			pattern := strings.TrimPrefix(h.MatchPattern, "regex:")
			re, err := regexp.Compile(pattern)
			if err != nil {
				fmt.Fprintf(os.Stderr, "hooks: %q has invalid regex %q (hook will never fire): %v\n",
					h.Name, pattern, err)
				continue
			}
			d.regexes[h.Name] = re
		}
	}
	return d
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
	if h.MatchPattern == "" {
		return true
	}
	// Regex hook (v0.0.56+) — prefix "regex:" routes through pre-compiled regex
	if strings.HasPrefix(h.MatchPattern, "regex:") {
		re, ok := d.regexes[h.Name]
		if !ok {
			// Regex didn't compile at startup; never fire
			return false
		}
		return re.MatchString(e.Payload)
	}
	// Plain substring match — case-insensitive (v0.0.55 behavior)
	return strings.Contains(strings.ToLower(e.Payload), strings.ToLower(h.MatchPattern))
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
	// Only increment on actual HTTP attempt + non-error path so the counter
	// reflects "fired" (request sent) not "matched" (would have fired).
	if c, ok := d.counts[h.Name]; ok {
		c.Add(1)
	}
}

// Status returns per-hook fire counts + config snapshot for /hooks endpoint.
// Safe to call concurrently.
type HookStatus struct {
	Name         string `json:"name"`
	URL          string `json:"url"`
	MatchPattern string `json:"match_pattern,omitempty"`
	MatchSurface string `json:"match_surface,omitempty"`
	IsRegex      bool   `json:"is_regex,omitempty"`
	FireCount    uint64 `json:"fire_count"`
}

func (d *Dispatcher) Status() []HookStatus {
	if d == nil {
		return nil
	}
	out := make([]HookStatus, 0, len(d.hooks))
	for _, h := range d.hooks {
		st := HookStatus{
			Name:         h.Name,
			URL:          h.URL,
			MatchPattern: h.MatchPattern,
			MatchSurface: h.MatchSurface,
			IsRegex:      strings.HasPrefix(h.MatchPattern, "regex:"),
		}
		if c, ok := d.counts[h.Name]; ok {
			st.FireCount = c.Load()
		}
		out = append(out, st)
	}
	return out
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
