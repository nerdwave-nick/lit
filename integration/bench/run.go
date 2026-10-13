package bench

import (
	"fmt"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Params are the benchmark's parameters. DefaultParams are the accepted ones
// (decision 341b612a); others are for trying the harness out.
type Params struct {
	// Projects is the target size; Scale multiplies it for the 10x phase.
	Projects, Scale int
	// Samples is the number of measurements per operation and phase.
	Samples int
	// Concurrent is how long the concurrent agents run, with Writers
	// writing agents and Readers searching readers.
	Concurrent       time.Duration
	Writers, Readers int
	// SeedWorkers clients seed projects in parallel.
	SeedWorkers int
	// Restarts is the number of timed restarts per size.
	Restarts int
	// Seed makes the corpus reproducible.
	Seed uint64
}

// DefaultParams: 100 projects of about 15 issues, and 1,000 for 10x.
var DefaultParams = Params{Projects: 100, Scale: 10, Samples: 200, Concurrent: time.Minute, Writers: 8, Readers: 2, SeedWorkers: 8, Restarts: 3, Seed: 1}

// Runner drives one benchmark run against one lit-server.
type Runner struct {
	P                 Params
	Pool              *TextPool
	Bin, Config, Data string
	Server            *Server
	HTTP              *http.Client
	Logf              func(format string, args ...any)

	// Projects are the seeded projects (without their transactions);
	// Comments, Txns and Rows count what was written so far, Rows in
	// history rows (one per changed record).
	Projects             []*ProjectPlan
	Comments, Txns, Rows int
	// StartupLog holds the startup checks' repair and failure lines.
	StartupLog []string
}

// NewHTTPClient is the harness's HTTP client: lit's default 30 s request
// timeout, and enough idle connections for every simulated agent.
func NewHTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{MaxIdleConns: 64, MaxIdleConnsPerHost: 64}}
}

// Seed creates projects [from, to) through the API, SeedWorkers projects at
// a time; each project's transactions run in order.
func (r *Runner) Seed(from, to int) (CorpusStats, error) {
	start := time.Now()
	for len(r.Projects) < to {
		r.Projects = append(r.Projects, nil)
	}
	jobs := make(chan int)
	var mu sync.Mutex
	var first error
	var wg sync.WaitGroup
	done := 0
	for w := range r.P.SeedWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := Connect(r.Server.Endpoint, fmt.Sprintf("seeder-%d", w), r.HTTP)
			for i := range jobs {
				if err != nil {
					continue
				}
				plan := PlanProject(r.Pool, r.P.Seed, i, ProjectShape(i))
				rows, comments := 0, 0
				for _, t := range plan.Txns {
					var n int
					if n, err = c.Apply(t.Operation, t); err != nil {
						err = fmt.Errorf("project %d %s: %w", i, t.Operation, err)
						break
					}
					rows += n
					if t.Operation == "comment.create" {
						comments += len(t.Items)
					}
				}
				mu.Lock()
				if err != nil && first == nil {
					first = err
				}
				r.Txns += len(plan.Txns)
				plan.Txns = nil
				r.Projects[i], r.Rows, r.Comments = plan, r.Rows+rows, r.Comments+comments
				if done++; done%100 == 0 {
					r.Logf("seeded %d of %d projects in %s", done, to-from, time.Since(start).Round(time.Second))
				}
				mu.Unlock()
			}
			if err != nil {
				mu.Lock()
				if first == nil {
					first = err
				}
				mu.Unlock()
			}
		}()
	}
	for i := from; i < to; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	if first != nil {
		return CorpusStats{}, first
	}
	return r.Stats(fmt.Sprintf("%d projects", to), to, time.Since(start)), nil
}

// Stats describes the first n seeded projects and the totals written so far.
func (r *Runner) Stats(name string, n int, seed time.Duration) CorpusStats {
	return CorpusStats{Name: name, Projects: n, Issues: len(r.issues(n, true)), Comments: r.Comments, Txns: r.Txns, HistoryRow: r.Rows, Seed: seed}
}

// issues lists the issues of the first n projects, with or without the
// long-lived ones.
func (r *Runner) issues(n int, long bool) []*IssueState {
	var out []*IssueState
	for _, p := range r.Projects[:n] {
		for _, st := range p.Issues {
			if long || !st.LongLived {
				out = append(out, st)
			}
		}
	}
	return out
}

// Startups are the timed restarts at one size: Clean after a graceful stop,
// which skips the full integrity scan, and Unclean after kill -9, which runs
// it.
type Startups struct{ Clean, Unclean []time.Duration }

// Restart stops the server gracefully, records the database size, and
// restarts it r.P.Restarts times; then kills it (kill -9) and restarts it
// r.P.Restarts times. A restart that did not take the expected path, a skipped
// scan after the graceful stop and a full scan after kill -9, is an error.
func (r *Runner) Restart() (SizeStats, Startups, error) {
	var size SizeStats
	var startups Startups
	restart := func(want string, into *[]time.Duration) error {
		s, d, err := StartServer(r.Bin, r.Data, r.Config)
		if err != nil {
			return err
		}
		r.Server = s
		if !strings.Contains(s.Log(), want) {
			return fmt.Errorf("lit-server at a timed restart did not log %q: %s", want, s.Log())
		}
		*into = append(*into, d)
		for _, line := range strings.Split(s.Log(), "\n") {
			if strings.Contains(line, "rebuilt") || strings.Contains(line, "rewrote") || strings.Contains(line, "integrity check") {
				r.StartupLog = append(r.StartupLog, line)
			}
		}
		return nil
	}
	for n := range r.P.Restarts {
		if err := r.Server.Stop(); err != nil {
			return size, startups, err
		}
		if n == 0 {
			size.DB, size.WAL = DatabaseSize(r.Data)
			var err error
			if size.Tables, err = TableSizes(r.Data, 8); err != nil {
				r.Logf("dbstat: %v", err)
			}
		}
		if err := restart(CleanStartLog, &startups.Clean); err != nil {
			return size, startups, err
		}
	}
	for range r.P.Restarts {
		if err := r.Server.Kill(); err != nil {
			return size, startups, err
		}
		if err := restart(UncleanStartLog, &startups.Unclean); err != nil {
			return size, startups, err
		}
	}
	return size, startups, nil
}

// The startup log lines of lit-server's two startup paths.
const (
	CleanStartLog   = "lit-server: clean shutdown: full integrity scan skipped"
	UncleanStartLog = "lit-server: unclean shutdown: running full integrity scan"
)

// Double adds as many history rows as the first n projects hold, with
// update-only transactions of 1-5 random issues.
func (r *Runner) Double(n int) (CorpusStats, error) {
	start := time.Now()
	c, err := Connect(r.Server.Endpoint, "doubler", r.HTTP)
	if err != nil {
		return CorpusStats{}, err
	}
	rng := rand.New(rand.NewPCG(r.P.Seed, 2))
	for _, t := range UpdateTxns(rng, r.Pool, r.issues(n, true), r.Rows) {
		rows, err := c.Apply(t.Operation, t)
		if err != nil {
			return CorpusStats{}, fmt.Errorf("doubling: %w", err)
		}
		r.Rows += rows
		r.Txns++
	}
	return r.Stats("after doubling the history", n, time.Since(start)), nil
}
