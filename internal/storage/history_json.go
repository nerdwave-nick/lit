package storage

import (
	"slices"
	"strconv"
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
	bodies, hunks := quoteHunks(rows)
	size := 2
	for n, r := range rows {
		size += len(`{"schema_version":1,"owner_id":,"before_revision":,"revision":,"timestamp":,"request_hash":,"intent":,"results":,"differences":,"body_hunks":[]},`)
		size += quotedLen(id) + 2*20 + quotedLen(r.Timestamp) + quotedLen(r.RequestHash) + len(r.Intent) + len(r.Results) + len(r.Differences)
		if r.Body != nil {
			size += len(`{"before":,"after":}`) + hunks[n].before.len() + hunks[n].after.len()
		}
	}
	b = slices.Grow(b, size)
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
			b = append(b, hunks[n].before.of(bodies)...)
			b = append(b, `,"after":`...)
			b = append(b, hunks[n].after.of(bodies)...)
			b = append(b, '}')
		}
		b = append(b, "]}"...)
	}
	return append(b, ']')
}

// span is the part [start, end) of a buffer.
type span struct{ start, end int }

func (s span) len() int           { return s.end - s.start }
func (s span) of(b []byte) []byte { return b[s.start:s.end] }

// quotedHunk is where a body hunk's encoded before and after are.
type quotedHunk struct{ before, after span }

// quoteHunks encodes the rows' body hunks into bodies; hunks[n] locates row
// n's. A before that equals the previous hunk's after, as it does along a
// history, reuses that encoding, so each body is encoded once.
func quoteHunks(rows []HistoryRow) (bodies []byte, hunks []quotedHunk) {
	size := 0
	for _, r := range rows {
		if r.Body != nil {
			size += len(r.Body.After) + 2
		}
	}
	bodies = make([]byte, 0, size+size/8)
	hunks = make([]quotedHunk, len(rows))
	after, encoded := "", span{-1, -1} // the previous after
	for n, r := range rows {
		if r.Body == nil {
			continue
		}
		if encoded.start >= 0 && r.Body.Before == after {
			hunks[n].before = encoded
		} else {
			start := len(bodies)
			bodies = appendString(bodies, r.Body.Before)
			hunks[n].before = span{start, len(bodies)}
		}
		start := len(bodies)
		bodies = appendString(bodies, r.Body.After)
		hunks[n].after = span{start, len(bodies)}
		after, encoded = r.Body.After, hunks[n].after
	}
	return bodies, hunks
}
