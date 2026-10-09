package lifecycle

import (
	"context"
	"fmt"
	"syscall"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/recovery"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/registry"
)

// Phase is where the lifecycle is.
type Phase int

const (
	Running Phase = iota
	Stopping
	Stopped
)

// Outcome is how shutdown ended.
type Outcome string

const (
	// Clean: the disconnect was confirmed (or nothing was registered) and the
	// registry entry was removed.
	Clean Outcome = "clean"
	// Uncertain: a retained request (possibly the disconnect) is uncertain.
	Uncertain Outcome = "uncertain"
	// Unknown: the result could not be verified.
	Unknown Outcome = "unknown"
)

// Result is the outcome of shutdown.
type Result struct {
	Outcome  Outcome
	Reason   string
	Requests []recovery.Request
	// Claims are held claims that were not released: they stay until their
	// leases expire.
	Claims []string
	// Kept reports that this instance's registry entry stays for recovery.
	Kept bool
}

// Status is the lifecycle state shown by the UI.
type Status struct {
	Instance string
	Client   string
	Phase    Phase
	Note     string
	Result   Result
}

// StatusMsg delivers a Status to the program.
type StatusMsg Status

const statusKey = "lifecycle"

// Shutdown starts the orderly shutdown unless it has already started, and
// reports whether this call started it. It never blocks.
func (l *Lifecycle) Shutdown(reason string) bool {
	l.mu.Lock()
	if l.stopping {
		l.mu.Unlock()
		return false
	}
	l.stopping = true
	l.mu.Unlock()
	l.gate.close() // refuse new requests before Shutdown returns
	go l.run(reason)
	return true
}

// Interrupt is a Ctrl-C or signal: the first starts shutdown, a second forces
// an immediate exit with code.
func (l *Lifecycle) Interrupt(code int) {
	if !l.Shutdown("interrupt") {
		l.Force(code)
	}
}

// Force asks main to exit at once with code, reporting what is outstanding.
// Only the first call counts.
func (l *Lifecycle) Force(code int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	select {
	case <-l.forced:
	default:
		l.code = code
		close(l.forced)
	}
}

// Done is closed when shutdown has finished.
func (l *Lifecycle) Done() <-chan struct{} { return l.done }

// Forced is closed when an immediate exit was forced.
func (l *Lifecycle) Forced() <-chan struct{} { return l.forced }

// ForceCode is the exit status of a forced exit.
func (l *Lifecycle) ForceCode() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.code
}

// Started reports whether shutdown has started.
func (l *Lifecycle) Started() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stopping
}

// Result is shutdown's outcome once Done is closed.
func (l *Lifecycle) Result() Result {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.status.Result
}

// Outstanding describes, from local state only, what a forced exit leaves:
// this instance's retained requests and the renewer's claims. The registry
// entry stays, so the next launch checks it again.
func (l *Lifecycle) Outstanding() Result {
	return Result{Outcome: Unknown, Reason: "exit was forced before shutdown finished", Requests: l.retained(), Claims: l.held(), Kept: true}
}

func (l *Lifecycle) run(reason string) {
	claims := l.held()
	note := "Disconnecting…"
	if len(claims) > 0 {
		note = fmt.Sprintf("Releasing %d claim(s), disconnecting…", len(claims))
	}
	l.update(func(st *Status) { st.Phase, st.Note = Stopping, note })
	var res Result
	if !l.gate.drain(l.cfg.CallTimeout + time.Second) {
		// Never cancel or replay it: its outcome decides what is safe.
		res = Result{Outcome: Unknown, Reason: "a request was still running at the drain deadline; nothing was disconnected", Requests: l.retained()}
	} else {
		l.stopRenewer()
		res = l.release()
	}
	if res.Outcome == Clean {
		if err := registry.Remove(l.entry, l.s.Close); err != nil {
			res.Kept, res.Reason = true, "the registry entry could not be removed: "+err.Error()
		}
	} else {
		res.Kept, res.Claims = true, claims
	}
	l.update(func(st *Status) { st.Phase, st.Note, st.Result = Stopped, "", res })
	close(l.done)
	l.mu.Lock()
	p := l.prog
	l.mu.Unlock()
	// A result that is not clean waits for a second keypress, except after
	// SIGHUP: the terminal is gone and no keypress can come.
	if p != nil && (res.Outcome == Clean || reason == syscall.SIGHUP.String()) {
		go p.Quit() // waits while an editor holds the terminal
	}
}

// release inspects this instance's retained requests read-only and, when none
// is uncertain, runs one bounded disconnect. The caller holds the gate.
func (l *Lifecycle) release() Result {
	s := l.s
	ctx, cancel := context.WithTimeout(context.Background(), l.cfg.StatusTimeout)
	requests, err := recovery.Inspect(ctx, s, l.entry.ID)
	cancel()
	switch {
	case err != nil:
		return Result{Outcome: Unknown, Reason: "retained requests could not be checked, so nothing was disconnected: " + recovery.Describe(err)}
	case len(requests) > 0:
		// Disconnecting could make their outcomes permanently unprovable.
		return Result{Outcome: Uncertain, Reason: "retained requests are uncertain, so nothing was disconnected", Requests: requests}
	case s.Mapping.ClientID == "" || s.Client.Status == "disconnected":
		return Result{Outcome: Clean}
	}
	ctx, cancel = context.WithTimeout(context.Background(), l.cfg.DisconnectTimeout)
	defer cancel()
	if _, err := s.Disconnect(ctx); err != nil {
		if r, ok := recovery.UncertainRequest(l.cfg.StateDir, err); ok {
			return Result{Outcome: Uncertain, Reason: "the disconnect was not confirmed", Requests: []recovery.Request{r}}
		}
		return Result{Outcome: Unknown, Reason: "the disconnect was not confirmed: " + recovery.Describe(err)}
	}
	return Result{Outcome: Clean}
}

func (l *Lifecycle) stopRenewer() {
	l.mu.Lock()
	r := l.renewer
	l.mu.Unlock()
	if r != nil {
		ctx, cancel := context.WithTimeout(context.Background(), l.cfg.CallTimeout)
		defer cancel()
		_ = r.Stop(ctx)
	}
}

func (l *Lifecycle) held() []string {
	l.mu.Lock()
	r := l.renewer
	l.mu.Unlock()
	if r == nil {
		return nil
	}
	return r.Held()
}

// retained lists this instance's retained requests from local state only.
func (l *Lifecycle) retained() []recovery.Request {
	l.mu.Lock()
	m := l.mapping // never l.s: the session belongs to whoever holds the gate
	l.mu.Unlock()
	// Best effort: an unreadable pending directory leaves the list empty, and
	// the kept registry entry still sends the next launch to check it.
	found, _ := recovery.Find(l.cfg.StateDir, m.ServiceID, m.ClientID, l.entry.ID)
	out := make([]recovery.Request, 0, len(found))
	for _, ev := range found {
		out = append(out, recovery.Request{ID: ev.RequestID, Status: "unknown", Path: ev.Path})
	}
	return out
}

func (l *Lifecycle) update(change func(*Status)) {
	l.mu.Lock()
	change(&l.status)
	st := l.status
	l.mu.Unlock()
	l.Post(statusKey, StatusMsg(st))
}
