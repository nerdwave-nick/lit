package hook

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"
)

// The stop guard is soft (see "4. Stop guard" of the workflow hooks
// specification): while the session holds claims, the first attempt to end the
// turn is blocked once with a reminder to hand off or continue. The host sets
// stop_hook_active on the attempt that follows a block, and that one is always
// allowed. The guard only reads: it never releases, renews or closes anything.
//
// It fails open. Every doubt - no session, a session that is not connected, a
// server that does not answer, a failed read, input it cannot trust - allows
// the stop, because a guard that cannot see the claims has nothing to remind
// about and a wrongly blocked agent is the worse outcome. Failures are logged;
// a server that is down and a session that is not connected are not failures.

func init() {
	register(Event{
		Name:     "stop",
		HostName: "Stop",
		Summary:  "Remind the agent once about held claims before it stops",
		Description: "Serve the host's Stop hook. Read the hook JSON on stdin; when this session holds claims and the stop is not already the\n" +
			"result of a stop hook (stop_hook_active is false), print a block decision whose reason names the claims and asks the agent to\n" +
			"record a checkpoint or handoff and release them, or to continue. The stop that follows is allowed. Without claims, without a\n" +
			"connected session, when the server does not answer, and on any internal error nothing is printed and the stop is allowed. The\n" +
			"guard only reads: it never releases, renews or closes anything. The command exits 0 whatever happens; errors go to the hook log.",
		Budget: time.Second,
		Handle: stopGuard,
	})
}

const (
	// stopProbeTimeout keeps a server that does not answer from using the
	// whole budget.
	stopProbeTimeout = 400 * time.Millisecond
	// maxStopClaims bounds the claims the reason names.
	maxStopClaims = 10
)

// Block renders host output that stops the host from ending the turn and
// gives the agent the reason to continue. Both hosts take the same top-level
// decision for their Stop event.
func (h Host) Block(reason string) []byte {
	out, _ := json.Marshal(map[string]string{"decision": "block", "reason": reason})
	return append(out, '\n')
}

func stopGuard(c *Call) ([]byte, error) {
	active, err := stopHookActive(c.Input)
	if err != nil || active {
		return nil, err
	}
	a, err := c.adapter(c.Input.SessionID)
	if err != nil {
		return nil, err
	}
	assoc, err := a.Read()
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil // this conversation has no lit session, so it holds no claims
	case err != nil:
		return nil, fmt.Errorf("reading the association of this runtime: %w", err)
	case assoc.Status != "ready":
		c.Log.Warn("the session " + assoc.Session + " is not connected, so its claims cannot be read")
		return nil, nil
	}
	if err = probe(c.Ctx, assoc.Endpoint, min(c.probeTimeout(), stopProbeTimeout)); err != nil {
		c.Log.Warn(fmt.Sprintf("lit does not answer at %s: %v", assoc.Endpoint, err))
		return nil, nil
	}
	items, err := a.Query(c.Ctx, "claims", "list")
	if err != nil {
		return nil, fmt.Errorf("reading the claims failed: %w", err)
	}
	if len(items) == 0 {
		return nil, nil
	}
	return c.Host.Block(c.holdingReason(items)), nil
}

// stopHookActive reports whether the host is already continuing because of a
// stop hook. A host that does not say cannot be told apart from one whose
// every stop would be blocked again, so that is an error and the stop is allowed.
func stopHookActive(in Input) (bool, error) {
	var active *bool
	if err := json.Unmarshal(in.Raw["stop_hook_active"], &active); err != nil || active == nil {
		return false, errors.New("the hook input has no boolean stop_hook_active")
	}
	return *active, nil
}

// holdingReason is what the agent reads when its stop is blocked. It names
// each claim by issue and lease expiry and never prints a claim token.
func (c *Call) holdingReason(claims []map[string]json.RawMessage) string {
	noun := "claims"
	if len(claims) == 1 {
		noun = "claim"
	}
	lines := []string{fmt.Sprintf("This lit session still holds %d %s:", len(claims), noun)}
	for i, claim := range claims {
		if i == maxStopClaims {
			lines = append(lines, fmt.Sprintf("- and %d more", len(claims)-i))
			break
		}
		var issue, expires string
		_ = json.Unmarshal(claim["issue_id"], &issue)
		_ = json.Unmarshal(claim["expires_at"], &expires)
		lines = append(lines, fmt.Sprintf("- %s (lease expires %s)", issue, expires))
	}
	lines = append(lines,
		"Before you stop, record a checkpoint or handoff comment on each issue and release its claim, or continue the work.",
		"  checkpoint: "+c.workflow("run", "--", "comments", "create", "--issue", "ISSUE_ID", "--content", "'what is done, what remains'"),
		"  release: "+c.workflow("run", "--", "claims", "release", "ISSUE_ID"),
		"If you are waiting for the user and keep the claims on purpose, say so and stop again: this reminder is shown once, and stopping again is allowed.")
	return strings.Join(lines, "\n")
}
