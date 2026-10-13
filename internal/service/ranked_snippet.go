package service

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/storage"
)

// snippetRunes is the most characters of a field that a ranked search
// snippet shows; snippetLead of them come before the first match when the
// field is longer.
const (
	snippetRunes = 160
	snippetLead  = 40
	ellipsis     = "…"
)

// rankedSnippet is the match of a field: the text around its first match
// (from the start without one), cut at word boundaries where possible, with
// each whitespace run shown as one space, an ellipsis where the field
// continues, and the matched words as byte ranges in the snippet.
func rankedSnippet(f storage.RankedField) protocol.RankedMatch {
	text := f.Text
	first, firstEnd := 0, 0
	if len(f.Matches) > 0 {
		first, firstEnd = f.Matches[0][0], f.Matches[0][1]
	}
	start := snippetStart(text, first)
	end := snippetEnd(text, start, firstEnd)
	for start < end {
		r, size := utf8.DecodeRuneInString(text[start:])
		if !unicode.IsSpace(r) {
			break
		}
		start += size
	}
	for end > start {
		r, size := utf8.DecodeLastRuneInString(text[:end])
		if !unicode.IsSpace(r) {
			break
		}
		end -= size
	}
	var b strings.Builder
	if strings.TrimSpace(text[:start]) != "" {
		b.WriteString(ellipsis)
	}
	// at maps each byte offset of the window to its offset in the snippet.
	at := make([]int, end-start+1)
	space := false
	for i, r := range text[start:end] {
		if unicode.IsSpace(r) {
			if !space {
				at[i] = b.Len()
				b.WriteByte(' ')
			} else {
				at[i] = b.Len()
			}
			space = true
			continue
		}
		space = false
		at[i] = b.Len()
		b.WriteRune(r)
	}
	at[end-start] = b.Len()
	if strings.TrimSpace(text[end:]) != "" {
		b.WriteString(ellipsis)
	}
	m := protocol.RankedMatch{Field: f.Name, CommentID: f.CommentID, Snippet: b.String(), Highlights: []protocol.Highlight{}}
	for _, r := range f.Matches {
		s, e := max(r[0], start), min(r[1], end)
		if s < e {
			m.Highlights = append(m.Highlights, protocol.Highlight{Start: at[s-start], End: at[e-start]})
		}
	}
	return m
}

// snippetStart is where a snippet whose first match begins at first starts:
// snippetLead characters before it, moved forward to the next word when it
// falls inside one before the match.
func snippetStart(text string, first int) int {
	start := first
	for n := 0; n < snippetLead && start > 0; n++ {
		_, size := utf8.DecodeLastRuneInString(text[:start])
		start -= size
	}
	if start > 0 {
		r, _ := utf8.DecodeLastRuneInString(text[:start])
		if !unicode.IsSpace(r) {
			if k := strings.IndexFunc(text[start:first], unicode.IsSpace); k >= 0 {
				start += k
			}
		}
	}
	return start
}

// snippetEnd is where a snippet from start ends: after snippetRunes
// characters, moved back to the end of a word when it falls inside one after
// the first match, which ends at firstEnd.
func snippetEnd(text string, start, firstEnd int) int {
	end := start
	for n := 0; n < snippetRunes && end < len(text); n++ {
		_, size := utf8.DecodeRuneInString(text[end:])
		end += size
	}
	if end < len(text) {
		r, _ := utf8.DecodeRuneInString(text[end:])
		from := max(firstEnd, start)
		if !unicode.IsSpace(r) && end > from {
			if k := strings.LastIndexFunc(text[from:end], unicode.IsSpace); k >= 0 {
				end = from + k
			}
		}
	}
	return end
}
