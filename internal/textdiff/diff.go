// Package textdiff is the line diff shared by lit-server and lit-tui: unified
// diffs that keep body bytes exact. It must not depend on the TUI stack.
package textdiff

import (
	"fmt"
	"strings"
)

// Context is how many unchanged lines surround each hunk.
const Context = 3

// maxDiffCells bounds the line comparison table (16 MiB of int32); beyond
// it the differing middle is shown as removed and added as a whole. A middle
// of up to 2047 lines on each side fits, so a 64 KB body gets a minimal diff
// whenever its lines average at least 33 bytes (see bound_test.go).
const maxDiffCells = 1 << 22

// NoNewline follows a last line that has no line break.
const NoNewline = `\ No newline at end of file`

type edit struct {
	op   byte // ' ', '-' or '+'
	text string
}

// Diff is a unified line diff from old to new: hunk headers (`@@ -a,b +c,d @@`),
// then lines starting with ' ', '-' or '+', and NoNewline after a last line
// without a line break. It is empty when old and new are equal. Lines are
// returned raw; sanitize them before display.
func Diff(old, new string) []string {
	if old == new {
		return nil
	}
	return hunks(script(lines(old), lines(new)))
}

func lines(s string) []string {
	out := strings.SplitAfter(s, "\n")
	if out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// script is an edit script from a to b: the common prefix and suffix, and
// a longest common subsequence of the middle when it is small enough.
func script(a, b []string) []edit {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	var out []edit
	for _, l := range a[:pre] {
		out = append(out, edit{' ', l})
	}
	out = append(out, middle(a[pre:len(a)-suf], b[pre:len(b)-suf])...)
	for _, l := range a[len(a)-suf:] {
		out = append(out, edit{' ', l})
	}
	return out
}

func middle(a, b []string) []edit {
	var out []edit
	if (len(a)+1)*(len(b)+1) > maxDiffCells {
		for _, l := range a {
			out = append(out, edit{'-', l})
		}
		for _, l := range b {
			out = append(out, edit{'+', l})
		}
		return out
	}
	w := len(b) + 1
	lcs := make([]int32, (len(a)+1)*w) // lcs[i*w+j]: of a[i:] and b[j:]
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i*w+j] = lcs[(i+1)*w+j+1] + 1
			} else {
				lcs[i*w+j] = max(lcs[(i+1)*w+j], lcs[i*w+j+1])
			}
		}
	}
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			out = append(out, edit{' ', a[i]})
			i, j = i+1, j+1
		case i < len(a) && (j == len(b) || lcs[(i+1)*w+j] >= lcs[i*w+j+1]):
			out = append(out, edit{'-', a[i]})
			i++
		default:
			out = append(out, edit{'+', b[j]})
			j++
		}
	}
	return out
}

// hunks groups the changes of s with Context lines around them.
func hunks(s []edit) []string {
	var out []string
	for start := 0; start < len(s); {
		first := start
		for first < len(s) && s[first].op == ' ' {
			first++
		}
		if first == len(s) {
			break
		}
		from := max(first-Context, 0)
		end := first // one past the hunk's last change
		for k := first; k < len(s); k++ {
			if s[k].op != ' ' {
				end = k + 1
			} else if k-end >= 2*Context {
				break
			}
		}
		to := min(end+Context, len(s))
		out = append(out, hunk(s, from, to)...)
		start = to
	}
	return out
}

func hunk(s []edit, from, to int) []string {
	oldAt, newAt := 1, 1 // line numbers of s[from]
	for _, e := range s[:from] {
		if e.op != '+' {
			oldAt++
		}
		if e.op != '-' {
			newAt++
		}
	}
	oldN, newN := 0, 0
	var body []string
	for _, e := range s[from:to] {
		if e.op != '+' {
			oldN++
		}
		if e.op != '-' {
			newN++
		}
		text, eol := strings.CutSuffix(e.text, "\n")
		body = append(body, string(e.op)+text)
		if !eol {
			body = append(body, NoNewline)
		}
	}
	if oldN == 0 {
		oldAt--
	}
	if newN == 0 {
		newAt--
	}
	return append([]string{fmt.Sprintf("@@ -%d,%d +%d,%d @@", oldAt, oldN, newAt, newN)}, body...)
}
