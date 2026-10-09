package history

import "strings"

// Body diff (Proposals): ContextLines unchanged lines are kept around each
// change, and a longer unchanged run is elided. Above MaxDiffCells (lines
// before × lines after, once the common start and end are trimmed) the
// changed middle is shown as removed, then added, instead of a line diff.
const (
	ContextLines = 2
	MaxDiffCells = 1 << 20
)

// edit is one line of a line diff: kept, removed or added.
type edit struct {
	kind kind // kept, removed or added
	text string
}

// lines splits text into lines; empty text has none.
func lines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// diffLines is a shortest line edit script from before to after.
func diffLines(before, after string) []edit {
	a, b := lines(before), lines(after)
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	var out []edit
	for _, s := range a[:pre] {
		out = append(out, edit{kept, s})
	}
	out = append(out, middle(a[pre:len(a)-suf], b[pre:len(b)-suf])...)
	for _, s := range a[len(a)-suf:] {
		out = append(out, edit{kept, s})
	}
	return out
}

// middle diffs the changed middle by longest common subsequence.
func middle(a, b []string) []edit {
	var out []edit
	if len(a)*len(b) > MaxDiffCells || len(a) == 0 || len(b) == 0 {
		for _, s := range a {
			out = append(out, edit{removed, s})
		}
		for _, s := range b {
			out = append(out, edit{added, s})
		}
		return out
	}
	// lcs[i][j] is the LCS length of a[i:] and b[j:].
	w := len(b) + 1
	lcs := make([]int32, (len(a)+1)*w)
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
			out = append(out, edit{kept, a[i]})
			i, j = i+1, j+1
		case i < len(a) && (j == len(b) || lcs[(i+1)*w+j] >= lcs[i*w+j+1]):
			out = append(out, edit{removed, a[i]}) // removals before additions
			i++
		default:
			out = append(out, edit{added, b[j]})
			j++
		}
	}
	return out
}

// hunks keeps the changes with up to context kept lines around each; a
// longer kept run between or around them is elided with a single gap line.
func hunks(es []edit, context int) []edit {
	var out []edit
	for i := 0; i < len(es); {
		if es[i].kind != kept {
			out = append(out, es[i])
			i++
			continue
		}
		j := i
		for j < len(es) && es[j].kind == kept {
			j++
		}
		head, tail := context, context // kept lines after the previous change, before the next
		if i == 0 {
			head = 0
		}
		if j == len(es) {
			tail = 0
		}
		if j-i <= head+tail {
			out = append(out, es[i:j]...)
		} else {
			out = append(out, es[i:i+head]...)
			out = append(out, edit{gap, ""})
			out = append(out, es[j-tail:j]...)
		}
		i = j
	}
	return out
}
