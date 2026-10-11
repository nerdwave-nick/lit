package export

import (
	"strings"
	"testing"
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
