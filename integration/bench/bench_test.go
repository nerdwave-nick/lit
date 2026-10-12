package bench

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestBenchmark is the gated end-to-end benchmark; see the package
// documentation.
func TestBenchmark(t *testing.T) {
	if os.Getenv("LIT_BENCH") != "1" {
		t.Skip("end-to-end benchmark: run with LIT_BENCH=1 (see integration/bench/doc.go)")
	}
	p, standard := paramsFromEnv(t)
	if deadline, ok := t.Deadline(); ok && standard && time.Until(deadline) < 90*time.Minute {
		t.Fatal("the benchmark takes up to an hour: pass -timeout 3h")
	}
	base := os.Getenv("LIT_BENCH_DIR")
	if base == "" {
		base = os.TempDir()
	}
	root, err := os.MkdirTemp(base, "lit-bench-")
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("LIT_BENCH_KEEP") == "1" {
		t.Log("keeping", root)
	} else {
		t.Cleanup(func() { _ = os.RemoveAll(root) })
	}
	report := &Report{Started: time.Now(), LoadBefore: LoadAverage(), Machine: DescribeMachine(root), Commit: commit(), Parameters: fmt.Sprintf("%+v", p), Standard: standard}
	t.Logf("load average before: %s; data under %s (%s)", report.LoadBefore, root, report.Machine.Filesystem)

	bin := filepath.Join(root, "lit-server")
	build := exec.Command("go", "build", "-o", bin, "../../cmd/lit-server")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build lit-server: %v: %s", err, out)
	}
	config, err := WriteConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := LoadTextPool("../..")
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(root, "data")
	srv, fresh, err := StartServer(bin, data, config)
	if err != nil {
		t.Fatal(err)
	}
	r := &Runner{P: p, Pool: pool, Bin: bin, Config: config, Data: data, Server: srv, HTTP: NewHTTPClient(), Logf: t.Logf}
	defer func() {
		if r.Server != nil {
			_ = r.Server.Stop()
		}
	}()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%v\nlit-server log:\n%s", err, r.Server.Log())
		}
	}
	var checks []Check
	phase := func(result PhaseResult, err error) PhaseResult {
		t.Helper()
		must(err)
		report.Phases = append(report.Phases, result)
		t.Logf("measured %s", result.Name)
		return result
	}
	n, n10 := p.Projects, p.Projects*p.Scale

	stats, err := r.Seed(0, n)
	must(err)
	report.Corpus = append(report.Corpus, stats)
	size, startups, err := r.Restart()
	must(err)
	size.Name = fmt.Sprintf("%d projects", n)
	report.Sizes = append(report.Sizes, size)
	target := phase(r.Measure(fmt.Sprintf("Target size: %d projects, warm cache", n), n, 1, startups))
	checks = append(append(checks, TargetChecks("target size", target.Summaries, 1)...), FailureCheck("target size", target.Failures))

	stats, err = r.Double(n)
	must(err)
	report.Corpus = append(report.Corpus, stats)
	after := phase(r.Measure("After doubling the history with updates only", n, 0, nil))
	checks = append(append(checks, HistoryChecks(target.Summaries, after.Summaries)...), FailureCheck("after doubling", after.Failures))

	conc, result, err := r.Concurrent(fmt.Sprintf("Concurrency at the target size: %d agents writing, %d readers, %s", p.Writers, p.Readers, p.Concurrent), n)
	phase(result, err)
	checks = append(checks, ConcurrencyChecks(conc)...)

	stats, err = r.Seed(n, n10)
	must(err)
	report.Corpus = append(report.Corpus, stats)
	size, startups, err = r.Restart()
	must(err)
	size.Name = fmt.Sprintf("%d projects", n10)
	report.Sizes = append(report.Sizes, size)
	ten := phase(r.Measure(fmt.Sprintf("10x: %d projects, warm cache", n10), n10, 2, startups))
	checks = append(append(checks, TargetChecks("10x", ten.Summaries, 2)...), FailureCheck("10x", ten.Failures))
	_, result, err = r.Concurrent(fmt.Sprintf("Concurrency at 10x (informational): %d agents writing, %d readers, %s", p.Writers, p.Readers, p.Concurrent), n10)
	phase(result, err)

	must(r.Server.Stop())
	r.Server = nil
	end := SizeStats{Name: "end of run"}
	end.DB, end.WAL = DatabaseSize(data)
	if end.Tables, err = TableSizes(data, 8); err != nil {
		t.Logf("dbstat: %v", err)
	}
	report.Sizes = append(report.Sizes, end)
	report.Finished, report.LoadAfter, report.Checks = time.Now(), LoadAverage(), checks
	report.Notes = append([]string{fmt.Sprintf("Startup of the fresh, empty database: %s.", formatDuration(fresh))}, notes...)
	for _, line := range r.StartupLog {
		report.Notes = append(report.Notes, "lit-server at a timed startup: "+line)
	}

	md := report.Markdown()
	path := os.Getenv("LIT_BENCH_REPORT")
	if path == "" {
		path = filepath.Join(os.TempDir(), "lit-bench-report-"+report.Started.UTC().Format("20060102T150405Z")+".md")
	}
	if err := os.WriteFile(path, []byte(md), 0o644); err != nil {
		t.Error(err)
	}
	t.Logf("report written to %s\n%s", path, md)
	if !Passed(checks) {
		t.Errorf("the benchmark missed a target or criterion; see %s", path)
	}
}

