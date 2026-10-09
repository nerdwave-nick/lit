package claims_test

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/claims"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/lifecycle"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/tuitest"
)

const project = "test/claims"

// fixture is a connected lit-tui lifecycle and an agent client of the same
// temporary service, which owns the project and its issues.
type fixture struct {
	t     *testing.T
	srv   *tuitest.Server
	l     *lifecycle.Lifecycle
	agent *litclient.Session
	ids   []string
	self  string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	srv := tuitest.Service(t)
	_, agent := tuitest.InstanceAs(t, t.TempDir(), srv.URL, protocol.Actor{Name: "Claude", Kind: "agent"})
	tuitest.Apply(t, agent, litclient.CreateProject(project, "spec"))
	ids := tuitest.CreateIssues(t, agent, project, "", "Issue A", "Issue B")
	l, err := lifecycle.New(lifecycle.Config{StateDir: t.TempDir(), Endpoint: srv.URL, HTTP: &http.Client{}, Actor: protocol.Actor{Name: "person", Kind: "human"},
		Host: "this-host", PID: os.Getpid(), CallTimeout: 3 * time.Second, StatusTimeout: 2 * time.Second, DisconnectTimeout: 2 * time.Second,
		Trust: func(string) (bool, string) { return true, "" }})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		l.Shutdown("test")
		<-l.Done()
	})
	return &fixture{t: t, srv: srv, l: l, agent: agent, ids: ids, self: l.Status().Client}
}

// claim reads issue id's live claim as the agent sees it, or nil.
func (f *fixture) claim(id string) *protocol.Claim {
	f.t.Helper()
	snap, err := f.agent.SnapshotClaims(context.Background(), litclient.ClaimSelection{Issues: []string{id}})
	if err != nil || len(snap.Items) != 1 {
		f.t.Fatalf("claim snapshot: %+v %v", snap, err)
	}
	return snap.Items[0].Claim
}

// ours reports whether this instance's client holds issue id's live claim.
func (f *fixture) ours(id string) bool {
	c := f.claim(id)
	return c != nil && c.OwnerClientID == f.self
}

func (f *fixture) operations() int { return f.srv.Count("POST /v1/operations") }

func eventually(t *testing.T, d time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for %s", d, what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func hold(t *testing.T, h *claims.Holder, id string) claims.Hold {
	t.Helper()
	got, ok := h.Snapshot().Hold(id)
	if !ok {
		t.Fatalf("no hold on %s: %+v", id, h.Snapshot())
	}
	return got
}

func state(h *claims.Holder, id string) claims.State {
	got, _ := h.Snapshot().Hold(id)
	return got.State
}

func mustClaim(t *testing.T, h *claims.Holder, id, title string) claims.Result {
	t.Helper()
	r := h.Claim(context.Background(), id, title)
	if r.Err != nil {
		t.Fatalf("claim %s: %v", title, r.Err)
	}
	return r
}

// blocked is a program model whose Update blocks on the first holder update
// until release is closed, like an event loop paused by an editor.
type blocked struct {
	entered chan struct{}
	release chan struct{}
	updates *atomic.Int32
}

func (m blocked) Init() tea.Cmd { return nil }
func (m blocked) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(claims.Msg); ok {
		if m.updates.Add(1) == 1 {
			close(m.entered)
			<-m.release
		}
	}
	return m, nil
}
func (m blocked) View() tea.View { return tea.NewView("") }

// Renewal keeps a claim alive for several leases while the event loop is
// blocked inside Update and the forwarder is stuck in Program.Send.
func TestRenewalContinuesWhileTheEventLoopIsBlocked(t *testing.T) {
	f := setup(t)
	model := blocked{entered: make(chan struct{}), release: make(chan struct{}), updates: &atomic.Int32{}}
	p := tea.NewProgram(model, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithoutSignalHandler())
	ran := make(chan struct{})
	go func() { _, _ = p.Run(); close(ran) }()
	f.l.Attach(p)
	var unblock sync.Once
	t.Cleanup(func() {
		unblock.Do(func() { close(model.release) }) // a failure must not leave Update blocked
		p.Quit()
		<-ran
	})
	const lease = 1500 * time.Millisecond
	h := claims.Start(f.l, claims.Config{Lease: lease, Idle: time.Hour})
	start := time.Now()
	mustClaim(t, h, f.ids[0], "Issue A")
	select {
	case <-model.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the program never received the holder's update")
	}
	expiries := map[string]bool{}
	eventually(t, 20*time.Second, "three renewals over more than two leases while Update is blocked", func() bool {
		if c := f.claim(f.ids[0]); c != nil && c.OwnerClientID == f.self {
			expiries[c.ExpiresAt] = true
		}
		return len(expiries) >= 4 && time.Since(start) > 2*lease
	})
	if !f.ours(f.ids[0]) || state(h, f.ids[0]) != claims.Holding || hold(t, h, f.ids[0]).Renewals < 3 {
		t.Fatalf("claim not kept alive: %+v", h.Snapshot())
	}
	if n := model.updates.Load(); n != 1 {
		t.Fatalf("the event loop was not blocked throughout: %d updates", n)
	}
}

