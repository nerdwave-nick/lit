package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
)

// openTestServer opens (or reopens) the service on the data directory root.
func openTestServer(t *testing.T, root string, config Config) *Server {
	t.Helper()
	db, e := storage.OpenWith(root, storage.Options{ReadConcurrency: config.ReadConcurrency})
	if e != nil {
		t.Fatal(e)
	}
	s, e := New(db, config)
	if e != nil {
		db.Close()
		t.Fatal(e)
	}
	t.Cleanup(func() { s.DB.Close() })
	return s
}
func projectTestServer(t *testing.T, root string) (*Server, protocol.Client) {
	t.Helper()
	s := openTestServer(t, root, Config{Limits: protocol.DefaultLimits(), TitlePrefixes: []string{"feat", "fix"}})
	c := protocol.Client{SchemaVersion: 1, ClientID: protocol.UUID(), StateRevision: 1, Status: "connected", Actor: protocol.Actor{Name: "Test", Kind: "human"}}
	if e := s.SaveClient(c); e != nil {
		t.Fatal(e)
	}
	return s, c
}
func prepareProjectTest(t *testing.T, s *Server, c protocol.Client, kind string, items ...protocol.ProjectInput) protocol.Prepared {
	t.Helper()
	p, e := s.PrepareProjects(protocol.PrepareRequest{Operation: "project." + kind, Items: items}, c)
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func executeProjectTest(t *testing.T, s *Server, c protocol.Client, p protocol.Prepared) (int, []byte) {
	t.Helper()
	b, _ := json.Marshal(protocol.DurableRequest{SchemaVersion: 1, Intent: p.Intent, RequestHash: p.RequestHash})
	r := httptest.NewRequest("POST", "/v1/transactions", bytes.NewReader(b))
	r.Header.Set("X-Lit-Client-ID", c.ClientID)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w.Code, w.Body.Bytes()
}
func textPointer(s string) *string { return &s }

// A multi-record request that fails after all its writes, here because its
// response exceeds the limit, leaves every record as it was, also after a restart.
func TestMultiRecordRequestIsAllOrNothing(t *testing.T) {
	root := t.TempDir()
	s, c := projectTestServer(t, root)
	ids := []string{protocol.UUID(), protocol.UUID(), protocol.UUID()}
	create := []protocol.ProjectInput{}
	for n, id := range ids {
		create = append(create, protocol.ProjectInput{ID: id, Title: textPointer(fmt.Sprintf("feat/p%d", n)), Content: textPointer("before\r\n")})
	}
	mustExecute(t, s, c, prepareProjectTest(t, s, c, "create", create...))
	before := map[string][]byte{}
	for _, id := range ids {
		p, _ := s.Project(id)
		hs, _ := s.RecordHistory("projects", id)
		before[id] = mustJSON([]any{p, hs})
	}
	update := prepareProjectTest(t, s, c, "update", protocol.ProjectInput{Target: ids[0], Set: protocol.ProjectSet{Description: textPointer("after\n")}}, protocol.ProjectInput{Target: ids[1], Set: protocol.ProjectSet{Description: textPointer("after\n")}})
	limits := s.Config.Limits
	s.Config.Limits.SnapshotBytes = 100
	if code, body := executeProjectTest(t, s, c, update); code != 413 {
		t.Fatalf("oversized response: %d %s", code, body)
	}
	s.Config.Limits = limits
	if st, _ := s.Reconcile(protocol.ReconcileRequest{ServiceID: s.DB.ServiceID(), ClientID: c.ClientID, Method: "POST", Path: "/v1/transactions", Body: mustJSON(protocol.DurableRequest{SchemaVersion: 1, Intent: update.Intent, RequestHash: update.RequestHash})}, c); st.Status != "uncommitted" {
		t.Fatalf("failed request reconciles as %+v", st)
	}
	config := s.Config
	s.DB.Close()
	reopened := openTestServer(t, root, config)
	for _, id := range ids {
		p, e := reopened.Project(id)
		hs, _ := reopened.RecordHistory("projects", id)
		if e != nil || !bytes.Equal(mustJSON([]any{p, hs}), before[id]) {
			t.Fatalf("project %s changed by a failed request: %+v %v", id, p, e)
		}
	}
	if code, body := executeProjectTest(t, reopened, c, update); code != 200 {
		t.Fatalf("retry after restart: %d %s", code, body)
	}
}
func TestProjectResponseBoundBeforeCommit(t *testing.T) {
	s, c := projectTestServer(t, t.TempDir())
	id := protocol.UUID()
	p := prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: id, Title: textPointer("feat/a")})
	s.Config.Limits.SnapshotBytes = 100
	if code, _ := executeProjectTest(t, s, c, p); code != 413 {
		t.Fatal(code)
	}
	if _, e := s.Project(id); e == nil {
		t.Fatal("committed mutation rejected as too large")
	}
	if hs, _ := s.RecordHistory("projects", id); len(hs) != 0 {
		t.Fatal("rejected mutation added history")
	}
}
func TestProjectGuardAndNoopPreconditions(t *testing.T) {
	s, c := projectTestServer(t, t.TempDir())
	id, guard := protocol.UUID(), protocol.UUID()
	p := prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: id, Title: textPointer("feat/a")}, protocol.ProjectInput{ID: guard, Title: textPointer("feat/b")})
	executeProjectTest(t, s, c, p)
	noop := prepareProjectTest(t, s, c, "update", protocol.ProjectInput{Target: id, Set: protocol.ProjectSet{Title: textPointer("feat/a")}})
	rev := int64(1)
	noop.Intent.Guards = []protocol.ObjectRef{{Type: "projects", ID: guard, ExpectedRevision: &rev}}
	_, noop.RequestHash, _ = protocol.Canonical(noop.Intent)
	update := prepareProjectTest(t, s, c, "update", protocol.ProjectInput{Target: guard, Set: protocol.ProjectSet{Description: textPointer("changed")}})
	executeProjectTest(t, s, c, update)
	if code, _ := executeProjectTest(t, s, c, noop); code != 409 {
		t.Fatal("stale guard accepted noop", code)
	}
	if hs, _ := s.RecordHistory("projects", id); len(hs) != 1 {
		t.Fatal("guard changed owner")
	}
	update = prepareProjectTest(t, s, c, "update", protocol.ProjectInput{Target: id, Set: protocol.ProjectSet{Description: textPointer("change")}})
	executeProjectTest(t, s, c, update)
	noop.Intent.Guards = nil
	_, noop.RequestHash, _ = protocol.Canonical(noop.Intent)
	if code, _ := executeProjectTest(t, s, c, noop); code != 409 {
		t.Fatal("stale noop accepted", code)
	}
}

