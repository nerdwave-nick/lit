package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTransactionsStatusSyntax(t *testing.T) {
	for _, args := range [][]string{{"transactions", "status"}, {"transactions", "status", "--file", "pending.json"}} {
		a, e := Parse(args)
		if e != nil || validateArgs(a) != nil {
			t.Fatalf("%v %v", a, e)
		}
	}
	for _, args := range [][]string{{"transactions", "apply"}, {"transactions", "status", "uuid"}, {"transactions", "status", "--all"}} {
		a, e := Parse(args)
		if e == nil && validateArgs(a) == nil {
			t.Fatal("accepted", args)
		}
	}
}
func TestTransactionsStatusPreservesUncertainAndUserFiles(t *testing.T) {
	sid, cid := protocol.UUID(), protocol.UUID()
	known := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/meta" {
			json.NewEncoder(w).Encode(protocol.Response{Data: protocol.Meta{ServiceID: sid, APIMajor: 1}})
			return
		}
		if r.URL.Path != "/v1/transaction-status" {
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		var request protocol.ReconcileRequest
		json.NewDecoder(r.Body).Decode(&request)
		v := protocol.TransactionStatus{RequestDigest: protocol.ReconcileDigest(request), ServiceID: sid, ClientID: cid, Status: "uncertain", Proof: "client_state_changed_without_receipt"}
		if known {
			v.Status = "already_satisfied"
			v.Proof = "current_validated_state"
			v.Outcome = "already_disconnected"
			v.Response, _ = json.Marshal(map[string]any{"outcome": "already_disconnected", "client": protocol.Client{SchemaVersion: 1, ClientID: cid, StateRevision: 1, Status: "disconnected", Actor: protocol.Actor{Name: "test", Kind: "human"}}})
		}
		json.NewEncoder(w).Encode(protocol.Response{Data: v})
	}))
	defer server.Close()
	dir := t.TempDir()
	p := Pending{SchemaVersion: 1, RequestID: protocol.UUID(), ServiceID: sid, ClientID: cid, Endpoint: server.URL, Method: "POST", Path: "/v1/disconnect", Body: json.RawMessage(`{}`), Headers: map[string]string{"If-Match": "\"client:1\"", "X-Lit-Client-Id": cid}, CreatedAt: protocol.Now()}
	path := filepath.Join(dir, "pending", p.RequestID+".json")
	if e := litclient.WriteJSON(path, p); e != nil {
		t.Fatal(e)
	}
	var out, errs bytes.Buffer
	app := &App{Session: litclient.Session{StateDir: dir, HTTP: server.Client(), Context: context.Background()}, Args: Args{Command: "transactions", Verb: "status", Values: map[string][]string{}}, Out: &out, Err: &errs, Format: "json"}
	if code := app.transactionStatus(); code != 4 || errs.Len() != 0 {
		t.Fatal(code, out.String(), errs.String())
	}
	if _, e := os.Stat(path); e != nil {
		t.Fatal("lost uncertain evidence", e)
	}
	known = true
	app.Args.Values["file"] = []string{path}
	if code := app.transactionStatus(); code != 0 {
		t.Fatal(code, errs.String())
	}
	if _, e := os.Stat(path); e != nil {
		t.Fatal("removed explicit user file", e)
	}
	delete(app.Args.Values, "file")
	if code := app.transactionStatus(); code != 0 {
		t.Fatal(code)
	}
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("resolved private pending remains", e)
	}
}

