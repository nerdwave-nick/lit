package bench

import (
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"time"
	"unicode/utf8"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/textfold"
)

// GrepQueries are the literal searches measured, all over all projects:
// common and rare words, identifiers, punctuation and no match. Queries of
// fewer than 3 characters scan the folded text instead of using the trigram
// index and have their own target.
var GrepQueries = []string{"transaction", "net/http", "claim_conflict", "SQLITE_BUSY", "func (s *Server)", "Straße", "recovery_required", "no-such-needle-anywhere", "Go", "UI", "ß", "qz"}

func grepOp(query string) string {
	if utf8.RuneCountInString(textfold.Grep(query)) < 3 {
		return "grep.short"
	}
	return "grep"
}

// Measure measures every operation sequentially, after a warm-up, over the
// first n projects. The phase's recorder gets each operation's samples;
// startups are added as the "startup" operation.
func (r *Runner) Measure(name string, n int, factor float64, startups []time.Duration) (PhaseResult, error) {
	rec := NewRecorder()
	for _, d := range startups {
		rec.Add("startup", d)
	}
	c, err := Connect(r.Server.Endpoint, "measure", r.HTTP)
	if err != nil {
		return PhaseResult{}, err
	}
	h := fnv.New64a()
	h.Write([]byte(name))
	rng := rand.New(rand.NewPCG(r.P.Seed, h.Sum64()))
	m := measurer{r: r, c: c, rng: rng, n: n, all: r.issues(n, true), plain: r.issues(n, false)}
	for _, st := range m.all {
		if st.LongLived {
			m.long = append(m.long, st.ID)
		}
	}
	// Warm the caches and the connection without recording.
	for range 20 {
		if err = m.reads(); err != nil {
			return PhaseResult{}, fmt.Errorf("warm-up: %w", err)
		}
	}
	c.Rec = rec
	// A failed operation is recorded (and fails the phase's check) and the
	// measurement goes on, unless failures pile up.
	failures := 0
	tolerate := func(err error) error {
		if err != nil {
			if failures++; failures > 50 {
				return fmt.Errorf("more than 50 failed operations, the last: %w", err)
			}
		}
		return nil
	}
	for range r.P.Samples {
		if err = tolerate(m.reads()); err != nil {
			return PhaseResult{}, err
		}
	}
	perQuery := max(20, r.P.Samples/len(GrepQueries))
	for _, q := range GrepQueries {
		for range perQuery {
			_, failed := c.Grep("grep: "+q, q)
			if err = tolerate(failed); err != nil {
				return PhaseResult{}, err
			}
		}
		for _, d := range rec.Samples("grep: " + q) {
			rec.Add(grepOp(q), d)
		}
	}
	for range r.P.Samples {
		if err = tolerate(m.writes()); err != nil {
			return PhaseResult{}, err
		}
	}
	return PhaseResult{Name: name, Factor: factor, Summaries: rec.Summaries(), Failures: rec.Failures(), Examples: rec.Examples()}, nil
}

type measurer struct {
	r          *Runner
	c          *Client
	rng        *rand.Rand
	n          int
	all, plain []*IssueState
	long       []string
	cursor     string
	created    int
}

// reads runs each read operation once.
func (m *measurer) reads() error {
	if err := m.c.Meta(); err != nil {
		return err
	}
	if err := m.c.GetIssue(m.all[m.rng.IntN(len(m.all))].ID); err != nil {
		return err
	}
	next, err := m.c.ListIssues(m.cursor)
	if err != nil {
		return err
	}
	m.cursor = next
	if _, err = m.c.ProjectSnapshot(m.r.Projects[m.rng.IntN(m.n)].ID); err != nil {
		return err
	}
	if len(m.long) > 0 {
		revisions, err := m.c.History(m.long[m.rng.IntN(len(m.long))])
		if err != nil {
			return err
		}
		if revisions < 200 {
			err = fmt.Errorf("long-lived issue has %d history entries", revisions)
			if m.c.Rec != nil {
				m.c.Rec.Fail("history.200", "harness", err.Error())
			}
			return err
		}
	}
	return nil
}

// writes runs each write operation once, on single items, as one agent would.
func (m *measurer) writes() error {
	p := m.r.Projects[m.rng.IntN(m.n)]
	m.created++
	st := &IssueState{ID: uuid(m.rng), Body: m.r.Pool.Text(m.rng, bodySize(m.rng)), Labels: []string{}}
	title := fmt.Sprintf("Measured issue %x-%d", m.rng.Uint64(), m.created)
	body := st.Body
	create := Txn{Operation: "issue.create", Project: p.ID, Items: []protocol.ProjectInput{{ID: st.ID, Title: &title, Content: &body}}}
	if err := m.apply("issue.create", create); err != nil {
		return err
	}
	p.Issues = append(p.Issues, st)
	target := m.plain[m.rng.IntN(len(m.plain))]
	if err := m.apply("issue.update", Txn{Operation: "issue.update", Items: []protocol.ProjectInput{updateItem(m.rng, m.r.Pool, target)}}); err != nil {
		return err
	}
	body = m.r.Pool.Text(m.rng, bodySize(m.rng))
	comment := Txn{Operation: "comment.create", Items: []protocol.ProjectInput{{ID: uuid(m.rng), Issue: target.ID, Content: &body}}}
	if err := m.apply("comment.create", comment); err != nil {
		return err
	}
	claimed := m.plain[m.rng.IntN(len(m.plain))].ID
	for _, step := range []struct {
		verb  string
		lease time.Duration
	}{{"acquire", 30 * time.Minute}, {"renew", 45 * time.Minute}, {"release", 0}} {
		if err := m.c.Claim(step.verb, claimed, step.lease); err != nil {
			return err
		}
	}
	return nil
}

// apply commits t and counts what it wrote into the runner's totals.
func (m *measurer) apply(op string, t Txn) error {
	rows, err := m.c.Apply(op, t)
	m.r.Rows += rows
	if err == nil {
		m.r.Txns++
	}
	return err
}
