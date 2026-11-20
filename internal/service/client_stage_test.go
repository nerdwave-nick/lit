package service

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

// stageFixture is a service with a project (so transactions and history are
// not empty) and one connected client.
func stageFixture(t *testing.T) (root string, s *Server, h *httptest.Server, c protocol.Client) {
	t.Helper()
	root = t.TempDir()
	s, c = projectTestServer(t, root)
	mustExecute(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: protocol.UUID(), Title: textPointer("feat/stage"), Content: textPointer("body\n")}))
	h = httptest.NewServer(s)
	t.Cleanup(h.Close)
	return root, s, h, c
}

// patchStage sends one client update and returns the status and the response.
func patchStage(t *testing.T, h *httptest.Server, c protocol.Client, revision int, body string) (int, map[string]any) {
	t.Helper()
	return request(t, h, "PATCH", "/v1/clients/"+c.ClientID, body, c.ClientID, fmt.Sprintf(`"client:%d"`, revision))
}

func clientOf(t *testing.T, h *httptest.Server, c protocol.Client) map[string]any {
	t.Helper()
	status, v := request(t, h, "GET", "/v1/clients/"+c.ClientID, "", c.ClientID, "")
	if status != 200 {
		t.Fatal(status, v)
	}
	return v["data"].(map[string]any)
}

// historyRows counts the committed transactions and history rows.
func historyRows(t *testing.T, s *Server) (transactions, history int) {
	t.Helper()
	if e := outside(t, s).QueryRow("SELECT (SELECT count(*) FROM transactions), (SELECT count(*) FROM history)").Scan(&transactions, &history); e != nil {
		t.Fatal(e)
	}
	return transactions, history
}

// The workflow stage is a client property: set, overwritten and cleared by
// client updates, stored per client, and never part of issue history.
func TestClientStageSetOverwriteClear(t *testing.T) {
	_, s, h, c := stageFixture(t)
	transactions, history := historyRows(t, s)
	if transactions == 0 || history == 0 {
		t.Fatalf("fixture has no history: %d transactions, %d history rows", transactions, history)
	}
	if got, present := clientOf(t, h, c)["stage"]; !present || got != nil {
		t.Fatalf("a fresh client has stage %v (present %v), want null", got, present)
	}
	rev := 1
	for _, step := range []struct {
		name, body string
		want       any
		revises    bool
	}{
		{"set", `{"stage":"impl"}`, "impl", true},
		{"setting the same stage again changes nothing", `{"stage":"impl"}`, "impl", false},
		{"overwrite", `{"stage":"to-spec"}`, "to-spec", true},
		{"a well-formed value outside the standard list is stored", `{"stage":"ship"}`, "ship", true},
		{"clear", `{"stage":null}`, nil, true},
		{"clearing a cleared stage changes nothing", `{"stage":null}`, nil, false},
	} {
		status, v := patchStage(t, h, c, rev, step.body)
		got := v["data"].(map[string]any)
		if status != 200 || got["stage"] != step.want {
			t.Fatalf("%s: %d %v", step.name, status, v)
		}
		if step.revises {
			rev++
		}
		if got["state_revision"] != float64(rev) {
			t.Fatalf("%s: state_revision %v, want %d", step.name, got["state_revision"], rev)
		}
		if read := clientOf(t, h, c)["stage"]; read != step.want {
			t.Fatalf("%s: a later read shows %v, want %v", step.name, read, step.want)
		}
	}
	if tx, hs := historyRows(t, s); tx != transactions || hs != history {
		t.Fatalf("stage changes created history: transactions %d -> %d, history rows %d -> %d", transactions, tx, history, hs)
	}
}

