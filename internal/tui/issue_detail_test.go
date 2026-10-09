package tui

import (
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/tuitest"
)

const planBody = "## Plan\n\nThe **body** of the plan.\n\n```go\nfmt.Println(\"hi\")\n```\n"

// discussion creates project with three roots: "Plan" with a body and two
// comments (by the agent "Claude" and by "person"), then "Second root" and
// "Third root". It returns the agent's session and the Plan's UUID.
func discussion(t *testing.T, srv *tuitest.Server, project string) (*litclient.Session, string) {
	t.Helper()
	_, s := tuitest.InstanceAs(t, t.TempDir(), srv.URL, protocol.Actor{Name: "Claude", Kind: "agent"})
	tuitest.Apply(t, s, litclient.CreateProject(project, "spec"))
	req := litclient.CreateIssue(project, "Plan", planBody, "")
	tuitest.Apply(t, s, req)
	id := req.Items[0].ID
	tuitest.CreateIssues(t, s, project, "", "Second root", "Third root")
	tuitest.Apply(t, s, litclient.CreateComment(project, id, "First comment, with `code`."))
	_, p := tuitest.InstanceAs(t, t.TempDir(), srv.URL, protocol.Actor{Name: "person", Kind: "human"})
	tuitest.Apply(t, p, litclient.CreateComment(project, id, "A *second* comment."))
	return s, id
}

// Story 1: the detail of the selection shows its metadata, then its rendered
// body and its comments with their authors, loaded when it is shown.
func TestDetailShowsTheBodyAndCommentsOfTheSelection(t *testing.T) {
	srv := tuitest.Service(t)
	discussion(t, srv, "test/one")
	b := startBrowsing(t, srv, 140, 40, false)
	b.expect("metadata", b.bodyLine("State: open") != "")
	b.expect("rendered body", b.bodyLine("The body of the plan.") != "" && b.bodyLine(`fmt.Println("hi")`) != "" && b.bodyLine("**body**") == "")
	b.expect("comments heading", b.bodyLine("Comments (2)") != "")
	first, second := b.bodyLine("Claude · "), b.bodyLine("person · ")
	b.expect("comment authors in order", first != "" && second != "" && strings.Index(b.view(), "Claude · ") < strings.Index(b.view(), "person · "))
	b.expect("comment bodies", b.bodyLine("First comment, with") != "" && b.bodyLine("`code`") == "" && b.bodyLine("A second comment.") != "")

	b.press("down")
	b.expect("the next issue's body replaces it", b.bodyLine("body of Second root") != "" && b.bodyLine("The body of the plan.") == "" &&
		b.bodyLine("Comments") != "" && b.bodyLine("No comments.") != "" && b.bodyLine("Claude · ") == "")
}

// On narrow terminals the detail is a separate view: moving the selection
// reads nothing, and the body loads when the detail opens.
func TestNarrowDetailLoadsWhenItOpens(t *testing.T) {
	srv := tuitest.Service(t)
	discussion(t, srv, "test/one")
	b := startBrowsing(t, srv, 80, 30, false)
	reads := srv.Count("POST /v1/snapshots")
	b.press("down", "up")
	if got := srv.Count("POST /v1/snapshots"); got != reads {
		t.Fatalf("moving the selection read %d snapshots", got-reads)
	}
	b.press("enter")
	b.expect("the opened detail shows the body and comments", b.bodyLine("The body of the plan.") != "" && b.bodyLine("Comments (2)") != "")
	b.press("esc", "enter")
	b.expect("reopening shows it again", b.bodyLine("The body of the plan.") != "")
	b.press("esc", "down")
	b.send(tea.WindowSizeMsg{Width: 140, Height: 30})
	b.expect("widening shows the new selection's body beside the tree", strings.Contains(b.bodyLine("body of Second root"), "│"))
}

// A refresh while the narrow detail is closed reads no body, but the next
// opening shows the current one, not the body read before the refresh.
func TestNarrowDetailReopensWithTheCurrentBody(t *testing.T) {
	srv := tuitest.Service(t)
	s, id := discussion(t, srv, "test/one")
	b := startBrowsing(t, srv, 80, 30, false)
	b.press("enter")
	b.expect("loaded", b.bodyLine("The body of the plan.") != "")
	b.press("esc")
	issues, err := litclient.Get[protocol.Issue](t.Context(), s, "test/one", id)
	if err != nil {
		t.Fatal(err)
	}
	body := "A rewritten body."
	tuitest.Apply(t, s, litclient.UpdateIssue("test/one", id, issues[0].Revision, litclient.IssueChange{Body: &body}))
	reads := srv.Count("POST /v1/snapshots")
	b.press("r")
	if got := srv.Count("POST /v1/snapshots"); got != reads {
		t.Fatalf("the refresh read %d snapshots for a closed detail", got-reads)
	}
	b.press("enter")
	b.expect("the current body", b.bodyLine("A rewritten body.") != "" && b.bodyLine("The body of the plan.") == "")
}

