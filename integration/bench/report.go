package bench

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// PhaseResult is the latency of every operation measured in one phase.
type PhaseResult struct {
	Name string
	// Factor scales the targets shown next to the measurements (1 at the
	// target size, 2 at 10x); 0 shows none.
	Factor    float64
	Summaries map[string]Summary
	Failures  map[string]map[string]int
	// Examples maps "op code" to a failure message.
	Examples map[string]string
}

// CorpusStats describes a seeded corpus.
type CorpusStats struct {
	Name                                         string
	Projects, Issues, Comments, Txns, HistoryRow int
	Seed                                         time.Duration
}

// SizeStats is the database size at a point of the run.
type SizeStats struct {
	Name    string
	DB, WAL int64
	// Tables are the largest tables and indexes.
	Tables []TableSize
}

// Report is the benchmark report recorded on the milestone's ticket.
type Report struct {
	Commit, Parameters    string
	Standard              bool
	Machine               Machine
	LoadBefore, LoadAfter string
	Started, Finished     time.Time
	Corpus                []CorpusStats
	Sizes                 []SizeStats
	Phases                []PhaseResult
	Checks                []Check
	Notes                 []string
}

// Markdown renders the report.
func (r *Report) Markdown() string {
	var b strings.Builder
	verdict := "PASS"
	if !Passed(r.Checks) {
		verdict = "FAIL"
	}
	fmt.Fprintf(&b, "# lit-server benchmark: %s\n\n", verdict)
	if !r.Standard {
		b.WriteString("**Non-standard parameters: this is not an acceptance run.**\n\n")
	}
	fmt.Fprintf(&b, "- Commit: %s\n- Parameters: %s\n- Run: %s to %s (%s)\n", r.Commit, r.Parameters, r.Started.UTC().Format(time.RFC3339), r.Finished.UTC().Format(time.RFC3339), r.Finished.Sub(r.Started).Round(time.Second))
	m := r.Machine
	fmt.Fprintf(&b, "- Machine: %s, %d logical CPUs, %s RAM; kernel %s; %s\n- Data directory filesystem: %s\n", m.CPU, m.Cores, m.Memory, m.Kernel, m.Go, m.Filesystem)
	fmt.Fprintf(&b, "- Load average (1, 5, 15 min): %s before, %s after\n\n", r.LoadBefore, r.LoadAfter)

	b.WriteString("## Checks\n\n| Check | Measured | Limit | Result |\n| --- | --- | --- | --- |\n")
	for _, c := range r.Checks {
		result := "pass"
		if !c.Pass {
			result = "**FAIL**"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", c.Name, c.Measured, c.Limit, result)
	}

	b.WriteString("\n## Corpus\n\n| Corpus | Projects | Issues | Comments | Transactions | History rows | Seeding time |\n| --- | --- | --- | --- | --- | --- | --- |\n")
	for _, c := range r.Corpus {
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %d | %s |\n", c.Name, c.Projects, c.Issues, c.Comments, c.Txns, c.HistoryRow, c.Seed.Round(time.Second))
	}
	b.WriteString("\n## Database size\n\n| When | Database | WAL |\n| --- | --- | --- |\n")
	for _, s := range r.Sizes {
		fmt.Fprintf(&b, "| %s | %s | %s |\n", s.Name, mib(s.DB), mib(s.WAL))
	}
	for _, s := range r.Sizes {
		if len(s.Tables) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\nLargest tables and indexes at %s: ", s.Name)
		for i, t := range s.Tables {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%s %s", t.Name, mib(t.Bytes))
		}
		b.WriteString("\n")
	}
	for _, p := range r.Phases {
		fmt.Fprintf(&b, "\n## %s\n\n| Operation | n | p50 | p95 | p99 | max | p95 target |\n| --- | --- | --- | --- | --- | --- | --- |\n", p.Name)
		for _, op := range orderedOps(p.Summaries) {
			s := p.Summaries[op]
			target := ""
			for _, t := range M1Targets {
				if t.Op == op && p.Factor > 0 {
					target = "< " + formatDuration(time.Duration(float64(t.Limit)*p.Factor))
				}
			}
			fmt.Fprintf(&b, "| %s | %d | %s | %s | %s | %s | %s |\n", op, s.Count, formatDuration(s.P50), formatDuration(s.P95), formatDuration(s.P99), formatDuration(s.Max), target)
		}
		for _, op := range sortedKeys(p.Failures) {
			for _, code := range sortedKeys(p.Failures[op]) {
				fmt.Fprintf(&b, "\nFailures: %s %s x%d", op, code, p.Failures[op][code])
				if example := p.Examples[op+" "+code]; example != "" {
					fmt.Fprintf(&b, " (first: %s)", truncate(example, 300))
				}
				b.WriteString("\n")
			}
		}
	}
	if len(r.Notes) > 0 {
		b.WriteString("\n## Notes\n\n")
		for _, n := range r.Notes {
			fmt.Fprintf(&b, "- %s\n", n)
		}
	}
	return b.String()
}

// orderedOps lists the targeted operations first, in target order, then the
// rest alphabetically.
func orderedOps(s map[string]Summary) []string {
	var out []string
	for _, t := range M1Targets {
		if _, ok := s[t.Op]; ok {
			out = append(out, t.Op)
		}
	}
	for _, op := range sortedKeys(s) {
		if !slices.Contains(out, op) {
			out = append(out, op)
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func mib(n int64) string { return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20)) }
