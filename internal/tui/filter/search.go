package filter

import (
	"context"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/browse"
)

// Search limits (Proposals): pages of up to PageLimit results (the service's
// maximum), and at most MaxResults results per search.
const (
	PageLimit  = 1000
	MaxResults = 10000
)

// Found is a search's answer.
type Found struct {
	Query string
	// Issues are the issues whose title or body, or one of whose comments,
	// contains Query. Project descriptions and milestones are not issues.
	Issues    map[string]bool
	Results   int  // matching records read
	Truncated bool // more records match than MaxResults
	Err       error
}

// Search finds text in project's current content with lit grep's
// semantics: a literal substring, ignoring case, matched by the service. Each
// page is one call through d.
func Search(ctx context.Context, d browse.Doer, project, text string) Found {
	return search(ctx, d, project, text, MaxResults)
}

func search(ctx context.Context, d browse.Doer, project, text string, most int) Found {
	out := Found{Query: text, Issues: map[string]bool{}}
	q := protocol.SearchRequest{Scope: protocol.SearchScope{ProjectID: project}, Query: text}
	for {
		var page litclient.SearchPage
		out.Err = d.Do(ctx, func(ctx context.Context, s *litclient.Session) error {
			// Pages stay within what the service returns in one answer.
			q.Limit = PageLimit
			if n := s.Meta.Limits.ExpandedRecords; n > 0 {
				q.Limit = min(q.Limit, n)
			}
			var err error
			page, err = s.Search(ctx, q)
			return err
		})
		if out.Err != nil {
			return out
		}
		for _, r := range page.Items {
			out.Results++
			if r.IssueID != "" {
				out.Issues[r.IssueID] = true
			}
		}
		if page.NextCursor == nil {
			return out
		}
		if out.Results >= most {
			out.Truncated = true
			return out
		}
		q.Cursor = *page.NextCursor
	}
}
