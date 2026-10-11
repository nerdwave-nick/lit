package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/textfold"
)

// identity returns a record's type and identity fields.
func identity(r any) (typ, id string, rev int64, created, updated string, err error) {
	switch p := r.(type) {
	case protocol.Project:
		return Projects, p.ID, p.Revision, p.CreatedAt, p.UpdatedAt, nil
	case protocol.Issue:
		return Issues, p.ID, p.Revision, p.CreatedAt, p.UpdatedAt, nil
	case protocol.Comment:
		return Comments, p.ID, p.Revision, p.CreatedAt, p.UpdatedAt, nil
	case protocol.Milestone:
		return Milestones, p.ID, p.Revision, p.CreatedAt, p.UpdatedAt, nil
	}
	return "", "", 0, "", "", invalidWrite("unknown record %T", r)
}

// typeOrder writes owners before what refers to them.
var typeOrder = map[string]int{Projects: 0, Issues: 1, Comments: 2, Milestones: 3}

// change is one record of a request with its state before it.
type change struct {
	typ, id string
	before  any // nil on creation
	after   any
}

// writeRecords writes the changed records' rows, history rows and links, then
// checks the result: each record reads back as given, and changed parents and
// blocking edges form no cycle.
func (t *WriteTx) writeRecords(ctx context.Context, r Request, records []any) error {
	changes := make([]change, 0, len(records))
	seen := map[string]bool{}
	for _, rec := range records {
		typ, id, _, _, _, _ := identity(rec)
		if seen[typ+"/"+id] {
			return invalidWrite("%s %s changes twice", typ, id)
		}
		seen[typ+"/"+id] = true
		before, e := t.Record(ctx, typ, id)
		if errors.Is(e, ErrRecordNotFound) {
			before, e = nil, nil
		}
		if e != nil {
			return e
		}
		if e = checkStamps(r.Timestamp, before, rec); e != nil {
			return e
		}
		changes = append(changes, change{typ, id, before, rec})
	}
	sort.SliceStable(changes, func(i, j int) bool { return typeOrder[changes[i].typ] < typeOrder[changes[j].typ] })
	for _, c := range changes {
		if e := t.writeRow(ctx, c); e != nil {
			return e
		}
		if e := t.writeHistory(ctx, r.Hash, c); e != nil {
			return e
		}
	}
	var parents, links []string
	for _, c := range changes {
		if a, ok := c.after.(protocol.Issue); ok {
			var b protocol.Issue
			if c.before != nil {
				b = c.before.(protocol.Issue)
			}
			if !sameJSON(a.ParentID, b.ParentID) {
				if _, e := t.exec(ctx, "UPDATE issues SET parent_id = ? WHERE id = ?", a.ParentID, a.ID); e != nil {
					return e
				}
				parents = append(parents, a.ID)
			}
			changed, e := t.writeLinks(ctx, a, seen)
			if e != nil {
				return e
			}
			if changed {
				links = append(links, a.ID)
			}
		}
	}
	return t.checkWritten(ctx, changes, parents, links)
}

// checkStamps requires consecutive revisions stamped with the request time.
func checkStamps(stamp string, before, after any) error {
	typ, id, rev, created, updated, e := identity(after)
	if e != nil {
		return e
	}
	if before == nil {
		if rev != 1 || created != stamp || updated != stamp {
			return invalidWrite("creation of %s %s needs revision 1 stamped %s", typ, id, stamp)
		}
		return nil
	}
	_, _, old, oldCreated, _, _ := identity(before)
	if rev != old+1 || created != oldCreated || updated != stamp {
		return invalidWrite("update of %s %s needs revision %d, creation time %s and update time %s", typ, id, old+1, oldCreated, stamp)
	}
	return nil
}

