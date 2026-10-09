package lifecycle

import (
	"fmt"
	"strings"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/recovery"
)

// Overlay is the prompt or report that replaces the content, or "".
func (u UI) Overlay(km keys.Keymap) string {
	switch {
	case u.status.Phase == Stopped && u.status.Result.Outcome != Clean:
		return exitReport(u.status.Result)
	case u.status.Phase != Running:
		return ""
	case len(u.ask) > 0:
		o := u.ask[0]
		return "Orphaned lit-tui session " + describe(o) + "?\n" + o.Reason + ".\n" +
			"Disconnect it only if it is no longer running: its claims are released.\n\n" +
			hint(km, keys.Open, "disconnect it") + " · " + hint(km, keys.Cancel, "keep it")
	case len(u.panel) > 0:
		o := u.panel[0]
		var b strings.Builder
		fmt.Fprintf(&b, "Orphaned lit-tui session %s was not disconnected:\nthe outcome of its retained requests is uncertain.\n\n", describe(o))
		writeRequests(&b, o.Requests)
		b.WriteString("\nThe evidence is kept and never replayed. Inspect it read-only with\n`lit transactions status` and decide what to do (see docs/recovery.md).\n\n")
		b.WriteString(hint(km, keys.Cancel, "close"))
		return b.String()
	}
	return ""
}

// Lines are the session line and the notes shown under the content.
func (u UI) Lines() []string {
	var out []string
	if u.status.Client != "" {
		out = append(out, "Session: client "+short(u.status.Client)+" · lit-tui instance "+short(u.status.Instance))
	}
	out = append(out, u.notes...)
	if u.status.Note != "" {
		out = append(out, u.status.Note)
	}
	return out
}

func exitReport(r Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "lit-tui could not confirm a clean exit (%s).\n", r.Outcome)
	if r.Reason != "" {
		b.WriteString(capitalize(r.Reason) + ".\n")
	}
	if len(r.Requests) > 0 {
		b.WriteString("\n")
		writeRequests(&b, r.Requests)
	}
	b.WriteString("\n")
	for _, line := range ReportLines(r)[len(r.Requests)*2:] {
		b.WriteString(line + "\n")
	}
	b.WriteString("\nPress any key to exit.")
	return b.String()
}

// ReportLines describe a result that is not clean, for the exit screen and
// for stderr after the terminal is restored.
func ReportLines(r Result) []string {
	var out []string
	for _, q := range r.Requests {
		out = append(out, "request "+q.ID+" "+strings.TrimSpace(q.Status+" "+q.Proof), "  evidence "+q.Path)
	}
	if len(r.Claims) > 0 {
		out = append(out, "claims left until their leases expire: "+strings.Join(r.Claims, ", "))
	}
	if r.Kept {
		out = append(out, "this session's registry entry is kept, so the next launch checks it again")
	}
	return out
}

func writeRequests(b *strings.Builder, rs []recovery.Request) {
	for _, q := range rs {
		fmt.Fprintf(b, "  request %s  %s %s\n    evidence %s\n", q.ID, q.Status, q.Proof, q.Path)
	}
}

func hint(km keys.Keymap, a keys.Action, what string) string {
	if k := km.Keys(keys.Menu, a); len(k) > 0 {
		return k[0] + " " + what
	}
	return what
}

func describe(o recovery.Orphan) string {
	s := short(o.Entry.ID)
	var parts []string
	if o.Info.PID > 0 {
		parts = append(parts, fmt.Sprintf("pid %d", o.Info.PID))
	}
	if t, err := time.Parse(time.RFC3339Nano, o.Info.StartedAt); err == nil {
		parts = append(parts, "started "+t.Local().Format("2006-01-02 15:04"))
	}
	if len(parts) > 0 {
		s += " (" + strings.Join(parts, ", ") + ")"
	}
	return s
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
