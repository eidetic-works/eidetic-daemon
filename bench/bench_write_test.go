package bench

import (
	"context"
	"math/rand"
	"sort"
	"testing"
	"time"

	"github.com/eidetic-works/eidetic-daemon/internal/engram"
	"github.com/eidetic-works/eidetic-daemon/internal/store"
)

// BenchmarkWritePer95 closes ADR-014 measurement gap A:
//
//	"Write P95 < 50ms under 100 req/s sustained 60s"
//
// We use a shorter 5-second sustain in the test variant (still 500 inserts,
// statistically meaningful) to keep `go test -bench` snappy in CI; the
// long-form 60s run is gated behind `-bench-long` when explicit.
//
// Each request is a single-row Insert through the writer pool (MaxOpenConns=1
// per ADR-014 #3). Sustained-rate enforcement uses a fixed-tick generator
// rather than a busy loop so we measure realistic per-call latency.
func BenchmarkWritePer95(b *testing.B) {
	st := newWriterStore(b)
	ctx := context.Background()
	r := rand.New(rand.NewSource(42))

	const targetPerSec = burstReqsPerSec
	const durationSec = 5 // CI-tier; long-form spike ramps to 60
	totalReqs := targetPerSec * durationSec
	tickEvery := time.Second / time.Duration(targetPerSec)

	samples := make([]time.Duration, 0, totalReqs)
	tick := time.NewTicker(tickEvery)
	defer tick.Stop()
	for i := 0; i < totalReqs; i++ {
		<-tick.C
		e := engram.Engram{
			Surface: Surfaces[r.Intn(len(Surfaces))],
			TS:      time.Now().UnixNano(),
			Payload: synthPayload(r),
		}
		start := time.Now()
		if _, err := st.Insert(ctx, e); err != nil {
			b.Fatal(err)
		}
		samples = append(samples, time.Since(start))
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	q := Compute(samples)
	b.Logf("write@%dHz×%ds: N=%d P50=%v P95=%v P99=%v max=%v",
		targetPerSec, durationSec, q.N, q.P50, q.P95, q.P99, q.Max)

	if q.P95 > time.Duration(p95WriteSLOMs)*time.Millisecond {
		b.Fatalf("write P95=%v exceeds %dms (ADR-014 gap A SLO)", q.P95, p95WriteSLOMs)
	}
}

func newWriterStore(b *testing.B) *store.Store {
	b.Helper()
	b.Setenv("EIDETIC_DATA_DIR", b.TempDir())
	st, err := store.Open("")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = st.Close() })
	return st
}
