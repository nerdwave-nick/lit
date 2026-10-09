// Package lifecycle owns a lit-tui instance's logical session from launch to
// exit: the fresh client recorded in the per-instance registry, the single
// mutation gate in front of the (not concurrency-safe) litclient session, the
// forwarder that delivers UI updates without ever blocking its callers, and
// the root-owned shutdown coordinator.
//
// Shutdown is triggered by quit, Ctrl-C, SIGINT, SIGTERM and SIGHUP. It:
//  1. enters draining: new requests through the gate are refused;
//  2. waits for the in-flight request, bounded by its timeout, never
//     cancelling or replaying it;
//  3. stops and joins the claim renewer (T11 installs one with SetRenewer);
//  4. inspects this instance's retained requests read-only and, when none is
//     uncertain, runs one bounded disconnect, which releases every claim of
//     the client atomically;
//  5. removes only this instance's registry entry and lock after a confirmed
//     clean disconnect, then asks the program to quit, which restores the
//     terminal (a program suspended in an editor quits once the editor exits).
//
// When anything is uncertain the entry stays for recovery, the request IDs
// and evidence paths are shown, and leaving takes a second keypress. A
// second signal forces an immediate exit. Pending evidence is never removed,
// moved or replayed.
package lifecycle

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/recovery"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/registry"
)

// Default bounds (Proposals). CallTimeout bounds every request through the
// gate, and so how long draining waits for the in-flight one.
const (
	DefaultCallTimeout       = 10 * time.Second
	DefaultStatusTimeout     = 5 * time.Second
	DefaultDisconnectTimeout = 5 * time.Second
)

// Config describes one lit-tui instance.
type Config struct {
	// StateDir is the client state directory (litclient.StateDir): the
	// registry lives in it and pending evidence stays in its pending/.
	StateDir string
	// Endpoint is the resolved service URL.
	Endpoint string
	HTTP     *http.Client
	Actor    protocol.Actor
	Host     string
	PID      int

	CallTimeout, StatusTimeout, DisconnectTimeout time.Duration
	// Trust overrides registry.LockTrust for recovery.
	Trust func(dir string) (bool, string)
}

func (c *Config) defaults() {
	if c.CallTimeout <= 0 {
		c.CallTimeout = DefaultCallTimeout
	}
	if c.StatusTimeout <= 0 {
		c.StatusTimeout = DefaultStatusTimeout
	}
	if c.DisconnectTimeout <= 0 {
		c.DisconnectTimeout = DefaultDisconnectTimeout
	}
	if c.HTTP == nil {
		c.HTTP = &http.Client{}
	}
}

// HardDeadline bounds how long main waits for shutdown after the program has
// ended: draining, renewer stop, status and disconnect, plus a margin.
func (c Config) HardDeadline() time.Duration {
	c.defaults()
	return 2*c.CallTimeout + c.StatusTimeout + c.DisconnectTimeout + 2*time.Second
}

// Renewer is the lifecycle-owned claim renewer (T11). It never calls
// Program.Send itself; it posts UI updates through Lifecycle.Post and uses
// Lifecycle.Do for each renewal.
type Renewer interface {
	// Stop stops renewing and returns once its goroutine has exited, or when
	// ctx ends.
	Stop(ctx context.Context) error
	// Held describes the claims it holds, for exit reports.
	Held() []string
}

// Lifecycle is one instance's session lifecycle. Its methods are safe for
// concurrent use.
type Lifecycle struct {
	cfg   Config
	entry registry.Entry
	s     *litclient.Session
	gate  gate
	fwd   forwarder

	mutating atomic.Int32 // mutations admitted or waiting (Mutate)

	mu       sync.Mutex
	prog     *tea.Program
	renewer  Renewer
	stopping bool
	status   Status
	mapping  litclient.Mapping // the session's mapping once connected, for local reports
	done     chan struct{}
	forced   chan struct{}
	code     int
}

// New records a new instance in the registry of cfg.StateDir and takes its
// liveness lock. It makes no request; Connect registers the client.
func New(cfg Config) (*Lifecycle, error) {
	cfg.defaults()
	l := &Lifecycle{cfg: cfg, entry: registry.New(cfg.StateDir, protocol.UUID()), done: make(chan struct{}), forced: make(chan struct{})}
	l.gate.init()
	l.fwd.init()
	l.s = &litclient.Session{StateDir: cfg.StateDir, HTTP: cfg.HTTP, Context: context.Background()}
	if err := l.s.LockPath(l.entry.SessionPath(), time.Now().Add(time.Second)); err != nil {
		return nil, err
	}
	info := registry.Info{SchemaVersion: registry.SchemaVersion, InstanceID: l.entry.ID, PID: cfg.PID, Host: cfg.Host, StartedAt: protocol.Now(), Endpoint: cfg.Endpoint}
	if err := registry.Publish(l.entry, info); err != nil {
		l.s.Close()
		return nil, protocol.E(500, "local_state_error", "cannot record this lit-tui instance: "+err.Error())
	}
	l.status = Status{Instance: l.entry.ID}
	return l, nil
}