// After the idle period without activity, renewal stops and the user is
// asked. Without an answer the claim lapses and is shown as lost; nothing is
// renewed or reacquired, and keeping it afterwards does not report success.
func TestIdlePausesRenewalAsksAndLetsTheClaimLapse(t *testing.T) {
	f := setup(t)
	h := claims.Start(f.l, claims.Config{Lease: 1500 * time.Millisecond, Idle: 300 * time.Millisecond})
	mustClaim(t, h, f.ids[0], "Issue A")
	eventually(t, 10*time.Second, "the idle question", func() bool { return h.Snapshot().Asking })
	if s := hold(t, h, f.ids[0]); s.State != claims.Paused || s.Renewals != 0 {
		t.Fatalf("hold after idle: %+v", s)
	}
	if got := h.Held(); len(got) != 1 || !strings.Contains(got[0], "Issue A") {
		t.Fatalf("a paused claim is still held: %v", got)
	}
	ops := f.operations()
	eventually(t, 10*time.Second, "the hold to be lost", func() bool { return state(h, f.ids[0]) == claims.Lost })
	if s := hold(t, h, f.ids[0]); !strings.Contains(s.Reason, "lease") {
		t.Fatalf("lost reason: %+v", s)
	}
	eventually(t, 10*time.Second, "the server lease to pass", func() bool { return f.claim(f.ids[0]) == nil })
	r := h.KeepHolding(context.Background())
	if len(r.Kept) != 0 || len(r.Lost) != 1 || r.Lost[0].IssueID != f.ids[0] || state(h, f.ids[0]) != claims.Lost || h.Snapshot().Asking {
		t.Fatalf("keep holding after expiry reported success: %+v %+v", r, h.Snapshot())
	}
	if f.operations() != ops || f.claim(f.ids[0]) != nil || len(h.Held()) != 0 {
		t.Fatalf("a lapsed claim was renewed or reacquired: %d operations after %d; claim %+v", f.operations(), ops, f.claim(f.ids[0]))
	}
}

// Keeping the claim while its lease is live renews it at once and resumes
// renewal; only a confirmed renewal reports success.
func TestKeepHoldingRenewsAtOnceAndResumes(t *testing.T) {
	f := setup(t)
	h := claims.Start(f.l, claims.Config{Lease: 5 * time.Second, Idle: 300 * time.Millisecond})
	mustClaim(t, h, f.ids[0], "Issue A")
	eventually(t, 10*time.Second, "the idle question", func() bool { return h.Snapshot().Asking })
	before := f.claim(f.ids[0]).ExpiresAt
	r := h.KeepHolding(context.Background())
	if len(r.Kept) != 1 || len(r.Lost) != 0 || r.Err != nil {
		t.Fatalf("keep holding: %+v", r)
	}
	if s := hold(t, h, f.ids[0]); s.State != claims.Holding || s.Renewals != 1 || h.Snapshot().Asking {
		t.Fatalf("hold after keep: %+v", h.Snapshot())
	}
	if after := f.claim(f.ids[0]).ExpiresAt; after <= before {
		t.Fatalf("lease not extended: %s then %s", before, after)
	}
	// Without further activity it pauses again.
	eventually(t, 10*time.Second, "the idle question again", func() bool { return h.Snapshot().Asking })
}

