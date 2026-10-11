package bench

import (
	"sync"
	"testing"
	"time"
)

func ms(n float64) time.Duration { return time.Duration(n * float64(time.Millisecond)) }

// Percentiles use the nearest-rank method: the p-th percentile of n sorted
// samples is the sample at rank ceil(p/100*n), so it is always a measured
// value and p95 of fewer than 20 samples is the maximum.
func TestSummarizeUsesNearestRankPercentiles(t *testing.T) {
	var samples []time.Duration
	for i := 100; i >= 1; i-- { // unsorted input
		samples = append(samples, ms(float64(i)))
	}
	s := Summarize(samples)
	if s.Count != 100 || s.P50 != ms(50) || s.P95 != ms(95) || s.P99 != ms(99) || s.Max != ms(100) {
		t.Fatalf("got %+v", s)
	}
	if samples[0] != ms(100) {
		t.Fatal("Summarize reordered its input")
	}
	small := Summarize([]time.Duration{ms(3), ms(1), ms(2)})
	if small.P50 != ms(2) || small.P95 != ms(3) || small.P99 != ms(3) {
		t.Fatalf("three samples: %+v", small)
	}
	if one := Summarize([]time.Duration{ms(7)}); one.P50 != ms(7) || one.P99 != ms(7) {
		t.Fatalf("one sample: %+v", one)
	}
	if empty := Summarize(nil); empty != (Summary{}) {
		t.Fatalf("no samples: %+v", empty)
	}
}

func TestRecorderCollectsSamplesAndFailuresConcurrently(t *testing.T) {
	r := NewRecorder()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 1; i <= 10; i++ {
				r.Add("meta", ms(float64(i)))
			}
			r.Fail("issue.create", "storage_busy", "storage is busy")
		}()
	}
	wg.Wait()
	if s := r.Summaries()["meta"]; s.Count != 80 || s.Max != ms(10) {
		t.Fatalf("meta: %+v", s)
	}
	if n := r.Failures()["issue.create"]["storage_busy"]; n != 8 {
		t.Fatalf("failures: %v", r.Failures())
	}
	if got := r.Examples()["issue.create storage_busy"]; got != "storage is busy" {
		t.Fatalf("examples: %v", r.Examples())
	}
	if got := r.Samples("meta"); len(got) != 80 {
		t.Fatalf("samples: %d", len(got))
	}
}