// Connect opens the session at the configured endpoint and registers a fresh
// client, through the gate. It never adopts an existing client.
func (l *Lifecycle) Connect(ctx context.Context) error {
	return l.Do(ctx, func(ctx context.Context, s *litclient.Session) error {
		if err := s.Open(litclient.OpenOptions{Endpoint: l.cfg.Endpoint, EndpointSet: true, AllowUnmapped: true}); err != nil {
			return err
		}
		reg := litclient.Registration{Actor: l.cfg.Actor, Runtime: protocol.Runtime{Vendor: recovery.Vendor, SessionID: l.entry.ID}}
		if err := s.Connect(ctx, reg); err != nil {
			return err
		}
		l.mu.Lock()
		l.status.Client, l.mapping = s.Mapping.ClientID, s.Mapping
		st := l.status
		l.mu.Unlock()
		l.Post(statusKey, StatusMsg(st))
		return nil
	})
}

// ErrShuttingDown refuses requests once shutdown has started.
var ErrShuttingDown = protocol.E(503, "shutting_down", "lit-tui is shutting down; new requests are refused")

// Do runs fn with exclusive use of the session, bounded by the call timeout.
// Once shutdown has started it refuses with ErrShuttingDown; shutdown waits
// for an fn already admitted.
func (l *Lifecycle) Do(ctx context.Context, fn func(ctx context.Context, s *litclient.Session) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, l.cfg.CallTimeout)
	defer cancel()
	return l.gate.do(ctx, func(ctx context.Context) error { return fn(ctx, l.s) })
}

// Entry is this instance's registry entry.
func (l *Lifecycle) Entry() registry.Entry { return l.entry }

// Instance is this instance's ID.
func (l *Lifecycle) Instance() string { return l.entry.ID }

// SetRenewer installs the claim renewer that shutdown stops and joins.
func (l *Lifecycle) SetRenewer(r Renewer) {
	l.mu.Lock()
	l.renewer = r
	l.mu.Unlock()
}

// Attach connects the program the lifecycle delivers updates to and quits.
func (l *Lifecycle) Attach(p *tea.Program) {
	l.mu.Lock()
	l.prog = p
	l.mu.Unlock()
	l.fwd.start(p)
}

// Post delivers msg to the program through the forwarder, coalescing by key:
// only the latest message per key is delivered. It never blocks; a program
// that is suspended (an editor) or not yet running receives it later.
func (l *Lifecycle) Post(key string, msg tea.Msg) { l.fwd.post(key, msg) }

// Status is the current lifecycle status.
func (l *Lifecycle) Status() Status {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.status
}

// Recover scans the registry for orphans and recovers them, each under the
// gate so shutdown waits for it.
func (l *Lifecycle) Recover() (recovery.Report, error) { return recovery.Scan(l.recoveryOptions()) }

// RecoverEntry recovers one orphan the user confirmed.
func (l *Lifecycle) RecoverEntry(e registry.Entry) (recovery.Orphan, bool, error) {
	return recovery.Recover(l.recoveryOptions(), e)
}

func (l *Lifecycle) recoveryOptions() recovery.Options {
	return recovery.Options{StateDir: l.cfg.StateDir, Self: l.entry.ID, HTTP: l.cfg.HTTP, Host: l.cfg.Host, Trust: l.cfg.Trust,
		Do: func(fn func(context.Context) error) error {
			return l.Do(context.Background(), func(ctx context.Context, _ *litclient.Session) error { return fn(ctx) })
		}}
}

// HardDeadline bounds how long main waits for shutdown after the program has
// ended.
func (l *Lifecycle) HardDeadline() time.Duration { return l.cfg.HardDeadline() }

// Mutate runs a mutation through the gate like Do. Mutating reports true from
// the call until it returns, so background refresh pauses meanwhile.
func (l *Lifecycle) Mutate(ctx context.Context, fn func(ctx context.Context, s *litclient.Session) error) error {
	l.mutating.Add(1)
	defer l.mutating.Add(-1)
	return l.Do(ctx, fn)
}

// Mutating reports whether a mutation is waiting for or holding the gate.
func (l *Lifecycle) Mutating() bool { return l.mutating.Load() > 0 }
