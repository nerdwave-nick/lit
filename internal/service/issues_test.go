package service

import (
	"github.com/nerdwave-nick/lit/internal/protocol"
	"strings"
	"testing"
)

func TestIssueHierarchyAtomic(t *testing.T) {
	s, c := projectTestServer(t, t.TempDir())
	defer s.DB.Close()
	project := protocol.UUID()
	executeProjectTest(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: project, Title: textPointer("feat/issues")}))
	parent, child := protocol.UUID(), protocol.UUID()
	prep := func(operation string, items ...protocol.ProjectInput) protocol.Prepared {
		t.Helper()
		p, e := s.PrepareRecords(protocol.PrepareRequest{Operation: operation, Project: project, Items: items}, c)
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	executeProjectTest(t, s, c, prep("issue.create", protocol.ProjectInput{ID: parent, Title: textPointer("Straße")}, protocol.ProjectInput{ID: child, Title: textPointer("Child"), Parent: &parent}))
	p, _ := s.Project(project)
	a, _ := s.Issue(parent)
	b, _ := s.Issue(child)
	// Membership lists are computed: creating issues does not revise their project.
	if p.Revision != 1 || len(p.IssueIDs) != 2 || a.Revision != 1 || len(a.ChildIDs) != 1 || b.ParentID == nil || *b.ParentID != parent {
		t.Fatal(p, a, b)
	}
	if _, e := s.ResolveIssue("STRASSE", project); e != nil {
		t.Fatal(e)
	}
	if _, e := s.PrepareRecords(protocol.PrepareRequest{Operation: "issue.update", Project: project, Items: []protocol.ProjectInput{{Target: parent, Parent: &child}}}, c); e == nil {
		t.Fatal("cycle accepted")
	}
	comment := protocol.UUID()
	executeProjectTest(t, s, c, prep("comment.create", protocol.ProjectInput{ID: comment, Issue: child, Content: textPointer("# Exact\r\n雪\n")}))
	b, _ = s.Issue(child)
	note, _ := s.Comment(comment)
	if b.Revision != 1 || len(b.CommentIDs) != 1 || note.Author != c.Actor.Name {
		t.Fatal(b, note)
	}
	executeProjectTest(t, s, c, prep("issue.update", protocol.ProjectInput{Target: child, Set: protocol.ProjectSet{State: textPointer("closed")}}))
}

