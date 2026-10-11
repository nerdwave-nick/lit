package service

import (
	"encoding/json"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// targetKeys lists a prepared intent's targets as "type/id".
func targetKeys(p protocol.Prepared) []string {
	out := []string{}
	for _, r := range p.Intent.Targets {
		out = append(out, r.Type+"/"+r.ID)
	}
	return out
}

func mustExecute(t *testing.T, s *Server, c protocol.Client, p protocol.Prepared) {
	t.Helper()
	if code, body := executeProjectTest(t, s, c, p); code != 200 && code != 201 {
		t.Fatalf("execute: %d %s", code, body)
	}
}

// Membership lists are computed on read: creating an issue, adding a comment
// or reparenting changes only the records whose own fields change.
func TestMembershipListsAreComputed(t *testing.T) {
	s, c := projectTestServer(t, t.TempDir())
	project := protocol.UUID()
	mustExecute(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: project, Title: textPointer("feat/members")}))
	prepare := func(operation string, items ...protocol.ProjectInput) protocol.Prepared {
		t.Helper()
		p, e := s.PrepareRecords(protocol.PrepareRequest{Operation: operation, Project: project, Items: items}, c)
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	// A project edit prepared before an issue is created still applies (story 11).
	edit := prepareProjectTest(t, s, c, "update", protocol.ProjectInput{Target: project, Set: protocol.ProjectSet{Description: textPointer("spec")}})

	a, b, other := protocol.UUID(), protocol.UUID(), protocol.UUID()
	create := prepare("issue.create", protocol.ProjectInput{ID: a, Title: textPointer("A")}, protocol.ProjectInput{ID: b, Title: textPointer("B"), Parent: &a}, protocol.ProjectInput{ID: other, Title: textPointer("Other")})
	if got := targetKeys(create); !same(got, sortedKeys(map[string]bool{"issues/" + a: true, "issues/" + b: true, "issues/" + other: true})) {
		t.Fatalf("issue creation targets %v", got)
	}
	mustExecute(t, s, c, create)
	mustExecute(t, s, c, edit)
	p, _ := s.Project(project)
	if p.Revision != 2 || !same(p.IssueIDs, sortedKeys(map[string]bool{a: true, b: true, other: true})) {
		t.Fatalf("project after issue creation and edit: %+v", p)
	}
	parent, _ := s.Issue(a)
	if parent.Revision != 1 || !same(parent.ChildIDs, []string{b}) {
		t.Fatalf("parent after child creation: %+v", parent)
	}

	comment := protocol.UUID()
	add := prepare("comment.create", protocol.ProjectInput{ID: comment, Issue: b, Content: textPointer("note")})
	if got := targetKeys(add); !same(got, []string{"comments/" + comment}) {
		t.Fatalf("comment creation targets %v", got)
	}
	mustExecute(t, s, c, add)
	child, _ := s.Issue(b)
	if child.Revision != 1 || !same(child.CommentIDs, []string{comment}) {
		t.Fatalf("issue after a comment: %+v", child)
	}

	move := prepare("issue.update", protocol.ProjectInput{Target: b, Parent: &other})
	if got := targetKeys(move); !same(got, []string{"issues/" + b}) {
		t.Fatalf("reparenting targets %v", got)
	}
	mustExecute(t, s, c, move)
	oldParent, _ := s.Issue(a)
	newParent, _ := s.Issue(other)
	child, _ = s.Issue(b)
	if oldParent.Revision != 1 || newParent.Revision != 1 || child.Revision != 2 || len(oldParent.ChildIDs) != 0 || !same(newParent.ChildIDs, []string{b}) {
		t.Fatalf("after reparenting: old %+v new %+v child %+v", oldParent, newParent, child)
	}

	// New history never records computed membership lists.
	for _, owner := range []struct{ typ, id string }{{"projects", project}, {"issues", a}, {"issues", b}, {"issues", other}} {
		hs, e := s.RecordHistory(owner.typ, owner.id)
		if e != nil {
			t.Fatal(e)
		}
		for _, h := range hs {
			for _, key := range []string{"issue_ids", "child_ids", "comment_ids"} {
				if _, ok := h.Differences[key]; ok {
					b, _ := json.Marshal(h)
					t.Fatalf("history of %s %s records %s: %s", owner.typ, owner.id, key, b)
				}
			}
		}
	}
}
