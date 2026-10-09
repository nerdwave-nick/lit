// Package browse loads what lit-tui browses: a project's bodiless issue
// summaries and pages of projects. Every read goes through the lifecycle's
// gate (Doer), one request per call, so shutdown and mutations interleave
// between pages.
package browse

import (
	"context"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// Doer runs fn with exclusive use of the session (lifecycle.Lifecycle.Do).
type Doer interface {
	Do(ctx context.Context, fn func(ctx context.Context, s *litclient.Session) error) error
}

// Issues is one load of a project's issue summaries.
type Issues struct {
	Project string // the project reference the load was for
	Items   []protocol.IssueSummary
	// Consistent reports one bounded read: Items is the whole project at one
	// moment, so counts derived from it are true. Paged loads are never
	// consistent: issues may have changed between pages.
	Consistent bool
	// Err is the error that ended the load. Items keeps the pages read before
	// it.
	Err     error
	Pages   int // requests that returned items
	Elapsed time.Duration
}

// LoadIssues reads project's issue summaries: one bounded read when it fits,
// otherwise every page in turn. A page refused as too large is retried, from
// the start, at half the page size.
func LoadIssues(ctx context.Context, d Doer, project string) Issues {
	start := time.Now()
	out := Issues{Project: project}
	maxPage := 0
	err := d.Do(ctx, func(ctx context.Context, s *litclient.Session) error {
		maxPage = s.Meta.Limits.MaxPage
		page, err := s.IssueSummaries(ctx, map[string]any{"project_id": project, "all": true})
		out.Items = page.Items
		return err
	})
	switch {
	case err == nil:
		out.Consistent, out.Pages = true, 1
	case litclient.Code(err) == "limit_exceeded":
		out = paged(ctx, d, project, max(maxPage, 1))
	default:
		out.Err = err
	}
	out.Elapsed = time.Since(start)
	return out
}

func paged(ctx context.Context, d Doer, project string, limit int) Issues {
	for {
		out := Issues{Project: project}
		seen := map[string]bool{}
		cursor := ""
		for {
			query := map[string]any{"project_id": project, "limit": limit}
			if cursor != "" {
				query["cursor"] = cursor
			}
			var page litclient.IssueSummaryPage
			err := d.Do(ctx, func(ctx context.Context, s *litclient.Session) error {
				var err error
				page, err = s.IssueSummaries(ctx, query)
				return err
			})
			if litclient.Code(err) == "limit_exceeded" && limit > 1 {
				break // retry smaller, from the start: cursors are bound to the page size
			}
			if err != nil {
				out.Err = err
				return out
			}
			out.Pages++
			for _, it := range page.Items {
				if !seen[it.ID] {
					seen[it.ID] = true
					out.Items = append(out.Items, it)
				}
			}
			if page.NextCursor == nil {
				return out
			}
			cursor = *page.NextCursor
		}
		limit /= 2
	}
}

// LoadProjects reads one page of up to limit projects (at most the
// service's page limit) sorted by title; cursor continues a previous page of
// the same size.
func LoadProjects(ctx context.Context, d Doer, cursor string, limit int) (litclient.Page[protocol.Project], error) {
	var page litclient.Page[protocol.Project]
	err := d.Do(ctx, func(ctx context.Context, s *litclient.Session) error {
		if m := s.Meta.Limits.MaxPage; m > 0 {
			limit = min(limit, m)
		}
		var err error
		page, err = litclient.List[protocol.Project](ctx, s, litclient.Query{Sort: "title", Limit: limit, Cursor: cursor})
		return err
	})
	return page, err
}
