package bench

import (
	"testing"
	"time"
)

// The ranked queries fit the search length limit, ranked search has its
// < 100 ms target (200 ms at 10x), and the concurrent readers alternate
// literal grep and ranked search.
func TestRankedQueriesAndTarget(t *testing.T) {
	for _, q := range RankedQueries {
		if q == "" || len(q) > 1000 {
			t.Errorf("query %q", q)
		}
	}
	found := false
	for _, target := range M1Targets {
		found = found || target.Op == "ranked" && target.Limit == 100*time.Millisecond
	}
	if !found {
		t.Fatal("no ranked search target of 100 ms")
	}
	s := passingSummaries()
	slow := s["ranked"]
	slow.P95 = 150 * time.Millisecond
	s["ranked"] = slow
	if f := failed(TargetChecks("target size", s, 1)); len(f) != 1 || f[0] != "target size: ranked search top 20 (lit search), all projects p95" {
		t.Fatal("failed:", f)
	}
	if f := failed(TargetChecks("10x", s, 2)); len(f) != 0 {
		t.Fatal("failed at 10x:", f)
	}
	ops := map[string]int{}
	for i := range 2 * len(GrepQueries) * len(RankedQueries) {
		op, _ := readerQuery(i)
		ops[op]++
	}
	if ops["ranked"] == 0 || ops["grep"]+ops["grep.short"] == 0 || ops["ranked"] != ops["grep"]+ops["grep.short"] {
		t.Fatal("reader operations", ops)
	}
}
