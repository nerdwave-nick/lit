package tui

import (
	"fmt"
	"strings"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/tui/claims"
	"github.com/nerdwave-nick/lit/internal/tui/keys"
	"github.com/nerdwave-nick/lit/internal/tui/recovery"
	"github.com/nerdwave-nick/lit/internal/tui/render/sanitize"
	"github.com/nerdwave-nick/lit/internal/tui/tree"
)

// maxLostLines bounds how many lost claims the footer names one by one.
const maxLostLines = 3

// resultNote describes an action's result. Keeping never reports success
// for a claim that could not be kept.
func resultNote(action keys.Action, r claims.Result) string {
	t := titled(r.Title)
	switch {
	case action == keys.Open:
		var parts []string
		if n := len(r.Kept); n > 0 {
			parts = append(parts, "Holding "+plural(n, "claim")+" again.")
		}
		for _, h := range r.Lost {
			parts = append(parts, "Could not keep holding "+titled(h.Title)+": "+sanitize.Title(h.Reason)+".")
		}
		return strings.Join(parts, " ")
	case r.Err != nil && r.Owner != nil:
		return "Cannot claim " + t + ": claimed by " + tree.ClaimOwner(r.Owner, true) + " until " + sanitize.Title(r.Owner.ExpiresAt) + "."
	case r.Err != nil && action == keys.Release:
		return "Cannot release " + t + ": " + describe(r.Err) + "."
	case r.Err != nil:
		return "Cannot claim " + t + ": " + describe(r.Err) + "."
	case action == keys.Release && r.Forgot:
		return "Forgot the lost claim on " + t + ": it is no longer this session's."
	case action == keys.Release:
		return "Released the claim on " + t + "."
	case r.Already:
		return t + " is already claimed by this session."
	case r.Reacquired:
		note := fmt.Sprintf("Reacquired the claim on %s at revision %d", t, r.Revision)
		if r.Previous != 0 && r.Previous != r.Revision {
			note += fmt.Sprintf(" (it changed from revision %d to %d since the claim was taken)", r.Previous, r.Revision)
		}
		return note + "."
	}
	return "Claimed " + t + " for this session; it is renewed while you are active."
}

// claimLines are the footer's claim summary, the lost claims and the last
// action's note.
func (m Model) claimLines() []string {
	if m.cl == nil {
		return nil
	}
	st := m.cl.state
	var out []string
	if len(st.Holds) > 0 {
		var parts []string
		for _, s := range []struct {
			state claims.State
			name  string
		}{{claims.Holding, "held"}, {claims.Paused, "paused"}, {claims.Lost, "lost"}} {
			if n := count(st.Holds, s.state); n > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", n, s.name))
			}
		}
		out = append(out, "Claims: "+strings.Join(parts, " · "))
	}
	lost := st.Lost()
	for i, h := range lost {
		if i == maxLostLines {
			out = append(out, fmt.Sprintf("and %d more lost", len(lost)-maxLostLines))
			break
		}
		out = append(out, "Claim lost: "+titled(h.Title)+": "+sanitize.Title(h.Reason)+". "+
			m.claimKey(keys.Claim)+" reacquires (rereads first) · "+m.claimKey(keys.Release)+" forgets")
	}
	if m.cl.note != "" {
		out = append(out, m.cl.note)
	}
	return out
}

// claimsPrompt is the idle question, or "".
func (m Model) claimsPrompt() string {
	if m.cl == nil || !m.cl.state.Asking {
		return ""
	}
	st := m.cl.state
	var b strings.Builder
	fmt.Fprintf(&b, "Claim renewal paused: no input or draft change for %s.\nKeep holding these claims?\n\n", st.Idle)
	for _, h := range st.Asked() {
		what := "lapses about " + h.Expires.Format("15:04:05")
		if h.State == claims.Lost {
			what = "lost: " + sanitize.Title(h.Reason)
		}
		fmt.Fprintf(&b, "  %s: %s\n", titled(h.Title), what)
	}
	b.WriteString("\nWithout an answer they lapse when their leases end and are shown as lost.\n\n")
	km := m.prefs.Keymap
	b.WriteString(menuKey(km, keys.Open) + " keep holding · " + menuKey(km, keys.Cancel) + " let them lapse")
	return b.String()
}

func (m Model) claimKey(a keys.Action) string {
	if k := m.prefs.Keymap.Keys(keys.Tree, a); len(k) > 0 {
		return k[0]
	}
	return string(a)
}

func menuKey(km keys.Keymap, a keys.Action) string {
	if k := km.Keys(keys.Menu, a); len(k) > 0 {
		return k[0]
	}
	return string(a)
}

func count(hs []claims.Hold, s claims.State) int {
	n := 0
	for _, h := range hs {
		if h.State == s {
			n++
		}
	}
	return n
}

func titled(title string) string { return "“" + sanitize.Title(title) + "”" }

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

func describe(err error) string {
	if litclient.Code(err) != "" {
		return sanitize.Title(recovery.Describe(err))
	}
	return sanitize.Title(err.Error())
}