// Stages are labels: lowercase ASCII words joined by hyphens, at most 32
// characters. Membership in the standard stage list is deliberately not
// checked: the list belongs to the workflow skills, which change on their own
// schedule, and a display label must never stop a skill from running.
func TestClientStageIsValidatedByShapeOnly(t *testing.T) {
	_, _, h, c := stageFixture(t)
	status, v := patchStage(t, h, c, 1, `{"stage":"impl"}`)
	if status != 200 {
		t.Fatal(status, v)
	}
	for _, bad := range []string{`""`, `" "`, `"Impl"`, `"to spec"`, `"to_spec"`, `"-impl"`, `"7"`, `"to-spec-"`, `"to--spec"`, `"é"`, `"impl\n"`, `"` + strings.Repeat("a", 33) + `"`, `7`, `true`, `["impl"]`, `{"a":1}`} {
		status, v = patchStage(t, h, c, 2, `{"stage":`+bad+`}`)
		if status != 422 || v["error"].(map[string]any)["code"] != "validation_failed" {
			t.Errorf("stage %s: %d %v", bad, status, v)
		}
	}
	for _, good := range []string{"planner", "clarify", "research", "prototype", "to-spec", "to-tickets", "triage", "impl", "orchestrate", "a", "x1-y2-z3", strings.Repeat("a", 32), "custom-stage"} {
		status, v = patchStage(t, h, c, int(clientOf(t, h, c)["state_revision"].(float64)), `{"stage":"`+good+`"}`)
		if status != 200 || v["data"].(map[string]any)["stage"] != good {
			t.Errorf("stage %q: %d %v", good, status, v)
		}
	}
	// Refused updates leave the stage and revision as they were.
	before := clientOf(t, h, c)
	if status, _ = patchStage(t, h, c, int(before["state_revision"].(float64)), `{"stage":"Not Valid"}`); status != 422 {
		t.Fatal(status)
	}
	if after := clientOf(t, h, c); string(mustJSON(after)) != string(mustJSON(before)) {
		t.Fatalf("a refused update changed the client: %v -> %v", before, after)
	}
}

// A stage survives a restart of the service and a resume of the client, and
// belongs to its client alone.
func TestClientStageSurvivesRestartAndResume(t *testing.T) {
	root, s, h, c := stageFixture(t)
	other := protocol.Client{SchemaVersion: 1, ClientID: protocol.UUID(), StateRevision: 1, Status: "connected", Actor: protocol.Actor{Name: "Other", Kind: "agent"}}
	if e := s.SaveClient(other); e != nil {
		t.Fatal(e)
	}
	if status, v := patchStage(t, h, c, 1, `{"stage":"orchestrate"}`); status != 200 {
		t.Fatal(status, v)
	}
	config := s.Config
	h.Close()
	s.DB.Close()
	reopened := openTestServer(t, root, config)
	h = httptest.NewServer(reopened)
	defer h.Close()
	if got := clientOf(t, h, c)["stage"]; got != "orchestrate" {
		t.Fatalf("after a restart the stage is %v", got)
	}
	if got := clientOf(t, h, other)["stage"]; got != nil {
		t.Fatalf("another client has stage %v", got)
	}
	// Resuming the client (connect with its ID, as workflow-session resume
	// does) returns it with its stage and keeps the stage when it is
	// disconnected and connected again.
	for _, step := range []string{"resume", "after disconnect"} {
		if step == "after disconnect" {
			rev := int(clientOf(t, h, c)["state_revision"].(float64))
			if status, v := request(t, h, "POST", "/v1/disconnect", `{}`, c.ClientID, fmt.Sprintf(`"client:%d"`, rev)); status != 200 {
				t.Fatal(status, v)
			}
		}
		status, v := request(t, h, "POST", "/v1/connect", `{"client_id":"`+c.ClientID+`"}`, "", "")
		client, _ := v["data"].(map[string]any)["client"].(map[string]any)
		if status != 200 || client["stage"] != "orchestrate" || client["status"] != "connected" {
			t.Fatalf("%s: %d %v", step, status, v)
		}
	}
}

// A stage update whose outcome was uncertain reconciles like any client update.
func TestReconcileClientStageUpdate(t *testing.T) {
	_, s, h, c := stageFixture(t)
	q := protocol.ReconcileRequest{ServiceID: s.DB.ServiceID(), ClientID: c.ClientID, Method: "PATCH", Path: "/v1/clients/" + c.ClientID,
		Body: json.RawMessage(`{"stage":"impl"}`), Headers: map[string]string{"If-Match": `"client:1"`}}
	if v, e := s.Reconcile(q, c); e != nil || v.Status != "uncommitted" {
		t.Fatalf("before the update: %+v %v", v, e)
	}
	status, v := patchStage(t, h, c, 1, `{"stage":"impl"}`)
	if status != 200 {
		t.Fatal(status, v)
	}
	current, e := s.Client(c.ClientID)
	if e != nil {
		t.Fatal(e)
	}
	if v, e := s.Reconcile(q, current); e != nil || v.Status != "already_satisfied" {
		t.Fatalf("after the update: %+v %v", v, e)
	}
}
