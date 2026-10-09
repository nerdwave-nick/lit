// Package claims holds the work claims a lit-tui instance takes and keeps
// them alive with a renewer the lifecycle owns.
//
// The renewer is independent of the Bubble Tea event loop: it runs on its own
// goroutine, holds the lifecycle's mutation gate only for each call, and
// never calls Program.Send. Every change is posted through the lifecycle's
// coalescing forwarder (Lifecycle.Post), which may block for a whole editor
// session without delaying renewal.
//
// A hold is Holding (renewed automatically), Paused (no input and no draft
// change for the idle period: renewal stopped and the user is asked whether
// to keep holding) or Lost (a renewal failed, or the lease passed). A lost
// hold is never retried or reacquired automatically; Claim reacquires it
// explicitly and rereads the issue first.
package claims

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/lifecycle"
)

// Defaults (Proposals). A claim is taken for Lease and renewed when two
// thirds of it have passed; renewal pauses after Idle without activity.
const (
	DefaultLease = 15 * time.Minute
	DefaultIdle  = 30 * time.Minute
)

// Key is the forwarder key of Msg: only the latest state is delivered.
const Key = "claims"

// Lifecycle is what the holder needs from the instance's lifecycle
// (lifecycle.Lifecycle): the gate, the forwarder and the renewer hook that
// shutdown stops and joins before it disconnects.
type Lifecycle interface {
	Do(ctx context.Context, fn func(ctx context.Context, s *litclient.Session) error) error
	Mutate(ctx context.Context, fn func(ctx context.Context, s *litclient.Session) error) error
	Post(key string, msg tea.Msg)
	SetRenewer(r lifecycle.Renewer)
}

// Config sets the lease, the renewal cadence and the idle period.
type Config struct {
	// Lease is how long each claim or renewal lasts (DefaultLease).
	Lease time.Duration
	// Renew is how long after a claim or renewal the next renewal runs
	// (two thirds of Lease).
	Renew time.Duration
	// Idle is how long without activity (Touch) renewal goes on before it
	// pauses and the user is asked (DefaultIdle).
	Idle time.Duration
}

func (c *Config) defaults() {
	if c.Lease <= 0 {
		c.Lease = DefaultLease
	}
	if c.Renew <= 0 || c.Renew >= c.Lease {
		c.Renew = c.Lease * 2 / 3
	}
	if c.Idle <= 0 {
		c.Idle = DefaultIdle
	}
}

// State is where a hold is.
type State string

const (
	// Holding: renewed automatically.
	Holding State = "holding"
	// Paused: renewal stopped after the idle period; it lapses at the end of
	// its lease unless the user keeps holding it.
	Paused State = "paused"
	// Lost: a renewal failed or the lease passed. Never retried.
	Lost State = "lost"
)

// Hold is one claim this instance took.
type Hold struct {
	IssueID string
	Title   string
	State   State
	// Reason says why the hold is paused or lost.
	Reason string
	// Revision is the issue revision read when the claim was (re)acquired.
	Revision int64
	// Expires is a conservative local estimate of the lease end: the local
	// time before the last successful claim or renewal, plus the lease.
	Expires  time.Time
	Renewals int
	// Asked reports that the hold paused for idleness and the idle question
	// about it is unanswered; it stays asked when its lease lapses meanwhile.
	Asked bool
}

// Msg is the holder's state, posted through the forwarder on every change.
type Msg struct {
	Holds []Hold
	// Asking reports that renewal paused for idleness and the user has not
	// answered whether to keep holding (the holds marked Asked).
	Asking bool
	Idle   time.Duration
	// Seq orders states: a state with a lower Seq is older.
	Seq uint64
}

// Asked lists the holds the idle question is about.
func (m Msg) Asked() []Hold {
	var out []Hold
	for _, h := range m.Holds {
		if h.Asked {
			out = append(out, h)
		}
	}
	return out
}

// Lost lists the lost holds.
func (m Msg) Lost() []Hold { return m.in(Lost) }

func (m Msg) in(s State) []Hold {
	var out []Hold
	for _, h := range m.Holds {
		if h.State == s {
			out = append(out, h)
		}
	}
	return out
}

// Hold returns the hold on issue id.
func (m Msg) Hold(id string) (Hold, bool) {
	for _, h := range m.Holds {
		if h.IssueID == id {
			return h, true
		}
	}
	return Hold{}, false
}

// Result is the outcome of an explicit action.
type Result struct {
	IssueID, Title string
	// Reacquired reports that Claim took back a lost or paused hold;
	// Already that the issue was held and renewing already.
	Reacquired, Already bool
	// Forgot reports that Release dropped a lost hold whose claim was no
	// longer this client's.
	Forgot bool
	Err    error
	// Owner is another client's live claim that refused the action.
	Owner *protocol.Claim
	// Revision is the issue revision Claim reread; Previous is the one the
	// lost hold had recorded (0 when there was no hold).
	Revision, Previous int64
	// Kept and Lost are KeepHolding's holds that are renewed again and those
	// that could not be kept.
	Kept, Lost []Hold
}
