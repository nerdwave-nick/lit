package hook

import (
	"unicode"

	"golang.org/x/text/width"
)

// displayWidth is how many terminal columns s takes: escape sequences take
// none, combining marks and zero-width characters take none, wide characters
// (emoji, East Asian) take two and everything else one.
func displayWidth(s string) int {
	columns := 0
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == 0x1b:
			i = skipEscape(runes, i)
		case unicode.IsControl(r), unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf):
		default:
			columns++
			if k := width.LookupRune(r).Kind(); k == width.EastAsianWide || k == width.EastAsianFullwidth {
				columns++
			}
		}
	}
	return columns
}

// skipEscape returns the index of the last rune of the escape sequence that
// starts at runes[i]: a CSI sequence ends at its final byte, an OSC string
// (such as a hyperlink) at BEL or ESC \, any other escape after one more rune.
func skipEscape(runes []rune, i int) int {
	if i+1 >= len(runes) {
		return i
	}
	switch runes[i+1] {
	case '[':
		for j := i + 2; j < len(runes); j++ {
			if runes[j] >= 0x40 && runes[j] <= 0x7e {
				return j
			}
		}
	case ']':
		for j := i + 2; j < len(runes); j++ {
			if runes[j] == 0x07 {
				return j
			}
			if runes[j] == 0x1b && j+1 < len(runes) && runes[j+1] == '\\' {
				return j + 1
			}
		}
	default:
		return i + 1
	}
	return len(runes) - 1
}
