package service

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime/pprof"
	"sort"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/nerdwave-nick/lit/integration/bench"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/storage"
)

// TestWriteProfile is a gated, in-process write latency harness: it seeds a
// corpus shaped like the benchmark's (integration/bench) through the service,
// then times writes as lit performs them, a preview (a read snapshot) and a
// commit (serialized), through ServeHTTP without a network. It runs only with
// LIT_WRITEPROF_DIR set to a scratch directory; the seeded database is kept
// there and reused by later runs with the same number of projects.
//
//	LIT_WRITEPROF_DIR=DIR go test -count=1 -run TestWriteProfile -v -timeout 30m ./internal/service/
//
// Optional settings: LIT_WRITEPROF_PROJECTS (100), LIT_WRITEPROF_SINGLE
// (single-writer writes, 300), LIT_WRITEPROF_SECONDS (the concurrent phase,
// 60), LIT_WRITEPROF_CPU (a CPU profile of the single-writer phase) and
// LIT_WRITEPROF_CPU_CONC (one of the concurrent phase), and LIT_WRITEPROF_SQL
// (statements run on the copied database before it opens, for experiments).
// Pinning to performance cores (taskset) steadies the numbers. In-lock CPU per
// commit is the profile's time under (*Server).serialized divided by the
// number of commits:
//
//	go tool pprof -top -focus 'service.\(\*Server\).serialized' FILE
func TestWriteProfile(t *testing.T) {
	root := os.Getenv("LIT_WRITEPROF_DIR")
	if root == "" {
		t.Skip("run with LIT_WRITEPROF_DIR set to a scratch directory")
	}
	projects := envInt(t, "LIT_WRITEPROF_PROJECTS", 100)
	pool, e := bench.LoadTextPool(filepath.Join("..", ".."))
	if e != nil {
		t.Fatal(e)
	}
	corpus := seededCorpus(t, root, projects, pool)
	dir, e := os.MkdirTemp(root, "run")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	copyFile(t, filepath.Join(corpus.dir, storage.DatabaseFile), filepath.Join(dir, storage.DatabaseFile))
	if v := os.Getenv("LIT_WRITEPROF_SQL"); v != "" {
		o, e := sql.Open("sqlite", "file:"+filepath.Join(dir, storage.DatabaseFile))
		if e == nil {
			_, e = o.Exec(v)
			o.Close()
		}
		if e != nil {
			t.Fatal(e)
		}
	}
	h := &writeHarness{t: t, pool: pool, corpus: corpus}
	h.s, h.c = projectTestServerWith(t, dir, corpus.Client)

	// Warm the caches, unrecorded.
	warm := rand.New(rand.NewPCG(1, 1))
	for n := range 30 {
		h.write(warm, &corpus.Projects[n%len(corpus.Projects)], wpOps[n%len(wpOps)], newSamples())
	}
	single := newSamples()
	rng := rand.New(rand.NewPCG(2, 2))
	stop := profile(t, "LIT_WRITEPROF_CPU")
	cpu0 := cpuTime()
	count := envInt(t, "LIT_WRITEPROF_SINGLE", 300)
	for n := range count {
		h.write(rng, &corpus.Projects[n%len(corpus.Projects)], wpOps[n%len(wpOps)], single)
	}
	cpu := cpuTime() - cpu0
	stop()
	t.Logf("single writer: %d writes, process CPU %.2f ms per write (preview and commit)", count, ms(cpu)/float64(count))
	single.log(t, "single")

	conc := newSamples()
	stop = profile(t, "LIT_WRITEPROF_CPU_CONC")
	h.concurrent(time.Duration(envInt(t, "LIT_WRITEPROF_SECONDS", 60))*time.Second, conc)
	stop()
	conc.log(t, "8 writers + 2 readers")
}

// wpOps are the timed write operations.
var wpOps = []string{"issue.create", "issue.update", "comment.create"}

type wpIssue struct{ ID, Body string }
type wpProject struct {
	ID     string
	Issues []wpIssue
}

// wpCorpus is a seeded database and what the harness needs to write to it.
type wpCorpus struct {
	dir      string
	Client   protocol.Client
	Projects []wpProject
}

func envInt(t *testing.T, name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, e := strconv.Atoi(v)
	if e != nil {
		t.Fatalf("%s: %v", name, e)
	}
	return n
}

