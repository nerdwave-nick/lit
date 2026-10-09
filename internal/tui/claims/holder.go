package claims

import (
	"context"
	"sync"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/recovery"
)

// entry is a hold and when it is next renewed. Renewals compare entries by
// pointer, so a hold released or replaced meanwhile is left alone.
type entry struct {
	Hold
	renewAt time.Time
}

// Holder holds this instance's claims. Its methods are safe for concurrent
// use; explicit actions block on the gate and belong in a tea.Cmd.
type Holder struct {
	l   Lifecycle
	cfg Config

	mu     sync.Mutex
	holds  map[string]*entry
	order  []string // issue IDs in the order they were first held
	asking bool
	active time.Time // the last activity

	kick       chan struct{} // wakes the renewer to recompute its timer
	stop, done chan struct{}
	stopOnce   sync.Once
}

// Start creates the holder, installs it as l's renewer, so shutdown stops
// and joins it before disconnecting, and starts renewing.
func Start(l Lifecycle, cfg Config) *Holder {
	cfg.defaults()
	h := &Holder{l: l, cfg: cfg, holds: map[string]*entry{}, active: time.Now(),
		kick: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{})}
	l.SetRenewer(h)
	go h.run()
	return h
}

// Config is the effective configuration.
func (h *Holder) Config() Config { return h.cfg }

// Snapshot is the current state, as Msg delivers it.
func (h *Holder) Snapshot() Msg {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.msg()
}

func (h *Holder) msg() Msg {
	m := Msg{Asking: h.asking, Idle: h.cfg.Idle}
	for _, id := range h.order {
		if e, ok := h.holds[id]; ok {
			m.Holds = append(m.Holds, e.Hold)
		}
	}
	return m
}

// publish posts the state; the caller holds h.mu, so posts keep their order.
// Post never blocks.
func (h *Holder) publish() { h.l.Post(Key, h.msg()) }

func (h *Holder) poke() {
	select {
	case h.kick <- struct{}{}:
	default: // a wake-up is pending
	}
}

// Touch records activity: TUI input, or a draft change (the draft watcher
// calls it). It postpones the idle pause; it never resumes paused holds.
func (h *Holder) Touch() {
	h.mu.Lock()
	h.active = time.Now()
	h.mu.Unlock()
	h.poke()
}

// Claim claims issue id, or reacquires a lost or paused hold on it. It
// rereads the issue and its claim first; another client's live claim is
// refused and named, never forced. A live claim of this client's own (a
// paused hold, or a renewal whose outcome was unclear) is renewed instead.
func (h *Holder) Claim(ctx context.Context, id, title string) Result {
	h.Touch()
	r := Result{IssueID: id, Title: title}
	h.mu.Lock()
	prev, had := h.holds[id]
	if had {
		r.Previous, r.Reacquired = prev.Revision, prev.State != Holding
		r.Already = prev.State == Holding
	}
	h.mu.Unlock()
	if r.Already {
		return r
	}
	var issue protocol.Issue
	var current *protocol.Claim
	var self string
	r.Err = h.l.Do(ctx, func(ctx context.Context, s *litclient.Session) error {
		got, err := litclient.Get[protocol.Issue](ctx, s, "", id)
		if err != nil {
			return err
		}
		if len(got) != 1 {
			return protocol.E(500, "invalid_response", "the issue read returned no single issue")
		}
		issue, self = got[0], s.Mapping.ClientID
		current, err = h.liveClaim(ctx, s, id)
		return err
	})
	if r.Err != nil {
		return r
	}
	r.Title, r.Revision = issue.Title, issue.Revision
	if current != nil && current.OwnerClientID != self {
		r.Err, r.Owner = protocol.E(409, "claim_conflict", "another client holds the claim"), current
		return r
	}
	start := time.Now()
	r.Err = h.l.Mutate(ctx, func(ctx context.Context, s *litclient.Session) error {
		req := litclient.ClaimRequest{ClaimSelection: litclient.ClaimSelection{Issues: []string{id}}, For: h.cfg.Lease}
		var res protocol.ClaimResult
		var err error
		if current == nil {
			res, err = s.AcquireClaims(ctx, req)
		} else {
			res, err = s.RenewClaims(ctx, req)
		}
		if err == nil && (len(res.Items) != 1 || res.Items[0].Claim == nil) {
			err = protocol.E(500, "invalid_response", "the claim result has no claim")
		}
		return err
	})
	switch code := litclient.Code(r.Err); {
	case code == "claim_conflict":
		_ = h.l.Do(ctx, func(ctx context.Context, s *litclient.Session) error {
			c, err := h.liveClaim(ctx, s, id)
			if c != nil && c.OwnerClientID != s.Mapping.ClientID {
				r.Owner = c
			}
			return err
		})
		return r
	case code == "outcome_uncertain":
		// The claim may or may not exist: show it as lost, so it is never
		// renewed blindly; reacquiring rereads it.
		h.set(id, &entry{Hold: Hold{IssueID: id, Title: issue.Title, State: Lost, Revision: issue.Revision,
			Reason: "the claim request's outcome is uncertain: " + recovery.Describe(r.Err)}})
		return r
	case r.Err != nil:
		return r
	}
	h.set(id, &entry{Hold: Hold{IssueID: id, Title: issue.Title, State: Holding, Revision: issue.Revision, Expires: start.Add(h.cfg.Lease)},
		renewAt: start.Add(h.cfg.Renew)})
	return r
}

