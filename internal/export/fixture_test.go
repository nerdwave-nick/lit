package export_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/storage"
)

const fixtureService = "50000000-0000-4000-8000-000000000001"

var (
	codex = protocol.DurableActor{ClientID: "60000000-0000-4000-8000-000000000001", Name: "codex", Kind: "agent"}
	nick  = protocol.DurableActor{ClientID: "60000000-0000-4000-8000-000000000002", Name: "nick", Kind: "human"}
)

// IDs of the fixture's records; each type has its own ID prefix.
const (
	p1, p2         = "11111111-0000-4000-8000-000000000001", "22222222-0000-4000-8000-000000000002"
	issueA, issueB = "aaaaaaaa-0000-4000-8000-000000000001", "bbbbbbbb-0000-4000-8000-000000000002"
	issueC, issueX = "cccccccc-0000-4000-8000-000000000003", "dddddddd-0000-4000-8000-000000000004"
	comment1       = "eeeeeeee-0000-4000-8000-000000000001"
	comment2       = "eeeeeeee-0000-4000-8000-000000000002"
	milestone1     = "ffffffff-0000-4000-8000-000000000001"
)

func at(n int) string { return fmt.Sprintf("2026-10-11T09:00:%02d.000000Z", n) }

// commit writes one durable request that leaves records in their given state.
func commit(t *testing.T, db *storage.DB, actor protocol.DurableActor, stamp string, records ...any) {
	t.Helper()
	intent := protocol.Intent{SchemaVersion: 1, Operation: "transaction", ServiceID: fixtureService, Actor: actor}
	var results []protocol.ChangedObject
	for _, r := range records {
		var typ, id string
		var rev int64
		switch p := r.(type) {
		case protocol.Project:
			typ, id, rev = "projects", p.ID, p.Revision
		case protocol.Issue:
			typ, id, rev = "issues", p.ID, p.Revision
		case protocol.Comment:
			typ, id, rev = "comments", p.ID, p.Revision
		case protocol.Milestone:
			typ, id, rev = "milestones", p.ID, p.Revision
		}
		kind := "create"
		var expected *int64
		if rev > 1 {
			kind, expected = "update", new(rev-1)
		}
		intent.Operations = append(intent.Operations, protocol.Operation{Type: typ, ID: id, Kind: kind})
		intent.Targets = append(intent.Targets, protocol.ObjectRef{Type: typ, ID: id, ExpectedRevision: expected})
		results = append(results, protocol.ChangedObject{Type: typ, ID: id, BeforeRevision: rev - 1, Revision: rev})
	}
	_, hash, e := protocol.Canonical(intent)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	w, e := db.LockWrites(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Unlock()
	tx, e := w.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = tx.WriteRequest(ctx, storage.Request{Hash: hash, Intent: intent, Timestamp: stamp, Results: results}, records); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
}

func project(id, title, description, stamp string, repos ...string) protocol.Project {
	return protocol.Project{SchemaVersion: 1, ID: id, Revision: 1, CreatedAt: stamp, UpdatedAt: stamp, Title: title, Description: description, RepositoryRefs: append([]string{}, repos...), IssueIDs: []string{}}
}

func issue(id, projectID, title, body, stamp string) protocol.Issue {
	return protocol.Issue{SchemaVersion: 1, ID: id, Revision: 1, CreatedAt: stamp, UpdatedAt: stamp, Title: title, Body: body, State: "open", Labels: []string{}, ProjectID: projectID, ChildIDs: []string{}, CommentIDs: []string{}, Blocks: []string{}, BlockedBy: []string{}, Related: []string{}}
}

func comment(id, issueID, author, body, stamp string) protocol.Comment {
	return protocol.Comment{SchemaVersion: 1, ID: id, Revision: 1, CreatedAt: stamp, UpdatedAt: stamp, Body: body, Author: author, IssueID: issueID}
}

// bump is the record's next revision at stamp.
func bump[T protocol.Project | protocol.Issue | protocol.Comment | protocol.Milestone](r *T, stamp string) {
	switch p := any(r).(type) {
	case *protocol.Project:
		p.Revision, p.UpdatedAt = p.Revision+1, stamp
	case *protocol.Issue:
		p.Revision, p.UpdatedAt = p.Revision+1, stamp
	case *protocol.Comment:
		p.Revision, p.UpdatedAt = p.Revision+1, stamp
	case *protocol.Milestone:
		p.Revision, p.UpdatedAt = p.Revision+1, stamp
	}
}

// fixture writes a small tracker with every exported feature: two projects,
// a parent with children, blockers, a cross-project relation, labels, an
// assignee, comments with headings and fences, body edits, a closed issue,
// a milestone whose members change, and lit:issue and author links.
func fixture(t *testing.T) *storage.DB {
	t.Helper()
	db, e := storage.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	pa := project(p1, "feat/lit-export", "Export the tracker for people.\n\nStart with [the plan](lit:issue:"+issueA+").\n", at(1), "https://github.com/nerdwave-nick/lit")
	pb := project(p2, "fix/other-project", "", at(1))
	commit(t, db, nick, at(1), pa, pb)

	a := issue(issueA, p1, "Plan the export: layout & naming", "## Goal\n\nWrite [the writer](lit:issue:"+issueB+") and see [elsewhere](lit:issue:"+issueX+").\nRead [the notes](docs/notes.md) first.\n\n```sh\n# not a heading\n```\n", at(2))
	a.Labels, a.Assignee = []string{"planning"}, new("nick")
	b := issue(issueB, p1, "Write the front-matter writer", "Quote every string.", at(2))
	b.ParentID = new(issueA)
	c := issue(issueC, p1, "Ünïcode title — with a very long name that needs cutting", "", at(2))
	c.ParentID = new(issueA)
	commit(t, db, codex, at(2), a, b, c)

	x := issue(issueX, p2, "Cross-project issue", "Relates to the writer.\r\nCRLF body.\r\n", at(3))
	bump(&b, at(3))
	bump(&c, at(3))
	c.Blocks, b.BlockedBy = []string{issueB}, []string{issueC}
	b.Related, x.Related = []string{issueX}, []string{issueB}
	commit(t, db, codex, at(3), x, b, c)

	c1 := comment(comment1, issueA, "codex", "## Findings\n\nAll good.\n\n```\n# code stays\n```\n\nSetext\n------\n", at(4))
	c2 := comment(comment2, issueA, "nick", "Looks fine.", at(4))
	commit(t, db, codex, at(4), c1, c2)

	m := protocol.Milestone{SchemaVersion: 1, ID: milestone1, Revision: 1, CreatedAt: at(5), UpdatedAt: at(5), Title: "M1: Export", Body: "Deliver the export.\n", ProjectID: p1, IssueIDs: []string{issueA, issueB}}
	commit(t, db, nick, at(5), m)

	bump(&a, at(6))
	a.Body = "## Goal\n\nWrite [the writer](lit:issue:" + issueB + ") and see [elsewhere](lit:issue:" + issueX + ").\nRead [the notes](docs/notes.md) first.\n\n```sh\n# not a heading\n```\n\n## Plan\n\n1. Layout\n2. Naming\n"
	bump(&pa, at(6))
	pa.Description = "Export the tracker for people, one way.\n\nStart with [the plan](lit:issue:" + issueA + ").\n"
	commit(t, db, codex, at(6), a, pa)

	bump(&b, at(7))
	b.State, b.Body = "closed", "Quote every string.\nDone."
	bump(&m, at(7))
	m.IssueIDs = []string{issueA, issueB, issueC}
	commit(t, db, codex, at(7), b, m)

	bump(&c2, at(8))
	c2.Body = "Looks fine.\nShip it.\n"
	commit(t, db, nick, at(8), c2)

	bump(&a, at(9))
	a.Labels, a.Assignee = []string{"planning", "ready-for-agent"}, nil
	commit(t, db, nick, at(9), a)
	return db
}
