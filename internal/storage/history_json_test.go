package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"math/rand"
	"strings"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

func historyRows(t *testing.T, db *DB, typ, id string, after int64, limit int) []HistoryRow {
	t.Helper()
	var out []HistoryRow
	read(t, db, func(ctx context.Context, r *ReadTx) {
		var e error
		if out, e = r.HistoryRows(ctx, typ, id, after, limit); e != nil {
			t.Fatal(e)
		}
	})
	return out
}

// escapes needs every kind of string escape encoding/json makes: quotes,
// backslashes, HTML characters, the JavaScript line separators, short and
// \u00XX control escapes, and non-ASCII text.
const escapes = "<b>Fish & \"chips\"</b> \\ / \u2028 \u2029 \b\f\x01\x1f\t é ✓ 🐟\r\n"

// escapedIssue writes an issue whose bodies, titles and requests' sets hold
// escapes, and whose requests set and clear (null) the assignee and parent.
func escapedIssue(t *testing.T) (*DB, string) {
	t.Helper()
	db := openTemp(t)
	p := newProject(id(1), "feat/one", escapes, at(1))
	mustApply(t, db, at(1), p)
	i := newIssue(id(2), p.ID, "Title <&>", escapes, at(2))
	mustApply(t, db, at(2), i)
	who, parent := "who <them>", id(3)
	var none *string
	sets := []protocol.ProjectSet{
		{Assignee: func() **string { p := &who; return &p }(), ParentID: &none},
		{Assignee: &none, ParentID: func() **string { p := &parent; return &p }(), Labels: &[]string{"a", "<b>"}},
		{State: func() *string { s := "closed"; return &s }()},
		{},
	}
	bodies := []string{escapes + "more " + escapes, escapes + "more " + escapes, "", "\u2028"}
	for n, set := range sets {
		i = next(i, at(3+n))
		i.Body, i.Title = bodies[n], i.Title+" & more"
		r := bodyUpdate(t, at(3+n), i, n%2)
		in := r.Intent
		in.Operations[0].Set.Assignee, in.Operations[0].Set.ParentID = set.Assignee, set.ParentID
		in.Operations[0].Set.Labels, in.Operations[0].Set.State = set.Labels, set.State
		in.Operations[0].Add = protocol.ProjectMembers{Related: []string{id(9)}}
		b, hash, e := protocol.Canonical(in)
		if e != nil {
			t.Fatal(e)
		}
		if e = protocol.Decode(b, &in); e != nil {
			t.Fatal(e)
		}
		r = Request{Hash: hash, Intent: in, Timestamp: r.Timestamp, Results: r.Results}
		if e := write(t, db, func(ctx context.Context, tx *WriteTx) error { return tx.WriteRequest(ctx, r, []any{i}) }); e != nil {
			t.Fatal(e)
		}
	}
	return db, i.ID
}

// AppendHistory encodes the rows of a history, and of every keyset page of
// it, exactly as encoding/json encodes the entries History decodes from
// them: for every record type, imported history, and requests with escapes
// and cleared fields.
func TestAppendHistoryEncodesAsTheDecodedEntries(t *testing.T) {
	type owner struct {
		db       *DB
		typ, id  string
		revision int
	}
	var owners []owner
	db, issue := escapedIssue(t)
	owners = append(owners, owner{db, Issues, issue, 5}, owner{db, Projects, id(1), 1})
	w := newWorld(t)
	s := w.stamp()
	p, c, m := next(w.p, s), next(w.comment, s), next(w.milestone, s)
	p.Description, c.Body, m.Body, m.IssueIDs = "New description", "Edited", "Goal\nmore", []string{w.a.ID}
	mustApply(t, w.db, s, p, c, m)
	owners = append(owners, owner{w.db, Projects, p.ID, 0}, owner{w.db, Comments, c.ID, 0}, owner{w.db, Milestones, m.ID, 0}, owner{w.db, Issues, w.a.ID, 0})
	legacy := newLegacyWorld(t)
	owners = append(owners, owner{legacy.db, Issues, legacy.a.ID, 3}, owner{legacy.db, Projects, legacy.p.ID, 0})
	for _, o := range owners {
		all := history(t, o.db, o.typ, o.id, 0, 0)
		if o.revision > 0 && len(all) != o.revision {
			t.Fatalf("%s %s has %d revisions, want %d", o.typ, o.id, len(all), o.revision)
		}
		for after := range int64(len(all)) + 2 {
			for _, limit := range []int{0, 1, 2} {
				want, e := json.Marshal(history(t, o.db, o.typ, o.id, after, limit))
				if e != nil {
					t.Fatal(e)
				}
				rows := historyRows(t, o.db, o.typ, o.id, after, limit)
				got := AppendHistory([]byte("prefix"), o.id, rows)
				if !bytes.Equal(got, append([]byte("prefix"), want...)) {
					t.Fatalf("%s %s after %d limit %d encodes as\n%s\nnot\nprefix%s", o.typ, o.id, after, limit, got, want)
				}
				// One allocation of about the right size holds the whole array.
				if size := historySize(o.id, rows); len(want) > size || size-len(want) > 2+64*len(rows) {
					t.Fatalf("%s %s after %d limit %d: size %d for %d bytes", o.typ, o.id, after, limit, size, len(want))
				}
			}
		}
	}
}

// appendString encodes a string as encoding/json does, and quotedLen is its
// length, for strings of runes and bytes that need every kind of escape,
// including invalid UTF-8.
func TestAppendStringEncodesAsEncodingJSON(t *testing.T) {
	pieces := []string{"a", " ", "~", "\x7f", "\"", "\\", "/", "<", ">", "&", "\b", "\f", "\n", "\r", "\t", "\x00", "\x01", "\x1f",
		"é", "✓", "🐟", "\u2027", "\u2028", "\u2029", "\u202a", "\ufffd", "\xff", "\xe2\x80", "\xe2", "\xc3", "\xed\xa0\x80", "\xf4\x90\x80\x80"}
	check := func(s string) {
		t.Helper()
		want, e := json.Marshal(s)
		if e != nil {
			t.Fatal(e)
		}
		if got := appendString([]byte("x"), s); string(got) != "x"+string(want) {
			t.Fatalf("%q encodes as %s, not %s", s, got[1:], want)
		}
		if n := quotedLen(s); n != len(want) {
			t.Fatalf("%q: quotedLen %d, encoded length %d", s, n, len(want))
		}
	}
	check("")
	check(escapes)
	for _, p := range pieces {
		check(p)
		check("plain " + p + " text")
	}
	rnd := rand.New(rand.NewSource(1))
	for range 5000 {
		var b strings.Builder
		for range rnd.Intn(12) {
			b.WriteString(pieces[rnd.Intn(len(pieces))])
		}
		check(b.String())
	}
}

func FuzzAppendString(f *testing.F) {
	f.Add(escapes)
	f.Add("\xe2\x80\xa8\xe2\x80")
	f.Fuzz(func(t *testing.T, s string) {
		want, _ := json.Marshal(s)
		if got := appendString(nil, s); !bytes.Equal(got, want) || quotedLen(s) != len(want) {
			t.Fatalf("%q encodes as %s (%d), not %s", s, got, quotedLen(s), want)
		}
	})
}
