package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

func TestHumanOutput(t *testing.T) {
	cursor := "opaque-next"
	cases := []struct {
		name, command, verb string
		result              Result
		want                []string
	}{
		{"projects", "projects", "list", Result{Items: []any{protocol.Project{ID: "full-project-id", Title: "test/poc", Description: "long description", IssueIDs: []string{"one", "two"}}}, NextCursor: &cursor}, []string{"TITLE", "ISSUES", "ID", "test/poc", "full-project-id", "2", "Next cursor: opaque-next", "--cursor"}},
		{"issues", "issues", "list", Result{Items: []any{protocol.Issue{ID: "issue-id", Title: "Ship this", State: "open", Labels: []string{"ready"}}}}, []string{"TITLE", "STATE", "ASSIGNEE", "Ship this", "open"}},
		{"comments", "comments", "list", Result{Items: []any{protocol.Comment{ID: "comment-id", Author: "Alex", Body: "first\nsecond"}}}, []string{"AUTHOR", "COMMENT", "Alex", "first", "second"}},
		{"details", "issues", "get", Result{Items: []any{protocol.Issue{Title: "Ship this", Body: "first\nsecond", Revision: 2}}}, []string{"Title: Ship this", "Body:\n  first\n  second", "Revision: 2"}},
		{"session", "session", "get", Result{Items: []any{map[string]any{"client_id": "client-id", "actor": protocol.Actor{Name: "Alex", Kind: "human"}}}}, []string{"Client ID: client-id", "Actor:", "Name: Alex", "Kind: human"}},
		{"mutation", "issues", "close", Result{Outcome: "committed", RequestHash: "hash", Items: []any{protocol.ChangedObject{ID: "issue-id", Type: "issues", BeforeRevision: 1, Revision: 2}}}, []string{"Outcome: committed", "Request hash: hash", "Before revision: 1", "Revision: 2"}},
		{"history", "issues", "history", Result{Items: []any{map[string]any{"revision": 2, "before": map[string]any{"body": "old"}, "after": map[string]any{"body": "new"}}}}, []string{"Revision: 2", "Before:", "Body: old", "After:", "Body: new"}},
		{"claims", "claims", "acquire", Result{Items: []any{protocol.RequiredClaim{IssueID: "issue-id", Claim: &protocol.Claim{OwnerClientID: "owner", ExpiresAt: "tomorrow"}}}}, []string{"Issue ID: issue-id", "Claim:", "Owner client ID: owner", "Expires at: tomorrow"}},
		{"released", "claims", "release", Result{Items: []any{protocol.RequiredClaim{IssueID: "issue-id"}}, Outcome: "released"}, []string{"Outcome: released", "Claim: none"}},
		{"transaction", "transactions", "status", Result{Items: []any{protocol.TransactionStatus{Status: "uncertain", Proof: "request_still_active"}}}, []string{"Status: uncertain", "Proof: request_still_active"}},
		{"empty", "projects", "list", Result{}, []string{"No projects found."}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errs bytes.Buffer
			app := App{Args: Args{Command: tc.command, Verb: tc.verb}, Format: "cli", Out: &out, Err: &errs}
			if code := printCLIForTest(&app, tc.result); code != 0 {
				t.Fatalf("exit %d: %s", code, &errs)
			}
			for _, want := range tc.want {
				if !strings.Contains(out.String(), want) {
					t.Errorf("missing %q in %s", want, &out)
				}
			}
			if strings.Contains(out.String(), "```") {
				t.Errorf("JSON fences in %s", &out)
			}
		})
	}
}

func TestHumanOutputEscapesTerminalControls(t *testing.T) {
	var out bytes.Buffer
	app := App{Args: Args{Command: "projects", Verb: "list"}, Format: "cli", Out: &out, Err: &out}
	printCLIForTest(&app, Result{Items: []any{map[string]any{"title": "bad\x1b[2J\r\b\t\u202e", "id": "id"}}})
	if strings.ContainsAny(out.String(), "\x1b\r\b\t\u202e") {
		t.Fatalf("unsafe output: %q", out.String())
	}
	if !strings.Contains(out.String(), `\x1b`) {
		t.Fatalf("missing visible escaped control: %q", out.String())
	}
}

func TestHumanOutputDoesNotChangeJSON(t *testing.T) {
	r := Result{Items: []any{map[string]any{"body": "raw\x1b\n", "revision": json.Number("9007199254740993")}}, Outcome: "committed"}
	want, _ := json.Marshal(r)
	var out bytes.Buffer
	app := App{Format: "json", Out: &out, Err: &out}
	if app.Print(r) != 0 || out.String() != string(want)+"\n" {
		t.Fatalf("JSON changed: %q", out.String())
	}
}

func TestHumanHistoryNestedChangesAndLargeRevision(t *testing.T) {
	var item any
	decoder := json.NewDecoder(strings.NewReader(`{"schema_version":1,"owner_id":"issue-id","before_revision":9007199254740992,"revision":9007199254740993,"request_hash":"hash","differences":{"state":{"before":"open","after":"closed"}},"body_hunks":[{"before":"old\ntext","after":"new\ntext"}],"results":[{"type":"issues","id":"issue-id","revision":9007199254740993}]}`))
	decoder.UseNumber()
	if err := decoder.Decode(&item); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	app := App{Args: Args{Command: "issues", Verb: "history"}, Format: "cli", Out: &out, Err: &out}
	if printCLIForTest(&app, Result{Items: []any{item}}) != 0 {
		t.Fatal(out.String())
	}
	for _, want := range []string{"Revision: 9007199254740993", "Differences:\n  State:\n    Before: open\n    After: closed", "Body hunks:\n  Item 1:", "old\n      text", "Request hash: hash"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q: %s", want, &out)
		}
	}
	if strings.Contains(out.String(), "Schema version") {
		t.Fatal("internal schema noise", out.String())
	}
	t.Log(out.String())
}

func TestHumanProjectTablePreview(t *testing.T) {
	var out bytes.Buffer
	app := App{Args: Args{Command: "projects", Verb: "list"}, Format: "cli", Out: &out, Err: &out}
	printCLIForTest(&app, Result{Items: []any{
		protocol.Project{ID: "8df82168-8358-4363-ae5e-34e1cca0fa92", Title: "test/poc"},
		protocol.Project{ID: "a099827d-a446-4016-ab1c-b3d1e807d627", Title: "test/poc-2"},
		protocol.Project{ID: "0347a340-1eed-42e4-9392-d3b13b18a921", Title: "test/poc-3"},
	}})
	if strings.Count(out.String(), "test/poc") != 3 {
		t.Fatal(out.String())
	}
	t.Log(out.String())
}

func TestHumanTableBoundsTextButPreservesIDs(t *testing.T) {
	var out bytes.Buffer
	id := strings.Repeat("i", 100)
	app := App{Args: Args{Command: "comments", Verb: "list"}, Format: "cli", Out: &out, Err: &out}
	printCLIForTest(&app, Result{Items: []any{protocol.Comment{ID: id, Body: strings.Repeat("界", 100)}}})
	if strings.Contains(out.String(), strings.Repeat("界", 100)) || !strings.Contains(out.String(), "…") || !strings.Contains(out.String(), "comments get ID") || !strings.Contains(out.String(), id) {
		t.Fatal(out.String())
	}
}

func printCLIForTest(a *App, r Result) int {
	b, err := a.cliResult(r)
	if err != nil {
		return 1
	}
	_, err = a.Out.Write(b)
	if err != nil {
		return 1
	}
	return 0
}
