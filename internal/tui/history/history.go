// Package history is lit-tui's read-only history view: the immutable
// mutation history of an issue or of one of its comments, entry by entry,
// with who made each change, when, which fields changed and how the body
// changed. Pages are read on demand through the lifecycle's gate, one request
// per call, following the service's cursors. Nothing here writes.
package history

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
)

// Paging (Proposals). PageSize entries are read per request (fewer when the
// service's maximum page is smaller): an entry can carry its body three
// times, so pages stay small. A page is read when the view's end comes
// within one screen of the last entry read. LoadTimeout bounds one page.
const (
	PageSize    = 10
	LoadTimeout = 30 * time.Second
)

// Doer runs fn with exclusive use of the session (lifecycle.Lifecycle.Do).
type Doer interface {
	Do(ctx context.Context, fn func(ctx context.Context, s *litclient.Session) error) error
}

// Target is a record whose history can be shown.
type Target struct {
	Type  string // "issues" or "comments"
	ID    string // its UUID
	Label string // names it in the chooser and the header; sanitized when drawn
}

// Page reads one page of t's history through d, from cursor ("" for the
// first page).
func Page(ctx context.Context, d Doer, t Target, cursor string) (litclient.HistoryPage, error) {
	var page litclient.HistoryPage
	err := d.Do(ctx, func(ctx context.Context, s *litclient.Session) error {
		limit := PageSize
		if most := s.Meta.Limits.MaxPage; most > 0 {
			limit = min(limit, most)
		}
		var err error
		page, err = s.History(ctx, t.Type, t.ID, litclient.HistoryQuery{Limit: limit, Cursor: cursor})
		return err
	})
	return page, err
}

// Model is the open view: a chooser when there is more than one target,
// and the history of the chosen one. It belongs to the UI goroutine.
type Model struct {
	doer    Doer
	choices []Target
	cursor  int
	shown   bool // the history of choices[cursor] is shown

	gen     int // the current load; older pages are dropped
	cancel  context.CancelFunc
	loading bool
	next    string // the next page's cursor; "" when none is known
	more    bool   // the history continues after the entries read
	err     error  // ended the load; the entries before it are kept
	count   int
	seen    map[string]bool
	logical []line

	offset, height int // the first body row shown; the body rows that fit in the last View
	rows           int // the body's rows at the last View, plus the logical lines read since
	wrapped        wrapCache
}

type pageMsg struct {
	m    *Model
	gen  int
	page litclient.HistoryPage
	err  error
}

// New opens the view for choices: with one, its history at once (the
// returned command reads the first page); with more, a chooser.
func New(d Doer, choices ...Target) (*Model, tea.Cmd) {
	m := &Model{doer: d, choices: choices}
	if len(choices) == 1 {
		return m, m.show()
	}
	return m, nil
}

// show starts the history of the chosen target from its first page.
func (m *Model) show() tea.Cmd {
	m.stop()
	m.shown, m.err, m.more, m.next = true, nil, true, ""
	m.count, m.seen, m.logical, m.offset, m.rows = 0, map[string]bool{}, nil, 0, 0
	m.wrapped = wrapCache{}
	return m.load()
}

// load reads the next page.
func (m *Model) load() tea.Cmd {
	m.gen++
	ctx, cancel := context.WithTimeout(context.Background(), LoadTimeout)
	m.cancel, m.loading = cancel, true
	gen, d, t, cursor := m.gen, m.doer, m.choices[m.cursor], m.next
	return func() tea.Msg {
		defer cancel()
		page, err := Page(ctx, d, t, cursor)
		return pageMsg{m, gen, page, err}
	}
}

// stop cancels a running read; its answer will be dropped.
func (m *Model) stop() {
	if m.cancel != nil {
		m.cancel()
	}
	m.gen++
	m.cancel, m.loading = nil, false
}

// Close cancels a running read. The view is not used afterwards.
func (m *Model) Close() { m.stop() }

// Update takes this view's own messages and reports whether msg was one;
// the command reads the next page when the view needs it.
func (m *Model) Update(msg tea.Msg) (tea.Cmd, bool) {
	got, ok := msg.(pageMsg)
	if !ok || got.m != m {
		return nil, false
	}
	if got.gen != m.gen {
		return nil, true
	}
	m.cancel, m.loading = nil, false
	if got.err != nil {
		m.err = got.err
		return nil, true
	}
	for _, e := range got.page.Items {
		if m.seen[e.RequestHash] {
			continue
		}
		m.seen[e.RequestHash] = true
		m.count++
		ls := entryLines(m.count, e)
		if len(m.logical) > 0 {
			ls = append([]line{{}}, ls...)
		}
		m.logical = append(m.logical, ls...)
		m.rows += len(ls)
	}
	m.more = got.page.NextCursor != nil
	if m.more {
		m.next = *got.page.NextCursor
	}
	return m.wantMore(), true
}

// wantMore reads the next page when the view's end is within a screen of
// the last entry read.
func (m *Model) wantMore() tea.Cmd {
	if !m.shown || !m.more || m.loading || m.err != nil || m.offset+2*m.height < m.rows {
		return nil
	}
	return m.load()
}

// Do performs a menu-context action. In the chooser: move, open or cancel
// (closed). In a history: scroll (reading more when needed), refresh (from
// the start), cancel (back to the chooser, or closed without one). The
// history action closes the view from either.
func (m *Model) Do(a keys.Action) (cmd tea.Cmd, closed bool) {
	if a == keys.History {
		m.stop()
		return nil, true
	}
	if !m.shown {
		return m.choose(a)
	}
	page := max(m.height-1, 1)
	switch a {
	case keys.Cancel:
		m.stop()
		if len(m.choices) > 1 {
			m.shown = false
			return nil, false
		}
		return nil, true
	case keys.Refresh:
		return m.show(), false
	case keys.Up:
		m.offset--
	case keys.Down:
		m.offset++
	case keys.PageUp:
		m.offset -= page
	case keys.PageDown:
		m.offset += page
	case keys.Top:
		m.offset = 0
	case keys.Bottom:
		m.offset = 1 << 30
	}
	m.clamp()
	return m.wantMore(), false
}

func (m *Model) choose(a keys.Action) (tea.Cmd, bool) {
	switch a {
	case keys.Cancel:
		return nil, true
	case keys.Open:
		return m.show(), false
	case keys.Up:
		m.cursor--
	case keys.Down:
		m.cursor++
	case keys.Top, keys.PageUp:
		m.cursor = 0
	case keys.Bottom, keys.PageDown:
		m.cursor = len(m.choices) - 1
	}
	m.cursor = max(min(m.cursor, len(m.choices)-1), 0)
	return nil, false
}

func (m *Model) clamp() { m.offset = max(min(m.offset, m.rows-m.height), 0) }
