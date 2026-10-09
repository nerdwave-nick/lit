package history

import (
	"strconv"
	"strings"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/recovery"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/cells"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/sanitize"
)

// styles colour lines by kind when styling is on; plain views rely on the
// "- " and "+ " prefixes alone.
var styles = map[kind]string{heading: "\x1b[1m", removed: "\x1b[31m", added: "\x1b[32m", gap: "\x1b[2m"}

// wrapCache keeps the wrapped body for one width, method and styling.
type wrapCache struct {
	w      int
	method cells.Method
	plain  bool
	n      int // logical lines wrapped
	rows   []string
}

// View draws the view as exactly h lines of at most w cells; an unknown size
// (zero) draws everything unwrapped. plain leaves out styling.
func (m *Model) View(method cells.Method, w, h int, km keys.Keymap, plain bool) string {
	var header, footer string
	var body []string
	if m.shown {
		header = "History · " + sanitize.Title(m.choices[m.cursor].Label) + " · read-only · " + m.status()
		footer = km.Hint(keys.Menu, keys.Cancel, keys.Down, keys.Up, keys.Bottom, keys.Refresh)
		body = m.body(method, w, plain)
	} else {
		header = "History of · read-only"
		footer = km.Hint(keys.Menu, keys.Open, keys.Cancel)
		body = m.chooser(method, w, plain)
	}
	if h <= 0 {
		return strings.Join(append(append([]string{header}, body...), footer), "\n")
	}
	m.height = max(h-2, 0)
	if m.shown {
		m.rows = len(body)
		m.clamp()
	}
	offset := max(m.cursor-m.height+1, 0) // the chooser keeps its cursor visible
	if m.shown {
		offset = m.offset
	}
	out := []string{clip(method, header, w)}
	out = append(out, body[min(offset, len(body)):min(offset+m.height, len(body))]...)
	for len(out) < h-1 {
		out = append(out, "")
	}
	return strings.Join(append(out[:max(h-1, 0)], clip(method, footer, w))[:h], "\n")
}

// status is how much of the history is read.
func (m *Model) status() string {
	n := strconv.Itoa(m.count) + " entries"
	if m.count == 1 {
		n = "1 entry"
	}
	switch {
	case m.count == 0 && m.err != nil:
		return "could not load: " + sanitize.Title(recovery.Describe(m.err))
	case m.count == 0 && m.loading:
		return "loading…"
	case m.err != nil:
		return n + " so far · stopped: " + sanitize.Title(recovery.Describe(m.err))
	case m.loading:
		return n + " so far · loading more…"
	case m.more:
		return n + " so far · more below"
	}
	return n
}

// body is the entries, wrapped to w.
func (m *Model) body(method cells.Method, w int, plain bool) []string {
	if len(m.logical) == 0 {
		if !m.loading && !m.more && m.err == nil {
			return []string{"No history entries."}
		}
		return nil
	}
	c := &m.wrapped
	if c.w != w || c.method != method || c.plain != plain || c.n > len(m.logical) {
		*c = wrapCache{w: w, method: method, plain: plain}
	}
	for _, l := range m.logical[c.n:] {
		for _, r := range wrap(method, strings.Repeat(" ", l.indent)+l.text, w, l.indent+2) {
			if s := styles[l.kind]; s != "" && !plain {
				r = s + r + "\x1b[m"
			}
			c.rows = append(c.rows, r)
		}
	}
	c.n = len(m.logical)
	return c.rows
}

// chooser lists the targets with the cursor marked.
func (m *Model) chooser(method cells.Method, w int, plain bool) []string {
	var out []string
	for i, t := range m.choices {
		s := "  " + sanitize.Title(t.Label)
		if i == m.cursor && plain {
			s = "> " + sanitize.Title(t.Label)
		}
		s = clip(method, s, w)
		if i == m.cursor && !plain {
			s = "\x1b[7m" + s + "\x1b[m"
		}
		out = append(out, s)
	}
	return out
}

// wrap breaks s into lines of at most w cells, never inside a grapheme and
// preferably at a space in the second half of the line; continuation lines
// are indented by indent cells.
func wrap(method cells.Method, s string, w, indent int) []string {
	if w <= 0 || cells.Width(method, s) <= w {
		return []string{s}
	}
	indent = min(indent, w/2)
	pad := strings.Repeat(" ", indent)
	var out []string
	cur := []byte{}
	col, space, spaceCol := 0, -1, 0 // space: byte offset of the last break candidate
	var state byte
	for len(s) > 0 {
		seq, cw, n, next := method.DecodeSequenceInString(s, state, nil)
		state, s = next, s[n:]
		if col+cw > w && col > indent {
			if space > 0 && spaceCol > w/2 {
				rest := cur[space+1:]
				out = append(out, string(cur[:space]))
				cur = append([]byte(pad), rest...)
				col -= spaceCol + 1 - indent
			} else {
				out = append(out, string(cur))
				cur, col = []byte(pad), indent
			}
			space = -1
		}
		if seq == " " && col > indent {
			space, spaceCol = len(cur), col
		}
		cur = append(cur, seq...)
		col += cw
	}
	return append(out, string(cur))
}

func clip(method cells.Method, s string, w int) string {
	if w <= 0 {
		return s
	}
	return cells.Clip(method, s, w, "…")
}
