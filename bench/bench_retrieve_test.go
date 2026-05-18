package bench

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/eidetic-works/eidetic-daemon/internal/store"
)

const (
	seedRows         = 10_000
	warmupReqs       = 50
	measureReqs      = 1000
	measureRuns      = 3
	p95SLOMillis     = 100
	p95WriteSLOMs    = 50
	burstReqsPerSec  = 100
	burstDurationSec = 60
)

func newSeededStore(b *testing.B) *store.Store {
	b.Helper()
	dir := b.TempDir()
	b.Setenv("EIDETIC_DATA_DIR", dir)
	st, err := store.Open("")
	if err != nil {
		b.Fatal(err)
	}
	if err := Seed(context.Background(), st, seedRows, 0); err != nil {
		_ = st.Close()
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = st.Close() })
	return st
}

// BenchmarkRetrievePer95 walks the spec section 3 protocol verbatim:
//   - seed 10K rows
//   - 50 warmup
//   - 1000 measure
//   - repeat ×3
//   - assert P95 ≤ 100ms across all 3 runs
//
// Failure of any run marks the benchmark as failed (FAIL-the-build per
// spec section 3 + PLAN.md Risk #12 mitigation).
func BenchmarkRetrievePer95(b *testing.B) {
	st := newSeededStore(b)
	ctx := context.Background()

	for run := 1; run <= measureRuns; run++ {
		// Warmup
		for i := 0; i < warmupReqs; i++ {
			_, _ = st.Retrieve(ctx, surfaceFor(i), 0, 0, 50)
		}

		samples := make([]time.Duration, 0, measureReqs)
		for i := 0; i < measureReqs; i++ {
			start := time.Now()
			if _, err := st.Retrieve(ctx, surfaceFor(i), 0, 0, 50); err != nil {
				b.Fatal(err)
			}
			samples = append(samples, time.Since(start))
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		q := Compute(samples)
		b.Logf("run %d: N=%d P50=%v P95=%v P99=%v max=%v", run, q.N, q.P50, q.P95, q.P99, q.Max)

		if q.P95 > time.Duration(p95SLOMillis)*time.Millisecond {
			b.Fatalf("run %d: P95=%v exceeds %dms SLO", run, q.P95, p95SLOMillis)
		}
	}
}

// surfaceFor returns a varied surface so cache lines / hot path don't get
// artificially favored by always-querying-one-surface.
func surfaceFor(i int) string {
	return Surfaces[i%len(Surfaces)]
}
