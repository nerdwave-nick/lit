// Package detail is lit-tui's detail viewport: a reusable Bubble Tea
// component that shows one or more Markdown sections (an issue body, its
// comments) rendered off the UI goroutine through the shared render.Service,
// and scrolls them.
//
// Prose wraps at the pane width and never scrolls sideways. Top-level code
// blocks and tables stay unwrapped; the focused one scrolls horizontally on
// its own, each block keeping its own offset, and clipped lines show an
// overflow marker. Every displayed line is clipped to the pane width as
// Bubble Tea measures it (SetMethod, following mode 2027).
//
// The scroll position is a content anchor (a block and the text offset of
// the top line inside it), restored whenever a render arrives: after a
// resize, a style change or a new revision. A "rendering…" placeholder
// appears only once a section has waited PlaceholderDelay for its first
// render; results that are no longer wanted are dropped (they stay cached).
// Resizes are debounced while a previous render can stay on screen.
//
// The model owns no data loading. Its owner sets the sections, the size and
// the style (see package theme), forwards messages to Update and draws View.
package detail

import (
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/cells"
)

const (
	// PlaceholderDelay is how long a section waits for its first render
	// before the placeholder appears, so fast renders (about 1 ms) never
	// flicker.
	PlaceholderDelay = 100 * time.Millisecond
	// ResizeDebounce is how long a width must stay unchanged before it is
	// rendered while an earlier render is on screen.
	ResizeDebounce = 75 * time.Millisecond
	// Placeholder is shown for a section whose first render is pending.
	Placeholder = "rendering…"
	// ScrollStep is how many cells left and right scroll a wide block.
	ScrollStep = 8
	// WheelLines is how many lines one mouse wheel step scrolls.
	WheelLines = 3
)

// Section is one Markdown document of the viewport, such as an issue body or
// a comment. RecordIDs must be unique within the viewport; a section keeps
// its scroll state and its last render while its RecordID stays.
type Section struct {
	RecordID string
	Revision int64
	Content  string // raw Markdown; sanitized by the renderer
}

// Model is the detail viewport. It belongs to the UI goroutine.
type Model struct {
	id     int64
	svc    *render.Service
	keymap keys.Keymap
	after  func(time.Duration, tea.Msg) tea.Cmd // tea.Tick; replaced in tests

	width, height int
	style         render.Style
	styleReady    bool
	method        cells.Method
	debouncing    bool
	resizeGen     int

	secs []*section
	lay  layout
	y    int
	anc  anchor
	foc  bref // focused wide block; rec == "" means none
	xoff map[bref]int
}

type section struct {
	Section
	serial      int64
	slot        *render.Slot
	doc         *render.Doc // shown; may be for an older width, style or revision
	err         error
	lastKey     render.Key // the render doc or err came from
	want        render.Key // the render wanted now
	requested   bool       // want has been requested from the slot
	placeholder bool       // the placeholder delay has passed without a render
}

type (
	renderedMsg struct {
		id   int64
		slot *render.Slot
		res  render.Result
	}
	placeholderMsg struct{ id, serial int64 }
	debounceMsg    struct {
		id  int64
		gen int
	}
)

var ids atomic.Int64

// New returns an empty viewport rendering through svc, with keys from km's
// detail context.
func New(svc *render.Service, km keys.Keymap) *Model {
	return &Model{id: ids.Add(1), svc: svc, keymap: km, method: cells.Default, xoff: map[bref]int{},
		after: func(d time.Duration, msg tea.Msg) tea.Cmd { return tea.Tick(d, func(time.Time) tea.Msg { return msg }) }}
}

// SetSections replaces the content. Sections whose RecordID was shown before
// keep their render (until the new one arrives) and scroll state; when none
// was, the viewport starts at the top.
func (m *Model) SetSections(secs ...Section) tea.Cmd {
	old := map[string]*section{}
	for _, s := range m.secs {
		old[s.RecordID] = s
	}
	kept := false
	var cmds []tea.Cmd
	m.secs = make([]*section, 0, len(secs))
	for _, sec := range secs {
		s, ok := old[sec.RecordID]
		delete(old, sec.RecordID)
		if ok {
			s.Section, kept = sec, true
		} else {
			s = &section{Section: sec, serial: ids.Add(1), slot: render.NewSlot(m.svc)}
			cmds = append(cmds, m.after(PlaceholderDelay, placeholderMsg{m.id, s.serial}))
		}
		m.secs = append(m.secs, s)
	}
	if !kept {
		m.y, m.anc, m.foc, m.xoff = 0, anchor{}, bref{}, map[bref]int{}
		if len(secs) > 0 {
			m.anc.rec = secs[0].RecordID
		}
	}
	return tea.Batch(append(cmds, m.sync())...)
}

// SetSize sets the pane size in cells.
func (m *Model) SetSize(w, h int) tea.Cmd {
	if w == m.width && h == m.height {
		return nil
	}
	resized := w != m.width && m.width > 0
	m.width, m.height = max(w, 0), max(h, 0)
	if !resized {
		return m.sync()
	}
	m.debouncing = true
	m.resizeGen++
	cmd := m.sync()
	for _, s := range m.secs {
		if !s.requested {
			return tea.Batch(cmd, m.after(ResizeDebounce, debounceMsg{m.id, m.resizeGen}))
		}
	}
	m.debouncing = false
	return cmd
}

// SetStyle sets the render style. Nothing renders before the first call, so
// the owner calls it once the background is known (theme.Detector.Ready).
func (m *Model) SetStyle(s render.Style) tea.Cmd {
	if m.styleReady && s == m.style {
		return nil
	}
	m.style, m.styleReady = s, true
	return m.sync()
}

// SetMethod sets how cells are measured: cells.Default until the terminal
// reports mode 2027. Update handles tea.ModeReportMsg itself.
func (m *Model) SetMethod(meth cells.Method) {
	if meth != m.method {
		m.method = meth
		m.relayout()
	}
}

// Pending reports whether any section still waits for its current render.
func (m *Model) Pending() bool {
	for _, s := range m.secs {
		if !s.requested || s.lastKey != s.want || s.doc == nil && s.err == nil {
			return true
		}
	}
	return false
}

func (m *Model) job(s *section) render.Job {
	return render.Job{RecordID: s.RecordID, Revision: s.Revision, Content: s.Content, Width: m.width, Style: m.style}
}

func (m *Model) current(s *section) bool {
	return s.requested && s.doc != nil && s.err == nil && s.lastKey == s.want
}

// sync requests the render every section wants now, from the cache or as a
// command. While a resize is debounced, sections that already show a render
// wait for the debounce unless the new width is cached.
func (m *Model) sync() tea.Cmd {
	defer m.relayout()
	if !m.styleReady || m.width <= 0 {
		return nil
	}
	var cmds []tea.Cmd
	for _, s := range m.secs {
		job := m.job(s)
		k := job.Key()
		if k == s.want && s.requested {
			continue
		}
		s.want, s.requested = k, false
		if m.debouncing && s.doc != nil {
			if _, hit := m.svc.Lookup(job); !hit {
				continue
			}
		}
		s.requested = true
		doc, run := s.slot.Request(job)
		switch {
		case doc != nil:
			s.show(k, doc, nil)
		case run != nil:
			id, slot := m.id, s.slot
			cmds = append(cmds, func() tea.Msg { return renderedMsg{id, slot, run()} })
		}
	}
	return tea.Batch(cmds...)
}

func (s *section) show(k render.Key, d *render.Doc, err error) {
	s.lastKey, s.doc, s.err = k, d, err
}
