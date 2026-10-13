package litclient_test

import (
	"context"
	"testing"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
)

// RankedSearch is lit search: results in order of relevance with snippets,
// paged through query-bound cursors, as a read that journals nothing.
func TestRankedSearchFindsTheMostRelevantFirst(t *testing.T) {
	h := newHarness(t)
	s := h.connect("ranked", "person")
	project, issues := seed(t, s, "ranked", "Renewal policy", "Unrelated", "Commented")
	apply(t, s, litclient.CreateComment(project, issues["Commented"].ID, "see the rénewal notes"))
	ctx := context.Background()
	scope := protocol.SearchScope{ProjectID: issues["Unrelated"].ProjectID}

	page, err := s.RankedSearch(ctx, protocol.RankedSearchRequest{Scope: scope, Query: "renewal"})
	if err != nil || page.NextCursor != nil || len(page.Items) != 2 {
		t.Fatalf("search: %+v %v", page, err)
	}
	if r := page.Items[0]; r.ID != issues["Renewal policy"].ID || r.Matches[0].Field != "title" || r.Matches[0].Snippet != "Renewal policy" {
		t.Fatalf("first result %+v", r)
	}
	if r := page.Items[1]; r.ID != issues["Commented"].ID || r.Matches[0].CommentID == "" || r.CommentMatches != 1 {
		t.Fatalf("second result %+v", r)
	}
	q := protocol.RankedSearchRequest{Scope: scope, Query: "renewal", Limit: 1}
	if page, err = s.RankedSearch(ctx, q); err != nil || len(page.Items) != 1 || page.NextCursor == nil {
		t.Fatalf("first page: %+v %v", page, err)
	}
	q.Cursor = *page.NextCursor
	if page, err = s.RankedSearch(ctx, q); err != nil || len(page.Items) != 1 || page.Items[0].ID != issues["Commented"].ID || page.NextCursor != nil {
		t.Fatalf("second page: %+v %v", page, err)
	}
	if _, err := s.RankedSearch(ctx, protocol.RankedSearchRequest{Scope: scope, Query: ""}); litclient.Code(err) != "validation_failed" {
		t.Fatalf("empty query: %v", err)
	}
	if n := h.count("POST /v1/ranked-search"); n != 4 {
		t.Fatalf("%d ranked search requests", n)
	}
	if files := h.pending(); len(files) != 0 {
		t.Fatalf("search journaled: %v", files)
	}
}
