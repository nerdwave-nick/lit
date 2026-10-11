package export

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// frontMatter writes a YAML front-matter block by hand, so its bytes are
// stable: keys in call order, every string double quoted, sequences as block
// lists of quoted strings. Empty strings are kept; empty lists and absent
// optional values are omitted.
type frontMatter struct{ b strings.Builder }

func (f *frontMatter) str(key, value string) { fmt.Fprintf(&f.b, "%s: %s\n", key, yamlQuote(value)) }

func (f *frontMatter) int(key string, value int64) { fmt.Fprintf(&f.b, "%s: %d\n", key, value) }

// optional writes a string only when it is present and not empty.
func (f *frontMatter) optional(key string, value *string) {
	if value != nil && *value != "" {
		f.str(key, *value)
	}
}

// list writes a block sequence, or nothing for an empty list.
func (f *frontMatter) list(key string, values []string) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(&f.b, "%s:\n", key)
	for _, v := range values {
		fmt.Fprintf(&f.b, "  - %s\n", yamlQuote(v))
	}
}

func (f *frontMatter) String() string { return "---\n" + f.b.String() + "---\n" }

// yamlQuote writes s as a YAML double-quoted scalar on one line. Printable
// characters (YAML 1.2 c-printable, without line breaks and tabs) stay as they
// are; everything else, and the quote and backslash, is escaped. Invalid UTF-8
// bytes, which YAML cannot carry, become U+FFFD.
func yamlQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		i += n
		switch {
		case r == '"', r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == utf8.RuneError && n == 1:
			b.WriteString(`\uFFFD`)
		case yamlPrintable(r):
			b.WriteRune(r)
		case r <= 0xff:
			fmt.Fprintf(&b, `\x%02X`, r)
		case r <= 0xffff:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			fmt.Fprintf(&b, `\U%08X`, r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// yamlPrintable reports YAML's printable set, minus tab and line breaks
// (U+0085, U+2028 and U+2029 break lines too) and the byte order mark.
func yamlPrintable(r rune) bool {
	switch {
	case r >= 0x20 && r <= 0x7e:
		return true
	case r >= 0xa0 && r <= 0xd7ff:
		return r != 0x2028 && r != 0x2029
	case r >= 0xe000 && r <= 0xfffd:
		return r != 0xfeff
	}
	return r >= 0x10000 && r <= 0x10ffff
}
