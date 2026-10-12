package litclient

import (
	"context"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// RankedSearchPage is one page of ranked search results.
type RankedSearchPage struct {
	Items      []protocol.RankedSearchResult `json:"items"`
	NextCursor *string                       `json:"next_cursor"`
}

// RankedSearch reads POST /v1/ranked-search with lit search's semantics:
// search box syntax over current titles, bodies, descriptions and comments,
// ignoring case and diacritics, results in order of relevance (one per
// issue with its matching comments, project or milestone) with snippets. A
// q.Scope naming neither a project nor all projects uses the session's
// selected project. It is a read and journals nothing. A non-nil NextCursor
// continues the same query. ctx bounds this read; nil keeps the session's
// context.
func (s *Session) RankedSearch(ctx context.Context, q protocol.RankedSearchRequest) (RankedSearchPage, error) {
	defer s.bind(ctx)()
	var page RankedSearchPage
	if e := s.Call("POST", "/v1/ranked-search", q, nil, &page, false); e != nil {
		return RankedSearchPage{}, e
	}
	if page.Items == nil {
		return RankedSearchPage{}, protocol.E(500, "invalid_response", "ranked search omitted items")
	}
	return page, nil
}