// liveClaim reads issue id's live claim, or nil.
func (h *Holder) liveClaim(ctx context.Context, s *litclient.Session, id string) (*protocol.Claim, error) {
	snap, err := s.SnapshotClaims(ctx, litclient.ClaimSelection{Issues: []string{id}})
	if err != nil {
		return nil, err
	}
	if len(snap.Items) != 1 {
		return nil, protocol.E(500, "invalid_response", "the claim snapshot returned no single issue")
	}
	return snap.Items[0].Claim, nil
}

// set replaces the hold on id, keeping its place, and publishes.
func (h *Holder) set(id string, e *entry) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.holds[id]; !ok {
		h.order = append(h.order, id)
	}
	h.holds[id] = e
	h.settle()
	h.publish()
	h.poke()
}

// forget drops the hold on id and publishes.
func (h *Holder) forget(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.holds, id)
	for i, x := range h.order {
		if x == id {
			h.order = append(h.order[:i:i], h.order[i+1:]...)
			break
		}
	}
	h.settle()
	h.publish()
}

// settle ends the idle question once no hold awaits its answer. The caller
// holds h.mu.
func (h *Holder) settle() {
	h.asking = false
	for _, e := range h.holds {
		h.asking = h.asking || e.Asked
	}
}

// Release releases the claim on issue id and forgets the hold. A lost hold
// is forgotten even when the release is refused: the claim is no longer
// this client's.
func (h *Holder) Release(ctx context.Context, id string) Result {
	h.Touch()
	h.mu.Lock()
	prev, had := h.holds[id]
	h.mu.Unlock()
	r := Result{IssueID: id}
	if had {
		r.Title = prev.Title
	}
	r.Err = h.l.Mutate(ctx, func(ctx context.Context, s *litclient.Session) error {
		_, err := s.ReleaseClaims(ctx, litclient.ClaimSelection{Issues: []string{id}})
		return err
	})
	switch code := litclient.Code(r.Err); {
	case r.Err == nil:
		h.forget(id)
	case had && prev.State == Lost && (code == "claim_conflict" || code == "claim_expired"):
		r.Err, r.Forgot = nil, true
		h.forget(id)
	}
	return r
}

// Held names the claims the instance still holds (holding or paused), for
// shutdown's status line and exit reports.
func (h *Holder) Held() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, id := range h.order {
		if e := h.holds[id]; e != nil && e.State != Lost {
			out = append(out, "“"+e.Title+"” ("+short(id)+")")
		}
	}
	return out
}

// Stop stops renewing and returns once the renewer goroutine has exited, or
// when ctx ends. Shutdown calls it after draining the gate and before the
// disconnect, which releases every claim.
func (h *Holder) Stop(ctx context.Context) error {
	h.stopOnce.Do(func() { close(h.stop) })
	select {
	case <-h.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
