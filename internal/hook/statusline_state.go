package hook

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/nerdwave-nick/lit/internal/clientendpoint"
	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/workflowsession"
)

// litStatus says what lit's part of the line shows for the host session: the
// association decides whether there is a session at all, the cache or a bounded
// refresh says whether and how the server answers.
func (c *Call) litStatus() litState {
	state := litState{Failing: c.hooksFailing()}
	adapter := workflowsession.Adapter{Host: c.Host.Name, RuntimeID: c.Input.SessionID}
	assoc, err := adapter.Read()
	var session *association
	if err == nil {
		session = &assoc
	}
	switch {
	case session != nil && session.Status == "pending":
		state.Kind = kindPending
		return state
	case session != nil && (session.Client == nil || session.Service == nil):
		session = nil
	}
	var endpoint string
	if session != nil {
		endpoint = session.Endpoint
	} else if endpoint, err = clientendpoint.Resolve("", false, os.Getenv("LIT_ENDPOINT"), ""); err != nil {
		state.Kind = kindOffline
		return state
	}
	entry := c.observe(endpoint, session)
	switch {
	case !entry.Reachable:
		state.Kind = kindOffline
	case session == nil:
		state.Kind = kindNoSession
	default:
		state.Kind, state.Claims, state.Stage = kindReady, entry.Claims, entry.Stage
		state.Project = entry.Title
		if state.Project == "" && entry.ProjectID != "" {
			state.Project = entry.ProjectID[:min(8, len(entry.ProjectID))]
		}
	}
	return state
}

// hooksFailing reports whether a hook of this host logged an error recently.
func (c *Call) hooksFailing() bool {
	failures, err := RecentFailures(time.Now().Add(-failureWindow))
	if err != nil {
		return false
	}
	for _, f := range failures {
		if f.Host == c.Host.Name {
			return true
		}
	}
	return false
}

// observe returns the cached observation of the server for this session, or,
// when it is old, a new one read within litTimeout. A failed read is an
// observation too (the server does not answer) and is cached like any other.
func (c *Call) observe(endpoint string, session *association) statusEntry {
	client := ""
	if session != nil {
		client = *session.Client
	}
	path, err := statusCachePath(c.Host.Name, c.Input.SessionID)
	var prev statusEntry
	usable := false
	if err == nil {
		prev, usable = readStatusEntry(path)
		usable = usable && prev.usableFor(endpoint, client)
	}
	now := time.Now()
	if usable && prev.fresh(now) {
		return prev
	}
	if !usable {
		prev = statusEntry{}
	}
	ctx, cancel := context.WithTimeout(c.Ctx, litTimeout)
	defer cancel()
	next := readServer(ctx, endpoint, session, prev, now)
	if err == nil {
		_ = writeStatusEntry(path, next) // the cache is an optimization
	}
	return next
}

// readServer asks the server what the line shows. prev lends the project's
// title while it is still young.
func readServer(ctx context.Context, endpoint string, session *association, prev statusEntry, now time.Time) statusEntry {
	next := statusEntry{CheckedAt: now, Endpoint: endpoint}
	if session == nil {
		next.Reachable = probe(ctx, endpoint, litTimeout) == nil
		return next
	}
	next.ClientID = *session.Client
	s := &litclient.Session{
		Endpoint: endpoint,
		HTTP:     &http.Client{},
		Context:  ctx,
		Mapping:  litclient.Mapping{SchemaVersion: 1, ClientID: *session.Client, ServiceID: *session.Service, Endpoint: endpoint},
	}
	// A disconnected client still has a project and a stage to show.
	if err := s.Open(litclient.OpenOptions{Endpoint: endpoint, EndpointSet: true, AllowDisconnected: true}); err != nil {
		return next
	}
	claims, err := s.SnapshotClaims(ctx, litclient.ClaimSelection{All: true})
	if err != nil {
		return next
	}
	next.Reachable = true
	for _, item := range claims.Items {
		if item.Claim != nil {
			next.Claims++
		}
	}
	if s.Client.Stage != nil {
		next.Stage = *s.Client.Stage
	}
	if s.Client.ProjectID != nil {
		next.ProjectID = *s.Client.ProjectID
		next.Title, next.TitleAt = projectTitle(ctx, s, prev, now)
	}
	return next
}

// projectTitle is the title of the client's project: the cached one while it
// is younger than titleTTL, otherwise read again (and, if that fails, the old
// one for the same project or none).
func projectTitle(ctx context.Context, s *litclient.Session, prev statusEntry, now time.Time) (string, time.Time) {
	project := *s.Client.ProjectID
	known := prev.ProjectID == project && prev.Title != ""
	if known && now.Sub(prev.TitleAt) >= 0 && now.Sub(prev.TitleAt) < titleTTL {
		return prev.Title, prev.TitleAt
	}
	if projects, err := litclient.Get[protocol.Project](ctx, s, "", project); err == nil && len(projects) == 1 {
		return projects[0].Title, now
	}
	if known {
		return prev.Title, prev.TitleAt
	}
	return "", time.Time{}
}
