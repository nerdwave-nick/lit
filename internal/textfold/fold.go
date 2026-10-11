// Package textfold is lit's text folding. Fold (NFC, full Unicode case
// folding, then NFC again) makes title keys and the discovery text filter;
// Grep (full case folding of each scalar on its own) is what lit grep matches
// against. Both are shared by the service and the storage module, which stores
// folded copies of searchable text.
package textfold

import (
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// Fold returns the folded form of s, so that strasse and Straße compare equal.
func Fold(s string) string { return norm.NFC.String(cases.Fold().String(norm.NFC.String(s))) }

// casers reuses fold casers, which keep state and are not safe to share.
var casers = sync.Pool{New: func() any { c := cases.Fold(); return &c }}

// Scalar returns the full case fold of the single scalar r: one or more
// scalars, such as "ss" for ß.
func Scalar(r rune) string {
	if r < utf8.RuneSelf {
		if 'A' <= r && r <= 'Z' {
			r += 'a' - 'A'
		}
		return string(r)
	}
	c := casers.Get().(*cases.Caser)
	defer casers.Put(c)
	return c.String(string(r))
}

// Grep folds s scalar by scalar: the concatenation of Scalar of each scalar
// of s, reading invalid bytes as U+FFFD, without normalization. Unlike Fold,
// it keeps substrings: Grep(a+b) == Grep(a)+Grep(b), so a substring of s folds
// to a substring of Grep(s). lit grep matches folded queries against it.
func Grep(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		b.WriteString(Scalar(r))
	}
	return b.String()
}
