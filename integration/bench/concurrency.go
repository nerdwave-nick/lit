package bench

import (
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

// writeOps are the operations the concurrent agents write with.
var writeOps = []string{"issue.create", "issue.update", "comment.create", "claim.acquire", "claim.renew", "claim.release"}

// Concurrent runs Writers agents, each writing about once per second
// (0.5-1.5 s apart) in its own project among the first n, while Readers
// readers run literal grep and ranked search over all projects in turn, in a
// loop, for r.P.Concurrent.
func (r *Runner) Concurrent(name string, n int) (Concurrency, PhaseResult, error) {
	rec := NewRecorder()
	if r.P.Writers > n {
		return Concurrency{}, PhaseResult{}, fmt.Errorf("%d writers need %d projects, not %d", r.P.Writers, r.P.Writers, n)
	}
	h := fnv.New64a()
	h.Write([]byte(name))
	phase := h.Sum64()
	end := time.Now().Add(r.P.Concurrent)
	var wg sync.WaitGroup
	for k := range r.P.Writers {
		c, err := Connect(r.Server.Endpoint, fmt.Sprintf("agent-%d", k), r.HTTP)
		if err != nil {
			return Concurrency{}, PhaseResult{}, err
		}
		c.Rec = rec
		wg.Add(1)
		go func() {
			defer wg.Done()
			agent{c: c, r: r, p: r.Projects[k], rng: rand.New(rand.NewPCG(r.P.Seed^phase, uint64(1000+k)))}.run(end)
		}()
	}
	for k := range r.P.Readers {
		c, err := Connect(r.Server.Endpoint, fmt.Sprintf("reader-%d", k), r.HTTP)
		if err != nil {
			return Concurrency{}, PhaseResult{}, err
		}
		c.Rec = rec
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := k; time.Now().Before(end); i++ {
				// failures are recorded
				if op, q := readerQuery(i); op == "ranked" {
					_, _ = c.RankedSearch(op, q)
				} else {
					_, _ = c.Grep(op, q)
				}
			}
		}()
	}
	wg.Wait()
	out := Concurrency{}
	var writes []time.Duration
	for _, op := range writeOps {
		writes = append(writes, rec.Samples(op)...)
	}
	out.Writes = Summarize(writes)
	out.Reads = Summarize(append(append(rec.Samples("grep"), rec.Samples("grep.short")...), rec.Samples("ranked")...))
	for _, codes := range rec.Failures() {
		for code, count := range codes {
			switch classifyFailure(code) {
			case "busy":
				out.Busy += count
			case "timeout":
				out.Timeouts += count
			default:
				out.Other += count
			}
		}
	}
	s := rec.Summaries()
	s["all writes"] = out.Writes
	return out, PhaseResult{Name: name, Summaries: s, Failures: rec.Failures(), Examples: rec.Examples()}, nil
}

// agent is one simulated agent working in its own project, so agents never
// conflict on revisions or claims.
type agent struct {
	c       *Client
	r       *Runner
	p       *ProjectPlan
	rng     *rand.Rand
	claimed string
}

func (a agent) run(end time.Time) {
	for n := 0; ; n++ {
		wait := 500*time.Millisecond + time.Duration(a.rng.IntN(1000))*time.Millisecond
		if time.Now().Add(wait).After(end) {
			break
		}
		time.Sleep(wait)
		a.write(n)
	}
	if a.claimed != "" {
		_ = a.c.Claim("release", a.claimed, 0)
	}
}

// write performs one write; failures are recorded by the client.
func (a *agent) write(n int) {
	var plain []*IssueState
	for _, st := range a.p.Issues {
		if !st.LongLived {
			plain = append(plain, st)
		}
	}
	issue := plain[a.rng.IntN(len(plain))]
	switch r := a.rng.Float64(); {
	case a.claimed != "" && r < 0.2:
		_ = a.c.Claim("renew", a.claimed, 45*time.Minute)
	case a.claimed != "" && r < 0.3:
		if a.c.Claim("release", a.claimed, 0) == nil {
			a.claimed = ""
		}
	case r < 0.2:
		st := &IssueState{ID: uuid(a.rng), Body: a.r.Pool.Text(a.rng, bodySize(a.rng)), Labels: []string{}}
		title := fmt.Sprintf("Agent issue %s-%d", a.c.ClientID[:8], n)
		body := st.Body
		if _, err := a.c.Apply("issue.create", Txn{Operation: "issue.create", Project: a.p.ID, Items: []protocol.ProjectInput{{ID: st.ID, Title: &title, Content: &body}}}); err == nil {
			a.p.Issues = append(a.p.Issues, st)
		}
	case r < 0.55:
		item := updateItem(a.rng, a.r.Pool, issue)
		_, err := a.c.Apply("issue.update", Txn{Operation: "issue.update", Items: []protocol.ProjectInput{item}})
		if err == nil && issue.ID == a.claimed && item.Set.State != nil && *item.Set.State == "closed" {
			a.claimed = "" // closing an issue releases its claim
		}
	case r < 0.85 || a.claimed != "":
		body := a.r.Pool.Text(a.rng, bodySize(a.rng))
		_, _ = a.c.Apply("comment.create", Txn{Operation: "comment.create", Items: []protocol.ProjectInput{{ID: uuid(a.rng), Issue: issue.ID, Content: &body}}})
	default:
		if a.c.Claim("acquire", issue.ID, 30*time.Minute) == nil {
			a.claimed = issue.ID
		}
	}
}
