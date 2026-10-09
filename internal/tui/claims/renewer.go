package claims

import (
	"context"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/recovery"
)

// idleWait is how long the renewer sleeps when nothing is due; any change
// wakes it earlier.
const idleWait = time.Hour

// run is the renewer goroutine. It never touches the program: each renewal
// holds the gate for its one call, and every change is posted through the
// forwarder.
func (h *Holder) run() {
	defer close(h.done)
	timer := time.NewTimer(idleWait)
	defer timer.Stop()
	for {
		due, wait := h.tick(time.Now())
		for _, e := range due {
			select {
			case <-h.stop:
				return
			default:
			}
			h.renew(context.Background(), e, false)
		}
		if len(due) > 0 {
			continue // recompute after the calls
		}
		timer.Reset(wait)
		select {
		case <-h.stop:
			return
		case <-h.kick:
		case <-timer.C:
		}
	}
}

// tick applies what is due at now: the idle pause, then lapsed leases. It
// returns the holds due for renewal and how long to sleep otherwise.
func (h *Holder) tick(now time.Time) ([]*entry, time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	changed := false
	holding := false
	for _, e := range h.holds {
		holding = holding || e.State == Holding
	}
	if holding && !h.asking && now.Sub(h.active) >= h.cfg.Idle {
		for _, e := range h.holds {
			if e.State == Holding {
				e.State, e.Reason, e.Asked = Paused, "no activity for "+h.cfg.Idle.String(), true
			}
		}
		h.asking, changed = true, true
	}
	var due []*entry
	next := now.Add(idleWait)
	for _, id := range h.order {
		e := h.holds[id]
		switch {
		case e.State == Lost:
			continue
		case !now.Before(e.Expires):
			e.State, e.Reason = Lost, lapsed(e.State)
			changed = true
			continue
		case e.State == Holding && !now.Before(e.renewAt):
			due = append(due, e)
		case e.State == Holding:
			next = earliest(next, e.renewAt)
		}
		next = earliest(next, e.Expires)
	}
	if holding && !h.asking {
		next = earliest(next, h.active.Add(h.cfg.Idle))
	}
	if changed {
		h.settle()
		h.publish()
	}
	return due, max(next.Sub(now), time.Millisecond)
}

func lapsed(s State) string {
	if s == Paused {
		return "the lease ended without confirmation to keep holding"
	}
	return "the lease passed before it could be renewed"
}

func earliest(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

// renew renews e's claim in one call through the gate. On success it
// extends the local lease estimate and, with resume, holds it again; any
// failure loses it for good. A refusal because shutdown has started changes
// nothing: the disconnect releases the claim.
func (h *Holder) renew(ctx context.Context, e *entry, resume bool) {
	start := time.Now()
	err := h.l.Mutate(ctx, func(ctx context.Context, s *litclient.Session) error {
		res, err := s.RenewClaims(ctx, litclient.ClaimRequest{ClaimSelection: litclient.ClaimSelection{Issues: []string{e.IssueID}}, For: h.cfg.Lease})
		if err == nil && (len(res.Items) != 1 || res.Items[0].Claim == nil || res.Items[0].Claim.OwnerClientID != s.Mapping.ClientID) {
			err = protocol.E(500, "invalid_response", "the renewal result has no claim of this client")
		}
		return err
	})
	if litclient.Code(err) == "shutting_down" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.holds[e.IssueID] != e || e.State == Lost {
		return // released, replaced or lost meanwhile
	}
	if err != nil {
		e.State, e.Reason = Lost, "renewal failed: "+recovery.Describe(err)
	} else {
		e.Expires, e.renewAt = start.Add(h.cfg.Lease), start.Add(h.cfg.Renew)
		e.Renewals++
		if resume {
			e.State, e.Reason = Holding, ""
		}
	}
	h.settle()
	h.publish()
}

// lose marks e lost for reason.
func (h *Holder) lose(e *entry, reason string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.holds[e.IssueID] == e && e.State != Lost {
		e.State, e.Reason = Lost, reason
		h.settle()
		h.publish()
	}
}

// KeepHolding answers the idle question: each asked hold whose lease has
// not passed is renewed at once and held again. A hold whose lease passed,
// or whose renewal fails, is lost: keeping it never reports success.
func (h *Holder) KeepHolding(ctx context.Context) Result {
	h.Touch()
	h.mu.Lock()
	var asked []*entry
	for _, id := range h.order {
		if e := h.holds[id]; e != nil && e.Asked {
			e.Asked = false
			asked = append(asked, e)
		}
	}
	h.settle()
	h.publish()
	h.mu.Unlock()
	var r Result
	for _, e := range asked {
		h.mu.Lock()
		paused, expired := e.State == Paused, !time.Now().Before(e.Expires)
		h.mu.Unlock()
		switch {
		case paused && expired:
			h.lose(e, "the lease ended before holding was confirmed")
		case paused:
			h.renew(ctx, e, true)
		}
		h.mu.Lock()
		got := e.Hold
		h.mu.Unlock()
		if got.State == Holding {
			r.Kept = append(r.Kept, got)
		} else {
			r.Lost = append(r.Lost, got)
		}
	}
	h.poke()
	return r
}

// LetLapse answers the idle question with no: the paused holds lapse at the
// end of their leases and are then shown as lost.
func (h *Holder) LetLapse() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, e := range h.holds {
		e.Asked = false
	}
	h.settle()
	h.publish()
}
