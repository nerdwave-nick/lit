package hook

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/nerdwave-nick/lit/internal/clientendpoint"
	"github.com/nerdwave-nick/lit/internal/workflowsession"
)

type (
	association = workflowsession.Association
	adapter     = workflowsession.Adapter
)

func init() {
	register(Event{
		Name:     "session-start",
		HostName: "SessionStart",
		Summary:  "Hand the agent its lit session at session start",
		Description: "Serve the host's SessionStart hook. Read the hook JSON on stdin; create the lit session for a new runtime (startup, clear,\n" +
			"fork) or reconnect the known one (resume), keyed by the host session_id, and print host output whose additionalContext names\n" +
			"the session, endpoint, runtime ID, selected project and current claims. A server that does not answer creates nothing and is\n" +
			"reported in the context. The command exits 0 whatever happens; errors go to the hook log.",
		Budget: 3 * time.Second,
		Handle: sessionStart,
		Failed: sessionStartFailed,
	})
}

// startSources maps the SessionStart sources to their handlers. The compact
// source is deliberately absent: restoring context after compaction is its own
// handler, added to this table by its own change; until then nothing is
// printed for it.
var startSources = map[string]func(*Call) ([]byte, error){
	"startup": startRuntime,
	"clear":   startRuntime,
	"fork":    startRuntime,
	"resume":  startRuntime,
}

func sessionStart(c *Call) ([]byte, error) {
	handler, ok := startSources[c.Input.Source]
	if !ok {
		if c.Input.Source != "compact" {
			c.Log.Warn(fmt.Sprintf("ignored the unsupported SessionStart source %q", c.Input.Source))
		}
		return nil, nil
	}
	return handler(c)
}

// startRuntime sets up this conversation's lit session. A runtime without an
// association gets one: startup, clear and fork are new runtimes, and so is a
// resumed one that was never set up. A resumed runtime reconnects its client.
// Any other source for a known runtime is a duplicate run that only reads the
// association, so a runtime never gets a second client.
func startRuntime(c *Call) ([]byte, error) {
	a, err := c.adapter(c.Input.SessionID)
	if err != nil {
		return nil, err
	}
	known, err := a.Read()
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("reading the association of this runtime: %w", err)
	}
	endpoint := known.Endpoint
	if !exists {
		if endpoint, err = clientendpoint.Resolve("", false, os.Getenv("LIT_ENDPOINT"), ""); err != nil {
			return nil, err
		}
	}
	if err = probe(c.Ctx, endpoint, c.probeTimeout()); err != nil {
		c.Log.Warn(fmt.Sprintf("lit does not answer at %s: %v", endpoint, err))
		return c.say(c.unavailable(exists, known, endpoint)), nil
	}
	assoc, how, err := c.settle(a, known, exists, endpoint)
	switch {
	case errors.Is(err, workflowsession.ErrBusy):
		return c.lockHeld(a), nil
	case err != nil && assoc.Status == "pending":
		c.Log.Error(fmt.Sprintf("setting up the session failed, the pending association %s was kept: %v", assoc.Session, err))
		return c.say(c.pending(assoc)), nil
	case err != nil:
		return nil, err
	case assoc.Status == "pending":
		return c.say(c.pending(assoc)), nil
	}
	return c.say(c.ready(a, assoc, how)), nil
}

// settle brings the runtime's association to its state for this source and
// names how it got there.
func (c *Call) settle(a adapter, known association, exists bool, endpoint string) (association, string, error) {
	switch {
	case !exists:
		return c.create(a, endpoint)
	case c.Input.Source == "resume":
		r, err := c.whileBusy(func() (association, error) { return a.Resume(c.Ctx) })
		return r, "resumed", err
	case known.Status == "pending":
		c.waitIdle(a)
		r, err := a.Read()
		return r, "existing session", err
	}
	return known, "existing session", nil
}

// create registers the runtime. Losing a race to another run of the same hook
// is not an error: the winner's association is used.
func (c *Call) create(a adapter, endpoint string) (association, string, error) {
	r, err := c.whileBusy(func() (association, error) {
		return a.Fresh(c.Ctx, workflowsession.FreshOptions{Endpoint: endpoint})
	})
	if errors.Is(err, workflowsession.ErrAlreadyAssociated) {
		r, err = a.Read()
		return r, "existing session", err
	}
	return r, "new session", err
}

// whileBusy repeats op while another process holds the runtime's lock, for at
// most the busy wait. A lock that outlasts it is returned as ErrBusy.
func (c *Call) whileBusy(op func() (association, error)) (association, error) {
	deadline := time.Now().Add(c.busyWait())
	for {
		r, err := op()
		if !errors.Is(err, workflowsession.ErrBusy) || time.Now().After(deadline) || c.Ctx.Err() != nil {
			return r, err
		}
		c.sleep(50 * time.Millisecond)
	}
}

// waitIdle waits, for at most the busy wait, until no adapter call holds the
// runtime's lock.
func (c *Call) waitIdle(a adapter) {
	deadline := time.Now().Add(c.busyWait())
	for a.Busy() && time.Now().Before(deadline) && c.Ctx.Err() == nil {
		c.sleep(25 * time.Millisecond)
	}
}

func (c *Call) sleep(d time.Duration) {
	select {
	case <-time.After(d):
	case <-c.Ctx.Done():
	}
}
