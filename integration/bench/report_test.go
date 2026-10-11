package bench

import (
	"strings"
	"testing"
	"time"
)

func TestReportShowsTheVerdictChecksAndScaledTargets(t *testing.T) {
	r := Report{Standard: true, Checks: []Check{{Name: "10x: create p95", Measured: "60.00 ms", Limit: "< 50.00 ms"}}, Phases: []PhaseResult{{
		Name: "10x", Factor: 2, Summaries: map[string]Summary{"issue.create": {Count: 3, P95: 60 * time.Millisecond}, "claim.release": {Count: 3}},
	}}}
	md := r.Markdown()
	for _, want := range []string{"# lit-server benchmark: FAIL", "| 10x: create p95 | 60.00 ms | < 50.00 ms | **FAIL** |", "| issue.create | 3 | 0.00 ms | 60.00 ms | 0.00 ms | 0.00 ms | < 50.00 ms |", "| claim.release | 3 |"} {
		if !strings.Contains(md, want) {
			t.Fatalf("missing %q in\n%s", want, md)
		}
	}
	if strings.Index(md, "| issue.create |") > strings.Index(md, "| claim.release |") {
		t.Fatal("targeted operations are not listed first")
	}
	if strings.Contains(md, "Non-standard") {
		t.Fatal("standard run marked non-standard")
	}
	r.Checks[0].Pass, r.Standard = true, false
	if md = r.Markdown(); !strings.Contains(md, "benchmark: PASS") || !strings.Contains(md, "Non-standard parameters") {
		t.Fatal(md)
	}
}
