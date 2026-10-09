// Package draftsview is lit-tui's view of the unapplied TUI drafts that
// outlive a session: the launch banner, the Drafts view listing them, their
// marks in the tree, and the one prompt shown when quitting with open drafts.
//
// It lists drafts generically by kind (drafts.Kind): an issue body today,
// comments, new issues, specifications and milestone descriptions later. It
// only reads the draft store; resuming, diffing and discarding a draft are
// the root model's, per kind. Drafts are removed only by a successful apply
// or an explicit discard, never by an exit, a crash or recovery.
package draftsview

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/drafts"
)

// Gate is the lifecycle's gate (lifecycle.Lifecycle.Do).
type Gate interface {
	Do(ctx context.Context, fn func(ctx context.Context, s *litclient.Session) error) error
}

// Loaded is the result of Load.
type Loaded struct {
	// Service is the lit service the drafts belong to.
	Service string
	// Drafts are that service's drafts in every project, oldest first.
	Drafts []drafts.Draft
	// Err reports drafts that could not be read (the others are listed) or
	// that the service could not be determined.
	Err error
}

// Load lists the drafts of service in every project of store. An empty
// service is read from the session through g first; with neither, every
// draft is listed. Drafts of another lit service are left out: they cannot
// be applied here.
func Load(ctx context.Context, g Gate, store drafts.Store, service string) Loaded {
	l := Loaded{Service: service}
	if l.Service == "" && g != nil {
		if err := g.Do(ctx, func(_ context.Context, s *litclient.Session) error {
			l.Service = s.Meta.ServiceID
			return nil
		}); err != nil {
			l.Err = err
			return l
		}
	}
	all, err := store.ListAll()
	for _, d := range all {
		if l.Service == "" || d.ServiceID == l.Service {
			l.Drafts = append(l.Drafts, d)
		}
	}
	sort.SliceStable(l.Drafts, func(i, j int) bool { return l.Drafts[i].CreatedAt < l.Drafts[j].CreatedAt })
	l.Err = err
	return l
}

// Banner is the launch banner for n unapplied drafts: "1 unapplied draft",
// "3 unapplied drafts"; "" for none.
func Banner(n int) string {
	switch {
	case n <= 0:
		return ""
	case n == 1:
		return "1 unapplied draft"
	}
	return fmt.Sprintf("%d unapplied drafts", n)
}

// KindLabel names a draft kind for people: "issue body" for issue_body,
// "new comment" and "comment" for the comment kinds.
func KindLabel(k drafts.Kind) string {
	switch k {
	case "":
		return "draft"
	case drafts.CommentNew:
		return "new comment"
	case drafts.CommentBody:
		return "comment"
	}
	return strings.ReplaceAll(string(k), "_", " ")
}

// Key identifies a draft: its kind and record (drafts.Draft.Key).
func Key(d drafts.Draft) string { return d.Key() }

// Revision is an issue's current revision, if known (from the tree).
type Revision func(issue string) (int64, bool)

// Changed reports whether d edits a record whose current revision is known
// and differs from the revision d was started at: it opens as a conflict.
// Only an issue body's record is the issue itself; other kinds compare their
// own records when opened.
func Changed(d drafts.Draft, current Revision) (now int64, changed bool) {
	if current == nil || d.Kind != drafts.IssueBody {
		return 0, false
	}
	rev, ok := current(d.IssueID)
	return rev, ok && rev != d.Revision
}

// Marks are the tree badges of drafts, by owning issue: "draft" for an
// issue body ("draft: changed" when its issue's revision changed since), and
// "<kind> draft" for other kinds, joined when an issue has several.
func Marks(ds []drafts.Draft, current Revision) map[string]string {
	marks := map[string]string{}
	for _, d := range ds {
		if d.IssueID == "" {
			continue
		}
		mark := KindLabel(d.Kind) + " draft"
		if d.Kind == drafts.IssueBody {
			mark = "draft"
			if _, changed := Changed(d, current); changed {
				mark = "draft: changed"
			}
		}
		if prev := marks[d.IssueID]; prev != "" {
			mark = prev + ", " + mark
		}
		marks[d.IssueID] = mark
	}
	return marks
}

// errorLine describes Loaded.Err in one line.
func errorLine(err error) string {
	if err == nil {
		return ""
	}
	return "Some drafts could not be read: " + strings.ReplaceAll(err.Error(), "\n", "; ")
}
