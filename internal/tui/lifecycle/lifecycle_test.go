package lifecycle_test

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/tui/lifecycle"
	"github.com/nerdwave-nick/lit/internal/tui/registry"
	"github.com/nerdwave-nick/lit/internal/tui/tuitest"
)

func config(state, url string) lifecycle.Config {
	return lifecycle.Config{StateDir: state, Endpoint: url, HTTP: &http.Client{}, Actor: protocol.Actor{Name: "person", Kind: "human"},
		Host: "this-host", PID: os.Getpid(), CallTimeout: 2 * time.Second, StatusTimeout: 2 * time.Second, DisconnectTimeout: 2 * time.Second,
		Trust: func(string) (bool, string) { return true, "" }}
}

// start creates and connects an instance.
func start(t *testing.T, state, url string) *lifecycle.Lifecycle {
	t.Helper()
	l, err := lifecycle.New(config(state, url))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	return l
}

func wait(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func published(e registry.Entry) bool {
	_, err := os.Stat(e.InfoPath())
	return err == nil
}

// mapping reads the instance's own session mapping.
func mapping(t *testing.T, l *lifecycle.Lifecycle) litclient.Mapping {
	t.Helper()
	m, err := litclient.ReadMapping(l.Entry().SessionPath())
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// Each launch registers a fresh client recorded in the TUI-owned registry,
// outside the CLI's sessions, and holds the entry's liveness lock.
func TestConnectRecordsAFreshClientInTheRegistry(t *testing.T) {
	srv := tuitest.Service(t)
	state := t.TempDir()
	a, b := start(t, state, srv.URL), start(t, state, srv.URL)
	ma, mb := mapping(t, a), mapping(t, b)
	if ma.ClientID == mb.ClientID || ma.ClientID == "" || a.Status().Client != ma.ClientID || a.Instance() == b.Instance() {
		t.Fatalf("instances share or lack a client: %+v %+v", ma, mb)
	}
	info, err := registry.ReadInfo(a.Entry())
	if err != nil || info.InstanceID != a.Instance() || info.PID != os.Getpid() || info.Host != "this-host" || info.Endpoint != srv.URL {
		t.Fatalf("info: %+v %v", info, err)
	}
	if sessions, _ := filepath.Glob(filepath.Join(state, "sessions", "*")); len(sessions) != 0 {
		t.Fatalf("wrote CLI sessions: %v", sessions)
	}
	probe := &litclient.Session{StateDir: state}
	if err := probe.LockPath(a.Entry().SessionPath(), time.Now()); litclient.Code(err) != "session_locked" {
		t.Fatalf("liveness lock not held: %v", err)
	}
	var c protocol.Client
	err = a.Do(context.Background(), func(ctx context.Context, s *litclient.Session) error {
		return s.Call("GET", "/v1/clients/"+s.Mapping.ClientID, nil, nil, &c, false)
	})
	if err != nil || c.Status != "connected" || c.Runtime.Vendor != "lit-tui" || c.Runtime.SessionID != a.Instance() || c.Actor.Kind != "human" {
		t.Fatalf("client: %+v %v", c, err)
	}
}

// After a confirmed clean disconnect only the instance's own entry and lock
// are removed; other entries, CLI sessions and pending evidence stay.
func TestShutdownDisconnectsAndRemovesOnlyItsOwnEntry(t *testing.T) {
	srv := tuitest.Service(t)
	state := t.TempDir()
	a, b := start(t, state, srv.URL), start(t, state, srv.URL)
	ma := mapping(t, a)
	other := tuitest.UnconfirmedRegistration(t, state, srv.URL, protocol.UUID(), protocol.UUID())
	cli := litclient.MappingPath(state, "default")
	if err := litclient.WriteJSON(cli, mapping(t, b)); err != nil {
		t.Fatal(err)
	}
	if !a.Shutdown("quit") || a.Shutdown("again") {
		t.Fatal("Shutdown must start exactly once")
	}
	wait(t, a.Done(), "shutdown")
	if r := a.Result(); r.Outcome != lifecycle.Clean || r.Kept || len(r.Requests) != 0 {
		t.Fatalf("result: %+v", r)
	}
	if st := tuitest.ClientStatus(t, srv.URL, ma.ClientID); st != "disconnected" {
		t.Fatalf("client %s", st)
	}
	for _, p := range []string{a.Entry().InfoPath(), a.Entry().SessionPath(), a.Entry().LockPath()} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s kept: %v", p, err)
		}
	}
	for _, p := range []string{b.Entry().InfoPath(), b.Entry().SessionPath(), other, cli} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s removed: %v", p, err)
		}
	}
	if tuitest.ClientStatus(t, srv.URL, mapping(t, b).ClientID) != "connected" {
		t.Fatal("another instance was disconnected")
	}
	if srv.Count("POST /v1/disconnect") != 1 {
		t.Fatalf("disconnects: %d", srv.Count("POST /v1/disconnect"))
	}
}

