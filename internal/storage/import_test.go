package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// legacyDiff is the legacy file store's history difference: every changed
// JSON field of the whole record, membership lists included, except the body.
func legacyDiff(typ string, before, after any) map[string]FieldDifference {
	fields := func(r any) map[string]json.RawMessage {
		m := map[string]json.RawMessage{}
		if r != nil {
			b, _ := json.Marshal(r)
			_ = json.Unmarshal(b, &m)
		}
		return m
	}
	a, b := fields(before), fields(after)
	delete(b, bodyField(typ))
	out := map[string]FieldDifference{}
	for k, v := range b {
		old := a[k]
		if old == nil {
			old = json.RawMessage("null")
		}
		if !bytes.Equal(old, v) {
			out[k] = FieldDifference{Before: old, After: v}
		}
	}
	return out
}

// legacyRevision is one legacy history entry of after, changed from before.
func legacyRevision(t *testing.T, hash string, before, after any) ImportRevision {
	t.Helper()
	typ, id, rev, _, _, e := identity(after)
	if e != nil {
		t.Fatal(e)
	}
	h := ImportRevision{OwnerType: typ, OwnerID: id, Revision: rev, RequestHash: hash, Differences: legacyDiff(typ, before, after)}
	old := ""
	if before != nil {
		old = bodyOf(before)
	}
	if old != bodyOf(after) {
		h.Body = &BodyHunk{Before: old, After: bodyOf(after)}
	}
	return h
}

// legacyWorld imports three legacy transactions: a project with an issue, a
// comment that changes only the issue's comment_ids, and a body edit.
type legacyWorld struct {
	db        *DB
	p         protocol.Project
	a         protocol.Issue
	c         protocol.Comment
	requests  []Request
	revisions []ImportRevision
}

func newLegacyWorld(t *testing.T) legacyWorld {
	t.Helper()
	w := legacyWorld{db: openTemp(t)}
	p := newProject(id(1), "feat/one", "Hello\n", at(1))
	a := newIssue(id(11), p.ID, "A", "A\n", at(1))
	p.IssueIDs = []string{a.ID}
	r1 := request(t, at(1), p, a)
	w.revisions = append(w.revisions, legacyRevision(t, r1.Hash, nil, p), legacyRevision(t, r1.Hash, nil, a))

	c := newComment(id(21), a.ID, "Comment", at(2))
	a2 := next(a, at(2))
	a2.CommentIDs = []string{c.ID}
	r2 := request(t, at(2), c, a2)
	w.revisions = append(w.revisions, legacyRevision(t, r2.Hash, nil, c), legacyRevision(t, r2.Hash, a, a2))

	a3 := next(a2, at(3))
	a3.Body = "A2\r\nno final newline"
	r3 := request(t, at(3), a3)
	w.revisions = append(w.revisions, legacyRevision(t, r3.Hash, a2, a3))

	w.p, w.a, w.c, w.requests = p, a3, c, []Request{r1, r2, r3}
	if e := write(t, w.db, func(ctx context.Context, tx *WriteTx) error { return w.importAll(ctx, tx) }); e != nil {
		t.Fatal(e)
	}
	return w
}

func (w legacyWorld) importAll(ctx context.Context, tx *WriteTx) error {
	for _, r := range w.requests {
		if e := tx.ImportTransaction(ctx, r); e != nil {
			return e
		}
	}
	// Records in any order: the comment before its issue, the issue before its project.
	if e := tx.ImportRecords(ctx, []any{w.c, w.a, w.p}); e != nil {
		return e
	}
	for _, h := range w.revisions {
		if e := tx.ImportRevision(ctx, h); e != nil {
			return e
		}
	}
	return nil
}

