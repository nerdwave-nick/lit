package quick

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/cells"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/sanitize"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/tree"
)

// Lines draws the action in exactly h lines of exactly w cells.
func (m *Model) Lines(method cells.Method, w, h int, plain bool) []string {
	var out []string
	switch {
	case m.stage == choosing && m.field != nil && m.form != nil:
		out = m.formLines()
	case m.stage == choosing && m.field != nil:
		out = append(out, m.fieldHeading(), "Title: "+sanitize.Title(m.field.value())+"▏")
	case m.stage == choosing:
		out = m.pickLines(method, w, h, plain)
	case m.stage == previewing:
		out = append(out, m.preview.Change.Summary, "", "Previewing…")
	default:
		out = m.previewLines()
	}
	if m.note != "" {
		out = append(out, "", m.note)
	}
	var lines []string
	for _, l := range out {
		lines = append(lines, wrap(method, l, w)...)
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	lines = lines[:max(h, 0)]
	for n, l := range lines {
		if !strings.HasPrefix(l, "\x1b[7m") {
			lines[n] = fit(method, l, w)
		}
	}
	return lines
}

// pickLines is the picker: its title, the filter and the visible options,
// scrolled to keep the cursor shown.
func (m *Model) pickLines(method cells.Method, w, h int, plain bool) []string {
	p := m.pick
	filter := "Type to filter."
	if p.create != "" {
		filter = "Type to filter or to add a new value."
	}
	if v := p.filter.value(); v != "" {
		filter = "Filter: " + sanitize.Title(v) + "▏"
	}
	out := []string{p.title, filter}
	vis := p.visible()
	if len(vis) == 0 {
		return append(out, "  (nothing matches)")
	}
	room := max(h-len(out)-2, 1) // keep room for a note
	p.cursor = min(p.cursor, len(vis)-1)
	p.offset = min(max(p.offset, p.cursor-room+1), p.cursor)
	for n := p.offset; n < len(vis) && n < p.offset+room; n++ {
		o := vis[n]
		text := o.label
		if p.multi && o.kind == optValue {
			box := "[ ] "
			if o.checked {
				box = "[x] "
			}
			text = box + text
		}
		switch {
		case n == p.cursor && plain:
			text = "> " + text
		case n == p.cursor:
			text = "\x1b[7m" + fit(method, "  "+text, w) + "\x1b[m"
		default:
			text = "  " + text
		}
		out = append(out, text)
	}
	return out
}

// previewLines is the preview: the change, every record it writes or reads
// with its fenced revision and claim, and its refusal or failure.
func (m *Model) previewLines() []string {
	p := m.preview
	out := []string{p.Change.Summary, ""}
	var changed, read []string
	for _, a := range p.Affected {
		line := "  • " + a.name()
		if a.Self {
			line += " (this issue)"
		}
		if a.Revision > 0 {
			line += " · revision " + strconv.FormatInt(a.Revision, 10)
		}
		switch {
		case a.Claim == nil:
		case a.Mine && a.Self && p.Change.Request.Operation == "issue.close":
			line += " · claimed by you: closing ends your claim"
		case a.Mine:
			line += " · claimed by you: your claim is passed on"
		default:
			line += " · claimed by " + claimOwner(a.Claim)
		}
		if a.Changed {
			changed = append(changed, line)
		} else {
			read = append(read, line)
		}
	}
	if len(changed) > 0 {
		out = append(append(out, "Would write:"), changed...)
	}
	if len(read) > 0 {
		out = append(append(out, "Would read without changing:"), read...)
	}
	if len(changed)+len(read) > 0 {
		out = append(out, "")
	}
	switch {
	case p.Err != nil:
		out = append(out, failure(p.Err, foreignOf(p.Affected)))
	case m.stage == committing:
		out = append(out, "Applying…")
	case m.stage == failed:
		out = append(out, failure(m.outcome.Err, m.outcome.Owners))
	default:
		out = append(out, "Nothing is written until you apply it. It applies exactly this preview, or is refused if any of these records changed meanwhile.")
	}
	return out
}

// Hint names the keys of the current stage.
func (m *Model) Hint(km keys.Keymap) string {
	open, cancel := first(km, keys.Open), first(km, keys.Cancel)
	switch m.stage {
	case choosing:
		switch {
		case m.field != nil:
			return open + " preview · " + cancel + " cancel · backspace delete · ctrl+u clear"
		case m.pick.multi:
			return "↑/↓ move · type to filter or add · " + open + " toggle or preview · " + cancel + " cancel"
		}
		return "↑/↓ move · type to filter · " + open + " choose · " + cancel + " cancel"
	case previewing:
		return "previewing… · " + cancel + " cancel"
	case previewed:
		if m.preview.Refused() {
			return cancel + " close"
		}
		return open + " apply · " + cancel + " cancel"
	case committing:
		return "applying…"
	}
	return cancel + " close"
}

func first(km keys.Keymap, a keys.Action) string {
	if k := km.Keys(keys.Menu, a); len(k) > 0 {
		return k[0]
	}
	return string(a)
}

// name is an affected issue's quoted title; other records and unknown
// issues are named with their type.
func (a Affected) name() string {
	kind := strings.TrimSuffix(sanitize.Title(a.Type), "s")
	switch {
	case a.Title != "" && a.Type == "issues":
		return quoted(a.Title)
	case a.Title != "":
		return kind + " " + quoted(a.Title)
	}
	return kind + " " + short(a.ID)
}

// claimOwner names a claim's owner by actor ("Claude (agent)") and when
// its lease ends.
func claimOwner(c *protocol.Claim) string {
	s := tree.ClaimOwner(c, true)
	if t, err := time.Parse(time.RFC3339Nano, c.ExpiresAt); err == nil {
		return s + " until " + t.Local().Format("15:04:05")
	}
	return s
}

// failure describes err from its typed code, naming claim owners.
func failure(err error, owners []Affected) string {
	code := litclient.Code(err)
	switch code {
	case "":
		return "Failed: " + sanitize.Title(err.Error())
	case "claim_conflict":
		if len(owners) == 0 {
			return "Refused (claim_conflict): another client claims an affected issue. Nothing was written."
		}
		var parts []string
		for _, a := range owners {
			parts = append(parts, a.name()+" is claimed by "+claimOwner(a.Claim))
		}
		return "Refused (claim_conflict): " + strings.Join(parts, "; ") + ". Nothing was written."
	case "revision_conflict":
		return "Refused (revision_conflict): an affected record changed since it was shown. Nothing was written; the tree reloads, try again."
	case "outcome_uncertain":
		id := "The request"
		var pe *protocol.Error
		if errors.As(err, &pe) {
			if d, ok := pe.Details.(map[string]any); ok {
				if v, ok := d["request_id"].(string); ok && protocol.ValidUUID(v) {
					id = "Request " + v
				}
			}
		}
		return "Outcome uncertain (outcome_uncertain): " + sanitize.Title(err.Error()) + ". " + id + " is kept for recovery and never replayed; the uncertain-requests view lists it."
	case "shutting_down":
		return "Not applied (shutting_down): lit-tui is shutting down."
	}
	return "Refused (" + sanitize.Title(code) + "): " + sanitize.Title(err.Error())
}

// wrap breaks s into lines of at most w cells at spaces; a word longer than
// w is clipped.
func wrap(method cells.Method, s string, w int) []string {
	if w <= 0 || cells.Width(method, s) <= w || strings.HasPrefix(s, "\x1b[") {
		return []string{s}
	}
	indent := s[:len(s)-len(strings.TrimLeft(s, " •"))]
	var out []string
	line := ""
	for _, word := range strings.Fields(s) {
		switch {
		case line == "":
			line = word
			if len(out) == 0 {
				line = indent + word
			}
		case cells.Width(method, line+" "+word) <= w:
			line += " " + word
		default:
			out = append(out, line)
			line = strings.Repeat(" ", cells.Width(method, indent)) + word
		}
	}
	return append(out, line)
}

// fit clips s to w cells and pads it.
func fit(method cells.Method, s string, w int) string {
	if w <= 0 {
		return s
	}
	s = cells.Clip(method, s, w, "…")
	return s + strings.Repeat(" ", max(w-cells.Width(method, s), 0))
}
