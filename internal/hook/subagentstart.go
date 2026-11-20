package hook

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/nerdwave-nick/lit/internal/workflowsession"
)

func init() {
	register(Event{
		Name:     "subagent-start",
		HostName: "SubagentStart",
		Summary:  "Give a subagent its own lit session",
		Description: "Serve the host's SubagentStart hook. Read the hook JSON on stdin and register the subagent, keyed by the input's agent_id, as a child\n" +
			"of the conversation's lit session (the input's session_id): its own client, endpoint and selected project taken explicitly from the\n" +
			"parent, and no claims. Print host output whose additionalContext names the subagent's session, endpoint, runtime ID and project.\n" +
			"Running again for the same agent_id (a resumed subagent) registers nothing and prints the same text. A conversation without a\n" +
			"lit session gets no subagent session. The command exits 0 whatever happens; errors go to the hook log.",
		Budget: time.Second,
		Handle: subagentStart,
	})
}

// subagentProbeTimeout leaves most of the one-second budget to connecting.
const subagentProbeTimeout = 400 * time.Millisecond

// subagentStart gives a subagent a lit session of its own, a child of the
// conversation's. Ownership is never inherited: the child starts without
// claims, and only the parent's endpoint and project are handed over, as
// explicit options of the child's registration.
//
// The text it prints depends only on the child's association. Hosts run the
// hook again for the same agent_id and inject the output only when the
// subagent's context does not already hold it, so a repeat must not differ.
func subagentStart(c *Call) ([]byte, error) {
	agent := agentID(c.Input.Raw)
	parent := c.Input.SessionID
	if agent == "" {
		return nil, errors.New("the hook input has no agent_id")
	}
	c = c.asRuntime(agent)
	c.Log.Runtime = agent
	if agent == parent {
		return nil, fmt.Errorf("the hook input names the same ID %s as session_id and agent_id", agent)
	}
	a, err := c.adapter(agent)
	if err != nil {
		return nil, err
	}
	known, err := a.Read()
	switch {
	case err == nil:
		return c.knownSubagent(a, known)
	case !errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("reading the association of subagent %s: %w", agent, err)
	}
	return c.newSubagent(a, agent, parent)
}

// asRuntime returns the call as served for another runtime of the same host,
// so that the shared context texts name that runtime.
func (c *Call) asRuntime(id string) *Call {
	other := *c
	other.Input.SessionID = id
	return &other
}

// knownSubagent answers from the association alone, so a repeat needs neither
// the server nor the parent. A pending association whose creator is still
// connecting is waited for.
func (c *Call) knownSubagent(a adapter, known association) ([]byte, error) {
	if known.Status == "pending" {
		c.waitIdle(a)
		var err error
		if known, err = a.Read(); err != nil {
			return nil, err
		}
	}
	if known.Status == "pending" {
		return c.say(c.pending(known)), nil
	}
	return c.say(c.subagentReady(known)), nil
}

// newSubagent registers the subagent as a child of the parent's ready session.
func (c *Call) newSubagent(a adapter, agent, parent string) ([]byte, error) {
	pa, err := c.adapter(parent)
	if err != nil {
		return nil, err
	}
	p, err := pa.Read()
	switch {
	case errors.Is(err, fs.ErrNotExist):
		c.Log.Warn(fmt.Sprintf("the conversation %s has no lit session, so subagent %s gets none", parent, agent))
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("reading the association of the conversation %s: %w", parent, err)
	case p.Status != "ready":
		c.Log.Warn(fmt.Sprintf("the lit session of the conversation %s is pending, so subagent %s gets none", parent, agent))
		return nil, nil
	}
	opt := workflowsession.FreshOptions{Endpoint: p.Endpoint}
	if p.Project != nil {
		opt.Project = *p.Project
	}
	timeout := subagentProbeTimeout
	if c.Opt.ProbeTimeout > 0 {
		timeout = c.Opt.ProbeTimeout
	}
	service, err := probeService(c.Ctx, p.Endpoint, timeout)
	if err != nil {
		c.Log.Warn(fmt.Sprintf("lit does not answer at %s: %v", p.Endpoint, err))
		return c.say(c.subagentUnavailable(parent, opt)), nil
	}
	if service != *p.Service {
		return nil, fmt.Errorf("the service at %s is not the one the conversation's lit session belongs to (%s, now %s); no session was created for subagent %s",
			p.Endpoint, *p.Service, service, agent)
	}
	created, err := c.whileBusy(func() (association, error) { return a.Subagent(c.Ctx, parent, opt) })
	switch {
	case errors.Is(err, workflowsession.ErrAlreadyAssociated):
		// Another run of this hook registered the subagent first; use its.
		known, err := a.Read()
		if err != nil {
			return nil, err
		}
		return c.knownSubagent(a, known)
	case err != nil && created.Status == "pending":
		c.Log.Error(fmt.Sprintf("registering subagent %s failed, the pending association %s was kept: %v", created.Runtime, created.Session, err))
		return c.say(c.pending(created)), nil
	case err != nil:
		return nil, fmt.Errorf("registering subagent %s: %w", agent, err)
	}
	return c.say(c.subagentReady(created)), nil
}

// subagentReady is the context of a registered subagent. It starts without
// claims whatever the parent holds.
func (c *Call) subagentReady(r association) string {
	project := "none selected (choose one with: " + c.workflow("run", "--", "session set --project REF") + ")"
	if r.Project != nil {
		project = *r.Project
	}
	return strings.Join([]string{
		"lit session for this subagent: its own, separate from the session of the agent that started it.",
		"session: " + r.Session,
		"endpoint: " + r.Endpoint,
		"runtime ID: " + r.Runtime,
		"project: " + project,
		"claims: none (a subagent session starts without claims; acquire your own before sustained work)",
		"Run lit tracker commands as this session: " + c.workflow("run", "--", "<lit command>"),
		"Never use another agent's session or runtime ID.",
	}, "\n")
}

// subagentUnavailable is the note for a subagent whose session could not be
// created because the server does not answer: it also keeps the subagent from
// borrowing its parent's session.
func (c *Call) subagentUnavailable(parent string, opt workflowsession.FreshOptions) string {
	args := []string{"--parent-runtime-id", quote(parent), "--endpoint", opt.Endpoint}
	if opt.Project != "" {
		args = append(args, "--project", opt.Project)
	}
	return fmt.Sprintf("lit is unavailable: nothing answered at %s, so no lit session was created for this subagent. "+
		"Never use another agent's session or runtime ID. When the server is running, set this subagent's own session up with: %s",
		opt.Endpoint, c.workflow("subagent", args...))
}

// agentID returns the hook input's agent_id, or "" if it is absent or not a
// string.
func agentID(fields map[string]json.RawMessage) string {
	var s string
	if json.Unmarshal(fields["agent_id"], &s) != nil {
		return ""
	}
	return s
}