// Activity (input or a draft change) postpones the idle pause, so renewal
// continues past several leases.
func TestTouchPostponesTheIdlePause(t *testing.T) {
	f := setup(t)
	const lease = 1200 * time.Millisecond
	h := claims.Start(f.l, claims.Config{Lease: lease, Idle: 400 * time.Millisecond})
	mustClaim(t, h, f.ids[0], "Issue A")
	end := time.Now().Add(2*lease + 500*time.Millisecond)
	for time.Now().Before(end) {
		h.Touch()
		if h.Snapshot().Asking {
			t.Fatalf("asked despite activity: %+v", h.Snapshot())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if s := hold(t, h, f.ids[0]); s.State != claims.Holding || s.Renewals < 2 || !f.ours(f.ids[0]) {
		t.Fatalf("not renewed while active: %+v", s)
	}
	eventually(t, 10*time.Second, "the idle question once activity stops", func() bool { return h.Snapshot().Asking })
}

// A failed renewal shows the hold as lost and is never retried; the claim is
// never reacquired automatically.
func TestAFailedRenewalIsLostAndNeverRetried(t *testing.T) {
	f := setup(t)
	const lease = 1200 * time.Millisecond
	h := claims.Start(f.l, claims.Config{Lease: lease, Idle: time.Hour})
	mustClaim(t, h, f.ids[0], "Issue A")
	if _, err := f.agent.AcquireClaims(context.Background(), litclient.ClaimRequest{ClaimSelection: litclient.ClaimSelection{Issues: []string{f.ids[0]}}, For: time.Hour, Force: true}); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, "the hold to be lost", func() bool { return state(h, f.ids[0]) == claims.Lost })
	if s := hold(t, h, f.ids[0]); !strings.Contains(s.Reason, "claim_conflict") {
		t.Fatalf("lost reason: %+v", s)
	}
	ops := f.operations()
	time.Sleep(3 * lease) // several renewal periods: nothing may be sent
	if f.operations() != ops || state(h, f.ids[0]) != claims.Lost || f.ours(f.ids[0]) || len(h.Held()) != 0 {
		t.Fatalf("lost claim retried or reacquired: %d operations after %d; %+v", f.operations(), ops, h.Snapshot())
	}
}

// An explicit claim of an issue another client holds is refused before
// anything is written, and the owner is named; nothing is forced.
func TestClaimRefusesAnotherClientsClaimAndNamesIt(t *testing.T) {
	f := setup(t)
	if _, err := f.agent.AcquireClaims(context.Background(), litclient.ClaimRequest{ClaimSelection: litclient.ClaimSelection{Issues: []string{f.ids[1]}}, For: time.Hour}); err != nil {
		t.Fatal(err)
	}
	h := claims.Start(f.l, claims.Config{})
	ops := f.operations()
	r := h.Claim(context.Background(), f.ids[1], "Issue B")
	if litclient.Code(r.Err) != "claim_conflict" || r.Owner == nil || r.Owner.OwnerActor == nil || r.Owner.OwnerActor.Name != "Claude" || r.Owner.OwnerActor.Kind != "agent" {
		t.Fatalf("refusal: %+v", r)
	}
	if _, ok := h.Snapshot().Hold(f.ids[1]); ok || f.operations() != ops || f.ours(f.ids[1]) {
		t.Fatalf("a refused claim was written or held: %+v", h.Snapshot())
	}
}

// Reacquiring a lost hold rereads the issue first: it is refused while
// another client holds it, and afterwards takes the claim at the issue's
// current revision.
func TestReacquireRereadsTheIssueFirst(t *testing.T) {
	f := setup(t)
	h := claims.Start(f.l, claims.Config{Lease: 1200 * time.Millisecond, Idle: time.Hour})
	first := mustClaim(t, h, f.ids[0], "Issue A")
	if first.Revision == 0 || first.Reacquired {
		t.Fatalf("first claim: %+v", first)
	}
	sel := litclient.ClaimSelection{Issues: []string{f.ids[0]}}
	if _, err := f.agent.AcquireClaims(context.Background(), litclient.ClaimRequest{ClaimSelection: sel, For: time.Hour, Force: true}); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, "the hold to be lost", func() bool { return state(h, f.ids[0]) == claims.Lost })
	if r := h.Claim(context.Background(), f.ids[0], "Issue A"); litclient.Code(r.Err) != "claim_conflict" || r.Owner == nil || state(h, f.ids[0]) != claims.Lost {
		t.Fatalf("reacquire while the agent holds it: %+v", r)
	}
	title := "Issue A renamed"
	tuitest.Apply(t, f.agent, litclient.UpdateIssue(project, f.ids[0], 0, litclient.IssueChange{Title: &title}))
	if _, err := f.agent.ReleaseClaims(context.Background(), sel); err != nil {
		t.Fatal(err)
	}
	r := h.Claim(context.Background(), f.ids[0], "Issue A")
	if r.Err != nil || !r.Reacquired || r.Previous != first.Revision || r.Revision <= first.Revision {
		t.Fatalf("reacquire: %+v (first %+v)", r, first)
	}
	if s := hold(t, h, f.ids[0]); s.State != claims.Holding || s.Revision != r.Revision || s.Title != title || !f.ours(f.ids[0]) {
		t.Fatalf("hold after reacquire: %+v", s)
	}
}

