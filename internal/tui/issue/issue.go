// Package issue loads and lays out what lit-tui's issue detail shows beyond
// the bodiless summary: the full issue (its body) and its comments. Every
// read goes through the lifecycle's gate (Doer), one request per call, so
// shutdown and mutations interleave between comment pages.
package issue

import (
	"context"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// Doer runs fn with exclusive use of the session (lifecycle.Lifecycle.Do).
type Doer interface {
	Do(ctx context.Context, fn func(ctx context.Context, s *litclient.Session) error) error
}

// Detail is one load of an issue and its comments.
type Detail struct {
	Issue    *protocol.Issue // nil when the issue read failed
	Comments []protocol.Comment
	// Consistent reports that the comments came from one bounded read, so
	// their count is true. Paged comments may have changed between pages.
	Consistent bool
	// Err ended the load: the issue read failed (Issue is nil), or a page of
	// comments did (Comments keeps the pages read before it).
	Err error
}

// Load reads issue id of project (UUIDs) and all of its comments, oldest
// first: one bounded comments read when it fits, otherwise every page in
// turn. A page refused as too large is retried, from the start, at half the
// page size. A cancelled ctx stops the load between requests.
func Load(ctx context.Context, d Doer, project, id string) Detail {
	var out Detail
	maxPage := 0
	err := d.Do(ctx, func(ctx context.Context, s *litclient.Session) error {
		maxPage = s.Meta.Limits.MaxPage
		got, err := litclient.Get[protocol.Issue](ctx, s, project, id)
		if err == nil {
			out.Issue = &got[0]
		}
		return err
	})
	if err != nil {
		out.Err = err
		return out
	}
	err = d.Do(ctx, func(ctx context.Context, s *litclient.Session) error {
		page, err := litclient.List[protocol.Comment](ctx, s, litclient.Query{IssueID: id, All: true})
		out.Comments = page.Items
		return err
	})
	switch {
	case err == nil:
		out.Consistent = true
	case litclient.Code(err) == "limit_exceeded":
		out.Comments, out.Err = paged(ctx, d, id, max(maxPage, 1))
	default:
		out.Comments, out.Err = nil, err
	}
	return out
}

func paged(ctx context.Context, d Doer, id string, limit int) ([]protocol.Comment, error) {
	for {
		var out []protocol.Comment
		seen := map[string]bool{}
		cursor := ""
		for {
			var page litclient.Page[protocol.Comment]
			err := d.Do(ctx, func(ctx context.Context, s *litclient.Session) error {
				var err error
				page, err = litclient.List[protocol.Comment](ctx, s, litclient.Query{IssueID: id, Limit: limit, Cursor: cursor})
				return err
			})
			if litclient.Code(err) == "limit_exceeded" && limit > 1 {
				break // retry smaller, from the start: cursors are bound to the page size
			}
			if err != nil {
				return out, err
			}
			for _, c := range page.Items {
				if !seen[c.ID] {
					seen[c.ID] = true
					out = append(out, c)
				}
			}
			if page.NextCursor == nil {
				return out, nil
			}
			cursor = *page.NextCursor
		}
		limit /= 2
	}
}
