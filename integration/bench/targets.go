package bench

import (
	"fmt"
	"strings"
	"time"
)

// Target is an accepted warm-cache p95 latency target at the target size
// (decision 341b612a, specification "Testing decisions"). Op names the
// operation in the Recorder.
type Target struct {
	Op, Name string
	Limit    time.Duration
}

// M1Targets are the targets measurable in milestone M1. Ranked search
// (lit search top 20, < 100 ms) and regex (lit grep -E, < 1 s) are added when
// M2 lands.
var M1Targets = []Target{
	{"meta", "/v1/meta", 5 * time.Millisecond},
	{"issue.get", "get one issue", 5 * time.Millisecond},
	{"issues.list", "issue list without bodies (100-item page)", 20 * time.Millisecond},
	{"project.snapshot", "whole project with bodies", 250 * time.Millisecond},
	{"issue.create", "create", 25 * time.Millisecond},
	{"issue.update", "update", 25 * time.Millisecond},
	{"comment.create", "comment", 25 * time.Millisecond},
	{"claim.acquire", "claim acquire", 25 * time.Millisecond},
	{"claim.renew", "claim renew", 25 * time.Millisecond},
	{"grep", "literal grep, all projects", 100 * time.Millisecond},
	{"grep.short", "literal grep, all projects, query under 3 characters", 500 * time.Millisecond},
	{"history.200", "history of an issue with 200 revisions", 50 * time.Millisecond},
	{"startup", "startup including the full integrity check", 2 * time.Second},
}

// History independence compares /v1/meta and get-issue latency before and
// after the history is doubled with updates only. The decision says "within
// noise" without a number; the harness reads that as at most 1.5x the
// baseline plus 1 ms, for p50 and p95. A cost that grows with history doubles
// with it (2x), the signature of fix/lit-server-store-scan; the absolute
// slack absorbs scheduler jitter on sub-millisecond operations.
const (
	historyRatio = 1.5
	historySlack = time.Millisecond
)

// ConcurrencyWriteP95 is the write p95 limit while 8 agents write and 2
// readers search.
const ConcurrencyWriteP95 = 50 * time.Millisecond

// Check is one pass/fail criterion with its measurement.
type Check struct {
	Name, Measured, Limit string
	Pass                  bool
}

// Concurrency is the outcome of the concurrent-agents phase.
type Concurrency struct {
	Writes, Reads Summary
	// Busy counts storage_busy answers, Timeouts client timeouts and
	// search_timeout answers, Other every other failed operation.
	Busy, Timeouts, Other int
}

// TargetChecks checks every M1 target's p95 against factor times its limit:
// 1 at the target size, 2 at 10x. An unmeasured target fails.
func TargetChecks(phase string, s map[string]Summary, factor float64) []Check {
	var out []Check
	for _, target := range M1Targets {
		limit := time.Duration(float64(target.Limit) * factor)
		c := Check{Name: phase + ": " + target.Name + " p95", Limit: "< " + formatDuration(limit)}
		if v, ok := s[target.Op]; ok && v.Count > 0 {
			c.Measured = formatDuration(v.P95)
			c.Pass = v.P95 < limit
		} else {
			c.Measured = "not measured"
		}
		out = append(out, c)
	}
	return out
}

// HistoryChecks compares p50 and p95 of /v1/meta and get-issue after the
// history was doubled (after) with the baseline (before).
func HistoryChecks(before, after map[string]Summary) []Check {
	var out []Check
	for _, op := range []struct{ key, name string }{{"meta", "/v1/meta"}, {"issue.get", "get one issue"}} {
		b, a := before[op.key], after[op.key]
		for _, p := range []struct {
			name string
			b, a time.Duration
		}{{"p50", b.P50, a.P50}, {"p95", b.P95, a.P95}} {
			bound := time.Duration(float64(p.b)*historyRatio) + historySlack
			c := Check{Name: "history independence: " + op.name + " " + p.name, Limit: fmt.Sprintf("<= %.1fx baseline (%s) + %s = %s", historyRatio, formatDuration(p.b), formatDuration(historySlack), formatDuration(bound))}
			if b.Count == 0 || a.Count == 0 {
				c.Measured = "not measured"
			} else {
				c.Measured = formatDuration(p.a)
				c.Pass = p.a <= bound
			}
			out = append(out, c)
		}
	}
	return out
}

// ConcurrencyChecks requires write p95 under ConcurrencyWriteP95, zero busy
// errors, zero timeouts, no other failure, and readers that actually ran.
func ConcurrencyChecks(c Concurrency) []Check {
	writes := Check{Name: "concurrency: write p95", Limit: "< " + formatDuration(ConcurrencyWriteP95), Measured: "no writes"}
	if c.Writes.Count > 0 {
		writes.Measured = fmt.Sprintf("%s (%d writes)", formatDuration(c.Writes.P95), c.Writes.Count)
		writes.Pass = c.Writes.P95 < ConcurrencyWriteP95
	}
	count := func(name string, n int) Check {
		return Check{Name: "concurrency: " + name, Limit: "0", Measured: fmt.Sprint(n), Pass: n == 0}
	}
	reads := Check{Name: "concurrency: readers ran", Limit: "> 0 searches", Measured: fmt.Sprint(c.Reads.Count), Pass: c.Reads.Count > 0}
	return []Check{writes, count("busy errors", c.Busy), count("timeouts", c.Timeouts), count("other failures", c.Other), reads}
}

// classifyFailure sorts an error code into busy, timeout or other.
func classifyFailure(code string) string {
	switch code {
	case "storage_busy":
		return "busy"
	case "timeout", "search_timeout":
		return "timeout"
	}
	return "other"
}

// Passed reports whether there are checks and all of them passed.
func Passed(checks []Check) bool {
	for _, c := range checks {
		if !c.Pass {
			return false
		}
	}
	return len(checks) > 0
}

func formatDuration(d time.Duration) string {
	if d >= time.Second {
		return fmt.Sprintf("%.2f s", d.Seconds())
	}
	return fmt.Sprintf("%.2f ms", float64(d)/float64(time.Millisecond))
}

// FailureCheck fails a phase in which any operation failed.
func FailureCheck(phase string, failures map[string]map[string]int) Check {
	total := 0
	var parts []string
	for _, op := range sortedKeys(failures) {
		for _, code := range sortedKeys(failures[op]) {
			total += failures[op][code]
			parts = append(parts, fmt.Sprintf("%s %s x%d", op, code, failures[op][code]))
		}
	}
	c := Check{Name: phase + ": failed operations", Limit: "0", Measured: fmt.Sprint(total), Pass: total == 0}
	if total > 0 {
		c.Measured += " (" + strings.Join(parts, "; ") + ")"
	}
	return c
}
