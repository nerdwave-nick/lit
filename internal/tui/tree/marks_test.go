package tree

import (
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

// Marks (such as a lost claim) are badges kept visible however narrow the
// pane, sanitized, and replaced as a whole by the next SetMarks.
func TestMarksAreShownAsBadges(t *testing.T) {
	m := New()
	m.SetSize(30, 3)
	m.SetIssues([]protocol.IssueSummary{iss("a", "", 1, "A title long enough to be cut in a narrow pane"), iss("b", "", 2, "Other")})
	m.SetMarks(map[string]string{"a": "lost\x1b[31m"})
	fits(t, m, 30, 3)
	got := plain(m)
	if !strings.Contains(got[0], "lost") || !strings.Contains(got[0], "…") || strings.Contains(got[0], "\x1b") || strings.Contains(got[1], "lost") {
		t.Fatalf("marks: %q", got)
	}
	m.SetMarks(nil)
	if strings.Contains(strings.Join(plain(m), "\n"), "lost") {
		t.Fatalf("marks not cleared: %q", plain(m))
	}
}
