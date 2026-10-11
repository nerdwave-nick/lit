package service

import (
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"net/http"
)

// issueSummaryRequest mirrors a query snapshot: one issue query, paged by the
// same limits and query-bound cursors, or one bounded all-results read.
type issueSummaryRequest struct {
	SchemaVersion int           `json:"schema_version"`
	Query         *ProjectQuery `json:"query"`
}

// issueSummaryRoute serves POST /v1/issue-summaries, the bodiless list projection.
func (s *Server) issueSummaryRoute(w http.ResponseWriter, r *http.Request, c protocol.Client) (bool, error) {
	if r.URL.Path != "/v1/issue-summaries" {
		return false, nil
	}
	if r.Method != "POST" {
		return true, protocol.E(404, "not_found", "unknown route")
	}
	var req issueSummaryRequest
	if e := s.Body(r, &req); e != nil {
		return true, e
	}
	if req.SchemaVersion != 0 && req.SchemaVersion != 1 {
		return true, invalid("unsupported schema_version")
	}
	if req.Query == nil {
		return true, invalid("issue summaries require a query")
	}
	q := *req.Query
	if q.Type == "" {
		q.Type = "issues"
	}
	if q.Type != "issues" {
		return true, protocol.E(400, "unsupported_filter", "issue summaries query only issues")
	}
	if q.ProjectID == "" && !q.AllProjects && c.ProjectID != nil {
		q.ProjectID = *c.ProjectID
	}
	page, e := s.recordPage(q)
	if e != nil {
		return true, e
	}
	for n, item := range page.Items {
		issue := item.(protocol.Issue)
		claim, e := s.publicClaim(issue.ID)
		if e != nil {
			return true, e
		}
		page.Items[n] = issue.Summarize(claim)
	}
	s.writeProjectPage(w, page)
	return true, nil
}

// publicClaim is the live claim as exposed to clients, naming its owner's actor.
func (s *Server) publicClaim(id string) (*protocol.Claim, error) {
	claim, e := s.LiveClaim(id)
	if e != nil {
		return nil, e
	}
	return s.withOwnerActor(claim)
}

// withOwnerActor projects only the owner's actor name and kind onto a copy;
// the owner's client record itself stays private to that client.
func (s *Server) withOwnerActor(claim *protocol.Claim) (*protocol.Claim, error) {
	if claim == nil {
		return nil, nil
	}
	owner, e := s.Client(claim.OwnerClientID)
	if e != nil {
		return nil, e
	}
	v := *claim
	actor := protocol.Actor{Name: owner.Actor.Name, Kind: owner.Actor.Kind}
	v.OwnerActor = &actor
	return &v, nil
}
