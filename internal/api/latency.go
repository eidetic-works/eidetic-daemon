package api

import (
	"math"
	"sort"
	"sync"
	"time"
)

// LatencyTracker is a thread-safe reservoir sampler that records durations
// and computes P50/P95/P99 percentiles on demand. The reservoir holds up to
// Cap samples; once full, new samples overwrite the oldest via a ring-buffer.
//
// This gives accurate percentiles over the most recent Cap observations
// without unbounded memory growth. Cap=1000 yields stable P99 estimates
// while keeping memory under ~8 KB (1000 × float64).
type LatencyTracker struct {
	mu      sync.Mutex
	buf     []float64 // microseconds, ring buffer
	cap     int
	head    int  // next write index
	filled  bool // true once buf has been written past cap once
}

// NewLatencyTracker creates a tracker with the given capacity.
// Capacity 0 returns a no-op tracker (Percentiles returns NaN).
func NewLatencyTracker(capacity int) *LatencyTracker {
	return &LatencyTracker{
		buf: make([]float64, capacity),
		cap: capacity,
	}
}

// Record adds a duration sample. Safe to call from multiple goroutines.
func (lt *LatencyTracker) Record(d time.Duration) {
	if lt.cap == 0 {
		return
	}
	us := float64(d.Nanoseconds()) / 1000.0
	lt.mu.Lock()
	lt.buf[lt.head] = us
	lt.head++
	if lt.head >= lt.cap {
		lt.head = 0
		lt.filled = true
	}
	lt.mu.Unlock()
}

// Percentiles returns P50, P95, P99 in microseconds, computed over all
// recorded samples. Returns NaN for all three if fewer than 2 samples
// have been recorded (not enough data for meaningful percentiles).
func (lt *LatencyTracker) Percentiles() (p50, p95, p99 float64) {
	nan := math.NaN()
	if lt.cap == 0 {
		return nan, nan, nan
	}
	lt.mu.Lock()
	var samples []float64
	if lt.filled {
		samples = make([]float64, lt.cap)
		copy(samples, lt.buf)
	} else if lt.head > 0 {
		samples = make([]float64, lt.head)
		copy(samples, lt.buf[:lt.head])
	}
	lt.mu.Unlock()

	if len(samples) < 2 {
		return nan, nan, nan
	}
	sort.Float64s(samples)
	return percentile(samples, 50), percentile(samples, 95), percentile(samples, 99)
}

// Count returns the number of samples recorded (capped at capacity).
func (lt *LatencyTracker) Count() int {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	if lt.filled {
		return lt.cap
	}
	return lt.head
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return math.NaN()
	}
	idx := p / 100.0 * float64(len(sorted)-1)
	lo := int(idx)
	hi := lo + 1
	if hi >= len(sorted) {
		return sorted[lo]
	}
	frac := idx - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}