func TestMalformedReconciliationNeverDeletesEvidence(t *testing.T) {
	sid, cid, id := protocol.UUID(), protocol.UUID(), protocol.UUID()
	title := "feat/exact"
	intent := protocol.Intent{SchemaVersion: 1, Operation: "transaction", ServiceID: sid, Actor: protocol.DurableActor{ClientID: cid, Name: "test", Kind: "human"}, Targets: []protocol.ObjectRef{{Type: "projects", ID: id}}, Operations: []protocol.Operation{{Type: "projects", ID: id, Kind: "create", Set: protocol.ProjectSet{Title: &title}}}}
	_, hash, e := protocol.Canonical(intent)
	if e != nil {
		t.Fatal(e)
	}
	body, _ := json.Marshal(protocol.DurableRequest{SchemaVersion: 1, Intent: intent, RequestHash: hash})
	results := []protocol.ChangedObject{{Type: "projects", ID: id, BeforeRevision: 0, Revision: 1}}
	response, _ := json.Marshal(protocol.MutationResult{Outcome: "applied", RequestHash: hash, Items: results})
	good := protocol.TransactionStatus{ServiceID: sid, ClientID: cid, Status: "committed", Proof: "immutable_history", Outcome: "applied", RequestHash: hash, Results: results, Response: response}
	for _, kind := range []string{"empty-proof", "wrong-hash", "wrong-client", "wrong-service", "wrong-results", "wrong-response-hash", "wrong-status", "wrong-outcome", "wrong-owner", "duplicate-json"} {
		t.Run(kind, func(t *testing.T) {
			bad := good
			switch kind {
			case "empty-proof":
				bad = protocol.TransactionStatus{Status: "committed", Proof: "test"}
			case "wrong-hash":
				bad.RequestHash = strings.Repeat("a", 64)
			case "wrong-client":
				bad.ClientID = protocol.UUID()
			case "wrong-service":
				bad.ServiceID = protocol.UUID()
			case "wrong-results":
				bad.Results = nil
			case "wrong-response-hash":
				bad.Response, _ = json.Marshal(protocol.MutationResult{Outcome: "applied", RequestHash: strings.Repeat("b", 64), Items: results})
			case "wrong-status":
				bad.Status = "done"
			case "wrong-outcome":
				bad.Outcome = "already_satisfied"
			case "wrong-owner":
				other := []protocol.ChangedObject{{Type: "projects", ID: protocol.UUID(), Revision: 1}}
				bad.Results = other
				bad.Response, _ = json.Marshal(protocol.MutationResult{Outcome: "applied", RequestHash: hash, Items: other})
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/meta" {
					json.NewEncoder(w).Encode(protocol.Response{Data: protocol.Meta{ServiceID: sid, APIMajor: 1}})
					return
				}
				if kind == "duplicate-json" {
					w.Write([]byte(`{"data":{"status":"uncertain","status":"committed"}}`))
					return
				}
				var request protocol.ReconcileRequest
				json.NewDecoder(r.Body).Decode(&request)
				bad.RequestDigest = protocol.ReconcileDigest(request)
				json.NewEncoder(w).Encode(protocol.Response{Data: bad})
			}))
			defer server.Close()
			dir := t.TempDir()
			p := Pending{SchemaVersion: 1, RequestID: protocol.UUID(), ServiceID: sid, ClientID: cid, Endpoint: server.URL, Method: "POST", Path: "/v1/transactions", Body: body, Headers: map[string]string{"X-Lit-Client-ID": cid}, CreatedAt: protocol.Now()}
			path := filepath.Join(dir, "pending", p.RequestID+".json")
			litclient.WriteJSON(path, p)
			before, _ := os.ReadFile(path)
			app := &App{Session: litclient.Session{StateDir: dir, HTTP: server.Client(), Context: context.Background()}, Args: Args{Command: "transactions", Verb: "status", Values: map[string][]string{}}, Out: new(bytes.Buffer), Err: new(bytes.Buffer), Format: "json"}
			if code := app.transactionStatus(); code != 4 {
				t.Fatal(code)
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("lost or rewrote pending evidence")
			}
		})
	}
}
func TestMalformedMutationErrorsRetainPending(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{{200, `{"error":{}}`}, {400, `{"error":{}}`}, {409, `{"error":{"code":"conflict"}}`}, {409, `{"error":{"message":"conflict"}}`}, {200, `{"data":null}`}} {
		t.Run(tc.body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); w.Write([]byte(tc.body)) }))
			defer server.Close()
			a := &App{Session: litclient.Session{StateDir: t.TempDir(), Endpoint: server.URL, Meta: protocol.Meta{ServiceID: protocol.UUID()}, Mapping: Mapping{ClientID: protocol.UUID()}, Context: context.Background(), HTTP: server.Client()}, Err: new(bytes.Buffer)}
			e := a.Call("POST", "/v1/disconnect", map[string]any{}, nil, nil, true)
			if pe, ok := e.(*protocol.Error); !ok || pe.Code != "outcome_uncertain" {
				t.Fatal(e)
			}
			files, _ := filepath.Glob(filepath.Join(a.StateDir, "pending", "*.json"))
			if len(files) != 1 {
				t.Fatal("discarded evidence", files)
			}
		})
	}
}

