package bench

import (
	"regexp"
	"testing"
	"time"
)

// The regex patterns are valid RE2 within the search length limit, and
// regex has its < 1 s target (2 s at 10x).
func TestRegexQueriesAndTarget(t *testing.T) {
	for _, q := range RegexQueries {
		if _, err := regexp.Compile("(?i)" + q); err != nil || len(q) > 1000 {
			t.Errorf("pattern %q: %v", q, err)
		}
	}
	found := false
	for _, target := range M1Targets {
		found = found || target.Op == "regex" && target.Limit == time.Second
	}
	if !found {
		t.Fatal("no regex target of 1 s")
	}
	s := passingSummaries()
	slow := s["regex"]
	slow.P95 = 1500 * time.Millisecond
	s["regex"] = slow
	if f := failed(TargetChecks("target size", s, 1)); len(f) != 1 || f[0] != "target size: regex grep (lit grep -E), all projects p95" {
		t.Fatal("failed:", f)
	}
	if f := failed(TargetChecks("10x", s, 2)); len(f) != 0 {
		t.Fatal("failed at 10x:", f)
	}
}
