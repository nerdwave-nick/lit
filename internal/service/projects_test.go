package service

import (
	"encoding/json"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"testing"
)

func TestProjectAtomicTransaction(t *testing.T) {
	_, h := fixture(t)
	_, v := request(t, h, "POST", "/v1/connect", `{"actor":{"name":"A","kind":"human"}}`, "", "")
	id := v["data"].(map[string]any)["client"].(map[string]any)["client_id"].(string)
	title := "feat/first"
	body := "# bytes\r\n雪\n"
	p := protocol.PrepareRequest{SchemaVersion: 1, Operation: "project.create", Items: []protocol.ProjectInput{{ID: protocol.UUID(), Title: &title, Content: &body}}}
	b, _ := json.Marshal(p)
	status, v := request(t, h, "POST", "/v1/transaction-previews", string(b), id, "")
	if status != 200 {
		t.Fatal(status, v)
	}
	raw, _ := json.Marshal(v["data"])
	var plan protocol.Prepared
	json.Unmarshal(raw, &plan)
	b, _ = json.Marshal(protocol.DurableRequest{SchemaVersion: 1, Intent: plan.Intent, RequestHash: plan.RequestHash})
	status, v = request(t, h, "POST", "/v1/transactions", string(b), id, "")
	if status != 200 {
		t.Fatal(status, v)
	}
	status, v = request(t, h, "GET", "/v1/projects/feat%2Ffirst", "", id, "")
	if status != 200 || v["data"].(map[string]any)["description"] != body {
		t.Fatal(status, v)
	}
	status, v = request(t, h, "POST", "/v1/transactions", string(b), id, "")
	if status != 409 {
		t.Fatal("stale creation", status, v)
	}
}

func TestProjectStrictSemanticInputs(t *testing.T) {
	s, c := projectTestServer(t, t.TempDir())
	defer s.DB.Close()
	id := protocol.UUID()
	plan := prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: id, Title: textPointer("feat/a")})
	executeProjectTest(t, s, c, plan)
	for _, raw := range []string{`{"target":"feat/a","set":{"title":null}}`, `{"target":"feat/a","set":{"repository_refs":null}}`, `{"target":"feat/a","set":null}`, `{"target":"feat/a","add":null}`, `{"target":"feat/a","remove":null}`, `{"target":"feat/a","title":null}`, `{"target":"feat/a","content":null}`, `{"target":"feat/a","repository_refs":null}`} {
		var item protocol.ProjectInput
		if e := protocol.Decode([]byte(raw), &item); e == nil {
			t.Errorf("accepted null input %s", raw)
		}
	}
	for _, field := range []string{`"set":{}`, `"add":{}`, `"remove":{}`, `"clear":[]`, `"target":""`} {
		raw := `{"id":"` + protocol.UUID() + `","title":"feat/new",` + field + `}`
		var item protocol.ProjectInput
		if e := protocol.Decode([]byte(raw), &item); e != nil {
			t.Fatal(e)
		}
		if _, e := s.PrepareProjects(protocol.PrepareRequest{Operation: "project.create", Items: []protocol.ProjectInput{item}}, c); e == nil {
			t.Error("inapplicable creation field accepted", raw)
		}
	}
	a := protocol.ProjectInput{Target: id, Add: protocol.ProjectMembers{RepositoryRefs: []string{"a"}}}
	b := protocol.ProjectInput{Target: id, Add: protocol.ProjectMembers{RepositoryRefs: []string{"b", "b"}}}
	for _, items := range [][]protocol.ProjectInput{{a, b}, {b, a}} {
		if _, e := s.PrepareProjects(protocol.PrepareRequest{Operation: "project.update", Items: items}, c); e == nil {
			t.Error("order-dependent duplicate acceptance")
		}
	}
	b.Add.RepositoryRefs = []string{"b"}
	p := prepareProjectTest(t, s, c, "update", a, b, a)
	if code, _ := executeProjectTest(t, s, c, p); code != 200 {
		t.Fatal(code)
	}
	record, e := s.Project(id)
	if e != nil || len(record.RepositoryRefs) != 2 || record.Revision != 2 {
		t.Fatal(record, e)
	}
}

func TestProjectConcurrentPreparedWrites(t *testing.T) {
	s, c := projectTestServer(t, t.TempDir())
	defer s.DB.Close()
	id := protocol.UUID()
	p := prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: id, Title: textPointer("feat/a")})
	executeProjectTest(t, s, c, p)
	a := prepareProjectTest(t, s, c, "update", protocol.ProjectInput{Target: id, Set: protocol.ProjectSet{Description: textPointer("A")}})
	b := prepareProjectTest(t, s, c, "update", protocol.ProjectInput{Target: id, Set: protocol.ProjectSet{Description: textPointer("B")}})
	results := make(chan int, 2)
	go func() { code, _ := executeProjectTest(t, s, c, a); results <- code }()
	go func() { code, _ := executeProjectTest(t, s, c, b); results <- code }()
	one, two := <-results, <-results
	if !(one == 200 && two == 409 || one == 409 && two == 200) {
		t.Fatal(one, two)
	}
	p2, e := s.Project(id)
	if e != nil || p2.Revision != 2 {
		t.Fatal(p2, e)
	}
	hs, _ := s.RecordHistory("projects", id)
	if len(hs) != 2 {
		t.Fatal(hs)
	}
}
