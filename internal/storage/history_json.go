package storage

import (
	"slices"
	"strconv"
	"unicode/utf8"
)

// AppendHistory appends the history rows of the record id as a JSON array,
// byte for byte as encoding/json encodes the []HistoryEntry that History
// decodes from them, without decoding and encoding each request again.
//
// The stored JSON is copied as it is: the intent is the request's canonical
// encoding (HistoryRows checks it against the request hash), and the results
// and differences are encoding/json's compact output of the values History
// decodes, which encode back to the same bytes. Only the strings are encoded
// here, and a body hunk's before, which is the previous hunk's after, is
// encoded once.
func AppendHistory(b []byte, id string, rows []HistoryRow) []byte {
	b = slices.Grow(b, historySize(id, rows))
	var after string
	var quotedAfter []byte
	b = append(b, '[')
	for n, r := range rows {
		if n > 0 {
			b = append(b, ',')
		}
		b = append(b, `{"schema_version":1,"owner_id":`...)
		b = appendString(b, id)
		b = append(b, `,"before_revision":`...)
		b = strconv.AppendInt(b, r.Revision-1, 10)
		b = append(b, `,"revision":`...)
		b = strconv.AppendInt(b, r.Revision, 10)
		b = append(b, `,"timestamp":`...)
		b = appendString(b, r.Timestamp)
		b = append(b, `,"request_hash":`...)
		b = appendString(b, r.RequestHash)
		b = append(b, `,"intent":`...)
		b = append(b, r.Intent...)
		b = append(b, `,"results":`...)
		b = append(b, r.Results...)
		b = append(b, `,"differences":`...)
		b = append(b, r.Differences...)
		b = append(b, `,"body_hunks":[`...)
		if r.Body != nil {
			b = append(b, `{"before":`...)
			if quotedAfter != nil && r.Body.Before == after {
				b = append(b, quotedAfter...)
			} else {
				b = appendString(b, r.Body.Before)
			}
			b = append(b, `,"after":`...)
			start := len(b)
			b = appendString(b, r.Body.After)
			after, quotedAfter = r.Body.After, b[start:]
			b = append(b, '}')
		}
		b = append(b, "]}"...)
	}
	return append(b, ']')
}

// historySize bounds the length of AppendHistory(nil, id, rows) from above,
// by at most 64 bytes per row and 2.
func historySize(id string, rows []HistoryRow) int {
	size, after, afterLen := 2, "", -1
	for _, r := range rows {
		size += len(`{"schema_version":1,"owner_id":,"before_revision":,"revision":,"timestamp":,"request_hash":,"intent":,"results":,"differences":,"body_hunks":[]},`)
		size += quotedLen(id) + 2*20 + quotedLen(r.Timestamp) + quotedLen(r.RequestHash) + len(r.Intent) + len(r.Results) + len(r.Differences)
		if r.Body != nil {
			if afterLen < 0 || r.Body.Before != after {
				afterLen = quotedLen(r.Body.Before)
			}
			after = r.Body.After
			size += len(`{"before":,"after":}`) + afterLen
			afterLen = quotedLen(after)
			size += afterLen
		}
	}
	return size
}

// plain marks the ASCII bytes encoding/json writes as they are.
var plain = func() (t [utf8.RuneSelf]bool) {
	for c := ' '; c < utf8.RuneSelf; c++ {
		t[c] = c != '"' && c != '\\' && c != '<' && c != '>' && c != '&'
	}
	return t
}()

const hexDigits = "0123456789abcdef"

// escape is how encoding/json writes s[i] when it is not plain and not part
// of a valid multi-byte rune other than U+2028 or U+2029, and the length of
// the input it stands for; ok is false for every other byte.
func escape(s string, i int) (e string, size int, ok bool) {
	c := s[i]
	if c < utf8.RuneSelf {
		if plain[c] {
			return "", 1, false
		}
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
	for i := 0; i < len(s); {
		if s[i] < utf8.RuneSelf && plain[s[i]] {
			i++
			continue
		}
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
	for i := 0; i < len(s); {
		if s[i] < utf8.RuneSelf && plain[s[i]] {
			i++
			continue
		}
		e, size, ok := escape(s, i)
		if ok {
			n += len(e) - size
		}
		i += size
	}
	return n
}