// seededCorpus returns the corpus seeded under root, seeding it first when
// it is missing: per project about 15 issues (one 64 KB body every 20
// projects), 5 comments and 5 updates per issue, in batches of 1-5.
func seededCorpus(t *testing.T, root string, projects int, pool *bench.TextPool) wpCorpus {
	dir := filepath.Join(root, fmt.Sprintf("seed-%d", projects))
	manifest := filepath.Join(dir, "corpus.json")
	var c wpCorpus
	if b, e := os.ReadFile(manifest); e == nil {
		if e = json.Unmarshal(b, &c); e != nil {
			t.Fatal(e)
		}
		c.dir = dir
		return c
	}
	_ = os.RemoveAll(dir)
	t0 := time.Now()
	s, client := projectTestServer(t, dir)
	c = wpCorpus{dir: dir, Client: client}
	rng := rand.New(rand.NewPCG(7, 7))
	prepare := func(req protocol.PrepareRequest) {
		p, e := s.PrepareRecords(req, client)
		if e != nil {
			t.Fatal(e)
		}
		mustExecute(t, s, client, p)
	}
	for p := range projects {
		pid := protocol.UUID()
		mustExecute(t, s, client, prepareProjectTest(t, s, client, "create", protocol.ProjectInput{ID: pid, Title: textPointer(fmt.Sprintf("feat/p%d", p)), Content: textPointer(pool.Text(rng, wpBodySize(rng)))}))
		proj := wpProject{ID: pid}
		for g := range 3 {
			var items []protocol.ProjectInput
			for k := range 5 {
				n := wpBodySize(rng)
				if p%20 == 0 && g == 0 && k == 0 {
					n = 64 << 10
				}
				st := wpIssue{protocol.UUID(), pool.Text(rng, n)}
				proj.Issues = append(proj.Issues, st)
				items = append(items, protocol.ProjectInput{ID: st.ID, Title: textPointer(fmt.Sprintf("Issue %d-%d-%d", p, g, k)), Content: textPointer(st.Body)})
			}
			prepare(protocol.PrepareRequest{Operation: "issue.create", Project: pid, Items: items})
		}
		for range 15 {
			var comments, updates []protocol.ProjectInput
			for _, k := range rng.Perm(len(proj.Issues))[:1+rng.IntN(5)] {
				comments = append(comments, protocol.ProjectInput{ID: protocol.UUID(), Issue: proj.Issues[k].ID, Content: textPointer(pool.Text(rng, wpBodySize(rng)))})
			}
			for _, k := range rng.Perm(len(proj.Issues))[:1+rng.IntN(5)] {
				updates = append(updates, wpUpdate(rng, pool, &proj.Issues[k]))
			}
			prepare(protocol.PrepareRequest{Operation: "comment.create", Items: comments})
			prepare(protocol.PrepareRequest{Operation: "issue.update", Items: updates})
		}
		c.Projects = append(c.Projects, proj)
	}
	if e := s.DB.Close(); e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(c)
	if e := os.WriteFile(manifest, b, 0o600); e != nil {
		t.Fatal(e)
	}
	t.Logf("seeded %d projects in %v", projects, time.Since(t0))
	c.dir = dir
	return c
}

// wpBodySize is log-normal with the live tracker's median of 870 bytes.
func wpBodySize(rng *rand.Rand) int {
	return min(max(int(math.Round(870*math.Exp(0.95*rng.NormFloat64()))), 40), 15<<10)
}

// wpUpdate edits an issue's body: a few lines replaced in the middle.
func wpUpdate(rng *rand.Rand, pool *bench.TextPool, st *wpIssue) protocol.ProjectInput {
	at := len(st.Body) / 2
	for at < len(st.Body) && st.Body[at] != '\n' {
		at++
	}
	end := min(at+60+rng.IntN(120), len(st.Body))
	st.Body = st.Body[:at] + pool.Text(rng, 60+rng.IntN(180)) + st.Body[end:]
	body := st.Body
	return protocol.ProjectInput{Target: st.ID, Set: protocol.ProjectSet{Body: &body}}
}

func copyFile(t *testing.T, from, to string) {
	in, e := os.Open(from)
	if e != nil {
		t.Fatal(e)
	}
	defer in.Close()
	out, e := os.Create(to)
	if e == nil {
		_, e = io.Copy(out, in)
	}
	if e == nil {
		e = out.Close()
	}
	if e != nil {
		t.Fatal(e)
	}
}

// projectTestServerWith opens dir with an existing client.
func projectTestServerWith(t *testing.T, dir string, c protocol.Client) (*Server, protocol.Client) {
	s := openTestServer(t, dir, Config{Limits: protocol.DefaultLimits(), TitlePrefixes: []string{"feat", "fix"}})
	return s, c
}

func profile(t *testing.T, env string) func() {
	f := os.Getenv(env)
	if f == "" {
		return func() {}
	}
	w, e := os.Create(filepath.Clean(f))
	if e == nil {
		e = pprof.StartCPUProfile(w)
	}
	if e != nil {
		t.Fatal(e)
	}
	return func() { pprof.StopCPUProfile(); _ = w.Close() }
}

func cpuTime() time.Duration {
	var u syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &u)
	return time.Duration(u.Utime.Nano() + u.Stime.Nano())
}

// samples are latencies by operation, safe for concurrent use.
type samples struct {
	mu sync.Mutex
	m  map[string][]time.Duration
}

func newSamples() *samples { return &samples{m: map[string][]time.Duration{}} }

func (s *samples) add(name string, d time.Duration) {
	s.mu.Lock()
	s.m[name] = append(s.m[name], d)
	s.mu.Unlock()
}

