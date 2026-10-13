package quick

import (
	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/tui/render/sanitize"
)

// Forms are quick actions that do not start from an issue, such as creating
// a project or changing its repositories (internal/tui/projectadmin). They
// run through the same exact preview and single commit. The form's own code
// turns the input into the Change to preview, or refuses the input with a
// note before anything is previewed.

// TextForm is an inline text field.
type TextForm struct {
	Heading string // the line above the field
	Help    string // shown under the heading, for example the syntax
	Label   string // names the field, for example "Title"
	Text    string // the field's initial text
	// Submit turns the text into the change to preview, or returns a note
	// that refuses the text before anything is previewed.
	Submit func(text string) (Change, string)
	// Applied, when set, runs after the change was committed.
	Applied func(protocol.MutationResult) tea.Cmd
}

// SetForm toggles the values of a set and adds typed new ones.
type SetForm struct {
	Title  string
	Values []string // the current set: checked at the start
	Create string   // the label prefix offering the typed text as a new value
	// Submit turns the values added and removed into the change to
	// preview, or returns a note that refuses them first.
	Submit  func(add, remove []string) (Change, string)
	Applied func(protocol.MutationResult) tea.Cmd
}

// form is what a form-started model does instead of its issue kind.
type form struct {
	heading, help, label string
	text                 func(string) (Change, string)
	set                  func(add, remove []string) (Change, string)
	applied              func(protocol.MutationResult) tea.Cmd
}

// StartText opens f. env.Issue is not used.
func StartText(env Env, f TextForm) *Model {
	return &Model{env: env, field: &field{text: []rune(f.Text)},
		form: &form{heading: f.Heading, help: f.Help, label: f.Label, text: f.Submit, applied: f.Applied}}
}

// StartSet opens f. env.Issue is not used.
func StartSet(env Env, f SetForm) *Model {
	p := &picker{title: f.Title, multi: true, create: f.Create, original: map[string]bool{}}
	p.options = append(p.options, option{kind: optSubmit, label: "Preview the change"})
	for _, v := range f.Values {
		p.original[v] = true
		p.options = append(p.options, option{label: sanitize.Title(v), value: v, checked: true})
	}
	if len(p.options) > 1 {
		p.cursor = 1
	}
	return &Model{env: env, pick: p, form: &form{set: f.Submit, applied: f.Applied}}
}

// formChange previews a form's change, or shows the note refusing it.
func (m *Model) formChange(c Change, note string) Result {
	if note != "" {
		m.note = note
		return Result{}
	}
	return m.prepare(c)
}

// formLines are a text form's heading, help and field.
func (m *Model) formLines() []string {
	out := []string{m.form.heading}
	if m.form.help != "" {
		out = append(out, m.form.help)
	}
	return append(out, m.form.label+": "+sanitize.Title(m.field.value())+"▏")
}
