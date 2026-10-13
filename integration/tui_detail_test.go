package integration

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/tui/tuitest"
)

// hostileBody and hostileComment try to clear the screen, retitle the
// window, write the clipboard, leave the alternate screen, ring the bell and
// reorder text. None of it may reach the terminal.
const (
	hostileBody = "## Rollout plan\n\nShip it **carefully**.\n\n" +
		"clear\x1b[2J\x1b[Htitle\x1b]2;pwned\x07 bell\x07 ‮evil‬\n\n" +
		"```go\nfunc main() { println(\"hi\") }\n```\n"
	hostileComment = "Reviewed: *looks good*.\n\nclip\x1b]52;c;cHduZWQ=\x07 alt\x1b[?1049l done"
)

// controls matches the escape sequences capture-pane -e may emit for a grid
// that holds only text and colours: SGR, and OSC 8 hyperlinks.
var controls = regexp.MustCompile(`\x1b\[[0-9;:]*m|\x1b\]8;[^\x07\x1b]*(\x07|\x1b\\)`)

// Story 1 of the shared specification: I run lit in a terminal, expand a deep
// branch, open an issue and read its rendered body and comments. Escape
// sequences in them show as visible markers and do not change the terminal.
func TestTUIIssueDetailInTmux(t *testing.T) {
	e := newTUIEnv(t)
	e.bin = cliBin // bare lit in a terminal hands over to the lit-tui beside it
	s := e.seeder(protocol.Actor{Name: "Claude", Kind: "agent"})
	tuitest.Apply(t, s, litclient.CreateProject("test/read", "spec"))
	parent := ""
	for d := 0; d < 5; d++ {
		parent = tuitest.CreateIssues(t, s, "test/read", parent, fmt.Sprintf("Depth %d", d))[0]
	}
	req := litclient.CreateIssue("test/read", "Hostile rollout", hostileBody, parent)
	tuitest.Apply(t, s, req)
	tuitest.Apply(t, s, litclient.CreateComment("test/read", req.Items[0].ID, hostileComment))
	tuitest.CreateIssues(t, s, "test/read", "", "Another root")

	p := e.launch("detail")
	e.tmux("set-option", "-s", "set-clipboard", "on") // so an OSC 52 that got through would create a buffer
	p.await("the tree", func(s string) bool { return strings.Contains(s, "Depth 0") && strings.Contains(s, "Another root") })
	title := strings.TrimSpace(e.tmux("display", "-p", "-t", "detail", "#{pane_title}"))
	for d := 0; d < 5; d++ {
		p.keys("Right", "Right")
	}
	p.await("the deep issue's breadcrumb", func(string) bool { return strings.HasSuffix(p.crumb(), "Depth 4 › Hostile rollout") })
	p.keys("Enter")
	screen := p.await("the rendered body and the comment", func(s string) bool {
		return strings.Contains(s, "Ship it carefully.") && !strings.Contains(s, "**carefully**") && strings.Contains(s, `func main() { println("hi") }`) &&
			strings.Contains(s, "Comments (1)") && strings.Contains(s, "Claude · ") && strings.Contains(s, "Reviewed: looks good.")
	})
	// The terminal is unchanged: still the alternate screen with the tree
	// drawn, the same title, no clipboard buffer, and a grid of only text
	// and colours.
	raw := e.tmux("capture-pane", "-e", "-p", "-t", "detail")
	plain := controls.ReplaceAllString(raw, "")
	for i, r := range plain {
		if r < 0x20 && r != '\n' || r == 0x7f || r >= 0x80 && r < 0xa0 || r == '\u202e' || r == '\u202c' {
			t.Fatalf("control %U in the captured grid at %d: %q", r, i, plain[max(0, i-40):min(len(plain), i+40)])
		}
	}
	if !strings.Contains(plain, "Another root") || !strings.Contains(plain, "test/read ›") {
		t.Fatalf("the screen was cleared:\n%s", plain)
	}
	if p.alternate() != "1" {
		t.Fatal("lit-tui left the alternate screen")
	}
	if got := strings.TrimSpace(e.tmux("display", "-p", "-t", "detail", "#{pane_title}")); got != title || strings.Contains(got, "pwned") {
		t.Fatalf("the title changed from %q to %q", title, got)
	}
	if buffers := strings.TrimSpace(e.tmux("list-buffers")); buffers != "" {
		t.Fatalf("the clipboard was written: %q", buffers)
	}
	// Instead, the sequences show as visible markers.
	for _, marker := range []string{"clear␛[2J␛[Htitle␛]2;pwned␇ bell␇ ⟨U+202E⟩evil⟨U+202C⟩", "clip␛]52;c;cHduZWQ=␇ alt␛[?1049l done"} {
		if !strings.Contains(screen, marker) {
			t.Fatalf("missing visible marker %q:\n%s", marker, screen)
		}
	}

	p.quit()
}
