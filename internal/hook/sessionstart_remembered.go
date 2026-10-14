package hook

import (
	"fmt"
	"os"
	"strings"

	"github.com/nerdwave-nick/lit/internal/checkoutcontext"
	"github.com/nerdwave-nick/lit/internal/clientendpoint"
	"github.com/nerdwave-nick/lit/internal/workflowsession"
)

// A new runtime's session adopts the project remembered for the checkout of
// the host's working directory (section 9 of the specification): fresh
// validates the preference and selects the project, or refuses before it
// creates anything. This file holds what the hook adds around that: where to
// look first, how adoption is reported, and what a refusal tells the agent.

// remembered asks Fresh to adopt the checkout's project. The hook has no
// endpoint flag, so LIT_ENDPOINT is the only caller input that outranks the
// endpoint saved with the preference.
func (c *Call) remembered() *workflowsession.Remembered {
	return &workflowsession.Remembered{
		Dir:      c.Input.Cwd,
		Endpoint: checkoutcontext.Endpoint{Environment: os.Getenv("LIT_ENDPOINT")},
	}
}

// newRuntimeEndpoints returns where a new runtime registers when it adopts
// nothing (LIT_ENDPOINT or the default) and which endpoint the reachability
// probe must ask: the one Fresh will contact, which for an adopted project is
// the endpoint saved with the preference unless LIT_ENDPOINT is set. A
// preference that cannot be read is refused by Fresh before it contacts
// anything, so there is nothing to probe (probeAt is empty).
func (c *Call) newRuntimeEndpoints(a adapter) (register, probeAt string, err error) {
	environment := os.Getenv("LIT_ENDPOINT")
	if register, err = clientendpoint.Resolve("", false, environment, ""); err != nil {
		return "", "", err
	}
	saved, err := a.RememberedEndpoint(c.Input.Cwd)
	if err != nil {
		return register, "", nil
	}
	probeAt, err = clientendpoint.Resolve("", false, environment, saved)
	return register, probeAt, err
}

// remembrance says how the session's project relates to the project remembered
// for the checkout, from the record Fresh kept of how the session was created.
// It is empty when that record says nothing about the session as it is now: no
// record (an older association), a project named by the caller, or a project
// the agent has since changed.
func remembrance(r association) string {
	s := r.Selection
	switch {
	case s == nil:
	case s.Source == workflowsession.SourceRemembered && r.Project != nil && *r.Project == s.ProjectID:
		// The title is the project line's: after a resume it may be newer than the one validated at start.
		line := fmt.Sprintf("remembered project: this project was remembered for this checkout (%s) and adopted when this session started", s.Checkout)
		if s.SavedTitle != "" {
			line += fmt.Sprintf("; it was saved as %s and has been renamed since", s.SavedTitle)
		}
		return line
	case s.Source == workflowsession.SourceNone && s.Checkout != "" && r.Project == nil:
		return fmt.Sprintf("remembered project: none for this checkout (%s); remember one only when the user asks, with: lit context set --project REF", s.Checkout)
	}
	return ""
}

// stale is the context for a remembered project that cannot be adopted. No
// session exists: the agent is told what is wrong and the two ways out, each
// with its exact command, and that the choice is the user's. endpoint is where
// a session without the remembered project would register.
func (c *Call) stale(e *workflowsession.AdoptionError, endpoint string) []byte {
	c.Log.Warn(fmt.Sprintf("the project remembered for checkout %s cannot be adopted (%s): %s; no session was created", e.Checkout, e.Code, e.Reason))
	lines := []string{
		"lit session not set up: the project remembered for this checkout cannot be used, so no lit session was created for this conversation.",
		"checkout: " + e.Checkout,
		fmt.Sprintf("problem: %s (%s)", e.Reason, e.Code),
	}
	if remedy := e.Remedy(); remedy != "" {
		lines = append(lines, "hint: "+remedy)
	}
	lines = append(lines,
		"Do not run lit tracker commands until a session exists. Ask the user which to do:",
		"- forget the remembered project: run 'lit context clear' from inside "+e.Checkout+", then set the session up with: "+c.workflow("fresh", "--endpoint", endpoint),
		"- use another project for this conversation only (the remembered project stays as it is): "+c.workflow("fresh", "--endpoint", endpoint, "--project", "REF"),
	)
	return c.say(strings.Join(lines, "\n"))
}
