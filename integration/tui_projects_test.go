package integration

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/tui/tuitest"
)

// These scenarios administer projects with the real lit-tui in a private
// tmux server against a temporary lit-server (title prefixes: the service's
// defaults): an invalid title is refused before any preview and a valid one
// creates the project, which lit-tui then browses; the specification is
// edited in a real editor through a project_spec draft; the repository list
// changes through an exact preview; and a revision conflict keeps the
// specification's draft.

func (x *editEnv) projectRecord(ref string) protocol.Project {
	x.t.Helper()
	got, err := litclient.Get[protocol.Project](context.Background(), x.agent, "", ref)
	if err != nil || len(got) != 1 {
		x.t.Fatalf("project %s: %v %v", ref, got, err)
	}
	return got[0]
}

func (x *editEnv) agentSpec(text string) {
	x.t.Helper()
	tuitest.Apply(x.t, x.agent, litclient.UpdateProject("test/edit", 0, litclient.ProjectChange{Description: &text}))
}

// inSpecEditor waits for the editor on the specification's draft.
func inSpecEditor(p *pane) {
	p.e.t.Helper()
	p.await("the editor on the specification's draft", func(s string) bool {
		return strings.Contains(s, "spec-test-edit--") && !strings.Contains(s, "Session: client")
	})
}

func TestTUIProjectsInTmux(t *testing.T) {
	t.Run("create a project and switch to it", createProject)
	for _, ed := range editors {
		t.Run("edit the specification with "+ed.name, func(t *testing.T) { editSpec(t, ed.command) })
	}
	t.Run("edit the repositories", editRepositories)
	t.Run("a revision conflict keeps the specification draft", specConflict)
}

func createProject(t *testing.T) {
	x := newEditEnv(t, "", `{"schema_version": 1}`)
	p := x.launch("create-project")
	p.await("the tree", func(s string) bool { return strings.Contains(s, "Edited body") })
	p.keys("p")
	p.await("the switcher", func(s string) bool {
		return strings.Contains(s, "Switch project") && strings.Contains(s, "n new project")
	})
	p.keys("n")
	p.await("the form", func(s string) bool {
		return strings.Contains(s, "New project") && strings.Contains(s, "allowed prefixes feat, fix") && strings.Contains(s, "Title: ")
	})
	p.text("nope/thing")
	p.keys("Enter")
	screen := p.await("refused before the preview", func(s string) bool {
		return strings.Contains(s, "“nope” is not an allowed prefix; the service allows feat, fix")
	})
	if strings.Contains(screen, "Create the project") || strings.Contains(screen, "Previewing") {
		t.Fatalf("an invalid title was previewed:\n%s", screen)
	}
	if _, err := litclient.Get[protocol.Project](context.Background(), x.agent, "", "nope/thing"); err == nil {
		t.Fatal("an invalid project was created")
	}
	p.keys("C-u")
	p.text("test/created")
	p.keys("Enter")
	p.await("the preview", func(s string) bool {
		return strings.Contains(s, "Create the project “test/created”") && strings.Contains(s, "• project “test/created”") &&
			strings.Contains(s, "enter apply")
	})
	p.keys("Enter")
	p.await("browsing the new project", func(s string) bool {
		return strings.HasPrefix(p.header(), "lit · test/created · 0 issues") && strings.Contains(s, "Created “test/created”: S writes its specification, R its repositories.")
	})
	if got := x.projectRecord("test/created"); got.Description != "" || len(got.RepositoryRefs) != 0 {
		t.Fatalf("created %+v", got)
	}
	p.keys("p")
	p.await("listed in the switcher", func(s string) bool {
		return strings.Contains(s, "test/created  (current)") && strings.Contains(s, "test/edit")
	})
	p.keys("Escape")
	p.await("the new project", func(s string) bool { return strings.Contains(s, "No issues in this project.") })
	p.quit()
}

func editSpec(t *testing.T, editor string) {
	x := newEditEnv(t, editor, `{"schema_version": 1}`)
	before := x.projectRecord("test/edit")
	p := x.launch("edit-spec")
	p.await("the tree", func(s string) bool { return strings.Contains(s, "Edited body") })
	p.keys("S")
	inSpecEditor(p)
	if files := x.draftFiles(); len(files) != 1 {
		t.Fatalf("draft files %v", files)
	}
	if text := x.draftText(); text != before.Description {
		t.Fatalf("the draft holds %q, the specification is %q", text, before.Description)
	}
	p.replaceAndSave("The specification written in the editor.")
	rev := strconv.FormatInt(before.Revision, 10)
	p.await("the preview", func(s string) bool {
		return strings.Contains(s, "Apply the draft of the specification of “test/edit”?") && strings.Contains(s, "-spec") &&
			strings.Contains(s, "+The specification written in the editor.") && strings.Contains(s, "project “test/edit” at revision "+rev+" (changed)") &&
			strings.Contains(s, "Applying is optimistic")
	})
	p.keys("Enter")
	p.await("applied", func(s string) bool {
		return strings.Contains(s, "Applied the draft of the specification of “test/edit”: revision "+rev+" → "+strconv.FormatInt(before.Revision+1, 10)+".")
	})
	if got := x.projectRecord("test/edit"); got.Description != "The specification written in the editor.\n" || got.Revision != before.Revision+1 {
		t.Fatalf("project %+v", got)
	}
	if c := x.claim(); c != nil {
		t.Fatalf("editing the specification claimed the issue: %+v", c)
	}
	if files := x.draftFiles(); len(files) != 0 {
		t.Fatalf("the draft was kept: %v", files)
	}
	p.quit()
}