// writeRow inserts or updates one record's row and its set-valued fields.
func (t *WriteTx) writeRow(ctx context.Context, c change) error {
	var e error
	switch a := c.after.(type) {
	case protocol.Project:
		var old []string
		if b, ok := c.before.(protocol.Project); ok {
			old = b.RepositoryRefs
			e = t.update(ctx, "UPDATE projects SET revision = ?, updated_at = ?, title = ?, description = ?, folded_description = ? WHERE id = ? AND revision = ?",
				a.Revision, a.UpdatedAt, a.Title, a.Description, textfold.Fold(a.Description), a.ID, a.Revision-1)
		} else {
			_, e = t.exec(ctx, "INSERT INTO projects (id, revision, created_at, updated_at, title, description, folded_description) VALUES (?, ?, ?, ?, ?, ?, ?)",
				a.ID, a.Revision, a.CreatedAt, a.UpdatedAt, a.Title, a.Description, textfold.Fold(a.Description))
		}
		if e == nil {
			e = t.syncSet(ctx, old, a.RepositoryRefs, "DELETE FROM project_repositories WHERE project_id = ? AND repository_ref = ?", "INSERT INTO project_repositories (project_id, repository_ref) VALUES (?, ?)", a.ID)
		}
	case protocol.Issue:
		var old []string
		if b, ok := c.before.(protocol.Issue); ok {
			old = b.Labels
			if a.ProjectID != b.ProjectID {
				return invalidWrite("issue %s cannot move to another project", a.ID)
			}
			e = t.update(ctx, "UPDATE issues SET revision = ?, updated_at = ?, title = ?, title_key = ?, body = ?, folded_body = ?, state = ?, assignee = ? WHERE id = ? AND revision = ?",
				a.Revision, a.UpdatedAt, a.Title, textfold.Fold(a.Title), a.Body, textfold.Fold(a.Body), a.State, a.Assignee, a.ID, a.Revision-1)
		} else {
			// The parent is set after every issue of the request exists.
			_, e = t.exec(ctx, "INSERT INTO issues (id, project_id, revision, created_at, updated_at, title, title_key, body, folded_body, state, assignee, parent_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)",
				a.ID, a.ProjectID, a.Revision, a.CreatedAt, a.UpdatedAt, a.Title, textfold.Fold(a.Title), a.Body, textfold.Fold(a.Body), a.State, a.Assignee)
		}
		if e == nil {
			e = t.syncSet(ctx, old, a.Labels, "DELETE FROM issue_labels WHERE issue_id = ? AND label = ?", "INSERT INTO issue_labels (issue_id, label) VALUES (?, ?)", a.ID)
		}
	case protocol.Comment:
		if b, ok := c.before.(protocol.Comment); ok {
			if a.IssueID != b.IssueID {
				return invalidWrite("comment %s cannot move to another issue", a.ID)
			}
			e = t.update(ctx, "UPDATE comments SET revision = ?, updated_at = ?, author = ?, body = ?, folded_body = ? WHERE id = ? AND revision = ?",
				a.Revision, a.UpdatedAt, a.Author, a.Body, textfold.Fold(a.Body), a.ID, a.Revision-1)
		} else {
			_, e = t.exec(ctx, "INSERT INTO comments (id, issue_id, revision, created_at, updated_at, author, body, folded_body) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
				a.ID, a.IssueID, a.Revision, a.CreatedAt, a.UpdatedAt, a.Author, a.Body, textfold.Fold(a.Body))
		}
	case protocol.Milestone:
		var old []string
		if b, ok := c.before.(protocol.Milestone); ok {
			old = b.IssueIDs
			if a.ProjectID != b.ProjectID {
				return invalidWrite("milestone %s cannot move to another project", a.ID)
			}
			e = t.update(ctx, "UPDATE milestones SET revision = ?, updated_at = ?, title = ?, title_key = ?, body = ?, folded_body = ? WHERE id = ? AND revision = ?",
				a.Revision, a.UpdatedAt, a.Title, textfold.Fold(a.Title), a.Body, textfold.Fold(a.Body), a.ID, a.Revision-1)
		} else {
			_, e = t.exec(ctx, "INSERT INTO milestones (id, project_id, revision, created_at, updated_at, title, title_key, body, folded_body) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
				a.ID, a.ProjectID, a.Revision, a.CreatedAt, a.UpdatedAt, a.Title, textfold.Fold(a.Title), a.Body, textfold.Fold(a.Body))
		}
		if e == nil {
			e = t.syncSet(ctx, old, a.IssueIDs, "DELETE FROM milestone_members WHERE milestone_id = ? AND issue_id = ?", "INSERT INTO milestone_members (milestone_id, issue_id, project_id) VALUES (?, ?, ?)", a.ID, a.ProjectID)
		}
	}
	return e
}

// update runs an UPDATE that must change exactly one row.
func (t *WriteTx) update(ctx context.Context, query string, args ...any) error {
	r, e := t.exec(ctx, query, args...)
	if e != nil {
		return e
	}
	if n, e := r.RowsAffected(); e != nil || n != 1 {
		return invalidWrite("record changed concurrently (%d rows, %v)", n, e)
	}
	return nil
}

// syncSet turns the stored set old into new for owner: del takes the owner
// and one value, ins the owner, one value and then extra.
func (t *WriteTx) syncSet(ctx context.Context, old, new []string, del, ins, owner string, extra ...any) error {
	have := map[string]bool{}
	for _, v := range old {
		have[v] = true
	}
	want := map[string]bool{}
	for _, v := range new {
		want[v] = true
		if !have[v] {
			if _, e := t.exec(ctx, ins, append([]any{owner, v}, extra...)...); e != nil {
				return e
			}
		}
	}
	for _, v := range old {
		if !want[v] {
			if _, e := t.exec(ctx, del, owner, v); e != nil {
				return e
			}
		}
	}
	return nil
}

// checkWritten reads every changed record back and compares its own fields
// with the record given, then checks changed parents and edges for cycles.
func (t *WriteTx) checkWritten(ctx context.Context, changes []change, parents, links []string) error {
	for _, c := range changes {
		got, e := t.Record(ctx, c.typ, c.id)
		if e != nil {
			return e
		}
		if !bytes.Equal(ownJSON(got), ownJSON(c.after)) {
			return invalidWrite("%s %s does not read back as written: %s", c.typ, c.id, ownJSON(got))
		}
	}
	if cycle, e := t.ParentCycle(ctx, parents); e != nil || cycle {
		return orInvalid(e, "parent cycle")
	}
	if cycle, e := t.BlockingCycle(ctx, links); e != nil || cycle {
		return orInvalid(e, "blocking cycle")
	}
	return nil
}

func orInvalid(e error, msg string) error {
	if e != nil {
		return e
	}
	return invalidWrite("%s", msg)
}

// ownFields is a record's JSON fields without its computed membership lists.
func ownFields(r any) map[string]json.RawMessage {
	b, _ := json.Marshal(r)
	m := map[string]json.RawMessage{}
	_ = json.Unmarshal(b, &m)
	switch r.(type) {
	case protocol.Project:
		delete(m, "issue_ids")
	case protocol.Issue:
		delete(m, "child_ids")
		delete(m, "comment_ids")
	}
	return m
}

func ownJSON(r any) []byte { b, _ := json.Marshal(ownFields(r)); return b }

func sameJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}
