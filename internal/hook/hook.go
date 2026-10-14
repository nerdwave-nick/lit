// Package hook implements lit hook, the command agent hosts call at session
// events (see "1. lit hook" of the workflow hooks specification).
//
// A hook never breaks the host's session: once an event is being served, Run
// returns 0 whatever happens. Failures go to the hook log (see Log and
// RecentFailures) and, for events that inject context, become a one-line note.
// Events register themselves with register, so a new event is a new file.
package hook

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/nerdwave-nick/lit/internal/workflowsession"
)

// Options tune a hook run; the zero value is the production behavior. Tests
// use the fields to shorten the waits.
type Options struct {
	// Binary is the lit executable for tracker calls; empty means this one.
	Binary string
	// Budget replaces the event's total time budget.
	Budget time.Duration
	// ProbeTimeout replaces the reachability probe's time limit.
	ProbeTimeout time.Duration
	// BusyWait replaces how long a held runtime lock is waited for.
	BusyWait time.Duration
	// Original is the status line command that statusline puts lit's part
	// after (--original).
	Original string
}

const (
	defaultProbeTimeout = time.Second
	defaultBusyWait     = 1500 * time.Millisecond
	maxInput            = 4 << 20
)

// Event is one hook event lit serves as `lit hook --host HOST NAME`.
type Event struct {
	Name        string // the command's spelling, for example "session-start"
	HostName    string // the host's hook_event_name, for example "SessionStart"; empty when the host sends none
	Summary     string // one line for help
	Description string // help text
	Budget      time.Duration
	// Handle serves the event and returns the host output to print, if any.
	// A returned error is an internal failure: it is logged, and Failed
	// decides what is printed instead.
	Handle func(*Call) ([]byte, error)
	// Failed returns the host output after an internal failure; nil prints
	// nothing.
	Failed func(*Call, error) []byte
}

var events = map[string]Event{}

func register(e Event) { events[e.Name] = e }

// Events lists the supported events by name.
func Events() []Event {
	list := make([]Event, 0, len(events))
	for _, e := range events {
		list = append(list, e)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list
}

// Call is one served hook event.
type Call struct {
	// Ctx ends with the event's budget.
	Ctx   context.Context
	Host  Host
	Event Event
	Input Input
	Opt   Options
	Log   *Log
}

// Run serves one hook event: host is claude or codex, event one of Events.
// The host's hook JSON is read from in and its output written to out; errOut
// receives usage errors and, when the hook log cannot be written, failures.
// The result is the exit status: 2 for an unknown host or event, otherwise 0.
func Run(host, event string, in io.Reader, out, errOut io.Writer, opt Options) int {
	h, ok := lookupHost(host)
	e, known := events[event]
	if !ok || !known {
		fmt.Fprintf(errOut, "lit hook: unsupported host %q or event %q\n", host, event)
		return 2
	}
	budget := e.Budget
	if opt.Budget > 0 {
		budget = opt.Budget
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	c := &Call{Ctx: ctx, Host: h, Event: e, Opt: opt, Log: &Log{Host: h.Name, Event: e.Name, fallback: errOut, now: time.Now}}
	if reply := c.serve(in); len(reply) > 0 {
		_, _ = out.Write(reply)
	}
	return 0
}

func (c *Call) serve(in io.Reader) (reply []byte) {
	defer func() {
		if r := recover(); r != nil {
			reply = c.failed(fmt.Errorf("panic in %s: %v", c.Event.Name, r))
		}
	}()
	raw, err := readInput(c.Ctx, in)
	if err == nil {
		c.Input, err = c.Host.Decode(c.Event, raw)
	}
	c.Log.Runtime = c.Input.SessionID
	if err != nil {
		return c.failed(err)
	}
	if reply, err = c.Event.Handle(c); err != nil {
		return c.failed(err)
	}
	return reply
}

func (c *Call) failed(err error) []byte {
	c.Log.Error(err.Error())
	if c.Event.Failed == nil {
		return nil
	}
	return c.Event.Failed(c, err)
}

// readInput reads the hook JSON from a host that may never close the pipe.
func readInput(ctx context.Context, in io.Reader) ([]byte, error) {
	type result struct {
		raw []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		raw, err := io.ReadAll(io.LimitReader(in, maxInput+1))
		done <- result{raw, err}
	}()
	select {
	case r := <-done:
		if r.err == nil && len(r.raw) > maxInput {
			return nil, fmt.Errorf("hook input exceeds %d bytes", maxInput)
		}
		return r.raw, r.err
	case <-ctx.Done():
		return nil, fmt.Errorf("reading the hook input: %w", ctx.Err())
	}
}

// adapter returns the workflow-session adapter for a runtime of this host.
// lit runs in a directory of its own so that its output cache stays out of
// the agent's project.
func (c *Call) adapter(runtime string) (workflowsession.Adapter, error) {
	dir, err := hooksDir()
	if err != nil {
		return workflowsession.Adapter{}, err
	}
	work := filepath.Join(dir, "work")
	if err = os.MkdirAll(work, 0o700); err != nil {
		return workflowsession.Adapter{}, err
	}
	return workflowsession.Adapter{Host: c.Host.Name, RuntimeID: runtime, Binary: c.Opt.Binary, WorkDir: work}, nil
}

func (c *Call) probeTimeout() time.Duration {
	if c.Opt.ProbeTimeout > 0 {
		return c.Opt.ProbeTimeout
	}
	return defaultProbeTimeout
}

func (c *Call) busyWait() time.Duration {
	if c.Opt.BusyWait > 0 {
		return c.Opt.BusyWait
	}
	return defaultBusyWait
}
