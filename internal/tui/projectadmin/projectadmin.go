// Package projectadmin is lit-tui's project administration: creating a
// project, editing its shared specification (the project's description)
// through a project_spec TUI draft, and changing its repository list.
//
// Projects have no claims. Every change is optimistic and revision-fenced:
// a new project's title is checked against the service's title_prefixes
// before anything is previewed, then created through an exact preview; the
// repository list changes through an exact preview fenced at the revision
// it was read at; and a specification draft records the project's revision
// and applies only at it, so a conflict keeps the draft.
package projectadmin

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/tui/drafts"
	"github.com/nerdwave-nick/lit/internal/tui/edit"
	"github.com/nerdwave-nick/lit/internal/tui/quick"
	"github.com/nerdwave-nick/lit/internal/tui/render/sanitize"
)

// MaxTitle is the longest project title the service accepts, in bytes.
const MaxTitle = 128

// segment is one slash-separated part of a project title, as lit-server
// checks it.
var segment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// ValidTitle checks a new project's title as lit-server does: an allowed
// prefix, then one or more parts, separated by slashes; each part ASCII
// letters, digits, hyphens and underscores, starting with a letter or
// digit; at most MaxTitle bytes. The error says what is wrong.
func ValidTitle(title string, prefixes []string) error {
	parts := strings.Split(title, "/")
	switch {
	case strings.TrimSpace(title) == "":
		return refuse("Type the project's title: prefix/name, for example " + example(prefixes) + ".")
	case len(title) > MaxTitle:
		return refuse(fmt.Sprintf("A project title has at most %d characters; this one has %d.", MaxTitle, len(title)))
	case len(parts) < 2:
		return refuse("A project title is prefix/name, for example " + example(prefixes) + "; “" + sanitize.Title(title) + "” has no slash.")
	case !slices.Contains(prefixes, parts[0]):
		return refuse("“" + sanitize.Title(parts[0]) + "” is not an allowed prefix; the service allows " + strings.Join(clean(prefixes), ", ") + ".")
	}
	for _, part := range parts {
		if !segment.MatchString(part) {
			if part == "" {
				return refuse("A project title has no empty part between slashes.")
			}
			return refuse("“" + sanitize.Title(part) + "” is not a valid part: use ASCII letters, digits, - and _, starting with a letter or digit.")
		}
	}
	return nil
}

func refuse(msg string) error { return protocol.E(422, "validation_failed", msg) }

// Syntax describes the title syntax with the allowed prefixes.
func Syntax(prefixes []string) string {
	return "A project title is prefix/name, for example " + example(prefixes) + ": allowed prefixes " + strings.Join(clean(prefixes), ", ") +
		"; parts of ASCII letters, digits, - and _, each starting with a letter or digit; at most " + fmt.Sprint(MaxTitle) + " characters."
}

func example(prefixes []string) string {
	if len(prefixes) == 0 {
		return "feat/name"
	}
	return sanitize.Title(prefixes[0]) + "/name"
}

func clean(prefixes []string) []string {
	out := make([]string, len(prefixes))
	for i, p := range prefixes {
		out[i] = sanitize.Title(p)
	}
	return out
}

// Prefixes are the title prefixes the service advertises (/v1/meta).
func Prefixes(ctx context.Context, g quick.Gate) ([]string, error) {
	var out []string
	err := g.Do(ctx, func(_ context.Context, s *litclient.Session) error {
		out = append(out, s.Meta.TitlePrefixes...)
		return nil
	})
	return out, err
}

// Read reads project id now.
func Read(ctx context.Context, g quick.Gate, id string) (protocol.Project, error) {
	var p protocol.Project
	err := g.Do(ctx, func(ctx context.Context, s *litclient.Session) error {
		var err error
		p, err = read(ctx, s, id)
		return err
	})
	return p, err
}

func read(ctx context.Context, s *litclient.Session, id string) (protocol.Project, error) {
	got, err := litclient.Get[protocol.Project](ctx, s, "", id)
	if err == nil && (len(got) != 1 || got[0].ID != id) {
		err = protocol.E(500, "invalid_response", "the project read returned no single project")
	}
	if err != nil {
		return protocol.Project{}, err
	}
	return got[0], nil
}

