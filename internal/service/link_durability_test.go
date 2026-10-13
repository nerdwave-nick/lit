package service

import (
	"testing"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

// A link changes both of its issues together: a failure after the writes
// leaves neither endpoint linked, also after a restart.
func TestLinkIsAllOrNothing(t *testing.T) {
	root := t.TempDir()
	s, c := projectTestServer(t, root)
	p, q, a, b := protocol.UUID(), protocol.UUID(), protocol.UUID(), protocol.UUID()
	executeRecordTest(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: p, Title: textPointer("feat/one")}, protocol.ProjectInput{ID: q, Title: textPointer("feat/two")}))
	executeRecordTest(t, s, c, prepareRecordTest(t, s, c, p, "issue.create", protocol.ProjectInput{ID: a, Title: textPointer("One"), Content: textPointer("# exact\r\n雪")}))
	executeRecordTest(t, s, c, prepareRecordTest(t, s, c, q, "issue.create", protocol.ProjectInput{ID: b, Title: textPointer("Two")}))
	plan := prepareRecordTest(t, s, c, "", "issue.link", protocol.ProjectInput{From: a, To: []string{b}, Relation: "blocks"})
	s.Config.Limits.SnapshotBytes = 64
	if code, body := executeProjectTest(t, s, c, plan); code != 413 {
		t.Fatalf("oversized response: %d %s", code, body)
	}
	config := s.Config
	config.Limits = protocol.DefaultLimits()
	s.DB.Close()
	s = openTestServer(t, root, config)
	endpoints := func() (protocol.Issue, protocol.Issue) {
		left, _ := s.Issue(a)
		right, _ := s.Issue(b)
		return left, right
	}
	if left, right := endpoints(); len(left.Blocks) != 0 || len(right.BlockedBy) != 0 || left.Revision != 1 || right.Revision != 1 {
		t.Fatal("partial link", left, right)
	}
	executeRecordTest(t, s, c, plan)
	if left, right := endpoints(); !same(left.Blocks, []string{b}) || !same(right.BlockedBy, []string{a}) || left.Revision != 2 || right.Revision != 2 {
		t.Fatal("link not applied to both endpoints", left, right)
	}
}
