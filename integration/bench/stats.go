package bench

import (
	"math"
	"slices"
	"sync"
	"time"
)

// Summary is the latency distribution of one operation.
type Summary struct {
	Count              int
	P50, P95, P99, Max time.Duration
}

// Summarize computes nearest-rank percentiles: the p-th percentile of n
// sorted samples is the sample at rank ceil(p/100*n). Every reported value is
// a measured one, and p95 of fewer than 20 samples is their maximum. The input
// is not modified.
func Summarize(samples []time.Duration) Summary {
	if len(samples) == 0 {
		return Summary{}
	}
	sorted := slices.Clone(samples)
	slices.Sort(sorted)
	rank := func(p float64) time.Duration {
		n := int(math.Ceil(p / 100 * float64(len(sorted))))
		return sorted[max(n, 1)-1]
	}
	return Summary{Count: len(sorted), P50: rank(50), P95: rank(95), P99: rank(99), Max: sorted[len(sorted)-1]}
}

// Recorder collects latency samples and failure codes per operation. It is
// safe for concurrent use.
type Recorder struct {
	mu       sync.Mutex
	samples  map[string][]time.Duration
	failures map[string]map[string]int
	examples map[string]string
}

func NewRecorder() *Recorder {
	return &Recorder{samples: map[string][]time.Duration{}, failures: map[string]map[string]int{}, examples: map[string]string{}}
}

// Add records one successful operation's latency.
func (r *Recorder) Add(op string, d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.samples[op] = append(r.samples[op], d)
}

// Fail records one failed operation by its error code, keeping the first
// message of each operation and code as an example.
func (r *Recorder) Fail(op, code, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failures[op] == nil {
		r.failures[op] = map[string]int{}
	}
	r.failures[op][code]++
	if _, ok := r.examples[op+" "+code]; !ok {
		r.examples[op+" "+code] = message
	}
}

// Examples maps "op code" to the first failure message recorded for it.
func (r *Recorder) Examples() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]string{}
	for k, v := range r.examples {
		out[k] = v
	}
	return out
}

// Samples returns a copy of op's samples.
func (r *Recorder) Samples(op string) []time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.samples[op])
}

// Summaries summarizes every operation that has samples.
func (r *Recorder) Summaries() map[string]Summary {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]Summary{}
	for op, s := range r.samples {
		out[op] = Summarize(s)
	}
	return out
}

// Failures returns a copy of the failure counts per operation and code.
func (r *Recorder) Failures() map[string]map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]map[string]int{}
	for op, codes := range r.failures {
		out[op] = map[string]int{}
		for code, n := range codes {
			out[op][code] = n
		}
	}
	return out
}