func editRepositories(t *testing.T) {
	x := newEditEnv(t, "", `{"schema_version": 1}`)
	before := x.projectRecord("test/edit")
	p := x.launch("edit-repositories")
	p.await("the tree", func(s string) bool { return strings.Contains(s, "Edited body") })
	p.keys("R")
	p.await("the list", func(s string) bool { return strings.Contains(s, "Repositories of “test/edit”") })
	p.text("https://example.org/team/api")
	p.await("offered", func(s string) bool {
		return strings.Contains(s, "add the repository “https://example.org/team/api”")
	})
	p.keys("Enter", "Home", "Enter")
	p.await("the preview", func(s string) bool {
		return strings.Contains(s, "Change the repositories of “test/edit”: +https://example.org/team/api") &&
			strings.Contains(s, "project “test/edit” · revision "+strconv.FormatInt(before.Revision, 10))
	})
	p.keys("Enter")
	p.await("applied", func(s string) bool { return strings.Contains(s, "Applied: Change the repositories of “test/edit”") })
	got := x.projectRecord("test/edit")
	if !slices.Equal(got.RepositoryRefs, []string{"https://example.org/team/api"}) || got.Revision != before.Revision+1 {
		t.Fatalf("project %+v", got)
	}
	p.keys("R")
	p.await("the current list", func(s string) bool { return strings.Contains(s, "[x] https://example.org/team/api") })
	p.keys("Enter", "Home", "Enter")
	p.await("the removal", func(s string) bool { return strings.Contains(s, "−https://example.org/team/api") })
	x.agentSpec("the agent's specification\n")
	p.keys("Enter") // the project changed after the preview
	p.await("refused", func(s string) bool { return strings.Contains(s, "Refused (revision_conflict)") })
	if got := x.projectRecord("test/edit"); !slices.Equal(got.RepositoryRefs, []string{"https://example.org/team/api"}) {
		t.Fatalf("the refused change wrote: %+v", got)
	}
	p.keys("Escape") // wait before the next key, or ESC q reads as alt+q
	p.await("closed", func(s string) bool { return !strings.Contains(s, "Refused") && strings.Contains(s, "Edited body") })
	p.quit()
}

func specConflict(t *testing.T) {
	x := newEditEnv(t, "nvim --clean", `{"schema_version": 1}`)
	p := x.launch("spec-conflict")
	p.await("the tree", func(s string) bool { return strings.Contains(s, "Edited body") })
	p.keys("S")
	inSpecEditor(p)
	p.replaceAndSave("My specification.")
	p.await("the preview", func(s string) bool {
		return strings.Contains(s, "Apply the draft of the specification of “test/edit”?")
	})
	x.agentSpec("the agent's specification\n") // after the preview
	p.keys("Enter")
	p.await("the apply refused", func(s string) bool {
		return strings.Contains(s, "Cannot apply the draft of the specification of “test/edit”") &&
			strings.Contains(s, "The specification of “test/edit” changed after the preview.") && strings.Contains(s, "the draft is kept")
	})
	if got := x.projectRecord("test/edit"); got.Description != "the agent's specification\n" {
		t.Fatalf("the refused apply wrote: %q", got.Description)
	}
	if text := x.draftText(); text != "My specification.\n" {
		t.Fatalf("draft %q", text)
	}
	p.keys("Escape")
	p.await("kept", func(s string) bool {
		return strings.Contains(s, "Kept the draft of the specification of “test/edit”")
	})
	p.keys("S") // the project changed: the draft opens as a conflict
	p.await("the conflict", func(s string) bool {
		return strings.Contains(s, "The specification of “test/edit” changed since the draft was started") &&
			strings.Contains(s, "Diff against the current specification")
	})
	p.keys("Enter")
	p.await("the diff", func(s string) bool {
		return strings.Contains(s, "-the agent's specification") && strings.Contains(s, "+My specification.")
	})
	p.keys("Escape")
	p.await("kept", func(s string) bool {
		return strings.Contains(s, "Kept the draft of the specification of “test/edit”")
	})
	p.quitKeepingDrafts("“test/edit”")
	if text := x.draftText(); text != "My specification.\n" {
		t.Fatalf("quitting changed the draft: %q", text)
	}
}
