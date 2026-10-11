package export

import (
	"regexp"
	"strings"
)

// line is one line of a Markdown text: its content and its terminator ("\n",
// "\r\n" or "" for a last line without one), so rewrites keep the bytes.
type line struct{ text, end string }

func splitLines(s string) []line {
	var out []line
	for _, l := range strings.SplitAfter(s, "\n") {
		if l == "" {
			continue
		}
		text := strings.TrimSuffix(l, "\n")
		end := l[len(text):]
		if strings.HasSuffix(text, "\r") && end == "\n" {
			text, end = text[:len(text)-1], "\r\n"
		}
		out = append(out, line{text, end})
	}
	return out
}

func joinLines(ls []line) string {
	var b strings.Builder
	for _, l := range ls {
		b.WriteString(l.text + l.end)
	}
	return b.String()
}

// indent counts leading spaces.
func indent(s string) int { return len(s) - len(strings.TrimLeft(s, " ")) }

// fences tracks fenced code blocks (CommonMark): an opening run of at least
// three backticks or tildes indented at most three spaces, closed by a run of
// the same character at least as long with nothing after it.
type fences struct {
	char byte
	size int
}

// step consumes a line and reports whether it belongs to a code fence,
// including the fence lines themselves.
func (f *fences) step(text string) bool {
	if indent(text) > 3 && f.size == 0 {
		return false
	}
	t := strings.TrimLeft(text, " ")
	run := 0
	for run < len(t) && (t[run] == '`' || t[run] == '~') && t[run] == t[0] {
		run++
	}
	if f.size > 0 {
		if run >= f.size && t[0] == f.char && indent(text) <= 3 && strings.TrimSpace(t[run:]) == "" {
			f.size = 0
		}
		return true
	}
	if run >= 3 && !(t[0] == '`' && strings.Contains(t[run:], "`")) {
		f.char, f.size = t[0], run
		return true
	}
	return false
}

var (
	atxHeading = regexp.MustCompile(`^( {0,3})(#{1,6})([ \t].*)?$`)
	underline  = regexp.MustCompile(`^ {0,3}(=+|-+)[ \t]*$`)
	blockStart = regexp.MustCompile(`^ {0,3}([>#<|]|[-*+]([ \t]|$)|\d{1,9}[.)]([ \t]|$)|(\*[ \t]*){3,}$|(_[ \t]*){3,}$)`)
)

// demoteHeadings lowers every heading outside code fences by two levels,
// down to level 6: ATX headings, and setext headings whose text is a single
// line after a blank line or the start, which become ATX headings.
func demoteHeadings(s string) string {
	var f fences
	var out []line
	paragraph := -1 // index in out of a single-line paragraph a setext underline may follow
	for _, l := range splitLines(s) {
		code := f.step(l.text)
		blank := strings.TrimSpace(l.text) == ""
		switch m := atxHeading.FindStringSubmatch(l.text); {
		case code:
		case m != nil:
			l.text = m[1] + strings.Repeat("#", min(len(m[2])+2, 6)) + m[3]
		case paragraph >= 0 && underline.MatchString(l.text):
			level := "###"
			if strings.Contains(l.text, "-") {
				level = "####"
			}
			p := &out[paragraph]
			p.text = level + " " + strings.TrimSpace(p.text)
			paragraph = -1
			continue
		}
		start := len(out) == 0 || strings.TrimSpace(out[len(out)-1].text) == ""
		paragraph = -1
		if !code && !blank && start && !blockStart.MatchString(l.text) && indent(l.text) <= 3 {
			paragraph = len(out)
		}
		out = append(out, l)
	}
	return joinLines(out)
}

const uuid = `[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`

var (
	inlineIssueLink = regexp.MustCompile(`\]\((<?)lit:issue:(` + uuid + `)([>)\s])`)
	issueLinkDef    = regexp.MustCompile(`^( {0,3}\[[^\]]+\]:[ \t]*)(<?)lit:issue:(` + uuid + `)([>\s]|$)`)
)

// rewriteIssueLinks turns Markdown link destinations lit:issue:UUID, inline or
// in reference definitions, into the paths target gives, outside code fences.
// Unknown issues, bare URIs and every other link stay as written.
func rewriteIssueLinks(s string, target func(id string) (string, bool)) string {
	var f fences
	ls := splitLines(s)
	for i, l := range ls {
		if f.step(l.text) {
			continue
		}
		replace := func(re *regexp.Regexp, idGroup int) {
			l.text = re.ReplaceAllStringFunc(l.text, func(m string) string {
				g := re.FindStringSubmatchIndex(m)
				path, ok := target(m[g[2*idGroup]:g[2*idGroup+1]])
				if !ok {
					return m
				}
				start := g[2*idGroup] - len("lit:issue:")
				return m[:start] + path + m[g[2*idGroup+1]:]
			})
		}
		replace(inlineIssueLink, 2)
		replace(issueLinkDef, 3)
		ls[i] = l
	}
	return joinLines(ls)
}

var inlineEscapes = strings.NewReplacer(`\`, `\\`, "`", "\\`", "*", `\*`, "_", `\_`, "[", `\[`, "]", `\]`, "<", `\<`, ">", `\>`, "#", `\#`)

// inline escapes text for a link text or heading, so it renders literally.
func inline(s string) string { return inlineEscapes.Replace(s) }

// longestRun is the longest run of backticks in any of the texts.
func longestRun(texts ...string) int {
	longest := 0
	for _, t := range texts {
		run := 0
		for i := 0; i < len(t); i++ {
			if t[i] != '`' {
				run = 0
				continue
			}
			run++
			longest = max(longest, run)
		}
	}
	return longest
}

// fenced wraps lines in a code fence longer than any backtick run inside, so
// bodies that contain fences cannot close it.
func fenced(info string, lines []string) string {
	fence := strings.Repeat("`", max(3, longestRun(lines...)+1))
	return fence + info + "\n" + strings.Join(lines, "\n") + "\n" + fence + "\n"
}

// codeSpan wraps s in a code span whose delimiter is longer than any
// backtick run inside.
func codeSpan(s string) string {
	fence := strings.Repeat("`", longestRun(s)+1)
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		s = " " + s + " "
	}
	return fence + s + fence
}
