package bench

import (
	"strings"
	"testing"
	"time"
)

// summaries returns a passing measurement of every M1 target.
func passingSummaries() map[string]Summary {
	out := map[string]Summary{}
	for _, target := range M1Targets {
		half := target.Limit / 2
		out[target.Op] = Summary{Count: 50, P50: half / 2, P95: half, P99: half, Max: half}
	}
	return out
}

func failed(checks []Check) []string {
	var out []string
	for _, c := range checks {
		if !c.Pass {
			out = append(out, c.Name)
		}
	}
	return out
}

func TestTargetChecksPassBelowEveryTarget(t *testing.T) {
	checks := TargetChecks("target size", passingSummaries(), 1)
	if len(checks) != len(M1Targets) {
		t.Fatalf("%d checks for %d targets", len(checks), len(M1Targets))
	}
	if f := failed(checks); len(f) > 0 {
		t.Fatal("failed:", f)
	}
}

// A p95 at or above the target fails, as does a target that was not measured.
func TestTargetChecksFailOnAMissedOrUnmeasuredTarget(t *testing.T) {
	s := passingSummaries()
	missed := s["issue.create"]
	missed.P95 = 25 * time.Millisecond // the target is < 25 ms
	s["issue.create"] = missed
	delete(s, "history.200")
	f := failed(TargetChecks("target size", s, 1))
	if len(f) != 2 || !strings.Contains(f[0]+f[1], "create") || !strings.Contains(f[0]+f[1], "200 revisions") {
		t.Fatal("failed:", f)
	}
}

// At 10x every target is doubled: a p95 between 1x and 2x passes, 2x fails.
func TestTargetChecksAtTenTimesAllowTwiceTheTarget(t *testing.T) {
	s := passingSummaries()
	for op, v := range s {
		for _, target := range M1Targets {
			if target.Op == op {
				v.P95 = target.Limit*2 - time.Microsecond
			}
		}
		s[op] = v
	}
	if f := failed(TargetChecks("10x", s, 2)); len(f) > 0 {
		t.Fatal("failed below 2x:", f)
	}
	grep := s["grep"]
	grep.P95 = 200 * time.Millisecond
	s["grep"] = grep
	if f := failed(TargetChecks("10x", s, 2)); len(f) != 1 || !strings.Contains(f[0], "grep") {
		t.Fatal("failed:", f)
	}
}

func TestHistoryIndependence(t *testing.T) {
	before := map[string]Summary{
		"meta":      {Count: 200, P50: ms(0.2), P95: ms(0.4)},
		"issue.get": {Count: 200, P50: ms(1), P95: ms(2)},
	}
	within := map[string]Summary{
		"meta":      {Count: 200, P50: ms(0.6), P95: ms(1.3)}, // sub-millisecond jitter: inside the absolute slack
		"issue.get": {Count: 200, P50: ms(1.4), P95: ms(3.9)}, // 2*1.5+1 = 4 ms
	}
	if f := failed(HistoryChecks(before, within)); len(f) > 0 {
		t.Fatal("failed within noise:", f)
	}
	// The original fix/lit-server-store-scan signature: latency grows with
	// history, so doubling the history doubles it.
	grown := map[string]Summary{
		"meta":      {Count: 200, P50: ms(0.2), P95: ms(0.4)},
		"issue.get": {Count: 200, P50: ms(1.1), P95: ms(4.1)},
	}
	if f := failed(HistoryChecks(before, grown)); len(f) != 1 || !strings.Contains(f[0], "get one issue") {
		t.Fatal("failed:", f)
	}
	if f := failed(HistoryChecks(before, map[string]Summary{"meta": before["meta"]})); len(f) == 0 {
		t.Fatal("an unmeasured operation passed")
	}
}

func TestConcurrencyChecks(t *testing.T) {
	ok := Concurrency{Writes: Summary{Count: 480, P95: ms(49)}, Reads: Summary{Count: 900}}
	if f := failed(ConcurrencyChecks(ok)); len(f) > 0 {
		t.Fatal("failed:", f)
	}
	for name, bad := range map[string]Concurrency{
		"write p95":   {Writes: Summary{Count: 480, P95: ms(50)}, Reads: Summary{Count: 900}},
		"busy":        {Writes: ok.Writes, Reads: ok.Reads, Busy: 1},
		"timeouts":    {Writes: ok.Writes, Reads: ok.Reads, Timeouts: 1},
		"other error": {Writes: ok.Writes, Reads: ok.Reads, Other: 1},
		"no writes":   {Reads: ok.Reads},
		"no reads":    {Writes: ok.Writes},
	} {
		if f := failed(ConcurrencyChecks(bad)); len(f) != 1 {
			t.Errorf("%s: failed %v", name, f)
		}
	}
}

func TestClassifyFailure(t *testing.T) {
	for code, want := range map[string]string{
		"storage_busy":   "busy",
		"search_timeout": "timeout",
		"timeout":        "timeout",
		"claim_conflict": "other",
	} {
		if got := classifyFailure(code); got != want {
			t.Errorf("%s: %s, want %s", code, got, want)
		}
	}
}

func TestPassedRequiresEveryCheck(t *testing.T) {
	if !Passed([]Check{{Pass: true}, {Pass: true}}) || Passed([]Check{{Pass: true}, {Pass: false}}) || Passed(nil) {
		t.Fatal("Passed")
	}
}

// Any failed operation in a measured phase fails it: a target measured only
// over the requests that succeeded would hide the failures.
func TestFailureCheck(t *testing.T) {
	if c := FailureCheck("target size", nil); !c.Pass {
		t.Fatal("no failures failed")
	}
	c := FailureCheck("target size", map[string]map[string]int{"issue.create": {"revision_conflict": 2}, "grep": {"timeout": 1}})
	if c.Pass || c.Measured != "3 (grep timeout x1; issue.create revision_conflict x2)" {
		t.Fatalf("%+v", c)
	}
}
