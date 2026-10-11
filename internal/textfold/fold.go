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

// blocks caches the folds of the scalars below U+10000 in blocks of 256,
// each computed on first use; "" means the scalar folds to itself.
var blocks [0x100]struct {
	once  sync.Once
	folds *[0x100]string
}

// bmpFold returns the fold of r (below U+10000) and whether it differs from r.
func bmpFold(r rune) (string, bool) {
	b := &blocks[r>>8]
	b.once.Do(func() {
		b.folds = new([0x100]string)
		c := cases.Fold()
		for i := range b.folds {
			s := string(r&^0xff | rune(i))
			if f := c.String(s); f != s {
				b.folds[i] = f
			}
		}
	})
	f := b.folds[r&0xff]
	return f, f != ""
}

// Scalar returns the full case fold of the single scalar r: one or more
// scalars, such as "ss" for ß.
func Scalar(r rune) string {
	switch {
	case r < utf8.RuneSelf:
		if 'A' <= r && r <= 'Z' {
			r += 'a' - 'A'
		}
	case r < 0x10000:
		if f, ok := bmpFold(r); ok {
			return f
		}
	default:
		return cases.Fold().String(string(r))
	}
	return string(r)
}

// Grep folds s scalar by scalar: the concatenation of Scalar of each scalar
// of s, reading invalid bytes as U+FFFD, without normalization. Unlike Fold,
// it keeps substrings: Grep(a+b) == Grep(a)+Grep(b), so a substring of s folds
// to a substring of Grep(s). lit grep matches folded queries against it.
func Grep(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r < utf8.RuneSelf:
			if 'A' <= r && r <= 'Z' {
				r += 'a' - 'A'
			}
			b.WriteByte(byte(r))
		case r < 0x10000:
			if f, ok := bmpFold(r); ok {
				b.WriteString(f)
			} else {
				b.WriteRune(r)
			}
		default:
			b.WriteString(Scalar(r))
		}
	}
	return b.String()
}