// Draining refuses new requests, waits for the in-flight one (never
// cancelling it), and only then disconnects.
func TestShutdownDrainsTheInFlightRequestBeforeDisconnecting(t *testing.T) {
	srv := tuitest.Service(t)
	l := start(t, t.TempDir(), srv.URL)
	admitted, release := make(chan struct{}), make(chan struct{})
	var cancelled atomic.Bool
	var disconnectsDuring atomic.Int32
	go l.Do(context.Background(), func(ctx context.Context, s *litclient.Session) error {
		close(admitted)
		<-release
		cancelled.Store(ctx.Err() != nil)
		disconnectsDuring.Store(int32(srv.Count("POST /v1/disconnect")))
		return nil
	})
	<-admitted
	l.Shutdown("quit")
	err := l.Do(context.Background(), func(context.Context, *litclient.Session) error { t.Error("admitted while draining"); return nil })
	if litclient.Code(err) != "shutting_down" {
		t.Fatalf("new request while draining: %v", err)
	}
	select {
	case <-l.Done():
		t.Fatal("finished before the in-flight request")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	wait(t, l.Done(), "shutdown")
	if cancelled.Load() || disconnectsDuring.Load() != 0 || l.Result().Outcome != lifecycle.Clean {
		t.Fatalf("cancelled %v, disconnects during %d, result %+v", cancelled.Load(), disconnectsDuring.Load(), l.Result())
	}
}

// An in-flight request that outlives the drain bound is never cancelled or
// replayed: nothing is disconnected and the result is unknown.
func TestDrainTimeoutLeavesTheSessionUnknown(t *testing.T) {
	srv := tuitest.Service(t)
	cfg := config(t.TempDir(), srv.URL)
	cfg.CallTimeout = 200 * time.Millisecond
	l, err := lifecycle.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	admitted, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	go l.Do(context.Background(), func(context.Context, *litclient.Session) error { close(admitted); <-release; return nil })
	<-admitted
	l.Shutdown("quit")
	wait(t, l.Done(), "shutdown")
	if r := l.Result(); r.Outcome != lifecycle.Unknown || !r.Kept || r.Reason == "" {
		t.Fatalf("result: %+v", r)
	}
	if srv.Count("POST /v1/disconnect") != 0 || !published(l.Entry()) {
		t.Fatal("disconnected or removed while a request was in flight")
	}
}

type renewer struct {
	stopped   atomic.Bool
	connected atomic.Bool
	check     func() bool
}

func (r *renewer) Stop(context.Context) error {
	r.connected.Store(r.check())
	r.stopped.Store(true)
	return nil
}
func (r *renewer) Held() []string { return []string{"issue A"} }

// The renewer is stopped and joined after draining and before the disconnect.
func TestShutdownStopsTheRenewerBeforeDisconnecting(t *testing.T) {
	srv := tuitest.Service(t)
	l := start(t, t.TempDir(), srv.URL)
	client := mapping(t, l).ClientID
	r := &renewer{check: func() bool { return tuitest.ClientStatus(t, srv.URL, client) == "connected" }}
	l.SetRenewer(r)
	l.Shutdown("quit")
	wait(t, l.Done(), "shutdown")
	if !r.stopped.Load() || !r.connected.Load() || l.Result().Outcome != lifecycle.Clean || len(l.Result().Claims) != 0 {
		t.Fatalf("stopped %v while connected %v; result %+v", r.stopped.Load(), r.connected.Load(), l.Result())
	}
}

// An uncertain retained request keeps the client connected (disconnecting it
// could make the outcome unprovable), keeps the entry and reports the
// request; the evidence is untouched.
func TestShutdownNeverDisconnectsWithAnUncertainRequest(t *testing.T) {
	srv := tuitest.Service(t)
	state := t.TempDir()
	l := start(t, state, srv.URL)
	var path string
	l.Do(context.Background(), func(_ context.Context, s *litclient.Session) error { path = tuitest.UncertainAcquire(t, s); return nil })
	before, _ := os.ReadFile(path)
	l.SetRenewer(&renewer{check: func() bool { return true }})
	l.Shutdown("quit")
	wait(t, l.Done(), "shutdown")
	r := l.Result()
	if r.Outcome != lifecycle.Uncertain || !r.Kept || len(r.Requests) != 1 || r.Requests[0].Path != path || len(r.Claims) != 1 {
		t.Fatalf("result: %+v", r)
	}
	if srv.Count("POST /v1/disconnect") != 0 || tuitest.ClientStatus(t, srv.URL, mapping(t, l).ClientID) != "connected" || !published(l.Entry()) {
		t.Fatal("disconnected or removed with an uncertain request")
	}
	if after, err := os.ReadFile(path); err != nil || !bytes.Equal(before, after) {
		t.Fatal("evidence changed")
	}
}

// A disconnect whose response is lost is reported uncertain with its request
// ID, never as clean, and the entry stays.
func TestUnconfirmedDisconnectIsNeverClean(t *testing.T) {
	srv := tuitest.Service(t)
	state := t.TempDir()
	l := start(t, state, srv.URL)
	srv.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/v1/disconnect" || r.URL.Path == "/v1/transaction-status" {
			tuitest.HangUp(w)
			return true
		}
		return false
	})
	l.Shutdown("quit")
	wait(t, l.Done(), "shutdown")
	r := l.Result()
	if r.Outcome != lifecycle.Uncertain || !r.Kept || len(r.Requests) != 1 || !strings.HasSuffix(r.Requests[0].Path, r.Requests[0].ID+".json") {
		t.Fatalf("result: %+v", r)
	}
	if _, err := os.Stat(r.Requests[0].Path); err != nil || !published(l.Entry()) {
		t.Fatalf("evidence or entry gone: %v", err)
	}
}

