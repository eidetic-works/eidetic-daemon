// Package bench holds the W1 P95 SLO bench harness.
//
// Per spec section 3 measurement protocol:
//
//  1. Seed 10K synthetic engrams (varied text 50-2000 chars, ts spanning 6
//     weeks, surfaces distributed across {cursor, cowork, claude_code,
//     windsurf, antigravity}).
//  2. Warmup 50 requests (results discarded).
//  3. Measure 1000 requests; record P50/P95/P99/max. Repeat 3 times.
//
// Acceptance for Day 7 ship: P95 ≤ 100ms across 3 consecutive runs. CI
// fails-the-build below this threshold.
package bench

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/eidetic-works/eidetic-daemon/internal/engram"
	"github.com/eidetic-works/eidetic-daemon/internal/store"
)

// Surfaces is the spec-aligned distribution of capture surfaces.
var Surfaces = []string{"cursor", "cowork", "claude_code", "windsurf", "antigravity"}

// Seed populates the store with `count` synthetic engrams whose timestamps
// span the prior `span` duration (default 6 weeks). Returns the store ready
// for read benches.
//
// Seeding uses InsertBatch in chunks of 500 to amortize transaction overhead.
func Seed(ctx context.Context, st *store.Store, count int, span time.Duration) error {
	if span <= 0 {
		span = 6 * 7 * 24 * time.Hour
	}
	r := rand.New(rand.NewSource(0xC0FFEE))
	now := time.Now().UnixNano()
	tsMin := now - int64(span)
	const chunk = 500
	batch := make([]engram.Engram, 0, chunk)
	for i := 0; i < count; i++ {
		batch = append(batch, engram.Engram{
			Surface: Surfaces[r.Intn(len(Surfaces))],
			TS:      tsMin + r.Int63n(int64(span)),
			Payload: synthPayload(r),
			Meta:    fmt.Sprintf(`{"seed_id":%d}`, i),
		})
		if len(batch) >= chunk {
			if err := st.InsertBatch(ctx, batch); err != nil {
				return err
			}
			batch = batch[:0]
		}
	}
	if len(batch) > 0 {
		if err := st.InsertBatch(ctx, batch); err != nil {
			return err
		}
	}
	return nil
}

// synthPayload returns a deterministic-but-varied 50-2000-char string built
// from a small word pool. Mirrors the original spike fixture shape so bench
// numbers are comparable across host runs.
func synthPayload(r *rand.Rand) string {
	const minLen = 50
	const maxLen = 2000
	target := minLen + r.Intn(maxLen-minLen)
	var b strings.Builder
	b.Grow(target)
	for b.Len() < target {
		b.WriteString(words[r.Intn(len(words))])
		b.WriteByte(' ')
	}
	out := b.String()
	if len(out) > maxLen {
		out = out[:maxLen]
	}
	return out
}

var words = []string{
	"engram", "capture", "retrieve", "surface", "session",
	"daemon", "watch", "event", "parse", "commit",
	"latency", "throughput", "index", "scan", "explain",
	"cursor", "cowork", "claude", "windsurf", "antigravity",
	"sqlite", "wal", "pragma", "synchronous", "journal",
	"writer", "reader", "pool", "concurrent", "atomic",
	"unix", "socket", "tcp", "http", "json",
	"benchmark", "p50", "p95", "p99", "max",
}

// Quantiles computes P50/P95/P99/max from a slice of latency samples.
// Mutates `lat` (sort in place); caller must clone if it wants to preserve.
type Quantiles struct {
	P50, P95, P99, Max time.Duration
	N                  int
}

// Compute requires `lat` be already sorted in non-descending order.
func Compute(lat []time.Duration) Quantiles {
	if len(lat) == 0 {
		return Quantiles{}
	}
	idx := func(p float64) time.Duration {
		i := int(float64(len(lat)) * p)
		if i >= len(lat) {
			i = len(lat) - 1
		}
		return lat[i]
	}
	return Quantiles{
		P50: idx(0.50),
		P95: idx(0.95),
		P99: idx(0.99),
		Max: lat[len(lat)-1],
		N:   len(lat),
	}
}