func (s *samples) log(t *testing.T, phase string) {
	names := sortedKeys(s.m)
	for _, name := range names {
		d := append([]time.Duration{}, s.m[name]...)
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		q := func(p float64) float64 { return ms(d[min(len(d)-1, int(p*float64(len(d))))]) }
		t.Logf("%s %-22s n=%4d p50 %7.2f ms  p95 %7.2f ms  p99 %7.2f ms  max %7.2f ms", phase, name, len(d), q(0.5), q(0.95), q(0.99), ms(d[len(d)-1]))
	}
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

type writeHarness struct {
	t      *testing.T
	s      *Server
	c      protocol.Client
	pool   *bench.TextPool
	corpus wpCorpus
	mu     sync.Mutex // guards project states shared by goroutines
}

// write performs one write in project p as lit does, a preview and then a
// commit, and records the preview, the commit, the whole write and "all".
func (h *writeHarness) write(rng *rand.Rand, p *wpProject, op string, out *samples) {
	var req protocol.PrepareRequest
	h.mu.Lock()
	switch op {
	case "issue.update":
		req = protocol.PrepareRequest{Operation: op, Items: []protocol.ProjectInput{wpUpdate(rng, h.pool, &p.Issues[rng.IntN(len(p.Issues))])}}
	case "comment.create":
		body := h.pool.Text(rng, wpBodySize(rng))
		req = protocol.PrepareRequest{Operation: op, Items: []protocol.ProjectInput{{ID: protocol.UUID(), Issue: p.Issues[rng.IntN(len(p.Issues))].ID, Content: &body}}}
	case "issue.create":
		st := wpIssue{protocol.UUID(), h.pool.Text(rng, wpBodySize(rng))}
		p.Issues = append(p.Issues, st)
		req = protocol.PrepareRequest{Operation: op, Project: p.ID, Items: []protocol.ProjectInput{{ID: st.ID, Title: textPointer("Agent issue " + st.ID[:8]), Content: textPointer(st.Body)}}}
	}
	h.mu.Unlock()
	req.SchemaVersion = 1
	t0 := time.Now()
	var prepared protocol.Prepared
	if code, body := h.serve("/v1/transaction-previews", req); code != 200 {
		h.t.Errorf("preview %s: %d %s", op, code, body)
		return
	} else if e := json.Unmarshal(body, &struct{ Data *protocol.Prepared }{&prepared}); e != nil {
		h.t.Error(e)
		return
	}
	t1 := time.Now()
	code, body := h.serve("/v1/transactions", protocol.DurableRequest{SchemaVersion: 1, Intent: prepared.Intent, RequestHash: prepared.RequestHash})
	t2 := time.Now()
	if code != 200 && code != 201 {
		h.t.Errorf("commit %s: %d %s", op, code, body)
		return
	}
	out.add(op+"/preview", t1.Sub(t0))
	out.add(op+"/commit", t2.Sub(t1))
	out.add(op, t2.Sub(t0))
	out.add("all writes", t2.Sub(t0))
	out.add("all commits", t2.Sub(t1))
}

func (h *writeHarness) serve(path string, body any) (int, []byte) {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", path, bytes.NewReader(b))
	r.Header.Set("X-Lit-Client-ID", h.c.ClientID)
	w := httptest.NewRecorder()
	h.s.ServeHTTP(w, r)
	return w.Code, w.Body.Bytes()
}

// concurrent runs the benchmark's concurrency phase in process: 8 writers,
// each writing every 0.5-1.5 s in its own project (20% creates, 35% updates,
// 45% comments), and 2 readers running literal grep and ranked search over
// all projects in turn, in a loop.
func (h *writeHarness) concurrent(d time.Duration, out *samples) {
	end := time.Now().Add(d)
	var wg sync.WaitGroup
	for k := range 8 {
		wg.Go(func() {
			rng := rand.New(rand.NewPCG(uint64(100+k), 3))
			p := &h.corpus.Projects[k]
			for {
				wait := 500*time.Millisecond + time.Duration(rng.IntN(1000))*time.Millisecond
				if time.Now().Add(wait).After(end) {
					return
				}
				time.Sleep(wait)
				op := "comment.create"
				switch r := rng.Float64(); {
				case r < 0.2:
					op = "issue.create"
				case r < 0.55:
					op = "issue.update"
				}
				h.write(rng, p, op, out)
			}
		})
	}
	for k := range 2 {
		wg.Go(func() {
			for i := k; time.Now().Before(end); i++ {
				t0 := time.Now()
				var code int
				var body []byte
				if i%2 == 1 {
					code, body = h.serve("/v1/ranked-search", protocol.RankedSearchRequest{Scope: protocol.SearchScope{AllProjects: true}, Query: bench.RankedQueries[(i/2)%len(bench.RankedQueries)]})
				} else {
					code, body = h.serve("/v1/search", protocol.SearchRequest{Scope: protocol.SearchScope{AllProjects: true}, Query: bench.GrepQueries[(i/2)%len(bench.GrepQueries)]})
				}
				if code != 200 {
					h.t.Errorf("read: %d %s", code, body)
					return
				}
				out.add("reads", time.Since(t0))
			}
		})
	}
	wg.Wait()
}