// A launch whose service cannot be reached registers nothing; its shutdown
// is clean and removes its entry. An uncertain registration keeps it.
func TestFailedRegistrationShutdown(t *testing.T) {
	srv := tuitest.Service(t)
	state := t.TempDir()
	unreachable, err := lifecycle.New(config(state, "http://127.0.0.1:1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := unreachable.Connect(context.Background()); litclient.Code(err) != "service_unavailable" {
		t.Fatalf("connect: %v", err)
	}
	unreachable.Shutdown("startup failed")
	wait(t, unreachable.Done(), "shutdown")
	if r := unreachable.Result(); r.Outcome != lifecycle.Clean || published(unreachable.Entry()) {
		t.Fatalf("unregistered: %+v", r)
	}

	srv.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/v1/connect" {
			tuitest.HangUp(w)
			return true
		}
		return false
	})
	lost, err := lifecycle.New(config(state, srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if err := lost.Connect(context.Background()); litclient.Code(err) != "outcome_uncertain" {
		t.Fatalf("connect: %v", err)
	}
	lost.Shutdown("startup failed")
	wait(t, lost.Done(), "shutdown")
	if r := lost.Result(); r.Outcome != lifecycle.Uncertain || !r.Kept || len(r.Requests) != 1 || r.Requests[0].Proof != "registration_identity_not_received" || !published(lost.Entry()) {
		t.Fatalf("uncertain registration: %+v", r)
	}
}

// A second interrupt forces an immediate exit with its code, and Outstanding
// reports the retained requests and claims from local state.
func TestSecondInterruptForcesExit(t *testing.T) {
	srv := tuitest.Service(t)
	l := start(t, t.TempDir(), srv.URL)
	admitted, release := make(chan struct{}), make(chan struct{})
	defer func() { close(release); wait(t, l.Done(), "shutdown") }()
	go l.Do(context.Background(), func(_ context.Context, s *litclient.Session) error {
		tuitest.UncertainAcquire(t, s)
		close(admitted)
		<-release
		return nil
	})
	<-admitted
	l.SetRenewer(&renewer{check: func() bool { return true }})
	l.Interrupt(130)
	select {
	case <-l.Forced():
		t.Fatal("the first interrupt forced exit")
	default:
	}
	l.Interrupt(143)
	wait(t, l.Forced(), "forced exit")
	o := l.Outstanding()
	if l.ForceCode() != 143 || len(o.Requests) != 1 || o.Requests[0].ID == "" || len(o.Claims) != 1 || !o.Kept {
		t.Fatalf("code %d outstanding %+v", l.ForceCode(), o)
	}
	l.Force(1)
	if l.ForceCode() != 143 {
		t.Fatal("a later force changed the code")
	}
}

// Signals take the same path: SIGTERM starts the shutdown, a second signal
// forces exit with 128+signal.
func TestSignalsStartShutdownAndASecondForces(t *testing.T) {
	srv := tuitest.Service(t)
	l := start(t, t.TempDir(), srv.URL)
	admitted, release := make(chan struct{}), make(chan struct{})
	defer func() { close(release); wait(t, l.Done(), "shutdown") }()
	go l.Do(context.Background(), func(context.Context, *litclient.Session) error { close(admitted); <-release; return nil })
	<-admitted
	l.Signal(syscall.SIGTERM)
	if !l.Started() {
		t.Fatal("SIGTERM did not start the shutdown")
	}
	l.Signal(syscall.SIGHUP)
	wait(t, l.Forced(), "forced exit")
	if l.ForceCode() != 128+int(syscall.SIGHUP) {
		t.Fatalf("code %d", l.ForceCode())
	}
}

// The gate serializes every use of the session.
func TestDoSerializesUseOfTheSession(t *testing.T) {
	srv := tuitest.Service(t)
	l := start(t, t.TempDir(), srv.URL)
	var active, most atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := l.Do(context.Background(), func(ctx context.Context, s *litclient.Session) error {
				n := active.Add(1)
				if n > most.Load() {
					most.Store(n)
				}
				_, err := litclient.List[protocol.Project](ctx, s, litclient.Query{})
				active.Add(-1)
				return err
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if most.Load() != 1 {
		t.Fatalf("%d concurrent uses", most.Load())
	}
}

type recorder struct {
	block chan struct{}
	mu    sync.Mutex
	last  map[string]any
	count int
}

func (r *recorder) Init() tea.Cmd { return nil }
func (r *recorder) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if s, ok := msg.(string); ok {
		if s == "block" {
			<-r.block
			return r, nil
		}
		k, v, _ := strings.Cut(s, "=")
		r.mu.Lock()
		r.last[k] = v
		r.count++
		r.mu.Unlock()
	}
	return r, nil
}
func (r *recorder) View() tea.View { return tea.NewView("") }

// Posting never blocks, even while the program's event loop is blocked (as
// during an editor session); updates coalesce per key and arrive afterwards.
func TestPostNeverBlocksAndCoalesces(t *testing.T) {
	srv := tuitest.Service(t)
	l := start(t, t.TempDir(), srv.URL)
	r := &recorder{block: make(chan struct{}), last: map[string]any{}}
	p := tea.NewProgram(r, tea.WithInput(nil), tea.WithOutput(&bytes.Buffer{}), tea.WithoutSignalHandler(), tea.WithoutRenderer())
	l.Attach(p)
	go p.Run()
	l.Post("block", "block")
	time.Sleep(50 * time.Millisecond)
	begin := time.Now()
	for i := 0; i < 1000; i++ {
		l.Post("a", "a="+strings.Repeat("x", i%7))
		l.Post("b", "b=1")
	}
	l.Post("a", "a=final")
	if d := time.Since(begin); d > time.Second {
		t.Fatalf("posting took %s while the program was blocked", d)
	}
	close(r.block)
	deadline := time.Now().Add(5 * time.Second)
	for {
		r.mu.Lock()
		a, b, n := r.last["a"], r.last["b"], r.count
		r.mu.Unlock()
		if a == "final" && b == "1" {
			if n > 20 {
				t.Errorf("%d deliveries: not coalesced", n)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not delivered: %v %v", a, b)
		}
		time.Sleep(10 * time.Millisecond)
	}
	p.Quit()
	p.Wait()
}

// Mutate marks a mutation as running from admission until it returns, so
// background refresh can pause; reads through Do never do.
func TestMutatingCoversOnlyMutations(t *testing.T) {
	srv := tuitest.Service(t)
	l := start(t, t.TempDir(), srv.URL)
	if l.Mutating() {
		t.Fatal("mutating before any mutation")
	}
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- l.Mutate(context.Background(), func(context.Context, *litclient.Session) error {
			close(entered)
			<-release
			return nil
		})
	}()
	wait(t, entered, "the mutation to start")
	if !l.Mutating() {
		t.Fatal("not mutating while a mutation runs")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if l.Mutating() {
		t.Fatal("still mutating after the mutation returned")
	}
	read := make(chan struct{})
	go func() {
		_ = l.Do(context.Background(), func(context.Context, *litclient.Session) error {
			if l.Mutating() {
				t.Error("a read counts as a mutation")
			}
			close(read)
			return nil
		})
	}()
	wait(t, read, "the read")
	l.Shutdown("test")
	wait(t, l.Done(), "shutdown")
	if err := l.Mutate(context.Background(), func(context.Context, *litclient.Session) error { return nil }); litclient.Code(err) != "shutting_down" || l.Mutating() {
		t.Fatalf("mutation after shutdown: %v, mutating %v", err, l.Mutating())
	}
}
