package recovery_test

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/tui/recovery"
	"github.com/nerdwave-nick/lit/internal/tui/registry"
	"github.com/nerdwave-nick/lit/internal/tui/tuitest"
)

func options(state string) recovery.Options {
	host, _ := os.Hostname()
	return recovery.Options{StateDir: state, Self: protocol.UUID(), HTTP: &http.Client{Timeout: 5 * time.Second}, Host: host,
		Trust: func(string) (bool, string) { return true, "" }}
}

func published(e registry.Entry) bool {
	_, err := os.Stat(e.InfoPath())
	return err == nil
}

func unchanged(t *testing.T, path string, before []byte) {
	t.Helper()
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("evidence %s changed or vanished: %v", path, err)
	}
}

// An orphan with nothing uncertain is disconnected (its claims released) and
// its entry removed; a live instance is never touched.
func TestScanDisconnectsVerifiedOrphansAndNeverTouchesLiveInstances(t *testing.T) {
	srv := tuitest.Service(t)
	state := t.TempDir()
	orphan, os1 := tuitest.Instance(t, state, srv.URL)
	live, ls := tuitest.Instance(t, state, srv.URL)
	ctx := context.Background()
	project := "test/claims"
	for _, req := range []protocol.PrepareRequest{litclient.CreateProject(project, "spec"), litclient.CreateIssue(project, "Held", "body", "")} {
		p, err := os1.Prepare(ctx, req)
		if err == nil {
			_, err = os1.Commit(ctx, p)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os1.AcquireClaims(ctx, litclient.ClaimRequest{ClaimSelection: litclient.ClaimSelection{Project: project, Issues: []string{"title:Held"}}}); err != nil {
		t.Fatal(err)
	}
	tuitest.Crash(os1)

	o := options(state)
	o.Self = protocol.UUID()
	r, err := recovery.Scan(o)
	if err != nil {
		t.Fatal(err)
	}
	if r.Live != 1 || len(r.Orphans) != 1 {
		t.Fatalf("report: %+v", r)
	}
	got := r.Orphans[0]
	if got.Entry.ID != orphan.ID || got.Outcome != recovery.Disconnected || !got.Removed || got.ClientID != os1.Mapping.ClientID || got.Info.InstanceID != orphan.ID {
		t.Fatalf("orphan: %+v", got)
	}
	if st := tuitest.ClientStatus(t, srv.URL, os1.Mapping.ClientID); st != "disconnected" {
		t.Fatalf("orphan client %s", st)
	}
	claims, err := ls.SnapshotClaims(ctx, litclient.ClaimSelection{Project: project, Issues: []string{"title:Held"}})
	if err != nil || claims.Items[0].Claim != nil {
		t.Fatalf("orphan claim not released: %+v %v", claims, err)
	}
	if published(orphan) {
		t.Fatal("orphan entry kept after a confirmed disconnect")
	}
	if !published(live) || tuitest.ClientStatus(t, srv.URL, ls.Mapping.ClientID) != "connected" {
		t.Fatal("live instance touched")
	}
	// Its own entry is never recovered, even when its lock is free.
	tuitest.Crash(ls)
	o.Self = live.ID
	if r, err = recovery.Scan(o); err != nil || r.Live != 0 || len(r.Orphans) != 0 {
		t.Fatalf("self scanned: %+v %v", r, err)
	}
}

// An orphan with an uncertain request is never disconnected: its request IDs
// and evidence are reported, and the entry and evidence stay.
func TestScanNeverDisconnectsAnOrphanWithUncertainRequests(t *testing.T) {
	srv := tuitest.Service(t)
	state := t.TempDir()
	e, s := tuitest.Instance(t, state, srv.URL)
	path := tuitest.UncertainAcquire(t, s)
	before, _ := os.ReadFile(path)
	tuitest.Crash(s)
	for attempt := 0; attempt < 2; attempt++ {
		r, err := recovery.Scan(options(state))
		if err != nil || len(r.Orphans) != 1 {
			t.Fatalf("report: %+v %v", r, err)
		}
		got := r.Orphans[0]
		if got.Outcome != recovery.Uncertain || got.Removed || len(got.Requests) != 1 || got.Requests[0].Path != path ||
			!strings.HasSuffix(path, got.Requests[0].ID+".json") || got.Requests[0].Status != "uncertain" || got.Requests[0].Proof == "" {
			t.Fatalf("orphan: %+v", got)
		}
	}
	if srv.Count("POST /v1/disconnect") != 0 || tuitest.ClientStatus(t, srv.URL, s.Mapping.ClientID) != "connected" {
		t.Fatal("disconnected an orphan with uncertain requests")
	}
	if !published(e) {
		t.Fatal("entry removed")
	}
	unchanged(t, path, before)
}

// A first registration whose client identity never arrived is uncertain
// without asking the service, and nothing is disconnected.
func TestScanReportsAnUnconfirmedRegistration(t *testing.T) {
	srv := tuitest.Service(t)
	state := t.TempDir()
	e := registry.New(state, protocol.UUID())
	s := &litclient.Session{StateDir: state}
	if err := s.LockPath(e.SessionPath(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := registry.Publish(e, registry.Info{SchemaVersion: 1, InstanceID: e.ID, PID: 7, StartedAt: protocol.Now(), Endpoint: srv.URL}); err != nil {
		t.Fatal(err)
	}
	path := tuitest.UnconfirmedRegistration(t, state, srv.URL, protocol.UUID(), e.ID)
	other := tuitest.UnconfirmedRegistration(t, state, srv.URL, protocol.UUID(), protocol.UUID())
	before, _ := os.ReadFile(path)
	s.Close()
	r, err := recovery.Scan(options(state))
	if err != nil || len(r.Orphans) != 1 {
		t.Fatalf("report: %+v %v", r, err)
	}
	got := r.Orphans[0]
	if got.Outcome != recovery.Uncertain || got.Removed || len(got.Requests) != 1 || got.Requests[0].Path != path || got.Requests[0].Proof != "registration_identity_not_received" {
		t.Fatalf("orphan: %+v (other instance's evidence %s)", got, other)
	}
	if srv.Count("POST /v1/disconnect") != 0 || srv.Count("POST /v1/transaction-status") != 0 || !published(e) {
		t.Fatal("touched an unconfirmed registration")
	}
	unchanged(t, path, before)
}

// When the disconnect's response is lost, the orphan is reported uncertain
// with the disconnect's request, never as clean, and its entry is kept.
func TestScanReportsAnUnconfirmedDisconnect(t *testing.T) {
	srv := tuitest.Service(t)
	state := t.TempDir()
	e, s := tuitest.Instance(t, state, srv.URL)
	tuitest.Crash(s)
	srv.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/v1/disconnect" || r.URL.Path == "/v1/transaction-status" {
			tuitest.HangUp(w)
			return true
		}
		return false
	})
	r, err := recovery.Scan(options(state))
	if err != nil || len(r.Orphans) != 1 {
		t.Fatalf("report: %+v %v", r, err)
	}
	got := r.Orphans[0]
	if got.Outcome == recovery.Disconnected || got.Removed || !published(e) {
		t.Fatalf("unconfirmed disconnect reported clean: %+v", got)
	}
	if got.Outcome != recovery.Uncertain || len(got.Requests) != 1 || !strings.Contains(got.Requests[0].Path, got.Requests[0].ID) {
		t.Fatalf("orphan: %+v", got)
	}
}

// An unreachable service leaves the orphan unknown and kept.
func TestScanLeavesAnUnreachableOrphanUnknown(t *testing.T) {
	srv := tuitest.Service(t)
	state := t.TempDir()
	e, s := tuitest.Instance(t, state, srv.URL)
	tuitest.Crash(s)
	srv.Intercept(func(w http.ResponseWriter, r *http.Request) bool { tuitest.HangUp(w); return true })
	r, err := recovery.Scan(options(state))
	if err != nil || len(r.Orphans) != 1 {
		t.Fatalf("report: %+v %v", r, err)
	}
	if got := r.Orphans[0]; got.Outcome != recovery.Unknown || got.Removed || got.Reason == "" || !published(e) {
		t.Fatalf("orphan: %+v", got)
	}
}

// Untrusted locks (a network file system) and orphans from another host fall
// back to asking; Recover then acts on the user's confirmation.
func TestUntrustedLocksFallBackToAsking(t *testing.T) {
	srv := tuitest.Service(t)
	for _, tc := range []struct {
		name  string
		setup func(o *recovery.Options, e registry.Entry)
		why   string
	}{
		{"network file system", func(o *recovery.Options, _ registry.Entry) {
			o.Trust = func(string) (bool, string) { return false, "nfs" }
		}, "nfs"},
		{"another host", func(o *recovery.Options, e registry.Entry) {
			info, _ := registry.ReadInfo(e)
			info.Host = "elsewhere.example"
			if err := registry.Publish(e, info); err != nil {
				panic(err)
			}
		}, "elsewhere.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := t.TempDir()
			e, s := tuitest.Instance(t, state, srv.URL)
			tuitest.Crash(s)
			o := options(state)
			tc.setup(&o, e)
			r, err := recovery.Scan(o)
			if err != nil || len(r.Orphans) != 1 {
				t.Fatalf("report: %+v %v", r, err)
			}
			if got := r.Orphans[0]; got.Outcome != recovery.Ask || !strings.Contains(got.Reason, tc.why) || got.Removed {
				t.Fatalf("orphan: %+v", got)
			}
			if tuitest.ClientStatus(t, srv.URL, s.Mapping.ClientID) != "connected" || !published(e) {
				t.Fatal("acted without asking")
			}
			got, ok, err := recovery.Recover(o, e)
			if err != nil || !ok || got.Outcome != recovery.Disconnected || !got.Removed {
				t.Fatalf("confirmed: %+v %v %v", got, ok, err)
			}
			if tuitest.ClientStatus(t, srv.URL, s.Mapping.ClientID) != "disconnected" || published(e) {
				t.Fatal("confirmed recovery did not disconnect")
			}
		})
	}
	// A confirmed entry that has come alive again (lock held) is not touched.
	state := t.TempDir()
	e, s := tuitest.Instance(t, state, srv.URL)
	if _, ok, err := recovery.Recover(options(state), e); ok || err != nil || tuitest.ClientStatus(t, srv.URL, s.Mapping.ClientID) != "connected" {
		t.Fatalf("recovered a live instance: %v %v", ok, err)
	}
}

