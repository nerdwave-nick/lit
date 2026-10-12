package storage

import (
	"strings"
	"unicode"
)

// The search box syntax of lit search, parsed into an FTS5 query. User text
// never reaches FTS5 as syntax: every term becomes a quoted FTS5 string, which
// the tokenizer splits into words, so C++ is the word c and net/http the
// phrase "net http".
//
//   - Words are ANDed.
//   - "a phrase" matches its words in order; a quote without its closing
//     quote runs to the end of the query.
//   - -word and -"a phrase" exclude records that contain them. A dash
//     followed by anything else, as in --force, is plain text.
//   - OR between two terms matches either; OR binds tighter than the
//     implicit AND, so a OR b c is (a OR b) AND c. Elsewhere OR is a word.
//   - word* matches words beginning with word.
//
// Everything else is plain text, including AND, NOT, NEAR, column filters
// and parentheses. Terms without a letter or digit (++, a lone dash) are
// dropped: FTS5 would find no word in them, and an empty term matches
// nothing.

// rankTerm is one term of a search box query.
type rankTerm struct {
	text             string
	prefix, negative bool
	or               bool // an unquoted OR, an operator if between two terms
}

// rankQuery is the FTS5 query of the search box query, or false when the
// query has nothing to search for (no words, or only exclusions).
func rankQuery(query string) (string, bool) {
	terms := lexRankQuery(query)
	var groups [][]string
	var excluded []string
	joined := false
	for i, t := range terms {
		switch {
		case t.or && i > 0 && i+1 < len(terms) && terms[i-1].positive() && terms[i+1].positive():
			joined = true
			continue
		case t.or:
			t.text = "OR"
		}
		if t.negative {
			excluded = append(excluded, t.fts())
		} else if joined {
			groups[len(groups)-1] = append(groups[len(groups)-1], t.fts())
		} else {
			groups = append(groups, []string{t.fts()})
		}
		joined = false
	}
	if len(groups) == 0 {
		return "", false
	}
	and := make([]string, len(groups))
	for i, g := range groups {
		and[i] = g[0]
		if len(g) > 1 {
			and[i] = "(" + strings.Join(g, " OR ") + ")"
		}
	}
	out := strings.Join(and, " AND ")
	if len(excluded) > 0 {
		// One NOT of all exclusions keeps the expression shallow.
		out = "(" + out + ") NOT (" + strings.Join(excluded, " OR ") + ")"
	}
	return out, true
}

func (t rankTerm) positive() bool { return !t.or && !t.negative }

// fts is the term as an FTS5 string, quotes doubled, with a prefix star.
func (t rankTerm) fts() string {
	s := `"` + strings.ReplaceAll(t.text, `"`, `""`) + `"`
	if t.prefix {
		s += " *"
	}
	return s
}

// lexRankQuery splits the query into its terms, dropping those without a
// word. NUL separates terms like a space, since FTS5 strings end at NUL.
func lexRankQuery(query string) []rankTerm {
	runes := []rune(query)
	space := func(r rune) bool { return r == 0 || unicode.IsSpace(r) }
	var terms []rankTerm
	for i := 0; i < len(runes); {
		if space(runes[i]) {
			i++
			continue
		}
		var t rankTerm
		if runes[i] == '-' && i+1 < len(runes) && (runes[i+1] == '"' || wordRune(runes[i+1])) {
			t.negative = true
			i++
		}
		start := i
		if runes[i] == '"' {
			start = i + 1
			for i = start; i < len(runes) && runes[i] != '"'; i++ {
			}
			t.text = string(runes[start:i])
			i++
		} else {
			for i < len(runes) && !space(runes[i]) {
				i++
			}
			t.text = string(runes[start:i])
			if !t.negative && t.text == "OR" {
				t.or = true
			} else if trimmed := strings.TrimRight(t.text, "*"); trimmed != t.text && trimmed != "" {
				t.text, t.prefix = trimmed, true
			}
		}
		t.text = strings.Map(func(r rune) rune {
			if r == 0 {
				return ' '
			}
			return r
		}, t.text)
		if t.or || strings.IndexFunc(t.text, wordRune) >= 0 {
			terms = append(terms, t)
		}
	}
	return terms
}

// wordRune reports whether r can be part of a word for FTS5's unicode61
// tokenizer: letters, digits and private use characters.
func wordRune(r rune) bool { return unicode.In(r, unicode.L, unicode.N, unicode.Co) }
