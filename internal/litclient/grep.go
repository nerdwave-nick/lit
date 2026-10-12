package litclient

import (
	"context"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// SearchPage is one page of content search results.
type SearchPage struct {
	Items      []protocol.SearchResult `json:"items"`
	NextCursor *string                 `json:"next_cursor"`
}

// Search reads POST /v1/search with lit grep's semantics: a literal
// substring of current content (project descriptions, issue titles and
// bodies, comments, milestone titles and bodies), case-insensitive unless
// q.CaseSensitive. With q.Regex it is lit grep -E instead: q.Query is a Go
// RE2 pattern matched line by line, with the same result shape, and an
// invalid pattern is validation_failed. q is sent as lit grep sends it; a q.Scope naming neither
// a project nor all projects uses the session's selected project. It is a
// read and journals nothing. A non-nil NextCursor continues the same query.
// ctx bounds this read; nil keeps the session's context.
func (s *Session) Search(ctx context.Context, q protocol.SearchRequest) (SearchPage, error) {
	defer s.bind(ctx)()
	var page SearchPage
	if e := s.Call("POST", "/v1/search", q, nil, &page, false); e != nil {
		return SearchPage{}, e
	}
	if page.Items == nil {
		return SearchPage{}, protocol.E(500, "invalid_response", "search omitted items")
	}
	return page, nil
}
