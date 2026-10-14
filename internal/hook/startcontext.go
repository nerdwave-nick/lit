package hook

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// The texts below are what the agent reads. Notes about a failure are one
// line; the ready context lists the session's facts one per line.

func (c *Call) say(text string) []byte { return c.Host.Context(c.Event, text) }

// workflow renders a lit workflow-session command for this runtime.
func (c *Call) workflow(action string, args ...string) string {
	parts := append([]string{"lit workflow-session --host", c.Host.Name, "--runtime-id", quote(c.Input.SessionID), action}, args...)
	return strings.Join(parts, " ")
}

func quote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./:@%+=,") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (c *Call) unavailable(exists bool, known association, endpoint string) string {
	if !exists {
		return fmt.Sprintf("lit is unavailable: nothing answered at %s, so no lit session was created for this conversation. "+
			"When the server is running, set the session up with: %s (it adopts the project remembered for this checkout, if any; add --project REF to use another project). "+
			"Until then do not run lit tracker commands.", endpoint, c.workflow("fresh", "--endpoint", endpoint))
	}
	return fmt.Sprintf("lit is unavailable: nothing answered at %s. This conversation's lit session is kept (session: %s, runtime ID: %s, project: %s) "+
		"but its claims could not be read and tracker commands fail until the server is back; then reconnect with: %s",
		endpoint, known.Session, known.Runtime, projectID(known), c.workflow("resume"))
}

func (c *Call) pending(r association) string {
	return fmt.Sprintf("lit session setup did not finish: the server answered but connecting did not complete in time, so the pending association "+
		"was kept (session: %s, runtime ID: %s). Before any tracker work run: %s. Do not run fresh again; if reconcile fails, tell the user.",
		r.Session, r.Runtime, c.workflow("reconcile"))
}

func (c *Call) lockHeld(a adapter) []byte {
	_, lock, _ := a.Paths()
	c.Log.Error("the adapter lock of this runtime is held: " + lock)
	return c.say(fmt.Sprintf("lit session setup skipped: another lit process holds the adapter lock for this runtime (%s). "+
		"If no lit process is running, inspect that directory and remove it, then set the session up with %s or %s.",
		lock, c.workflow("resume"), c.workflow("fresh")))
}

func sessionStartFailed(c *Call, err error) []byte {
	note := fmt.Sprintf("lit session setup failed: %s (details: %s).", oneLine(err.Error()), c.Log.Path())
	if c.Input.SessionID != "" {
		note += " Tracker commands need a session; set it up with: " + c.recoveryCommand()
	}
	return c.say(note)
}

// recoveryCommand is the command that sets up this runtime's session later:
// resume when an association record exists, otherwise fresh.
func (c *Call) recoveryCommand() string {
	if a, err := c.adapter(c.Input.SessionID); err == nil {
		if record, _, err := a.Paths(); err == nil {
			if _, err = os.Lstat(record); err == nil {
				return c.workflow("resume")
			}
		}
	}
	return c.workflow("fresh")
}

func projectID(r association) string {
	if r.Project == nil {
		return "none selected"
	}
	return *r.Project
}

// ready is the context of a connected session.
func (c *Call) ready(a adapter, r association, how string) string {
	lines := []string{
		fmt.Sprintf("lit session ready for this conversation (%s).", how),
		"session: " + r.Session,
		"endpoint: " + r.Endpoint,
		"runtime ID: " + r.Runtime,
		"project: " + c.project(a, r),
	}
	if line := remembrance(r); line != "" {
		lines = append(lines, line)
	}
	lines = append(lines, c.claims(a, how == "new session")...)
	lines = append(lines, "Run lit tracker commands as this session: "+c.workflow("run", "--", "<lit command>"))
	return strings.Join(lines, "\n")
}

func (c *Call) project(a adapter, r association) string {
	if r.Project == nil {
		return "none selected (choose one with: " + c.workflow("run", "--", "session set --project REF") + ")"
	}
	items, err := a.Query(c.Ctx, "projects", "get", *r.Project)
	if err != nil {
		c.Log.Warn("reading the title of project " + *r.Project + " failed: " + err.Error())
		return *r.Project
	}
	var title string
	if json.Unmarshal(items[0]["title"], &title) != nil || title == "" {
		return *r.Project
	}
	return fmt.Sprintf("%s (%s)", title, *r.Project)
}

const maxClaimLines = 20

// claims lists the session's claims with their lease expiry; a session that
// was just created has none, and is not asked.
func (c *Call) claims(a adapter, fresh bool) []string {
	if fresh {
		return []string{"claims: none"}
	}
	items, err := a.Query(c.Ctx, "claims", "list")
	if err != nil {
		c.Log.Error("reading the claims failed: " + err.Error())
		return []string{"claims: could not be read; check with: " + c.workflow("run", "--", "claims list")}
	}
	lines := []string{fmt.Sprintf("claims: %d held", len(items))}
	for i, item := range items {
		if i == maxClaimLines {
			lines = append(lines, fmt.Sprintf("- and %d more", len(items)-i))
			break
		}
		var issue, expires string
		json.Unmarshal(item["issue_id"], &issue)
		json.Unmarshal(item["expires_at"], &expires)
		lines = append(lines, fmt.Sprintf("- %s (lease expires %s)", issue, expires))
	}
	if len(items) == 0 {
		return []string{"claims: none"}
	}
	return lines
}