func TestCorruptPendingFailsClosedWithoutNetwork(t *testing.T) {
	for _, kind := range []string{"version", "unknown-field", "duplicate-key", "wrong-filename", "unknown-file", "symlink", "pending-is-file"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			p := Pending{SchemaVersion: 1, RequestID: protocol.UUID(), ServiceID: protocol.UUID(), ClientID: protocol.UUID(), Endpoint: "http://127.0.0.1:1", Method: "POST", Path: "/v1/disconnect", Body: json.RawMessage(`{}`), Headers: map[string]string{}, CreatedAt: protocol.Now()}
			path := filepath.Join(dir, "pending", p.RequestID+".json")
			litclient.WriteJSON(path, p)
			raw, _ := os.ReadFile(path)
			switch kind {
			case "version":
				raw = bytes.Replace(raw, []byte(`"schema_version":1`), []byte(`"schema_version":2`), 1)
			case "unknown-field":
				raw = append([]byte(`{"unexpected":true,`), raw[1:]...)
			case "duplicate-key":
				raw = append([]byte(`{"schema_version":1,`), raw[1:]...)
			case "wrong-filename":
				os.Remove(path)
				path = filepath.Join(dir, "pending", protocol.UUID()+".json")
			case "unknown-file":
				os.Remove(path)
				path = filepath.Join(dir, "pending", "evidence.txt")
			case "symlink":
				os.Remove(path)
				target := filepath.Join(dir, "outside")
				os.WriteFile(target, raw, 0600)
				os.Symlink(target, path)
			case "pending-is-file":
				os.RemoveAll(filepath.Join(dir, "pending"))
				path = filepath.Join(dir, "pending")
			}
			if kind != "symlink" {
				os.WriteFile(path, raw, 0600)
			}
			var out, errs bytes.Buffer
			a := &App{Session: litclient.Session{StateDir: dir, Context: context.Background()}, Args: Args{Command: "transactions", Verb: "status", Values: map[string][]string{}}, Format: "json", Out: &out, Err: &errs}
			if code := a.transactionStatus(); code != 1 || out.Len() != 0 {
				t.Fatal(code, out.String(), errs.String())
			}
			after, e := os.ReadFile(path)
			if e != nil || !bytes.Equal(raw, after) {
				t.Fatal("destroyed corrupt evidence", e)
			}
		})
	}
}

func TestStatusCannotDiscardEvidenceOwnedByActiveCommand(t *testing.T) {
	dir := t.TempDir()
	p := Pending{SchemaVersion: 1, RequestID: protocol.UUID(), ServiceID: protocol.UUID(), ClientID: protocol.UUID(), Endpoint: "http://127.0.0.1:1", Method: "POST", Path: "/v1/disconnect", Body: json.RawMessage(`{}`), Headers: map[string]string{"If-Match": "\"client:1\""}, CreatedAt: protocol.Now()}
	path := filepath.Join(dir, "pending", p.RequestID+".json")
	litclient.WriteJSON(path, p)
	before, _ := os.ReadFile(path)
	unlock, e := litclient.AcquireLock(filepath.Join(dir, "locks", "pending", p.RequestID+".lock"), time.Now().Add(time.Second))
	if e != nil {
		t.Fatal(e)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	var out, errs bytes.Buffer
	a := &App{Session: litclient.Session{StateDir: dir, Context: ctx}, Args: Args{Command: "transactions", Verb: "status", Values: map[string][]string{"timeout": {"10ms"}}}, Format: "json", Out: &out, Err: &errs}
	if code := a.transactionStatus(); code != 4 || errs.Len() != 0 {
		t.Fatal(code, out.String(), errs.String())
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("active request lost evidence")
	}
}
