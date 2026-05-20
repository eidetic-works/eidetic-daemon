// Package versioncheck polls the GitHub releases API for the latest
// eidetic-daemon tag and caches the result on disk for 24h.
//
// Design:
//   - Single goroutine, started by main, ticks every 24h.
//   - Cache file at <dataDir>/version-check.json: {checked_at, latest_version}.
//   - Network failures are silent — Get() returns the last successful cache
//     entry (possibly stale), or empty string if no successful poll has ever
//     fired. Never errors back to /metrics.
//   - Uses HEAD on github.com/eidetic-works/eidetic-daemon/releases/latest
//     (302 redirect to /tag/vX.Y.Z); reads tag from Location header. No
//     auth required, no rate-limit concern at 1 req/24h.
package versioncheck

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	releasesURL = "https://github.com/eidetic-works/eidetic-daemon/releases/latest"
	cacheFile   = "version-check.json"
	pollPeriod  = 24 * time.Hour
)

type cache struct {
	CheckedAt     time.Time `json:"checked_at"`
	LatestVersion string    `json:"latest_version"`
}

// Checker holds the latest known release tag.
type Checker struct {
	mu      sync.RWMutex
	latest  string
	dataDir string
}

// New constructs a Checker, seeding from disk cache if present.
func New(dataDir string) *Checker {
	c := &Checker{dataDir: dataDir}
	if b, err := os.ReadFile(filepath.Join(dataDir, cacheFile)); err == nil {
		var cached cache
		if json.Unmarshal(b, &cached) == nil {
			c.latest = cached.LatestVersion
		}
	}
	return c
}

// Latest returns the most recently observed release tag (e.g. "v0.0.38").
// Empty if no successful poll has ever fired.
func (c *Checker) Latest() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.latest
}

// UpdateAvailable reports whether `current` is strictly older than the latest
// known release. Conservative: returns false if no release info is cached, if
// `current` is "dev", or if version strings don't parse cleanly.
func (c *Checker) UpdateAvailable(current string) bool {
	latest := c.Latest()
	if latest == "" || current == "" || current == "dev" {
		return false
	}
	return semverLess(current, latest)
}

// Run polls every pollPeriod and writes to disk on success. Returns when ctx
// is canceled or the channel closes. Designed to be started as: go ck.Run(ctx).
func (c *Checker) Run(stop <-chan struct{}) {
	c.pollOnce() // fire immediately at startup
	ticker := time.NewTicker(pollPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			c.pollOnce()
		}
	}
}

func (c *Checker) pollOnce() {
	// Use a client that returns the 302 without following — we want the
	// Location header to extract the tag, not the rendered HTML page.
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(releasesURL)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	loc := resp.Header.Get("Location")
	if loc == "" {
		return
	}
	// Location format: https://github.com/.../releases/tag/v0.0.38
	idx := strings.LastIndex(loc, "/tag/")
	if idx == -1 {
		return
	}
	tag := loc[idx+len("/tag/"):]
	if tag == "" {
		return
	}

	c.mu.Lock()
	c.latest = tag
	c.mu.Unlock()

	cached, _ := json.Marshal(cache{CheckedAt: time.Now(), LatestVersion: tag})
	tmp := filepath.Join(c.dataDir, cacheFile+".tmp")
	if os.WriteFile(tmp, cached, 0o600) == nil {
		_ = os.Rename(tmp, filepath.Join(c.dataDir, cacheFile))
	}
}

// semverLess reports whether v0 < v1 for tags of the form "vMAJOR.MINOR.PATCH".
// Returns false on any parse failure (conservative — never falsely claim update).
func semverLess(v0, v1 string) bool {
	a, ok := parseSemver(v0)
	if !ok {
		return false
	}
	b, ok := parseSemver(v1)
	if !ok {
		return false
	}
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func parseSemver(s string) ([3]int, bool) {
	var out [3]int
	s = strings.TrimPrefix(s, "v")
	parts := strings.SplitN(s, ".", 3)
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		// Strip any pre-release / build metadata suffix.
		for j, r := range p {
			if r < '0' || r > '9' {
				p = p[:j]
				break
			}
		}
		if p == "" {
			return out, false
		}
		n := 0
		for _, r := range p {
			n = n*10 + int(r-'0')
		}
		out[i] = n
	}
	return out, true
}
