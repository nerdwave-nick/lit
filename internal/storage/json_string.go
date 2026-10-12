package storage

import "unicode/utf8"

// plain marks the ASCII bytes encoding/json writes as they are.
var plain = func() (t [utf8.RuneSelf]bool) {
	for c := ' '; c < utf8.RuneSelf; c++ {
		t[c] = c != '"' && c != '\\' && c != '<' && c != '>' && c != '&'
	}
	return t
}()

// Bytes repeated in each byte of a word, for testing 8 bytes at once.
const (
	ones  = 0x0101010101010101
	highs = 0x8080808080808080
)

// zeroByte reports whether a byte of x is zero.
func zeroByte(x uint64) bool { return (x-ones)&^x&highs != 0 }

// skipPlain returns the index of the first byte at or after i that is not
// plain, or len(s). It tests 8 bytes at a time while all of them are.
func skipPlain(s string, i int) int {
	for ; i+8 <= len(s); i += 8 {
		x := uint64(s[i]) | uint64(s[i+1])<<8 | uint64(s[i+2])<<16 | uint64(s[i+3])<<24 |
			uint64(s[i+4])<<32 | uint64(s[i+5])<<40 | uint64(s[i+6])<<48 | uint64(s[i+7])<<56
		if x&highs != 0 || // a byte of a multi-byte rune or invalid UTF-8
			(x-' '*ones)&^x&highs != 0 || // a control character
			zeroByte(x^'"'*ones) || zeroByte(x^'\\'*ones) ||
			zeroByte(x^'<'*ones) || zeroByte(x^'>'*ones) || zeroByte(x^'&'*ones) {
			break
		}
	}
	for i < len(s) && s[i] < utf8.RuneSelf && plain[s[i]] {
		i++
	}
	return i
}

const hexDigits = "0123456789abcdef"

// escape is how encoding/json writes the input at s[i], which is not plain,
// and the length of that input; ok is false when it is written as it is (a
// multi-byte rune other than U+2028 and U+2029).
func escape(s string, i int) (e string, size int, ok bool) {
	c := s[i]
	if c < utf8.RuneSelf {
		switch c {
		case '"':
			return `\"`, 1, true
		case '\\':
			return `\\`, 1, true
		case '\b':
			return `\b`, 1, true
		case '\f':
			return `\f`, 1, true
		case '\n':
			return `\n`, 1, true
		case '\r':
			return `\r`, 1, true
		case '\t':
			return `\t`, 1, true
		}
		return `\u00` + hexDigits[c>>4:c>>4+1] + hexDigits[c&15:c&15+1], 1, true
	}
	switch r, size := utf8.DecodeRuneInString(s[i:]); {
	case r == utf8.RuneError && size == 1:
		return string(utf8.RuneError), 1, true
	case r == '\u2028':
		return `\u2028`, size, true
	case r == '\u2029':
		return `\u2029`, size, true
	default:
		return "", size, false
	}
}

// appendString appends s as encoding/json encodes a string: with HTML
// characters, U+2028 and U+2029 escaped and each byte of invalid UTF-8
// replaced by U+FFFD.
func appendString(b []byte, s string) []byte {
	b = append(b, '"')
	start := 0
	for i := skipPlain(s, 0); i < len(s); i = skipPlain(s, i) {
		e, size, ok := escape(s, i)
		if ok {
			b = append(append(b, s[start:i]...), e...)
			start = i + size
		}
		i += size
	}
	return append(append(b, s[start:]...), '"')
}

// quotedLen is the length of appendString(nil, s).
func quotedLen(s string) int {
	n := len(s) + 2
	for i := skipPlain(s, 0); i < len(s); i = skipPlain(s, i) {
		e, size, ok := escape(s, i)
		if ok {
			n += len(e) - size
		}
		i += size
	}
	return n
}
