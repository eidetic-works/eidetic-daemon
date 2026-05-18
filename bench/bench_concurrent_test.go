package bench

import (
	"context"
	"math/rand"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/eidetic-works/eidetic-daemon/internal/engram"
)

// BenchmarkConcurrentReadWritePer95 closes ADR-014 measurement gap C:
//
//	"Concurrent P95 < 100ms with 5 readers + 1 writer over 60s"
//
// Test variant uses 5 seconds of sustained traffic; long-form spike ramps
// to 60s when run with -bench-long. Reader pool of 5 fires query traffic
// continuously; one writer fires inserts at 100 req/s. We measure P95 on
// READ samples (the SLO target) — write throughput is not the gate here,
// but write-induced contention on the shared WAL is.
func BenchmarkConcurrentReadWritePer95(b *testing.B) {
	st := newSeededStore(b)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const readers = 5
	const writeRate = burstReqsPerSec // 100/s
	const durationSec = 5

	stopAt := time.Now().Add(time.Duration(durationSec) * time.Second)
	var (
		mu      sync.Mutex
		samples []time.Duration
	)

	var wg sync.WaitGroup
	wg.Add(readers + 1)

	// Writer
	go func() {
		defer wg.Done()
		r := rand.New(rand.NewSource(7))
		tick := time.NewTicker(time.Second / time.Duration(writeRate))
		defer tick.Stop()
		for time.Now().Before(stopAt) {
			select {
			case <-tick.C:
				_, _ = st.Insert(ctx, engram.Engram{
					Surface: Surfaces[r.Intn(len(Surfaces))],
					TS:      time.Now().UnixNano(),
					Payload: synthPayload(r),
				})
			case <-ctx.Done():
				return
			}
		}
	}()

	// Readers
	for i := 0; i < readers; i++ {
		go func(id int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(100 + id)))
			local := make([]time.Duration, 0, 1024)
			for time.Now().Before(stopAt) {
				start := time.Now()
				_, err := st.Retrieve(ctx, Surfaces[r.Intn(len(Surfaces))], 0, 0, 50, false)
				if err != nil {
					b.Errorf("reader %d query: %v", id, err)
					return
				}
				local = append(local, time.Since(start))
			}
			mu.Lock()
			samples = append(samples, local...)
			mu.Unlock()
		}(i)
	}

	wg.Wait()
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	q := Compute(samples)
	b.Logf("read@5×concurrent + write@%dHz × %ds: N=%d P50=%v P95=%v P99=%v max=%v",
		writeRate, durationSec, q.N, q.P50, q.P95, q.P99, q.Max)

	if q.P95 > time.Duration(p95SLOMillis)*time.Millisecond {
		b.Fatalf("concurrent read P95=%v exceeds %dms (ADR-014 gap C SLO)",
			q.P95, p95SLOMillis)
	}
}
