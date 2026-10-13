package service

import (
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"testing"
)

func TestLinksMirroredFinalState(t *testing.T) {
	s, c := projectTestServer(t, t.TempDir())
	defer s.DB.Close()
	p, q := protocol.UUID(), protocol.UUID()
	executeProjectTest(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: p, Title: textPointer("feat/links")}, protocol.ProjectInput{ID: q, Title: textPointer("feat/other")}))
	a, b := protocol.UUID(), protocol.UUID()
	for _, x := range []struct{ id, project, title string }{{a, p, "A"}, {b, q, "B"}} {
		plan, e := s.PrepareRecords(protocol.PrepareRequest{Operation: "issue.create", Project: x.project, Items: []protocol.ProjectInput{{ID: x.id, Title: &x.title}}}, c)
		if e != nil {
			t.Fatal(e)
		}
		executeProjectTest(t, s, c, plan)
	}
	var input protocol.ProjectInput
	if e := protocol.Decode([]byte(`{"from":"`+a+`","to":["`+b+`"],"relation":"blocks"}`), &input); e != nil {
		t.Fatal(e)
	}
	plan, e := s.PrepareRecords(protocol.PrepareRequest{Operation: "issue.link", Items: []protocol.ProjectInput{input}}, c)
	if e != nil {
		t.Fatal(e)
	}
	if code, body := executeProjectTest(t, s, c, plan); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	ai, _ := s.Issue(a)
	bi, _ := s.Issue(b)
	if len(ai.Blocks) != 1 || ai.Blocks[0] != b || len(bi.BlockedBy) != 1 || bi.BlockedBy[0] != a {
		t.Fatal(ai, bi)
	}
}

func TestLinkRejectionLimitsAndCanonicalEquivalence(t *testing.T) {
	s, c := projectTestServer(t, t.TempDir())
	defer s.DB.Close()
	p := protocol.UUID()
	executeRecordTest(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: p, Title: textPointer("feat/limits")}))
	a, b, d := protocol.UUID(), protocol.UUID(), protocol.UUID()
	executeRecordTest(t, s, c, prepareRecordTest(t, s, c, p, "issue.create", protocol.ProjectInput{ID: a, Title: textPointer("A")}, protocol.ProjectInput{ID: b, Title: textPointer("B")}, protocol.ProjectInput{ID: d, Title: textPointer("C")}))
	req := func(items ...protocol.ProjectInput) (protocol.Prepared, error) {
		return s.PrepareRecords(protocol.PrepareRequest{Operation: "issue.link", Items: items}, c)
	}
	x, e := req(protocol.ProjectInput{From: a, To: []string{b}, Relation: "blocks"})
	if e != nil {
		t.Fatal(e)
	}
	y, e := req(protocol.ProjectInput{From: b, To: []string{a}, Relation: "blocked-by"})
	if e != nil || x.RequestHash != y.RequestHash {
		t.Fatal("direction changes canonical identity", e)
	}
	y, e = req(protocol.ProjectInput{From: a, To: []string{b, b}, Relation: "blocks"}, protocol.ProjectInput{From: a, To: []string{b}, Relation: "blocks"})
	if e != nil || x.RequestHash != y.RequestHash {
		t.Fatal("duplicate edges change canonical identity", e)
	}
	s.Config.Limits.ExplicitItems = 2
	plan, e := req(protocol.ProjectInput{From: a, To: []string{b}, Relation: "blocks"}, protocol.ProjectInput{From: b, To: []string{d}, Relation: "blocks"})
	if e != nil {
		t.Fatal("two edges expand to three records", e)
	}
	executeRecordTest(t, s, c, plan)
	before, _ := allRecords(s)
	if _, e = req(protocol.ProjectInput{From: d, To: []string{a}, Relation: "blocks"}, protocol.ProjectInput{From: a, To: []string{d}, Relation: "related"}); e == nil {
		t.Fatal("cyclic batch accepted")
	}
	after, _ := allRecords(s)
	if !same(before, after) {
		t.Fatal("rejected batch changed state")
	}
	s.Config.Limits.ExplicitItems = 1
	if _, e = req(protocol.ProjectInput{From: a, To: []string{b, d}, Relation: "related"}); e == nil {
		t.Fatal("logical edge cap bypassed")
	}
}

func TestRelationshipConvenienceRoute(t *testing.T) {
	s, c := projectTestServer(t, t.TempDir())
	defer s.DB.Close()
	p, a, b, d := protocol.UUID(), protocol.UUID(), protocol.UUID(), protocol.UUID()
	executeRecordTest(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: p, Title: textPointer("feat/routes")}))
	executeRecordTest(t, s, c, prepareRecordTest(t, s, c, p, "issue.create", protocol.ProjectInput{ID: a, Title: textPointer("A")}, protocol.ProjectInput{ID: b, Title: textPointer("B")}, protocol.ProjectInput{ID: d, Title: textPointer("D")}))
	plan := prepareRecordTest(t, s, c, p, "issue.link", protocol.ProjectInput{From: a, To: []string{b}, Relation: "blocks"})
	req := protocol.DurableRequest{Intent: plan.Intent, RequestHash: plan.RequestHash}
	code, _ := directRequest(t, s, c, "POST", "/v1/issues/"+d+"/relationship-changes", req)
	if code != 422 {
		t.Fatal("wrong route issue accepted", code)
	}
	code, v := directRequest(t, s, c, "POST", "/v1/issues/"+a+"/relationship-changes", req)
	if code != 200 {
		t.Fatal(code, v)
	}
	plan = prepareRecordTest(t, s, c, p, "issue.update", protocol.ProjectInput{Target: a, Set: protocol.ProjectSet{Body: textPointer("forbidden")}})
	code, _ = directRequest(t, s, c, "POST", "/v1/issues/"+a+"/relationship-changes", protocol.DurableRequest{Intent: plan.Intent, RequestHash: plan.RequestHash})
	if code != 422 {
		t.Fatal("ordinary update accepted", code)
	}
}
