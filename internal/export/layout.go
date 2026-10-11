package export

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// maxSlug is the longest slug in a file name.
const maxSlug = 40

// folds spells letters that do not decompose into an ASCII base letter.
var folds = map[rune]string{'ß': "ss", 'æ': "ae", 'œ': "oe", 'ø': "o", 'ł': "l", 'đ': "d", 'ð': "d", 'þ': "th", 'ı': "i"}

// slug makes a file-name part from a title: ASCII, lowercase, hyphen
// separated, at most maxSlug characters, cut at a hyphen in its second half
// when there is one. Accents are dropped; other characters separate words.
func slug(title string) string {
	var b strings.Builder
	hyphen := false
	for _, r := range norm.NFKD.String(strings.ToLower(title)) {
		var s string
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			s = string(r)
		case unicode.Is(unicode.Mn, r):
			continue
		default:
			s = folds[r]
		}
		if s == "" {
			hyphen = b.Len() > 0
			continue
		}
		if hyphen {
			b.WriteByte('-')
			hyphen = false
		}
		b.WriteString(s)
	}
	out := b.String()
	if len(out) > maxSlug {
		out = out[:maxSlug]
		if k := strings.LastIndexByte(out, '-'); k >= maxSlug/2 {
			out = out[:k]
		}
		out = strings.TrimRight(out, "-")
	}
	if out == "" {
		return "untitled"
	}
	return out
}

// projectDir maps a project title to its directory: each slash-separated
// segment becomes a directory. Titles allow only letters, digits, hyphens and
// underscores; anything else becomes a hyphen, and an empty or dot segment
// becomes "_", so a path never leaves the tree. A segment named issues or
// milestones below the second level could be a parent project's record
// directory, so it gets a leading underscore, which titles cannot start with.
func projectDir(title string) string {
	segments := strings.Split(title, "/")
	for i, s := range segments {
		if s == "" || strings.Trim(s, ".") == "" {
			segments[i] = "_"
			continue
		}
		s = strings.Map(func(r rune) rune {
			if r < 0x80 && (r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)) {
				return r
			}
			return '-'
		}, s)
		if i >= 2 && (s == "issues" || s == "milestones") {
			s = "_" + s
		}
		segments[i] = s
	}
	return strings.Join(segments, "/")
}

// rel is the relative link from the file from to the file to; both are
// slash-separated paths from the export root.
func rel(from, to string) string {
	var dir []string
	if k := strings.LastIndexByte(from, '/'); k >= 0 {
		dir = strings.Split(from[:k], "/")
	}
	target := strings.Split(to, "/")
	common := 0
	for common < len(dir) && common < len(target)-1 && dir[common] == target[common] {
		common++
	}
	return strings.Repeat("../", len(dir)-common) + strings.Join(target[common:], "/")
}

// historyFile is the sibling history file of a record file.
func historyFile(file string) string { return strings.TrimSuffix(file, ".md") + ".history.md" }