func TestIssueStaleStructuralGuards(t *testing.T) {
	for _, mode := range []string{"project", "ancestor"} {
		t.Run(mode, func(t *testing.T) {
			s, c := projectTestServer(t, t.TempDir())
			defer s.DB.Close()
			project := protocol.UUID()
			executeProjectTest(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: project, Title: textPointer("feat/guard")}))
			parent, child := protocol.UUID(), protocol.UUID()
			prepare := func(op string, items ...protocol.ProjectInput) protocol.Prepared {
				t.Helper()
				p, e := s.PrepareRecords(protocol.PrepareRequest{Operation: op, Project: project, Items: items}, c)
				if e != nil {
					t.Fatal(e)
				}
				return p
			}
			executeProjectTest(t, s, c, prepare("issue.create", protocol.ProjectInput{ID: parent, Title: textPointer("Parent")}, protocol.ProjectInput{ID: child, Title: textPointer("Child")}))
			item := protocol.ProjectInput{Target: "feat/guard:Child", Set: protocol.ProjectSet{Body: textPointer("pending")}}
			if mode == "ancestor" {
				item.Parent = &parent
			}
			pending := prepare("issue.update", item)
			if mode == "project" {
				executeProjectTest(t, s, c, prepareProjectTest(t, s, c, "update", protocol.ProjectInput{Target: project, Set: protocol.ProjectSet{Title: textPointer("feat/renamed")}}))
			} else {
				executeProjectTest(t, s, c, prepare("issue.update", protocol.ProjectInput{Target: parent, Set: protocol.ProjectSet{Body: textPointer("changed ancestor")}}))
			}
			before, _ := s.Issue(child)
			hs, _ := s.RecordHistory("issues", child)
			code, body := executeProjectTest(t, s, c, pending)
			if code != 409 {
				t.Fatalf("stale structural guard accepted: %d %s", code, body)
			}
			after, _ := s.Issue(child)
			newHistory, _ := s.RecordHistory("issues", child)
			if !same(before, after) || len(newHistory) != len(hs) {
				t.Fatal("stale guard changed owner")
			}
		})
	}
}
func TestIssueTitleAndSemanticValidation(t *testing.T) {
	s, c := projectTestServer(t, t.TempDir())
	defer s.DB.Close()
	project := protocol.UUID()
	executeProjectTest(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: project, Title: textPointer("feat/title")}))
	prepare := func(op string, items ...protocol.ProjectInput) (protocol.Prepared, error) {
		return s.PrepareRecords(protocol.PrepareRequest{Operation: op, Project: project, Items: items}, c)
	}
	for _, title := range []string{"", " leading", "trailing\u00a0", "line\nfeed", "line\u2028separator", "paragraph\u2029separator", "zero\x00control"} {
		if _, e := prepare("issue.create", protocol.ProjectInput{ID: protocol.UUID(), Title: &title}); e == nil {
			t.Error("accepted title", title)
		}
	}
	title := strings.Repeat("雪", 128)
	p, e := prepare("issue.create", protocol.ProjectInput{ID: protocol.UUID(), Title: &title})
	if e != nil {
		t.Fatal(e)
	}
	executeProjectTest(t, s, c, p)
	title += "雪"
	if _, e = prepare("issue.create", protocol.ProjectInput{ID: protocol.UUID(), Title: &title}); e == nil {
		t.Fatal("scalar limit")
	}
	id := protocol.UUID()
	p, e = prepare("issue.create", protocol.ProjectInput{ID: id, Title: textPointer("Café")})
	if e != nil {
		t.Fatal(e)
	}
	executeProjectTest(t, s, c, p)
	if _, e = prepare("issue.create", protocol.ProjectInput{ID: protocol.UUID(), Title: textPointer("CAFE\u0301")}); e == nil {
		t.Fatal("NFC uniqueness")
	}
	if _, e = s.ResolveIssue("cafe\u0301", project); e != nil {
		t.Fatal(e)
	}
	for _, raw := range []string{`{"target":"` + id + `","set":{"body":null}}`, `{"target":"` + id + `","add":{"labels":null}}`, `{"target":"` + id + `","set":{"body":"x","body":"y"}}`} {
		var item protocol.ProjectInput
		if e := protocol.Decode([]byte(raw), &item); e == nil {
			t.Fatal("accepted invalid JSON", raw)
		}
	}
	for _, raw := range []string{`{"id":"` + protocol.UUID() + `","title":"bad","author":"x"}`, `{"id":"` + protocol.UUID() + `","title":"bad","set":{}}`} {
		var item protocol.ProjectInput
		if e := protocol.Decode([]byte(raw), &item); e != nil {
			t.Fatal(e)
		}
		if _, e := prepare("issue.create", item); e == nil {
			t.Fatal("inapplicable creation fields", raw)
		}
	}
	for _, items := range [][]protocol.ProjectInput{{{Target: id, Add: protocol.ProjectMembers{Labels: []string{"a"}}}, {Target: id, Add: protocol.ProjectMembers{Labels: []string{"b", "b"}}}}, {{Target: id, Clear: []string{"labels"}}, {Target: id, Add: protocol.ProjectMembers{Labels: []string{"a"}}}}, {{Target: id, Set: protocol.ProjectSet{Body: textPointer("a")}}, {Target: id, Set: protocol.ProjectSet{Body: textPointer("b")}}}} {
		if _, e = prepare("issue.update", items...); e == nil {
			t.Fatal("invalid duplicate accepted")
		}
	}
	before, _ := s.Issue(id)
	p, e = prepare("issue.update", protocol.ProjectInput{Target: id, Set: protocol.ProjectSet{Title: &before.Title}})
	if e != nil {
		t.Fatal(e)
	}
	code, b := executeProjectTest(t, s, c, p)
	if code != 200 || !strings.Contains(string(b), "already_satisfied") {
		t.Fatal(code, string(b))
	}
	after, _ := s.Issue(id)
	if !same(before, after) {
		t.Fatal("noop changed record")
	}
}

func TestContradictoryParentInputs(t *testing.T) {
	s, c := projectTestServer(t, t.TempDir())
	defer s.DB.Close()
	project, a, b, child := protocol.UUID(), protocol.UUID(), protocol.UUID(), protocol.UUID()
	executeRecordTest(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: project, Title: textPointer("feat/parents")}))
	executeRecordTest(t, s, c, prepareRecordTest(t, s, c, project, "issue.create", protocol.ProjectInput{ID: a, Title: textPointer("A")}, protocol.ProjectInput{ID: b, Title: textPointer("B")}, protocol.ProjectInput{ID: child, Title: textPointer("Child")}))
	for _, parent := range []*string{nil, &b} {
		_, e := s.PrepareRecords(protocol.PrepareRequest{Operation: "issue.update", Project: project, Items: []protocol.ProjectInput{{Target: child, Parent: &a, Set: protocol.ProjectSet{ParentID: nullable(parent)}}}}, c)
		if e == nil {
			t.Fatal("contradictory parent fields accepted")
		}
	}
}
