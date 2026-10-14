package hook

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"time"

	"github.com/nerdwave-nick/lit/internal/clientendpoint"
)

func init() { startSources["compact"] = restoreAfterCompaction }

// restoreAfterCompaction reminds the agent, after its conversation compacted,
// of its lit session, the claims it holds and the latest comment of each
// claimed issue. It only reads: it creates no session, connects no client and
// never acquires, renews or releases a claim (the lit hook rules in the
// specification, "3. After compaction").
func restoreAfterCompaction(c *Call) ([]byte, error) {
	a, err := c.adapter(c.Input.SessionID)
	if err != nil {
		return nil, err
	}
	known, err := a.Read()
	switch {
	case errors.Is(err, fs.ErrNotExist):
		c.Log.Warn("the conversation compacted but this runtime has no lit session")
		return c.say(c.noSession()), nil
	case err != nil:
		return nil, fmt.Errorf("reading the association of this runtime: %w", err)
	case known.Status == "pending":
		return c.say(c.pending(known)), nil
	}
	if err = probe(c.Ctx, known.Endpoint, c.probeTimeout()); err != nil {
		c.Log.Warn(fmt.Sprintf("lit does not answer at %s: %v", known.Endpoint, err))
		return c.say(c.unreachableAfterCompaction(known)), nil
	}
	return c.say(c.restore(a, known).String()), nil
}

// noSession is the note for a conversation that never got a lit session, for
// example because the hooks were installed after it started.
func (c *Call) noSession() string {
	endpoint, err := clientendpoint.Resolve("", false, os.Getenv("LIT_ENDPOINT"), "")
	if err != nil {
		endpoint = "URL"
	}
	return fmt.Sprintf("lit: this conversation has no lit session (no association is recorded for runtime ID %s), so there are no claims to restore and nothing was created. "+
		"To use lit, set one up with: %s (add --project REF to select a project).", c.Input.SessionID, c.workflow("fresh", "--endpoint", endpoint))
}

// unreachableAfterCompaction gives the session and project of the local
// association and says the claims must be rechecked. It never names a claim:
// anything remembered about them could be stale.
func (c *Call) unreachableAfterCompaction(r association) string {
	run := c.workflow("run", "--")
	return fmt.Sprintf("lit context after compaction (lit is unavailable: nothing answered at %s).\n"+
		"session: %s\nendpoint: %s\nruntime ID: %s\nproject: %s\n"+
		"claims: could not be read. Do not rely on any claim you remember holding: it may have expired or been released. "+
		"When the server answers, recheck with: %s claims list, then reread each claimed issue before continuing.",
		r.Endpoint, r.Session, r.Endpoint, r.Runtime, projectID(r), run)
}

// restore reads the session's project, claims and the latest comment of the
// claimed issues.
func (c *Call) restore(a adapter, r association) restoreView {
	v := restoreView{Session: r.Session, Endpoint: r.Endpoint, Runtime: r.Runtime, Project: c.project(a, r), Run: c.workflow("run", "--")}
	items, at, err := a.QueryTimed(c.Ctx, "claims", "list", "--all", "--direction", "desc")
	if err != nil {
		c.Log.Error("reading the claims failed: " + err.Error())
		v.Unread = true
		return v
	}
	for _, item := range items {
		claim := claimView{Issue: field(item, "issue_id"), Lease: field(item, "expires_at")}
		if ends, err := time.Parse(time.RFC3339Nano, claim.Lease); err == nil && !at.IsZero() {
			claim.Ends, claim.Left = ends, ends.Sub(at)
			claim.Expired = !ends.After(at)
		}
		v.Claims = append(v.Claims, claim)
	}
	c.latestComments(a, v.Claims[:min(len(v.Claims), maxDetailedClaims)])
	return v
}

// latestComments fills in the latest comment of each claim, concurrently:
// the reads share the event's time budget.
func (c *Call) latestComments(a adapter, claims []claimView) {
	errs := make([]error, len(claims))
	var wg sync.WaitGroup
	for i := range claims {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claims[i].Comment, errs[i] = latestComment(c, a, claims[i].Issue)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		claims[i].Shown = true
		if err != nil {
			claims[i].Unread = true
			c.Log.Error("reading the latest comment of " + claims[i].Issue + " failed: " + err.Error())
		}
	}
}

// latestComment returns the most recent comment of an issue, nil when it has
// none. lit comments carry no type, so the latest one stands for the latest
// checkpoint.
func latestComment(c *Call, a adapter, issue string) (*comment, error) {
	items, err := a.Query(c.Ctx, "comments", "list", "--issue", issue, "--sort", "created-at", "--direction", "desc", "--limit", "1")
	if err != nil || len(items) == 0 {
		return nil, err
	}
	return &comment{ID: field(items[0], "id"), Author: field(items[0], "author"), At: field(items[0], "created_at"), Body: field(items[0], "body")}, nil
}

// field reads a string property of a tracker item, empty when it is not one.
func field(item map[string]json.RawMessage, name string) string {
	var s string
	if json.Unmarshal(item[name], &s) != nil {
		return ""
	}
	return s
}
