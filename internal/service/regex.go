package service

import (
	"context"
	"regexp"
	"regexp/syntax"
	"strings"
	"unicode/utf8"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
)

// grepMatcher is how lit grep matches one request: the scan that reads its
// candidate records and the match of one field of a candidate.
type grepMatcher struct {
	scan  func(ctx context.Context, q reads, projectID string, fn func(storage.SearchRecord) error) error
	match func(field storage.SearchField) (protocol.SearchExcerpt, bool)
}

// newGrepMatcher is the matcher of q: a literal substring, or with q.Regex a
// Go RE2 pattern (validation_failed when it is not one).
func newGrepMatcher(q protocol.SearchRequest) (grepMatcher, error) {
	if !q.Regex {
		needle := grepNeedle(q.Query, q.CaseSensitive)
		return grepMatcher{
			scan: func(ctx context.Context, r reads, projectID string, fn func(storage.SearchRecord) error) error {
				return r.SearchScan(ctx, projectID, needle, fn)
			},
			match: func(f storage.SearchField) (protocol.SearchExcerpt, bool) {
				return searchExcerpt(f.Name, f.Text, q.Query, q.CaseSensitive, q.ContextLines)
			},
		}, nil
	}
	re, needles, e := compileGrepRegex(q.Query, q.CaseSensitive)
	if e != nil {
		return grepMatcher{}, e
	}
	return grepMatcher{
		scan: func(ctx context.Context, r reads, projectID string, fn func(storage.SearchRecord) error) error {
			return r.SearchScanAny(ctx, projectID, needles, fn)
		},
		match: func(f storage.SearchField) (protocol.SearchExcerpt, bool) {
			return regexExcerpt(f.Name, f.Text, re, q.ContextLines)
		},
	}, nil
}

// compileGrepRegex compiles a lit grep -E pattern: Go RE2 syntax,
// case-insensitive (RE2's simple case folding) unless sensitive. It returns
// the regexp and the folded literals that prefilter its candidates (nil:
// every record is one).
func compileGrepRegex(pattern string, sensitive bool) (*regexp.Regexp, []string, error) {
	// The pattern alone first, so that errors quote only what was sent.
	if _, e := regexp.Compile(pattern); e != nil {
		return nil, nil, invalid("query is not a valid Go RE2 regular expression: " + e.Error())
	}
	if !sensitive {
		pattern = "(?i)" + pattern
	}
	re, e := regexp.Compile(pattern)
	if e != nil {
		return nil, nil, invalid("query is not a valid Go RE2 regular expression: " + e.Error())
	}
	tree, e := syntax.Parse(pattern, syntax.Perl)
	if e != nil {
		return nil, nil, invalid("query is not a valid Go RE2 regular expression: " + e.Error())
	}
	return re, regexNeedles(tree), nil
}

// regexExcerpt matches re against body line by line, as grep does: each
// line on its own, so ^ and $ (and \A and \z) are the line's start and end,
// and no part of a match crosses a newline. The excerpt is literal grep's,
// around the first match.
func regexExcerpt(field, body string, re *regexp.Regexp, context int) (protocol.SearchExcerpt, bool) {
	before := 0 // scalars before the line
	for start := 0; ; {
		line, end := body[start:], -1
		if i := strings.IndexByte(line, '\n'); i >= 0 {
			line, end = line[:i], start+i
		}
		if loc := re.FindStringIndex(line); loc != nil {
			first := before + utf8.RuneCountInString(line[:loc[0]])
			last := first + utf8.RuneCountInString(line[loc[0]:loc[1]])
			return excerptAround(field, []rune(body), first, last, context), true
		}
		if end < 0 {
			return protocol.SearchExcerpt{}, false
		}
		before += utf8.RuneCountInString(line) + 1
		start = end + 1
	}
}
