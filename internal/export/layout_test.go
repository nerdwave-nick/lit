package export

import (
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

func TestSlugIsShortLowercaseASCII(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"Plan the manual lit TUI", "plan-the-manual-lit-tui"},
		{"Straße, Ünïcode & Café", "strasse-unicode-cafe"},
		{"  --Leading and trailing!!  ", "leading-and-trailing"},
		{"C++ and net/http: 100% (APP-123)", "c-and-net-http-100-app-123"},
		{"日本語", "untitled"},
		{"", "untitled"},
		// At most 40 characters, cut at a word boundary in the second half.
		{"Choose TUI data-volume targets and list projection scope", "choose-tui-data-volume-targets-and-list"},
		{"Supercalifragilisticexpialidocious and more words here", "supercalifragilisticexpialidocious-and"},
		// Without a boundary in the second half the cut is hard.
		{"A " + strings.Repeat("b", 50), "a-" + strings.Repeat("b", 38)},
		{strings.Repeat("a", 60), strings.Repeat("a", 40)},
	} {
		got := slug(c.in)
		if got != c.want {
			t.Errorf("slug(%q) = %q, want %q", c.in, got, c.want)
		}
		if len(got) > maxSlug {
			t.Errorf("slug(%q) has %d characters", c.in, len(got))
		}
	}
}

func TestProjectDirFollowsTheTitle(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"feat/lit-tui", "feat/lit-tui"},
		{"feat/a_b/c-d", "feat/a_b/c-d"},
		// A segment that would land in a parent project's record directory
		// is escaped with a character titles cannot start with.
		{"feat/x/issues", "feat/x/_issues"},
		{"feat/x/milestones/y", "feat/x/_milestones/y"},
		{"feat/issues", "feat/issues"},
		// Defensive: unexpected characters never escape the tree.
		{"feat/../x", "feat/_/x"},
		{"feat/a b", "feat/a-b"},
	} {
		if got := projectDir(c.in); got != c.want {
			t.Errorf("projectDir(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRelativeLinks(t *testing.T) {
	for _, c := range []struct{ from, to, want string }{
		{"feat/a/issues/x.md", "feat/a/issues/y.md", "y.md"},
		{"feat/a/issues/x.md", "feat/a/project.md", "../project.md"},
		{"feat/a/issues/x.md", "fix/b/issues/y.md", "../../../fix/b/issues/y.md"},
		{"feat/a/milestones/m.md", "feat/a/issues/y.md", "../issues/y.md"},
		{"README.md", "feat/a/project.md", "feat/a/project.md"},
		{"feat/a/project.md", "feat/a/b/project.md", "b/project.md"},
	} {
		if got := rel(c.from, c.to); got != c.want {
			t.Errorf("rel(%q, %q) = %q, want %q", c.from, c.to, got, c.want)
		}
	}
}

func TestLayoutKeepsTheOlderRecordsName(t *testing.T) {
	p := protocol.Project{ID: "11111111-0000-4000-8000-000000000001", Title: "feat/x"}
	nested := protocol.Project{ID: "22222222-0000-4000-8000-000000000002", Title: "feat/x/issues"}
	tr := &tracker{
		projects: []protocol.Project{p, nested},
		issues: []protocol.Issue{
			{ID: "aaaaaaaa-0000-4000-8000-000000000002", ProjectID: p.ID, Title: "Same title!", CreatedAt: "2026-01-01T00:00:01.000000Z"},
			{ID: "aaaaaaaa-0000-4000-8000-000000000001", ProjectID: p.ID, Title: "Same title?", CreatedAt: "2026-01-01T00:00:02.000000Z"},
		},
		milestones: []protocol.Milestone{
			{ID: "ffffffff-0000-4000-8000-000000000001", ProjectID: p.ID, Title: "M1: Export", CreatedAt: "2026-01-01T00:00:02.000000Z"},
			{ID: "eeeeeeee-0000-4000-8000-000000000002", ProjectID: p.ID, Title: "M1 export", CreatedAt: "2026-01-01T00:00:01.000000Z"},
		},
	}
	tr.layout()
	for got, want := range map[string]string{
		tr.dir[nested.ID]: "feat/x/_issues",
		tr.issueFile["aaaaaaaa-0000-4000-8000-000000000002"]:     "feat/x/issues/aaaaaaaa-same-title.md",
		tr.issueFile["aaaaaaaa-0000-4000-8000-000000000001"]:     "feat/x/issues/aaaaaaaa-0000-4000-8000-000000000001-same-title.md",
		tr.milestoneFile["eeeeeeee-0000-4000-8000-000000000002"]: "feat/x/milestones/m1-export.md",
		tr.milestoneFile["ffffffff-0000-4000-8000-000000000001"]: "feat/x/milestones/m1-export-ffffffff.md",
	} {
		if got != want {
			t.Errorf("path %q, want %q", got, want)
		}
	}
}
