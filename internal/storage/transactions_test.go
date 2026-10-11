package storage

import (
	"context"
	"fmt"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

const testService = "50000000-0000-4000-8000-000000000001"

var testActor = protocol.DurableActor{ClientID: "60000000-0000-4000-8000-000000000001", Name: "Test", Kind: "agent"}

// at is the n-th request time of a test, one second apart.
func at(n int) string { return fmt.Sprintf("2026-10-11T13:%02d:%02d.000000Z", n/60, n%60) }

// request builds the canonical request that changes records at stamp: one
// operation and target per record, and results from the records' revisions.
func request(t *testing.T, stamp string, records ...any) Request {
	t.Helper()
	i := protocol.Intent{SchemaVersion: 1, Operation: "transaction", ServiceID: testService, Actor: testActor}
	results := []protocol.ChangedObject{}
	for _, r := range records {
		typ, id, rev, _, _, e := identity(r)
		if e != nil {
			t.Fatal(e)
		}
		kind, expected := "create", (*int64)(nil)
		if rev > 1 {
			v := rev - 1
			kind, expected = "update", &v
		}
		i.Operations = append(i.Operations, protocol.Operation{Type: typ, ID: id, Kind: kind})
		i.Targets = append(i.Targets, protocol.ObjectRef{Type: typ, ID: id, ExpectedRevision: expected})
		results = append(results, protocol.ChangedObject{Type: typ, ID: id, BeforeRevision: rev - 1, Revision: rev})
	}
	b, hash, e := protocol.Canonical(i)
	if e != nil {
		t.Fatal(e)
	}
	if e = protocol.Decode(b, &i); e != nil {
		t.Fatal(e)
	}
	return Request{Hash: hash, Intent: i, Timestamp: stamp, Results: results}
}

// apply writes and commits one request built by request.
func apply(t *testing.T, db *DB, stamp string, records ...any) error {
	t.Helper()
	r := request(t, stamp, records...)
	return write(t, db, func(ctx context.Context, tx *WriteTx) error { return tx.WriteRequest(ctx, r, records) })
}

func mustApply(t *testing.T, db *DB, stamp string, records ...any) {
	t.Helper()
	if e := apply(t, db, stamp, records...); e != nil {
		t.Fatal(e)
	}
}

func newProject(id, title, description, stamp string, repos ...string) protocol.Project {
	return protocol.Project{SchemaVersion: 1, ID: id, Revision: 1, CreatedAt: stamp, UpdatedAt: stamp, Title: title, Description: description, RepositoryRefs: append([]string{}, repos...), IssueIDs: []string{}}
}

func newIssue(id, project, title, body, stamp string) protocol.Issue {
	return protocol.Issue{SchemaVersion: 1, ID: id, Revision: 1, CreatedAt: stamp, UpdatedAt: stamp, Title: title, Body: body, State: "open", Labels: []string{}, ProjectID: project, ChildIDs: []string{}, CommentIDs: []string{}, Blocks: []string{}, BlockedBy: []string{}, Related: []string{}}
}

func newComment(id, issue, body, stamp string) protocol.Comment {
	return protocol.Comment{SchemaVersion: 1, ID: id, Revision: 1, CreatedAt: stamp, UpdatedAt: stamp, Body: body, Author: "me", IssueID: issue}
}

func newMilestone(id, project, title, body, stamp string, members ...string) protocol.Milestone {
	return protocol.Milestone{SchemaVersion: 1, ID: id, Revision: 1, CreatedAt: stamp, UpdatedAt: stamp, Title: title, Body: body, ProjectID: project, IssueIDs: append([]string{}, members...)}
}

// next is a record's following revision, stamped at stamp.
func next[T protocol.Project | protocol.Issue | protocol.Comment | protocol.Milestone](r T, stamp string) T {
	switch p := any(&r).(type) {
	case *protocol.Project:
		p.Revision, p.UpdatedAt = p.Revision+1, stamp
	case *protocol.Issue:
		p.Revision, p.UpdatedAt = p.Revision+1, stamp
	case *protocol.Comment:
		p.Revision, p.UpdatedAt = p.Revision+1, stamp
	case *protocol.Milestone:
		p.Revision, p.UpdatedAt = p.Revision+1, stamp
	}
	return r
}

func TestTransactionsStoreEachRequestOnce(t *testing.T) {
	db := openTemp(t)
	p := newProject(id(1), "feat/one", "", at(1))
	r := request(t, at(1), p)
	if e := write(t, db, func(ctx context.Context, tx *WriteTx) error { return tx.WriteRequest(ctx, r, []any{p}) }); e != nil {
		t.Fatal(e)
	}
	read(t, db, func(ctx context.Context, q *ReadTx) {
		got, found, e := q.Transaction(ctx, r.Hash)
		if e != nil || !found {
			t.Fatal(found, e)
		}
		sameJSON(t, got, r)
		if _, found, e = q.Transaction(ctx, "unknown"); e != nil || found {
			t.Fatal("unknown hash", found, e)
		}
		if s, e := q.NewestStamp(ctx); e != nil || s != at(1) {
			t.Fatal(s, e)
		}
		var n int
		if e := q.queryRow(ctx, []any{&n}, "SELECT count(*) FROM transactions"); e != nil || n != 1 {
			t.Fatal(n, e)
		}
	})
	// The same intent again, even later, is the same request.
	q := next(p, at(2))
	q.Description = "changed"
	r.Timestamp = at(2)
	r.Results = []protocol.ChangedObject{{Type: Projects, ID: p.ID, BeforeRevision: 1, Revision: 2}}
	e := write(t, db, func(ctx context.Context, tx *WriteTx) error { return tx.WriteRequest(ctx, r, []any{q}) })
	if e == nil {
		t.Fatal("stored a request hash twice")
	}
}

func TestNewestStampOfAnEmptyDatabase(t *testing.T) {
	db := openTemp(t)
	read(t, db, func(ctx context.Context, q *ReadTx) {
		if s, e := q.NewestStamp(ctx); e != nil || s != "" {
			t.Fatal(s, e)
		}
	})
}

// id is a fixed, readable test UUID.
func id(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }
