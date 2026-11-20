package hook

import (
	"strconv"
	"strings"
)

const (
	// spareColumns stay free at the right of the line: the host shows its
	// notifications there and may truncate a line that reaches them.
	spareColumns = 2
	separator    = " | "
)

// promptPwd shortens dir the way fish's prompt_pwd does: home becomes ~ and
// every directory but the last is cut to its first character (a leading dot is
// kept).
func promptPwd(dir, home string) string {
	if dir != "/" {
		dir = strings.TrimRight(dir, "/")
	}
	if home != "" && home != "/" {
		home = strings.TrimRight(home, "/")
		if dir == home || strings.HasPrefix(dir, home+"/") {
			dir = "~" + strings.TrimPrefix(dir, home)
		}
	}
	parts := strings.Split(dir, "/")
	for i, part := range parts[:len(parts)-1] {
		runes := []rune(part)
		switch {
		case part == "~" || len(runes) == 0:
		case runes[0] == '.' && len(runes) > 1:
			parts[i] = string(runes[:2])
		default:
			parts[i] = string(runes[:1])
		}
	}
	if short := strings.Join(parts, "/"); short != "" {
		return short
	}
	return "/"
}

// defaultLine is the built-in status line: model and effort, the short
// directory, git, the context used, then lit's part.
type defaultLine struct {
	Model, Effort string
	Dir           string // already shortened; empty when unknown
	Git           *gitInfo
	Percent       int
	Lit           litState
}

// render is the line at a collapse level. The levels drop or shorten one more
// thing each, in this order: the ahead/behind counts, the long "no project
// selected", the effort, the "context:" label, lit's wording, the directory
// to its last component, git, the model, the directory and the project.
func (d defaultLine) render(level int) string {
	var parts []string
	model := d.Model
	if level < 3 && d.Effort != "" {
		model = strings.TrimSpace(model + " " + d.Effort)
	}
	if level < 8 && model != "" {
		parts = append(parts, model)
	}
	if d.Dir != "" && level < 9 {
		dir := d.Dir
		if level >= 6 {
			dir = lastComponent(dir)
		}
		parts = append(parts, dir)
	}
	if d.Git != nil && level < 7 {
		parts = append(parts, d.Git.label(level < 1))
	}
	context := strconv.Itoa(d.Percent) + "%"
	if level < 4 {
		context = "context: " + context
	}
	parts = append(parts, context)
	parts = append(parts, d.Lit.segments(level)...)
	return strings.Join(parts, separator)
}

func lastComponent(dir string) string {
	if last := dir[strings.LastIndex(dir, "/")+1:]; last != "" {
		return last
	}
	return "/"
}

// fit renders at the fullest level that fits cols columns (with a little room
// spare); unknown cols (0) means no limit. The most collapsed form is used when
// none fits.
func fit(cols int, render func(level int) string) string {
	line := ""
	for level := 0; level <= maxLevel; level++ {
		if line = render(level); cols <= 0 || displayWidth(line) <= cols-spareColumns {
			break
		}
	}
	return line
}

// withOriginal puts lit's part after the last line of the original status line,
// shortening lit's part until the line fits cols. The original is never cut.
func withOriginal(original string, lit litState, cols int) string {
	head, last := "", original
	if i := strings.LastIndex(original, "\n"); i >= 0 {
		head, last = original[:i+1], original[i+1:]
	}
	return head + fit(cols, func(level int) string {
		return last + separator + strings.Join(lit.segments(level), separator)
	})
}

// originalColumns is the width handed to the original: the terminal's minus
// the room lit's most collapsed part, the separator and the spare columns take.
// It is 0 when the terminal's width is unknown.
func originalColumns(cols int, lit litState) int {
	if cols <= 0 {
		return 0
	}
	return max(1, cols-spareColumns-len(separator)-displayWidth(strings.Join(lit.segments(maxLevel), separator)))
}
