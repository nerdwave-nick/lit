package litclient

import "github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"

// IssueSummaryPage is one page of the bodiless issue list projection.
type IssueSummaryPage struct {
	Items      []protocol.IssueSummary `json:"items"`
	NextCursor *string                 `json:"next_cursor"`
}

// IssueSummaries reads POST /v1/issue-summaries: issue summaries without bodies
// or comments, each with its live claim and the owner's actor. query is an
// issue query object as for snapshot queries (for example project_id, filters,
// sort, limit, cursor or all); the session's project applies when it names
// neither project_id nor all_projects. It is a read and journals nothing.
// Service limits are refused with typed errors such as limit_exceeded; a page
// is never truncated, and a non-nil NextCursor continues the same query.
func (s *Session) IssueSummaries(query map[string]any) (IssueSummaryPage, error) {
	var page IssueSummaryPage
	body := map[string]any{"schema_version": 1, "query": query}
	if e := s.Call("POST", "/v1/issue-summaries", body, nil, &page, false); e != nil {
		return IssueSummaryPage{}, e
	}
	if page.Items == nil {
		return IssueSummaryPage{}, protocol.E(500, "invalid_response", "issue summaries omitted items")
	}
	return page, nil
}
