package filter

import (
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

func summary(id, state string, labels []string, assignee string, claimed bool) protocol.IssueSummary {
	i := protocol.IssueSummary{ID: id, Title: id, State: state, Labels: labels, Claimed: claimed}
	if assignee != "" {
		i.Assignee = &assignee
	}
	return i
}

// Kinds combine with AND; labels and assignees match any of their values;
// "" stands for unassigned; the search accepts only the issues it found.
func TestCriteriaCombineKindsWithAnd(t *testing.T) {
	bug := summary("bug", "open", []string{"bug", "ui"}, "nick", true)
	docs := summary("docs", "closed", []string{"docs"}, "", false)
	plain := summary("plain", "open", []string{}, "anna", false)
	found := map[string]bool{"bug": true, "docs": true}
	cases := []struct {
		name string
		c    Criteria
		want []string
	}{
		{"none", Criteria{}, []string{"bug", "docs", "plain"}},
		{"state", Criteria{State: "open"}, []string{"bug", "plain"}},
		{"closed", Criteria{State: "closed"}, []string{"docs"}},
		{"claimed", Criteria{Claimed: Claimed}, []string{"bug"}},
		{"unclaimed", Criteria{Claimed: Unclaimed}, []string{"docs", "plain"}},
		{"labels any of", Criteria{Labels: []string{"ui", "docs"}}, []string{"bug", "docs"}},
		{"unlabelled never matches a label", Criteria{Labels: []string{"none"}}, nil},
		{"assignee", Criteria{Assignees: []string{"anna"}}, []string{"plain"}},
		{"unassigned", Criteria{Assignees: []string{""}}, []string{"docs"}},
		{"assignee any of", Criteria{Assignees: []string{"", "nick"}}, []string{"bug", "docs"}},
		{"search", Criteria{Search: "x"}, []string{"bug", "docs"}},
		{"and across kinds", Criteria{State: "open", Labels: []string{"ui", "docs"}, Search: "x"}, []string{"bug"}},
		{"and with no result", Criteria{State: "closed", Claimed: Claimed}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, i := range []protocol.IssueSummary{bug, docs, plain} {
				if tc.c.Match(i, found) {
					got = append(got, i.ID)
				}
			}
			if len(got) != len(tc.want) {
				t.Fatalf("matched %v, want %v", got, tc.want)
			}
			for n := range got {
				if got[n] != tc.want[n] {
					t.Fatalf("matched %v, want %v", got, tc.want)
				}
			}
			if tc.c.Active() != (tc.name != "none") {
				t.Fatalf("active %v", tc.c.Active())
			}
		})
	}
}

// The summary names every active filter, sanitized.
func TestCriteriaSummary(t *testing.T) {
	c := Criteria{State: "open", Claimed: Unclaimed, Labels: []string{"bug", "u\x1bi"}, Assignees: []string{"", "nick"}, Search: "re\nnewal"}
	want := `open · unclaimed · label bug, u␛i · assignee unassigned, nick · search "re␤newal"`
	if got := c.Summary(); got != want {
		t.Fatalf("summary %q, want %q", got, want)
	}
	if got := (Criteria{}).Summary(); got != "" {
		t.Fatalf("empty summary %q", got)
	}
}