// Release ends the claim and forgets the hold; a lost hold is forgotten
// without touching the claim that replaced it.
func TestReleaseEndsTheClaimAndForgetsTheHold(t *testing.T) {
	f := setup(t)
	h := claims.Start(f.l, claims.Config{Lease: 1200 * time.Millisecond, Idle: time.Hour})
	mustClaim(t, h, f.ids[0], "Issue A")
	mustClaim(t, h, f.ids[1], "Issue B")
	if got := h.Held(); len(got) != 2 {
		t.Fatalf("held: %v", got)
	}
	if r := h.Release(context.Background(), f.ids[0]); r.Err != nil {
		t.Fatal(r.Err)
	}
	if _, ok := h.Snapshot().Hold(f.ids[0]); ok || f.claim(f.ids[0]) != nil || len(h.Held()) != 1 {
		t.Fatalf("after release: %+v %+v", h.Snapshot(), f.claim(f.ids[0]))
	}
	if _, err := f.agent.AcquireClaims(context.Background(), litclient.ClaimRequest{ClaimSelection: litclient.ClaimSelection{Issues: []string{f.ids[1]}}, For: time.Hour, Force: true}); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, "the hold to be lost", func() bool { return state(h, f.ids[1]) == claims.Lost })
	h.Release(context.Background(), f.ids[1])
	if c := f.claim(f.ids[1]); len(h.Snapshot().Holds) != 0 || c == nil || c.OwnerClientID == f.self {
		t.Fatalf("after releasing a lost hold: %+v %+v", h.Snapshot(), c)
	}
}

// Stop joins the renewer: it returns only after an in-flight renewal has
// finished, and nothing is sent afterwards.
func TestStopJoinsAnInFlightRenewal(t *testing.T) {
	f := setup(t)
	const lease = 900 * time.Millisecond
	h := claims.Start(f.l, claims.Config{Lease: lease, Idle: time.Hour})
	mustClaim(t, h, f.ids[0], "Issue A")
	var inflight, finished atomic.Bool
	entered := make(chan struct{}, 1)
	f.srv.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/v1/operations" && r.Header.Get("X-Lit-Client-ID") == f.self && inflight.CompareAndSwap(false, true) {
			entered <- struct{}{}
			time.Sleep(400 * time.Millisecond)
			finished.Store(true)
		}
		return false
	})
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("no renewal")
	}
	if err := h.Stop(context.Background()); err != nil || !finished.Load() {
		t.Fatalf("stop returned before the renewal finished: %v", err)
	}
	ops := f.operations()
	time.Sleep(2 * lease)
	if f.operations() != ops {
		t.Fatal("renewed after Stop")
	}
}

// The holder is the lifecycle's renewer: shutdown names its claims, stops it
// before the one disconnect, which releases the held claim, and nothing is
// renewed afterwards.
func TestShutdownStopsTheHolderAndReleasesItsClaims(t *testing.T) {
	f := setup(t)
	const lease = 900 * time.Millisecond
	h := claims.Start(f.l, claims.Config{Lease: lease, Idle: time.Hour})
	mustClaim(t, h, f.ids[0], "Issue A")
	if got := f.l.Outstanding().Claims; len(got) != 1 || !strings.Contains(got[0], "Issue A") {
		t.Fatalf("the lifecycle does not see the holder's claims: %v", got)
	}
	f.l.Shutdown("quit")
	select {
	case <-f.l.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown did not finish")
	}
	if r := f.l.Result(); r.Outcome != lifecycle.Clean || f.claim(f.ids[0]) != nil {
		t.Fatalf("result %+v; claim %+v", r, f.claim(f.ids[0]))
	}
	ops := f.operations()
	time.Sleep(2 * lease)
	if f.operations() != ops {
		t.Fatal("the renewer kept sending after shutdown")
	}
}
