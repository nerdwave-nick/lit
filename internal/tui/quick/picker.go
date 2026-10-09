package quick

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/sanitize"
)

type optionKind int

const (
	optValue  optionKind = iota // a value to choose or toggle
	optSubmit                   // preview the toggled values
	optCreate                   // a new value: the filter text
)

// option is one row of a picker. label is shown and filtered; it is
// sanitized when the option is built.
type option struct {
	kind    optionKind
	label   string
	value   string
	checked bool // multi-select: the value is in the set
}

// picker chooses one option, or toggles several (multi), filtered by typed
// text. The arrows, page and home/end keys move; typed letters (j and k
// included) filter. create, when set, offers the filter text as a new value
// with that label prefix when no option's value equals it.
type picker struct {
	title    string
	options  []option
	multi    bool
	create   string
	filter   field
	cursor   int // index into visible()
	offset   int
	original map[string]bool // multi: the values checked at start
}

// visible is the options matching the filter (case-insensitive), with the
// submit row always first and the create row last.
func (p *picker) visible() []option {
	q := strings.ToLower(p.filter.value())
	var out []option
	exact := false
	for _, o := range p.options {
		if o.kind == optValue && strings.EqualFold(o.value, p.filter.value()) {
			exact = true
		}
		if o.kind == optSubmit || strings.Contains(strings.ToLower(o.label), q) {
			out = append(out, o)
		}
	}
	if text := strings.TrimSpace(p.filter.value()); p.create != "" && text != "" && !exact {
		out = append(out, option{kind: optCreate, label: p.create + quoted(text), value: text})
	}
	return out
}

// key handles a key press. It returns the chosen option when one was
// chosen (single) or submitted (multi), and whether the picker was
// cancelled.
func (p *picker) key(k tea.KeyPressMsg, km keys.Keymap) (chosen *option, cancel bool) {
	if p.filter.typed(k) {
		p.cursor, p.offset = 0, 0
		if p.multi && len(p.visible()) > 1 {
			p.cursor = 1 // the first match, after the submit row
		}
		return nil, false
	}
	vis := p.visible()
	a, _ := km.Lookup(keys.Menu, k.Key())
	switch a {
	case keys.Up:
		p.cursor = max(p.cursor-1, 0)
	case keys.Down:
		p.cursor = min(p.cursor+1, len(vis)-1)
	case keys.Top, keys.PageUp:
		p.cursor = 0
	case keys.Bottom, keys.PageDown:
		p.cursor = len(vis) - 1
	case keys.Cancel:
		return nil, true
	case keys.Open:
		if len(vis) == 0 {
			return nil, false
		}
		o := vis[p.cursor]
		if !p.multi || o.kind == optSubmit {
			return &o, false
		}
		p.toggle(o)
	}
	return nil, false
}

// toggle flips o in the set; a created value is added checked and the
// filter cleared.
func (p *picker) toggle(o option) {
	if o.kind == optCreate {
		p.options = append(p.options, option{kind: optValue, label: sanitize.Title(o.value), value: o.value, checked: true})
		p.filter.text = nil
		p.cursor = len(p.visible()) - 1
		return
	}
	for n := range p.options {
		if p.options[n].kind == optValue && p.options[n].value == o.value {
			p.options[n].checked = !p.options[n].checked
		}
	}
}

// diff is the values checked now but not at the start (add) and the other
// way round (remove), in option order.
func (p *picker) diff() (add, remove []string) {
	for _, o := range p.options {
		switch {
		case o.kind != optValue:
		case o.checked && !p.original[o.value]:
			add = append(add, o.value)
		case !o.checked && p.original[o.value]:
			remove = append(remove, o.value)
		}
	}
	return add, remove
}
