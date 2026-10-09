package uncertain

import (
	"context"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/cells"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/sanitize"
)

// LoadTimeout bounds one check of every group.
const LoadTimeout = 30 * time.Second

// Loader collects the groups to show. It runs off the UI goroutine and must
// only read.
type Loader func(ctx context.Context) []Group

// Model is the open view: the groups, the scroll offset, and whether a check
// is running. It belongs to the UI goroutine.
type Model struct {
	load    Loader
	groups  []Group
	loaded  bool
	loading bool
	gen     int
	offset  int
	height  int // body rows of the last View, for paging
}

type loadedMsg struct {
	m      *Model
	gen    int
	groups []Group
}

// New returns the view; Load starts its first check.
func New(load Loader) *Model { return &Model{load: load} }

// Load (re)checks the retained requests.
func (m *Model) Load() tea.Cmd {
	m.gen++
	m.loading = true
	gen, load := m.gen, m.load
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), LoadTimeout)
		defer cancel()
		return loadedMsg{m, gen, load(ctx)}
	}
}

// Update takes this view's own messages; it reports whether msg was one.
func (m *Model) Update(msg tea.Msg) bool {
	got, ok := msg.(loadedMsg)
	if !ok || got.m != m {
		return false
	}
	if got.gen == m.gen {
		m.groups, m.loaded, m.loading = got.groups, true, false
	}
	return true
}

// Do performs a menu-context action: scrolling, refresh, and cancel or the
// view's own action, which close it (closed).
func (m *Model) Do(a keys.Action) (cmd tea.Cmd, closed bool) {
	page := max(m.height-1, 1)
	switch a {
	case keys.Cancel, keys.UncertainRequests:
		return nil, true
	case keys.Refresh:
		return m.Load(), false
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
	return nil, false
}

// View draws the view as exactly h lines of at most w cells; an unknown size
// (zero) draws everything unwrapped.
func (m *Model) View(method cells.Method, w, h int, km keys.Keymap) string {
	header := "Uncertain requests · read-only"
	if m.loading {
		header += " · checking…"
	}
	body := m.body(method, w)
	footer := km.Hint(keys.Menu, keys.Cancel, keys.Down, keys.Up, keys.Refresh)
	if h <= 0 {
		return strings.Join(append(append([]string{header}, body...), footer), "\n")
	}
	m.height = max(h-2, 0)
	m.offset = max(min(m.offset, len(body)-m.height), 0)
	out := []string{clip(method, header, w)}
	out = append(out, body[m.offset:min(m.offset+m.height, len(body))]...)
	for len(out) < h-1 {
		out = append(out, "")
	}
	return strings.Join(append(out[:max(h-1, 0)], clip(method, footer, w))[:h], "\n")
}

// guidance summarizes docs/recovery.md.
var guidance = []string{
	"These requests were interrupted: each may or may not have taken effect. lit-tui only reads their evidence; it never replays, retires, moves or deletes it.",
	"",
	"What to do (docs/recovery.md):",
	"- Checking again cannot always settle an outcome; some stay uncertain for good (registration_identity_not_received, original_preconditions_no_longer_valid).",
	"- Do not delete or move the evidence files, create a replacement client, or replay the operation to get past it.",
	"- Stop changes that depend on an uncertain request and escalate to the human owner. Keep the evidence file, the request, service and client IDs, and note what is confirmed and what is still unknown.",
	"- `lit transactions status --format json` checks retained requests again and never replays them.",
}

// body is every line below the header, wrapped to w.
func (m *Model) body(method cells.Method, w int) []string {
	var lines []string
	add := func(indent int, s string) { lines = append(lines, wrap(method, indent, sanitize.Title(s), w)...) }
	for _, g := range guidance {
		add(2, g)
	}
	if !m.loaded {
		lines = append(lines, "")
		add(0, "Checking retained requests…")
		return lines
	}
	for _, g := range m.groups {
		lines = append(lines, "")
		title := g.Title
		if g.Checked != "" {
			title += " · " + g.Checked
		}
		add(2, title)
		if len(g.Items) == 0 {
			add(4, "  No retained requests.")
			continue
		}
		items := slices.Clone(g.Items)
		slices.SortStableFunc(items, func(a, b Item) int {
			switch {
			case a.Uncertain() == b.Uncertain():
				return 0
			case a.Uncertain():
				return -1
			}
			return 1
		})
		for _, it := range items {
			add(4, "  request "+it.ID+"  "+outcome(it))
			op := strings.TrimSpace(it.Method + " " + it.Path)
			if it.Operation != "" {
				op += " · " + it.Operation
			}
			client := it.ClientID
			if client == "" && it.Problem == "" {
				client = "(no identity received)"
			}
			for _, f := range [][2]string{{"operation", op}, {"service", it.ServiceID}, {"client", client}, {"endpoint", it.Endpoint},
				{"recorded", it.CreatedAt}, {"evidence", it.Evidence}, {"problem", it.Problem}} {
				if strings.TrimSpace(f[1]) != "" {
					add(15, "    "+f[0]+strings.Repeat(" ", 9-len(f[0]))+f[1])
				}
			}
		}
	}
	return lines
}

// outcome is a request's status, outcome and proof: "uncertain (proof)" or
// "committed: applied (immutable_history)".
func outcome(it Item) string {
	s := it.Status
	if it.Outcome != "" {
		s += ": " + it.Outcome
	}
	if it.Proof != "" {
		s += " (" + it.Proof + ")"
	}
	return s
}

// wrap breaks s into lines of at most w cells; continuation lines are
// indented by indent cells. Paths and IDs are broken, never cut off.
func wrap(method cells.Method, indent int, s string, w int) []string {
	if w <= 0 || cells.Width(method, s) <= w {
		return []string{s}
	}
	indent = min(indent, w/2)
	pad := strings.Repeat(" ", indent)
	var out []string
	first := true
	for s != "" {
		room := w
		if !first {
			room -= indent
		}
		width := cells.Width(method, s)
		if width <= room {
			out = append(out, prefix(first, pad)+s)
			break
		}
		cut := room
		// Prefer a break at a space within the line.
		head := cells.Cut(method, s, 0, room)
		if i := strings.LastIndexByte(head, ' '); i > 0 && cells.Width(method, head[:i]) > room/2 {
			head = head[:i]
			cut = cells.Width(method, head) + 1
		}
		out = append(out, prefix(first, pad)+strings.TrimRight(head, " "))
		s = strings.TrimLeft(cells.Cut(method, s, cut, width), " ")
		first = false
	}
	return out
}

func prefix(first bool, pad string) string {
	if first {
		return ""
	}
	return pad
}

func clip(method cells.Method, s string, w int) string {
	if w <= 0 {
		return s
	}
	return cells.Clip(method, s, w, "…")
}
