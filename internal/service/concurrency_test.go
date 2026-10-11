package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// serve runs one request in process as client id and decodes its envelope.
func serve(t *testing.T, s *Server, id, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, path, strings.NewReader(string(b)))
	r.Header.Set("X-Lit-Client-ID", id)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	var v map[string]any
	if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
		t.Errorf("%s %s: %d %q", method, path, w.Code, w.Body.String())
	}
	return w.Code, v
}

// parkedClock replaces the request clock. Once armed, the next request that
// consults it waits until released. The clock is consulted once the request's
// transaction is open, so a parked request is held inside its read snapshot or
// write transaction.
type parkedClock struct {
	mu               sync.Mutex
	now              time.Time
	armed            atomic.Bool
	entered, release chan struct{}
}

func newParkedClock(s *Server, now time.Time) *parkedClock {
	p := &parkedClock{now: now}
	s.claimClock = p.read
	return p
}
func (p *parkedClock) read() time.Time {
	if p.armed.CompareAndSwap(true, false) {
		close(p.entered)
		<-p.release
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.now
}
func (p *parkedClock) set(now time.Time) {
	p.mu.Lock()
	p.now = now
	p.mu.Unlock()
}

// park arms the clock and starts f, which must consult it; it returns once f
// is parked, with a function that releases f and returns its result.
func park[T any](t *testing.T, p *parkedClock, f func() T) func() T {
	t.Helper()
	p.entered, p.release = make(chan struct{}), make(chan struct{})
	p.armed.Store(true)
	done := make(chan T, 1)
	go func() { done <- f() }()
	select {
	case <-p.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("request never consulted the clock")
	}
	released := false
	t.Cleanup(func() {
		if !released {
			close(p.release)
		}
	})
	return func() T {
		released = true
		close(p.release)
		return <-done
	}
}

// within fails the test unless f finishes in time.
func within(t *testing.T, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { f(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s waited for a parked read", what)
	}
}

type response struct {
	status int
	body   map[string]any
}

func acquireOp(c protocol.Client, id string) protocol.ClaimOperation {
	return protocol.ClaimOperation{SchemaVersion: 1, Operation: "claims.acquire", OwnerClientID: c.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: id}}}
}

// A read in progress, here parked inside its snapshot, does not delay writes:
// a claim is acquired and renewed and an issue is created meanwhile. The read
// answers from its snapshot, taken before those writes.
func TestSlowReadDoesNotDelayWrites(t *testing.T) {
	s, c, id := claimFixture(t)
	clock := newParkedClock(s, time.Now().UTC())
	finish := park(t, clock, func() response {
		status, v := serve(t, s, c.ClientID, "GET", "/v1/issues/"+id+"/claim", nil)
		return response{status, v}
	})
	within(t, "writes", func() {
		status, v := serve(t, s, c.ClientID, "POST", "/v1/operations", acquireOp(c, id))
		if status != 200 {
			t.Error(status, v)
			return
		}
		claim := v["data"].(map[string]any)["items"].([]any)[0].(map[string]any)["claim"].(map[string]any)
		extend := clock.read().Add(time.Hour).Format(time.RFC3339Nano)
		if status, v = serve(t, s, c.ClientID, "POST", "/v1/issues/"+id+"/claim/renew", map[string]any{"token": claim["token"], "extend_to": extend}); status != 200 {
			t.Error(status, v)
		}
		p, e := s.PrepareRecords(protocol.PrepareRequest{Operation: "issue.create", Project: "feat/claims", Items: []protocol.ProjectInput{{ID: protocol.UUID(), Title: textPointer("Created during a read")}}}, c)
		if e != nil {
			t.Error(e)
			return
		}
		mustExecute(t, s, c, p)
	})
	read := finish()
	if read.status != 200 || read.body["data"] != nil {
		t.Fatal("read did not answer from its snapshot", read)
	}
	if status, v := serve(t, s, c.ClientID, "GET", "/v1/issues/"+id+"/claim", nil); status != 200 || v["data"] == nil {
		t.Fatal("later read misses the claim", status, v)
	}
}

