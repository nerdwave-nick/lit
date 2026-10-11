// Package textfold is lit's text folding for title keys and literal search:
// NFC, full Unicode case folding, then NFC again. It is shared by the service
// and the storage module, which stores folded copies of searchable text.
package textfold

import (
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// Fold returns the folded form of s, so that strasse and Straße compare equal.
func Fold(s string) string { return norm.NFC.String(cases.Fold().String(norm.NFC.String(s))) }