func TestCreateKeepsTheGivenServiceIdentity(t *testing.T) {
	const sid = "7a1d6f1e-2b3c-4d5e-8f90-123456789abc"
	dir := t.TempDir()
	db, e := Create(dir, sid)
	if e != nil {
		t.Fatal(e)
	}
	if db.ServiceID() != sid {
		t.Fatalf("service %q, want %q", db.ServiceID(), sid)
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	again, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer again.Close()
	if again.ServiceID() != sid {
		t.Fatalf("reopened service %q, want %q", again.ServiceID(), sid)
	}
}

func TestCreateRefusesAnExistingDatabaseOrAnInvalidIdentity(t *testing.T) {
	existing := t.TempDir()
	db, e := Open(existing)
	if e != nil {
		t.Fatal(e)
	}
	sid := db.ServiceID()
	db.Close()
	if db, e := Create(existing, protocol.UUID()); e == nil {
		db.Close()
		t.Fatal("created over an existing database")
	}
	again, e := Open(existing)
	if e != nil {
		t.Fatal(e)
	}
	if again.ServiceID() != sid {
		t.Fatal("the existing database changed its identity")
	}
	again.Close()

	empty := t.TempDir()
	if db, e := Create(empty, "not-a-uuid"); e == nil {
		db.Close()
		t.Fatal("created with an invalid service identity")
	}
	if entries, _ := os.ReadDir(empty); len(entries) != 0 {
		t.Fatalf("an invalid identity left %d files", len(entries))
	}
}

func TestImportedHistoryIsKeptVerbatimAndVerifies(t *testing.T) {
	w := newLegacyWorld(t)
	hs := history(t, w.db, Issues, w.a.ID, 0, 0)
	if len(hs) != 3 {
		t.Fatalf("%d issue revisions, want 3", len(hs))
	}
	// The comment's creation changed only the issue's comment_ids: kept verbatim.
	if d, ok := hs[1].Differences["comment_ids"]; !ok || string(d.After) != `["`+w.c.ID+`"]` || len(hs[1].BodyHunks) != 0 {
		t.Fatalf("membership revision %+v", hs[1])
	}
	if got := hs[2].BodyHunks; len(got) != 1 || got[0].Before != "A\n" || got[0].After != w.a.Body {
		t.Fatalf("body hunks %+v", got)
	}
	for i, h := range hs {
		if h.RequestHash != w.requests[i].Hash || h.Timestamp != w.requests[i].Timestamp {
			t.Fatalf("revision %d from %s at %s", h.Revision, h.RequestHash, h.Timestamp)
		}
		sameJSON(t, h.Differences, w.revisions[[]int{1, 3, 4}[i]].Differences)
	}
	if p := history(t, w.db, Projects, w.p.ID, 0, 0); len(p) != 1 || p[0].Differences["issue_ids"].After == nil {
		t.Fatalf("project history %+v", p)
	}
	read(t, w.db, func(ctx context.Context, q *ReadTx) {
		got, e := q.Issue(ctx, w.a.ID)
		if e != nil {
			t.Fatal(e)
		}
		sameJSON(t, got, w.a)
		r, found, e := q.Transaction(ctx, w.requests[1].Hash)
		if e != nil || !found {
			t.Fatal(found, e)
		}
		sameJSON(t, r, w.requests[1])
		n, e := q.Counts(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if want := (Counts{Projects: 1, Issues: 1, Comments: 1, Transactions: 3, History: 5}); n != want {
			t.Fatalf("counts %+v, want %+v", n, want)
		}
	})
	if r := verify(t, w.db); len(r.Problems) != 0 || r.Records != 3 || r.Revisions != 5 {
		t.Fatalf("verify %+v", r)
	}
}

func TestImportRefusesWhatItCannotStoreFaithfully(t *testing.T) {
	base := newLegacyWorld(t)
	p := base.p
	later := request(t, at(9), next(p, at(9)))
	cases := []struct {
		name string
		f    func(ctx context.Context, tx *WriteTx) error
	}{
		{"hash of another intent", func(ctx context.Context, tx *WriteTx) error {
			r := later
			r.Hash = base.requests[0].Hash
			return tx.ImportTransaction(ctx, r)
		}},
		{"timestamp not after the newest", func(ctx context.Context, tx *WriteTx) error {
			r := later
			r.Timestamp = at(3)
			return tx.ImportTransaction(ctx, r)
		}},
		{"timestamp in another form", func(ctx context.Context, tx *WriteTx) error {
			r := later
			r.Timestamp = "2026-10-11T13:00:09Z"
			return tx.ImportTransaction(ctx, r)
		}},
		{"body in the differences", func(ctx context.Context, tx *WriteTx) error {
			h := legacyRevision(t, base.requests[2].Hash, nil, next(p, at(3)))
			h.Revision = 2
			h.Differences["description"] = FieldDifference{raw(`"a"`), raw(`"b"`)}
			return tx.ImportRevision(ctx, h)
		}},
		{"unchanged body hunk", func(ctx context.Context, tx *WriteTx) error {
			h := legacyRevision(t, base.requests[2].Hash, p, next(p, at(3)))
			h.Body = &BodyHunk{Before: "same", After: "same"}
			return tx.ImportRevision(ctx, h)
		}},
		{"unknown owner type", func(ctx context.Context, tx *WriteTx) error {
			h := legacyRevision(t, base.requests[2].Hash, p, next(p, at(3)))
			h.OwnerType = "drafts"
			return tx.ImportRevision(ctx, h)
		}},
		{"revision of no transaction", func(ctx context.Context, tx *WriteTx) error {
			return tx.ImportRevision(ctx, legacyRevision(t, later.Hash, p, next(p, at(9))))
		}},
		{"parent cycle", func(ctx context.Context, tx *WriteTx) error {
			x, y := newIssue(id(41), p.ID, "X", "", at(1)), newIssue(id(42), p.ID, "Y", "", at(1))
			x.ParentID, y.ParentID = &y.ID, &x.ID
			return tx.ImportRecords(ctx, []any{x, y})
		}},
		{"unmirrored link", func(ctx context.Context, tx *WriteTx) error {
			x := newIssue(id(43), p.ID, "X", "", at(1))
			x.Blocks = []string{base.a.ID}
			return tx.ImportRecords(ctx, []any{x})
		}},
	}
	before := dump(t, base.db)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := write(t, base.db, c.f)
			if e == nil {
				t.Fatal("imported")
			}
			if c.name != "revision of no transaction" && !errors.Is(e, ErrInvalidWrite) {
				t.Fatalf("error %v is not ErrInvalidWrite", e)
			}
			if after := dump(t, base.db); after != before {
				t.Fatal("a refused import changed the database")
			}
		})
	}
}