// A load for an issue that is no longer selected is dropped: its result
// never replaces the detail of the current selection.
func TestStaleDetailLoadsAreDropped(t *testing.T) {
	srv := tuitest.Service(t)
	discussion(t, srv, "test/one")
	b := startBrowsing(t, srv, 140, 40, false)
	m, toSecond := b.m.Update(keyMsg("down"))
	m, toThird := m.Update(keyMsg("down"))
	m = drive(t, m, toThird)
	b.m = drive(t, m, toSecond) // Second root's load arrives last
	b.expect("still the third issue's body", strings.HasSuffix(strings.TrimSpace(b.crumb()), "Third root") &&
		b.bodyLine("body of Third root") != "" && b.bodyLine("body of Second root") == "" && b.bodyLine("Could not load") == "")
}

// Refresh reloads the detail of the selection: a new body and a new comment
// appear, and an edited comment says so.
func TestRefreshReloadsTheDetail(t *testing.T) {
	srv := tuitest.Service(t)
	s, id := discussion(t, srv, "test/one")
	b := startBrowsing(t, srv, 140, 40, false)
	b.expect("loaded", b.bodyLine("Comments (2)") != "")
	issues, err := litclient.Get[protocol.Issue](t.Context(), s, "test/one", id)
	if err != nil {
		t.Fatal(err)
	}
	body := "A rewritten body."
	tuitest.Apply(t, s, litclient.UpdateIssue("test/one", id, issues[0].Revision, litclient.IssueChange{Body: &body}))
	tuitest.Apply(t, s, litclient.CreateComment("test/one", id, "A third comment."))
	comments, err := litclient.List[protocol.Comment](t.Context(), s, litclient.Query{IssueID: id})
	if err != nil {
		t.Fatal(err)
	}
	tuitest.Apply(t, s, litclient.UpdateComment(comments.Items[0].ID, comments.Items[0].Revision, "First comment, edited."))
	b.press("r")
	b.expect("the new body and comments", b.bodyLine("A rewritten body.") != "" && b.bodyLine("The body of the plan.") == "" &&
		b.bodyLine("Comments (3)") != "" && b.bodyLine("A third comment.") != "" && b.bodyLine("First comment, edited.") != "")
	b.expect("the edited comment is marked", strings.Contains(b.bodyLine("Claude · "), "edited"))
}

// allowed matches the only escape sequences that may reach the terminal from
// the detail: SGR and OSC 8 hyperlinks, generated by the renderer.
var allowed = regexp.MustCompile(`\x1b\[[0-9;:]*m|\x1b\]8;[^\x07\x1b]*(\x07|\x1b\\)`)

// Escape sequences and bidi overrides in bodies and comments show as visible
// markers and never reach the terminal.
func TestHostileBodiesAndCommentsNeverReachTheTerminal(t *testing.T) {
	srv := tuitest.Service(t)
	_, s := tuitest.InstanceAs(t, t.TempDir(), srv.URL, protocol.Actor{Name: "Mallory", Kind: "agent"})
	tuitest.Apply(t, s, litclient.CreateProject("test/hostile", "spec"))
	req := litclient.CreateIssue("test/hostile", "Hostile", "clear\x1b[2Jtitle\x1b]0;pwned\x07 bell\x07 cr\rx ‮evil‬ &#27;[8mhidden", "")
	tuitest.Apply(t, s, req)
	tuitest.Apply(t, s, litclient.CreateComment("test/hostile", req.Items[0].ID, "clip\x1b]52;c;Zm9v\x07 csi\u009b31m red ```\x1b[?1049l```"))
	b := startBrowsing(t, srv, 140, 40, false)
	b.expect("visible markers", b.bodyLine("clear␛[2Jtitle␛]0;pwned␇") != "" && b.bodyLine("⟨U+202E⟩evil⟨U+202C⟩") != "" &&
		b.bodyLine("␛[8mhidden") != "" && b.bodyLine("clip␛]52;c;Zm9v␇") != "" && b.bodyLine("⟨U+009B⟩31m") != "")
	raw := allowed.ReplaceAllString(b.m.View().Content, "")
	for i, r := range raw {
		if r < 0x20 && r != '\n' || r == 0x7f || r >= 0x80 && r < 0xa0 || r == '‮' || r == '‬' {
			t.Fatalf("control %U reaches the terminal at %d: %q", r, i, raw[max(0, i-40):min(len(raw), i+40)])
		}
	}
}

// The view and detail keys still work on a loaded detail: the detail
// scrolls to its comments.
func TestTheLoadedDetailScrolls(t *testing.T) {
	srv := tuitest.Service(t)
	discussion(t, srv, "test/one")
	b := startBrowsing(t, srv, 140, 12, false)
	b.expect("the comments are below the fold", b.bodyLine("A second comment.") == "")
	b.press("enter")
	for i := 0; i < 30 && b.bodyLine("A second comment.") == ""; i++ {
		b.send(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	b.expect("scrolled to the last comment", b.bodyLine("A second comment.") != "")
}