// Every page of the registry is scanned.
func TestScanFollowsEveryRegistryPage(t *testing.T) {
	srv := tuitest.Service(t)
	state := t.TempDir()
	var clients []string
	for i := 0; i < 5; i++ {
		_, s := tuitest.Instance(t, state, srv.URL)
		clients = append(clients, s.Mapping.ClientID)
		tuitest.Crash(s)
	}
	o := options(state)
	o.Page = 2
	r, err := recovery.Scan(o)
	if err != nil || len(r.Orphans) != 5 {
		t.Fatalf("report: %+v %v", r, err)
	}
	for _, c := range clients {
		if tuitest.ClientStatus(t, srv.URL, c) != "disconnected" {
			t.Errorf("client %s not recovered", c)
		}
	}
}

// Recovery runs through Do; a refusal (shutdown) stops the scan untouched.
func TestScanRunsThroughDoAndStopsWhenRefused(t *testing.T) {
	srv := tuitest.Service(t)
	state := t.TempDir()
	e, s := tuitest.Instance(t, state, srv.URL)
	tuitest.Crash(s)
	o := options(state)
	refused := protocol.E(503, "shutting_down", "refused")
	o.Do = func(func(context.Context) error) error { return refused }
	if _, err := recovery.Scan(o); err != refused {
		t.Fatalf("scan: %v", err)
	}
	if !published(e) || tuitest.ClientStatus(t, srv.URL, s.Mapping.ClientID) != "connected" {
		t.Fatal("refused recovery acted")
	}
	calls := 0
	o.Do = func(fn func(context.Context) error) error {
		calls++
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		return fn(ctx)
	}
	if r, err := recovery.Scan(o); err != nil || calls != 1 || len(r.Orphans) != 1 || r.Orphans[0].Outcome != recovery.Disconnected {
		t.Fatalf("scan through Do: %+v %v calls %d", r, err, calls)
	}
}