// Reconciliation is serialized with writes: while the original request waits
// inside its write transaction (another connection holds SQLite's write lock),
// transactions status and the retained-request lookup wait for it instead of
// reporting it uncommitted, and then prove it committed.
func TestReconcileWaitsForACommittingRequest(t *testing.T) {
	s, c, id := claimFixture(t)
	p, e := s.PrepareRecords(protocol.PrepareRequest{Operation: "issue.update", Project: "feat/claims", Items: []protocol.ProjectInput{{Target: id, Set: protocol.ProjectSet{Body: textPointer("changed\n")}}}}, c)
	if e != nil {
		t.Fatal(e)
	}
	durable, _ := json.Marshal(protocol.DurableRequest{SchemaVersion: 1, Intent: p.Intent, RequestHash: p.RequestHash})
	held, e := outside(t, s).Conn(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = held.ExecContext(context.Background(), "BEGIN IMMEDIATE"); e != nil {
		t.Fatal(e)
	}
	release := sync.OnceFunc(func() { held.ExecContext(context.Background(), "ROLLBACK"); held.Close() })
	defer release()
	original := make(chan int, 1)
	go func() { original <- func() int { code, _ := executeProjectTest(t, s, c, p); return code }() }()
	waitInStack(t, "storage.(*Writer).Begin")
	status := make(chan response, 1)
	go func() {
		code, v := serve(t, s, c.ClientID, "POST", "/v1/transaction-status", protocol.ReconcileRequest{ServiceID: s.DB.ServiceID(), ClientID: c.ClientID, Method: "POST", Path: "/v1/transactions", Body: durable})
		status <- response{code, v}
	}()
	lookup := make(chan response, 1)
	go func() {
		code, v := serve(t, s, c.ClientID, "GET", "/v1/issues/"+id+"/requests/"+p.RequestHash, nil)
		lookup <- response{code, v}
	}()
	waitForWaiters(t, "storage.(*DB).LockWrites", 2)
	select {
	case v := <-status:
		t.Fatal("reconciled a request still committing", v)
	case v := <-lookup:
		t.Fatal("looked up a request still committing", v)
	default:
	}
	release()
	if code := <-original; code != 200 {
		t.Fatal("original request", code)
	}
	if v := <-status; v.status != 200 || v.body["data"].(map[string]any)["status"] != "committed" {
		t.Fatal(v)
	}
	if v := <-lookup; v.status != 200 || v.body["data"].(map[string]any)["outcome"] != "recorded" {
		t.Fatal(v)
	}
}

// waitForWaiters waits until n goroutines run the named function.
func waitForWaiters(t *testing.T, function string, n int) {
	t.Helper()
	buf := make([]byte, 1<<20)
	for deadline := time.Now().Add(4 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if strings.Count(string(buf[:runtimeStack(buf)]), function+"(") >= n {
			return
		}
	}
	t.Fatalf("fewer than %d goroutines reached %s", n, function)
}

// A read may briefly see a claim as expired while its renewal commits (the
// accepted trade-off), but writers always see the renewal: a competing acquire
// still gets claim_conflict.
func TestClaimRenewalRacingAReadStaysCorrectForWriters(t *testing.T) {
	s, a, id := claimFixture(t)
	b := protocol.Client{SchemaVersion: 1, ClientID: protocol.UUID(), StateRevision: 1, Status: "connected", Actor: protocol.Actor{Name: "B", Kind: "agent"}}
	if e := s.SaveClient(b); e != nil {
		t.Fatal(e)
	}
	t0 := time.Now().UTC().Truncate(time.Microsecond)
	clock := newParkedClock(s, t0)
	status, v := serve(t, s, a.ClientID, "POST", "/v1/operations", acquireOp(a, id))
	if status != 200 {
		t.Fatal(status, v)
	}
	token := v["data"].(map[string]any)["items"].([]any)[0].(map[string]any)["claim"].(map[string]any)["token"]
	// The read's snapshot holds the claim that expires at t0+30m.
	finish := park(t, clock, func() response {
		status, v := serve(t, s, b.ClientID, "GET", "/v1/issues/"+id+"/claim", nil)
		return response{status, v}
	})
	clock.set(t0.Add(29 * time.Minute))
	renewed := t0.Add(89 * time.Minute).Format(time.RFC3339Nano)
	within(t, "renewal", func() {
		status, v = serve(t, s, a.ClientID, "POST", "/v1/issues/"+id+"/claim/renew", map[string]any{"token": token, "extend_to": renewed})
	})
	if status != 200 {
		t.Fatal(status, v)
	}
	clock.set(t0.Add(31 * time.Minute))
	if read := finish(); read.status != 200 || read.body["data"] != nil {
		t.Fatal("read outside its snapshot", read)
	}
	if status, v = serve(t, s, b.ClientID, "POST", "/v1/operations", acquireOp(b, id)); status != 409 || v["error"].(map[string]any)["code"] != "claim_conflict" {
		t.Fatal("competing acquire ignored the renewal", status, v)
	}
	status, v = serve(t, s, b.ClientID, "GET", "/v1/issues/"+id+"/claim", nil)
	if claim, _ := v["data"].(map[string]any); status != 200 || claim["token"] != token || claim["expires_at"] != renewed {
		t.Fatal(status, v)
	}
}

// The claim snapshot's server time never exceeds the clock of the claim write
// that follows it, so renewing for one hour from that time never trips the
// one-hour cap, also while reads and writes run concurrently.
func TestClaimSnapshotThenRenewForAnHourNeverTripsTheCap(t *testing.T) {
	s, c := projectTestServer(t, t.TempDir())
	project := protocol.UUID()
	mustExecute(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: project, Title: textPointer("feat/renewals")}))
	const workers, rounds = 4, 25
	ids := make([]string, workers)
	items := []protocol.ProjectInput{}
	for n := range ids {
		ids[n] = protocol.UUID()
		items = append(items, protocol.ProjectInput{ID: ids[n], Title: textPointer(fmt.Sprintf("Issue %d", n))})
	}
	p, e := s.PrepareRecords(protocol.PrepareRequest{Operation: "issue.create", Project: project, Items: items}, c)
	if e != nil {
		t.Fatal(e)
	}
	mustExecute(t, s, c, p)
	for _, id := range ids {
		if status, v := serve(t, s, c.ClientID, "POST", "/v1/operations", acquireOp(c, id)); status != 200 {
			t.Fatal(status, v)
		}
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range 2 { // background readers
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				serve(t, s, c.ClientID, "POST", "/v1/search", map[string]any{"query": "issue", "scope": map[string]any{"project_id": project}})
			}
		}()
	}
	var renewers sync.WaitGroup
	for _, id := range ids {
		renewers.Add(1)
		go func() {
			defer renewers.Done()
			for range rounds {
				status, v := serve(t, s, c.ClientID, "POST", "/v1/claim-snapshots", map[string]any{"targets": []string{id}})
				if status != 200 {
					t.Error(status, v)
					return
				}
				data := v["data"].(map[string]any)
				at, e := time.Parse(time.RFC3339Nano, data["server_time"].(string))
				if e != nil {
					t.Error(e)
					return
				}
				claim := data["items"].([]any)[0].(map[string]any)["claim"].(map[string]any)
				op := protocol.ClaimOperation{SchemaVersion: 1, Operation: "claims.renew", OwnerClientID: c.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: id, Token: claim["token"].(string), ExtendTo: at.Add(time.Hour).Format(time.RFC3339Nano)}}}
				if status, v = serve(t, s, c.ClientID, "POST", "/v1/operations", op); status != 200 {
					t.Error("renew for an hour from the snapshot time", status, v)
					return
				}
			}
		}()
	}
	renewers.Wait()
	close(stop)
	wg.Wait()
}

func runtimeStack(buf []byte) int { return runtime.Stack(buf, true) }
