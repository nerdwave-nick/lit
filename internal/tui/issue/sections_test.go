package issue_test

import (
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/tui/detail"
	"github.com/nerdwave-nick/lit/internal/tui/issue"
)

const issueID = "11111111-2222-4333-8444-555555555555"

func contents(secs []detail.Section) []string {
	var out []string
	for _, s := range secs {
		out = append(out, s.Content)
	}
	return out
}

// The body and every comment are sections of their own, keyed by record and
// revision; comment headings name the author and time literally.
func TestSectionsShowTheBodyAndEachComment(t *testing.T) {
	d := &issue.Detail{Issue: &protocol.Issue{ID: issueID, Revision: 7, Body: "# Plan\n\n```go\nx := 1\n```"}, Consistent: true,
		Comments: []protocol.Comment{
			{ID: "c1", Revision: 1, Author: "Claude *agent*", CreatedAt: "2026-10-09T10:00:00Z", UpdatedAt: "2026-10-09T10:00:00Z", Body: "```\nunclosed fence"},
			{ID: "c2", Revision: 3, Author: "person", CreatedAt: "2026-10-09T11:00:00Z", UpdatedAt: "2026-10-09T12:00:00Z", Body: "second"},
		}}
	secs := issue.Sections(issueID, d)
	if len(secs) != 4 {
		t.Fatalf("sections: %q", contents(secs))
	}
	if secs[0].RecordID != issueID+"/body" || secs[0].Revision != 7 || secs[0].Content != d.Issue.Body {
		t.Fatalf("body section: %+v", secs[0])
	}
	if secs[1].RecordID != issueID+"/comments" || !strings.Contains(secs[1].Content, "## Comments (2)") {
		t.Fatalf("comments heading: %+v", secs[1])
	}
	c1, c2 := secs[2], secs[3]
	if c1.RecordID != "c1" || c1.Revision != 1 || !strings.HasPrefix(c1.Content, `**Claude \*agent\*** · 2026\-10\-09T10:00:00Z`) ||
		!strings.HasSuffix(c1.Content, "\n\n```\nunclosed fence") || strings.Contains(c1.Content, "edited") {
		t.Fatalf("first comment: %+v", c1)
	}
	if c2.RecordID != "c2" || c2.Revision != 3 || !strings.Contains(c2.Content, `edited 2026\-10\-09T12:00:00Z`) {
		t.Fatalf("edited comment: %+v", c2)
	}
}

// Loading, empty, paged and failed loads each say so.
func TestSectionsDescribeTheLoad(t *testing.T) {
	failed := protocol.E(503, "timeout", "lit-tui is busy <now>")
	for _, tc := range []struct {
		name string
		d    *issue.Detail
		want []string
	}{
		{"loading", nil, []string{issue.Loading}},
		{"empty", &issue.Detail{Issue: &protocol.Issue{ID: issueID}, Consistent: true}, []string{issue.NoBody, "---\n\n## Comments\n\n" + issue.NoComments}},
		{"paged", &issue.Detail{Issue: &protocol.Issue{ID: issueID, Body: "b"}, Comments: []protocol.Comment{{ID: "c1", Body: "x"}}},
			[]string{"b", "---\n\n## Comments\n\n" + issue.PagedNote, "x"}},
		{"issue failed", &issue.Detail{Err: failed}, []string{`*Could not load the issue: timeout: lit\-tui is busy \<now\>*`}},
		{"pages failed", &issue.Detail{Issue: &protocol.Issue{ID: issueID, Body: "b"}, Comments: []protocol.Comment{{ID: "c1", Body: "x"}}, Err: failed},
			[]string{"b", "---\n\n## Comments\n\n" + issue.PagedNote + "\n\n" + `*Comments stopped loading: timeout: lit\-tui is busy \<now\>*`, "x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := contents(issue.Sections(issueID, tc.d))
			if len(got) != len(tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			for i := range got {
				if !strings.HasSuffix(got[i], tc.want[i]) {
					t.Fatalf("section %d: got %q, want suffix %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// Escape makes tracker text literal Markdown on one line.
func TestEscapeKeepsTrackerTextLiteral(t *testing.T) {
	if got := issue.Escape("*a* [b](c)\n`d` #e"); got != "\\*a\\* \\[b\\]\\(c\\) \\`d\\` \\#e" {
		t.Fatalf("got %q", got)
	}
}
