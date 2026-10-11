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

// bmp holds the fold of every scalar below U+10000 that folding changes,
// computed once on first use; the others fold to themselves.
var bmp struct {
	once  sync.Once
	folds []string
}

func bmpFolds() []string {
	bmp.once.Do(func() {
		bmp.folds = make([]string, 0x10000)
		c := cases.Fold()
		for r := rune(utf8.RuneSelf); r < 0x10000; r++ {
			if s, f := string(r), c.String(string(r)); f != s {
				bmp.folds[r] = f
			}
		}
	})
	return bmp.folds
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
		if f := bmpFolds()[r]; f != "" {
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
	folds := bmpFolds()
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r < utf8.RuneSelf:
			if 'A' <= r && r <= 'Z' {
				r += 'a' - 'A'
			}
			b.WriteByte(byte(r))
		case r < 0x10000 && folds[r] != "":
			b.WriteString(folds[r])
		case r < 0x10000:
			b.WriteRune(r)
		default:
			b.WriteString(Scalar(r))
		}
	}
	return b.String()
}