// Created is the message after a project was created: the new project as
// read afterwards, or why it could not be read.
type Created struct {
	ID      string
	Project protocol.Project
	Err     error
}

// NewProject is the form that creates a project with an empty
// specification and no repositories. The title is checked against
// prefixes before anything is previewed; after the commit the new project
// is read and delivered as Created.
func NewProject(env quick.Env, prefixes []string) *quick.Model {
	g := env.Gate
	var id string
	return quick.StartText(env, quick.TextForm{
		Heading: "New project",
		Help:    Syntax(prefixes),
		Label:   "Title",
		Submit: func(title string) (quick.Change, string) {
			if err := ValidTitle(title, prefixes); err != nil {
				return quick.Change{}, describe(err)
			}
			c := Create(title)
			id = c.Request.Items[0].ID
			return c, ""
		},
		Applied: func(protocol.MutationResult) tea.Cmd {
			created := id
			return func() tea.Msg {
				p, err := Read(context.Background(), g, created)
				return Created{ID: created, Project: p, Err: err}
			}
		},
	})
}

// Create is the change that creates a project titled title.
func Create(title string) quick.Change {
	return quick.Change{Summary: "Create the project " + quoted(title), Request: litclient.CreateProject(title, "")}
}

// Repositories is the form that changes p's repository list, fenced at p's
// revision: the current repositories are checked; unchecking removes one,
// typing adds a new one.
func Repositories(env quick.Env, p protocol.Project) *quick.Model {
	return quick.StartSet(env, quick.SetForm{
		Title:  "Repositories of " + quoted(p.Title) + " (enter toggles)",
		Values: p.RepositoryRefs,
		Create: "add the repository ",
		Submit: func(add, remove []string) (quick.Change, string) {
			if len(add)+len(remove) == 0 {
				return quick.Change{}, "Nothing changes: toggle repositories with enter, or type a new one, first."
			}
			return RepositoryChange(p, add, remove), ""
		},
	})
}

// RepositoryChange adds and removes repositories of p, fenced at p's
// revision.
func RepositoryChange(p protocol.Project, add, remove []string) quick.Change {
	s := "Change the repositories of " + quoted(p.Title) + ":"
	for _, r := range add {
		s += " +" + sanitize.Title(r)
	}
	for _, r := range remove {
		s += " −" + sanitize.Title(r)
	}
	return quick.Change{Summary: s, Request: litclient.UpdateProject(p.ID, p.Revision, litclient.ProjectChange{AddRepositories: add, RemoveRepositories: remove})}
}

// Register makes e edit project specifications (project_spec drafts):
// optimistic, at the project's recorded revision, with no claim.
func Register(e *edit.Editor) {
	e.Register(drafts.ProjectSpec, edit.Kind{
		Read: func(ctx context.Context, s *litclient.Session, d drafts.Draft) (edit.Record, error) {
			p, err := read(ctx, s, d.RecordID)
			if err != nil {
				return edit.Record{}, err
			}
			return edit.Record{Exists: true, Title: p.Title, Revision: p.Revision, Content: p.Description}, nil
		},
		Request: func(d drafts.Draft, content string) protocol.PrepareRequest {
			return litclient.UpdateProject(d.RecordID, d.Revision, litclient.ProjectChange{Description: &content})
		},
	})
}

// Spec is the target of editing p's specification: a project's drafts
// live in its own drafts directory, and no issue owns them.
func Spec(p protocol.Project) edit.Target {
	return edit.Target{Kind: drafts.ProjectSpec, Project: p.ID, Record: p.ID, Title: p.Title}
}

func quoted(title string) string { return "“" + sanitize.Title(title) + "”" }

// describe is an error's message for a note, sanitized.
func describe(err error) string {
	var pe *protocol.Error
	if errors.As(err, &pe) {
		return sanitize.Title(pe.Message)
	}
	return sanitize.Title(err.Error())
}
