package api

import (
	"math"
	"sync"
	"testing"
	"time"
)

func TestLatencyTrackerEmpty(t *testing.T) {
	lt := NewLatencyTracker(100)
	p50, p95, p99 := lt.Percentiles()
	if !math.IsNaN(p50) || !math.IsNaN(p95) || !math.IsNaN(p99) {
		t.Errorf("empty tracker: want NaN, got %v %v %v", p50, p95, p99)
	}
	if lt.Count() != 0 {
		t.Errorf("empty count: want 0, got %d", lt.Count())
	}
}

func TestLatencyTrackerOneSample(t *testing.T) {
	lt := NewLatencyTracker(100)
	lt.Record(time.Millisecond)
	p50, p95, p99 := lt.Percentiles()
	// < 2 samples → NaN.
	if !math.IsNaN(p50) || !math.IsNaN(p95) || !math.IsNaN(p99) {
		t.Errorf("single-sample tracker: want NaN, got %v %v %v", p50, p95, p99)
	}
}

func TestLatencyTrackerPercentiles(t *testing.T) {
	lt := NewLatencyTracker(1000)
	// Record 100 samples: 1µs, 2µs, …, 100µs.
	for i := 1; i <= 100; i++ {
		lt.Record(time.Duration(i) * time.Microsecond)
	}
	p50, p95, p99 := lt.Percentiles()

	// P50 of 1..100 ≈ 50.5 µs (linear interpolation between 50 and 51).
	if p50 < 49 || p50 > 52 {
		t.Errorf("P50 out of range: got %.2f", p50)
	}
	// P95 of 1..100 ≈ 95.05 µs.
	if p95 < 94 || p95 > 97 {
		t.Errorf("P95 out of range: got %.2f", p95)
	}
	// P99 of 1..100 ≈ 99.01 µs.
	if p99 < 98 || p99 > 100.5 {
		t.Errorf("P99 out of range: got %.2f", p99)
	}
	if lt.Count() != 100 {
		t.Errorf("Count: want 100, got %d", lt.Count())
	}
}

func TestLatencyTrackerRingBuffer(t *testing.T) {
	cap := 10
	lt := NewLatencyTracker(cap)
	// Write 20 samples: first 10 are 1µs, next 10 are 1000µs.
	for i := 0; i < 10; i++ {
		lt.Record(1 * time.Microsecond)
	}
	for i := 0; i < 10; i++ {
		lt.Record(1000 * time.Microsecond)
	}
	// After wrap, all 10 slots hold 1000µs; count capped at cap.
	if lt.Count() != cap {
		t.Errorf("Count after wrap: want %d, got %d", cap, lt.Count())
	}
	p50, _, _ := lt.Percentiles()
	// All samples ≈ 1000µs now.
	if p50 < 999 || p50 > 1001 {
		t.Errorf("P50 after ring wrap: want ~1000, got %.2f", p50)
	}
}

func TestLatencyTrackerZeroCapacity(t *testing.T) {
	lt := NewLatencyTracker(0)
	lt.Record(time.Millisecond)
	p50, p95, p99 := lt.Percentiles()
	if !math.IsNaN(p50) || !math.IsNaN(p95) || !math.IsNaN(p99) {
		t.Errorf("zero-cap tracker: want NaN, got %v %v %v", p50, p95, p99)
	}
}

func TestLatencyTrackerConcurrentSafety(t *testing.T) {
	lt := NewLatencyTracker(1000)
	var wg sync.WaitGroup
	workers := 8
	samplesEach := 200
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < samplesEach; j++ {
				lt.Record(time.Duration(j+1) * time.Microsecond)
			}
		}()
	}
	wg.Wait()
	// Must not panic and must have a valid count.
	p50, _, _ := lt.Percentiles()
	if math.IsNaN(p50) {
		t.Errorf("concurrent: unexpected NaN after %d samples", workers*samplesEach)
	}
	if lt.Count() == 0 {
		t.Errorf("concurrent: Count is 0 after %d writes", workers*samplesEach)
	}
}
