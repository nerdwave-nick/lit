package uncertain_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/recovery"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/tuitest"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/uncertain"
)

// snapshot reads every file under dir, so a test can prove that nothing was
// added, changed or removed.
func snapshot(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = b
	}
	return out
}

func sameFiles(t *testing.T, before, after map[string][]byte) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatalf("evidence files changed: %d before, %d after", len(before), len(after))
	}
	for name, b := range before {
		if !bytes.Equal(b, after[name]) {
			t.Fatalf("evidence %s changed or removed", name)
		}
	}
}

// mutations counts the requests that could change the tracker.
func mutations(srv *tuitest.Server) int {
	n := 0
	for _, k := range []string{"POST /v1/transactions", "POST /v1/operations", "POST /v1/disconnect", "POST /v1/connect"} {
		n += srv.Count(k)
	}
	return n
}

func byID(items []uncertain.Item) map[string]uncertain.Item {
	out := map[string]uncertain.Item{}
	for _, it := range items {
		out[it.ID] = it
	}
	return out
}

func id(path string) string { return filepath.Base(path)[:36] }

// Own lists this instance's retained requests with their status, the
// operation, and the service and client identity from the evidence. It
// leaves other clients' evidence out, sends no mutation, and leaves every
// evidence file byte-identical, however often it runs.
func TestOwnChecksRetainedRequestsReadOnly(t *testing.T) {
	srv := tuitest.Service(t)
	state := t.TempDir()
	e, s := tuitest.Instance(t, state, srv.URL)
	acquire := tuitest.UncertainAcquire(t, s)
	registration := tuitest.UnconfirmedRegistration(t, state, srv.URL, s.Mapping.ServiceID, e.ID)
	_, other := tuitest.Instance(t, state, srv.URL)
	foreign := tuitest.UncertainAcquire(t, other)
	before, sent := snapshot(t, filepath.Join(state, "pending")), mutations(srv)

	for range 2 {
		items, err := uncertain.Own(context.Background(), s, e.ID)
		if err != nil {
			t.Fatal(err)
		}
		got := byID(items)
		if len(items) != 2 {
			t.Fatalf("items %+v", items)
		}
		if _, ok := got[id(foreign)]; ok {
			t.Fatal("another client's request is listed as this session's")
		}
		a := got[id(acquire)]
		want := uncertain.Item{ID: id(acquire), Status: "uncertain", Proof: "acquisition_token_not_received", Evidence: acquire,
			Method: "POST", Path: "/v1/operations", Operation: "claims.acquire", ServiceID: s.Mapping.ServiceID, ClientID: s.Mapping.ClientID, Endpoint: s.Endpoint}
		a.CreatedAt = ""
		if a != want {
			t.Errorf("acquire\n got %+v\nwant %+v", a, want)
		}
		r := got[id(registration)]
		if r.Status != "uncertain" || r.Proof != "registration_identity_not_received" || r.Operation != "connect" || r.ClientID != "" ||
			r.ServiceID != s.Mapping.ServiceID || r.Evidence != registration || r.CreatedAt == "" || !r.Uncertain() {
			t.Errorf("registration %+v", r)
		}
	}
	if mutations(srv) != sent {
		t.Fatal("checking the requests sent a mutation")
	}
	sameFiles(t, before, snapshot(t, filepath.Join(state, "pending")))
}

// When the status check fails, the requests found locally are still listed,
// not checked, and nothing is touched.
func TestOwnListsRequestsItCouldNotCheck(t *testing.T) {
	srv := tuitest.Service(t)
	state := t.TempDir()
	e, s := tuitest.Instance(t, state, srv.URL)
	acquire := tuitest.UncertainAcquire(t, s)
	corrupt := filepath.Join(state, "pending", protocol.UUID()+".json")
	if err := os.WriteFile(corrupt, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, filepath.Join(state, "pending"))
	items, err := uncertain.Own(context.Background(), s, e.ID)
	if litclient.Code(err) != "invalid_local_state" {
		t.Fatalf("err %v", err)
	}
	if len(items) != 1 || items[0].ID != id(acquire) || items[0].Status != uncertain.NotChecked || items[0].Operation != "claims.acquire" || items[0].Uncertain() {
		t.Fatalf("items %+v", items)
	}
	sameFiles(t, before, snapshot(t, filepath.Join(state, "pending")))
}

// Describe names the operation of every kind of retained request, keeps the
// status it is given, and reports evidence it cannot read.
func TestDescribeReadsTheOperationAndIdentity(t *testing.T) {
	state := t.TempDir()
	service, client := protocol.UUID(), protocol.UUID()
	write := func(method, path string, body any) string {
		raw, _ := json.Marshal(body)
		p := litclient.Pending{SchemaVersion: 1, RequestID: protocol.UUID(), ServiceID: service, ClientID: client, Endpoint: "http://127.0.0.1:9",
			Method: method, Path: path, Body: raw, Headers: map[string]string{"Content-Type": "application/json"}, CreatedAt: "2026-10-09T08:00:00Z"}
		file := filepath.Join(state, "pending", p.RequestID+".json")
		if err := litclient.WriteJSON(file, p); err != nil {
			t.Fatal(err)
		}
		return file
	}
	tx := write("POST", "/v1/transactions", map[string]any{"schema_version": 1, "intent": map[string]any{"schema_version": 1, "operation": "issue.update"}, "request_hash": "h"})
	cases := []struct{ file, method, path, operation string }{
		{tx, "POST", "/v1/transactions", "issue.update"},
		{write("POST", "/v1/disconnect", map[string]any{}), "POST", "/v1/disconnect", "disconnect"},
		{write("PATCH", "/v1/clients/"+client, map[string]any{"project_id": nil}), "PATCH", "/v1/clients/" + client, "client update"},
	}
	before := snapshot(t, filepath.Join(state, "pending"))
	for _, c := range cases {
		st := recovery.Request{ID: id(c.file), Status: "uncertain", Proof: "original_preconditions_no_longer_valid", Path: c.file}
		it := uncertain.Describe(c.file, st)
		if it.ID != st.ID || it.Status != st.Status || it.Proof != st.Proof || it.Evidence != c.file || it.Method != c.method || it.Path != c.path ||
			it.Operation != c.operation || it.ServiceID != service || it.ClientID != client || it.CreatedAt != "2026-10-09T08:00:00Z" || it.Problem != "" {
			t.Errorf("%s: %+v", c.operation, it)
		}
	}
	missing := filepath.Join(state, "pending", protocol.UUID()+".json")
	items := uncertain.FromRequests([]recovery.Request{{ID: id(tx), Status: "uncertain", Proof: "outcome_uncertain", Path: tx}, {ID: id(missing), Status: "unknown", Path: missing}})
	if len(items) != 2 || items[0].Operation != "issue.update" || items[1].ID != id(missing) || items[1].Status != "unknown" || items[1].Problem == "" || items[1].Evidence != missing {
		t.Fatalf("items %+v", items)
	}
	sameFiles(t, before, snapshot(t, filepath.Join(state, "pending")))
}