// notes explain how the report's operations are measured.
var notes = []string{
	"Latency is measured by the client from sending a request to reading the whole response, over HTTP on loopback; warm cache means after 20 unrecorded rounds.",
	"Writes are measured as lit performs them, on single items: create, update and comment are a preview plus a commit (the two requests are also listed as /preview and /commit); claim acquire, renew and release are a claim snapshot plus the operation.",
	"Get one issue is POST /v1/snapshots with one target (lit issues get). The issue list without bodies is POST /v1/issue-summaries over all projects with limit 100, following the cursor. The whole project with bodies is POST /v1/snapshots for all issues of one project. History reads every entry (all=true) of an issue with 205 or more updates.",
	"Literal grep is POST /v1/search over all projects with the default page of 100 and no context lines; each query is also listed on its own. Queries of fewer than 3 folded characters have the 500 ms target.",
	"Regex grep (lit grep -E) is POST /v1/search with regex over all projects, case-insensitive, with the default page of 100 and no context lines; each pattern is also listed on its own. The first four patterns are prefiltered through the trigram index by a literal they require; ^#+ \\w+ and \\d{5}-\\d{5}-\\d{5} require none and read every record (the second matches nothing: a full scan). The concurrent readers do not run regex.",
	"Startup runs from starting the process to the first 200 from /v1/meta after a clean stop, so it includes the full integrity check; its p95 over the restarts is their maximum.",
	"History independence: the decision says \"within noise\" without a number; the harness reads it as at most 1.5x the baseline plus 1 ms, for p50 and p95. A history-dependent cost doubles with the history.",
	"The 10x corpus adds 900 projects to the 100 whose history was doubled. Concurrency at 10x is reported but not gated: the decision states the concurrency criterion without a size.",
	"Ranked search (lit search) is POST /v1/ranked-search over all projects with the default page of 20; each query is also listed on its own. The concurrent readers alternate literal grep and ranked search.",
	"Scheduled backups are off during the run.",
}

// paramsFromEnv reads the parameter overrides; standard is false when any
// differs from DefaultParams.
func paramsFromEnv(t *testing.T) (Params, bool) {
	p := DefaultParams
	for key, dst := range map[string]*int{"LIT_BENCH_PROJECTS": &p.Projects, "LIT_BENCH_SCALE": &p.Scale, "LIT_BENCH_SAMPLES": &p.Samples} {
		if v := os.Getenv(key); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				t.Fatalf("%s=%q: want a positive integer", key, v)
			}
			*dst = n
		}
	}
	if v := os.Getenv("LIT_BENCH_CONCURRENT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			t.Fatalf("LIT_BENCH_CONCURRENT=%q: want a positive duration", v)
		}
		p.Concurrent = d
	}
	return p, p == DefaultParams
}

// commit names the checked-out commit and whether the tree has changes.
func commit() string {
	out, err := exec.Command("git", "-C", "../..", "rev-parse", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	c := strings.TrimSpace(string(out))
	if status, err := exec.Command("git", "-C", "../..", "status", "--porcelain").Output(); err == nil && len(status) > 0 {
		c += " (with uncommitted changes)"
	}
	return c
}