func TestImportedLinksAndParentsReadBack(t *testing.T) {
	db := openTemp(t)
	p := newProject(id(1), "feat/one", "", at(1))
	a, b, c := newIssue(id(11), p.ID, "A", "", at(1)), newIssue(id(12), p.ID, "B", "", at(1)), newIssue(id(13), p.ID, "C", "", at(1))
	b.ParentID = &a.ID
	a.ChildIDs = []string{b.ID}
	a.Blocks, c.BlockedBy = []string{c.ID}, []string{a.ID}
	b.Related, c.Related = []string{c.ID}, []string{b.ID}
	a.Labels = []string{"bug", "ui"}
	m := newMilestone(id(31), p.ID, "M1", "", at(1), a.ID, c.ID)
	p.IssueIDs = []string{a.ID, b.ID, c.ID}
	if e := write(t, db, func(ctx context.Context, tx *WriteTx) error {
		return tx.ImportRecords(ctx, []any{m, c, b, a, p})
	}); e != nil {
		t.Fatal(e)
	}
	read(t, db, func(ctx context.Context, q *ReadTx) {
		for _, want := range []any{p, a, b, c, m} {
			typ, rid, _, _, _, _ := identity(want)
			got, e := q.Record(ctx, typ, rid)
			if e != nil {
				t.Fatal(e)
			}
			sameJSON(t, got, want)
		}
	})
	if !strings.Contains(dump(t, db), "blocks") {
		t.Fatal("no link rows")
	}
}

func TestCreateLeavesNoFilesBehindOnAMissingDirectoryParent(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if e := os.WriteFile(file, nil, 0o600); e != nil {
		t.Fatal(e)
	}
	if db, e := Create(filepath.Join(file, "sub"), protocol.UUID()); e == nil {
		db.Close()
		t.Fatal("created below a file")
	}
}

func TestRecordIDsListsEveryRecordOfAType(t *testing.T) {
	w := newLegacyWorld(t)
	read(t, w.db, func(ctx context.Context, q *ReadTx) {
		for typ, want := range map[string][]string{Projects: {w.p.ID}, Issues: {w.a.ID}, Comments: {w.c.ID}, Milestones: {}} {
			got, e := q.RecordIDs(ctx, typ)
			if e != nil {
				t.Fatal(e)
			}
			sameJSON(t, got, want)
		}
		if _, e := q.RecordIDs(ctx, "drafts"); e == nil {
			t.Fatal("listed an unknown type")
		}
	})
}