// A retired title prefix does not block a restart, and record stamps stay
// after the newest committed transaction even when it lies in the future.
func TestProjectRetiredPrefixAndClock(t *testing.T) {
	root := t.TempDir()
	s, c := projectTestServer(t, root)
	id := protocol.UUID()
	executeProjectTest(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: id, Title: textPointer("feat/a")}))
	executeProjectTest(t, s, c, prepareProjectTest(t, s, c, "update", protocol.ProjectInput{Target: id, Set: protocol.ProjectSet{Title: textPointer("fix/a")}}))
	config := s.Config
	s.DB.Close()
	config.TitlePrefixes = []string{"fix"}
	s = openTestServer(t, root, config)
	future := "2099-01-01T00:00:00.000000Z"
	other := protocol.Project{SchemaVersion: 1, ID: protocol.UUID(), Revision: 1, CreatedAt: future, UpdatedAt: future, Title: "fix/future", RepositoryRefs: []string{}, IssueIDs: []string{}}
	writeDirectly(t, s, c, future, other)
	p := prepareProjectTest(t, s, c, "update", protocol.ProjectInput{Target: id, Set: protocol.ProjectSet{Description: textPointer("later")}})
	mustExecute(t, s, c, p)
	if got, _ := s.Project(id); got.UpdatedAt != "2099-01-01T00:00:00.000001Z" {
		t.Fatal("clock moved backward", got.UpdatedAt)
	}
}

// writeDirectly commits records through the storage module, bypassing the
// service, as one request of client c stamped at stamp.
func writeDirectly(t *testing.T, s *Server, c protocol.Client, stamp string, records ...any) {
	t.Helper()
	i := protocol.Intent{SchemaVersion: 1, Operation: "transaction", ServiceID: s.DB.ServiceID(), Actor: protocol.DurableActor{ClientID: c.ClientID, Name: c.Actor.Name, Kind: c.Actor.Kind}}
	results := []protocol.ChangedObject{}
	for _, r := range records {
		typ, id, rev, _, _ := recordIdentity(r)
		i.Operations = append(i.Operations, protocol.Operation{Type: typ, ID: id, Kind: "create"})
		i.Targets = append(i.Targets, protocol.ObjectRef{Type: typ, ID: id})
		results = append(results, protocol.ChangedObject{Type: typ, ID: id, BeforeRevision: rev - 1, Revision: rev})
	}
	b, hash, e := protocol.Canonical(i)
	if e != nil {
		t.Fatal(e)
	}
	protocol.Decode(b, &i)
	ctx := context.Background()
	w, e := s.DB.LockWrites(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Unlock()
	tx, e := w.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = tx.WriteRequest(ctx, storage.Request{Hash: hash, Intent: i, Timestamp: stamp, Results: results}, records); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
}
