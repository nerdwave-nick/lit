package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
)

func TestMilestoneFlagsKeepAtomicBoundaries(t *testing.T) {
	a, err := Parse([]string{"milestones", "create", "--project", "feat/demo", "--milestone", "M1", "--issue", "First", "--issue", "id:01234567", "--milestone", "M2", "--content", "second"})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Groups) != 2 || a.Groups[0]["milestone"][0] != "M1" || len(a.Groups[0]["issue"]) != 2 || a.Groups[1]["milestone"][0] != "M2" {
		t.Fatalf("unexpected create groups: %#v", a.Groups)
	}
	u, err := Parse([]string{"milestones", "update", "M1", "--add-issue", "First", "--remove-issue", "Second", "--clear", "issues", "--revision", "3"})
	if err != nil || !reflectEqual(u.Positionals, []string{"M1"}) || len(u.Groups) != 1 {
		t.Fatalf("update parse: args=%#v err=%v", u, err)
	}
	if _, err = Parse([]string{"milestones", "create", "--milestone", "M1", "--file", "items.json"}); err == nil {
		t.Fatal("accepted mixed --file and flag inputs")
	}
}

func TestMilestoneHelpAndCompletion(t *testing.T) {
	for _, form := range [][]string{{"milestones", "create", "--help"}, {"milestones", "update", "-h"}, {"help", "milestones", "list"}, {"milestones", "history", "help"}} {
		var out, stderr bytes.Buffer
		if code := Run(form, &out, &stderr); code != 0 || !strings.Contains(out.String(), "Requirements:") {
			t.Fatalf("%v: code=%d out=%s err=%s", form, code, &out, &stderr)
		}
	}
	var createHelp, issueListHelp bytes.Buffer
	Run([]string{"milestones", "create", "--help"}, &createHelp, &createHelp)
	Run([]string{"issues", "list", "--help"}, &issueListHelp, &issueListHelp)
	if !strings.Contains(createHelp.String(), "Existing issue REF to include") || strings.Contains(createHelp.String(), "--issue TITLE") {
		t.Fatalf("milestone create gives member refs a creation title description: %s", &createHelp)
	}
	if !strings.Contains(issueListHelp.String(), "Milestone REF whose members") || strings.Contains(issueListHelp.String(), "--milestone TITLE") {
		t.Fatalf("issue filter gives its milestone ref a creation title description: %s", &issueListHelp)
	}
	for _, shell := range []string{"bash", "fish", "zsh"} {
		var out, stderr bytes.Buffer
		if code := Run([]string{"completion", shell}, &out, &stderr); code != 0 || out.Len() < 500 {
			t.Fatalf("%s completion lacks milestone family: %d %s", shell, code, &stderr)
		}
	}
	var out, stderr bytes.Buffer
	if code := Run([]string{"__complete", "milestones", ""}, &out, &stderr); code != 0 || !strings.Contains(out.String(), "create") || !strings.Contains(out.String(), "history") {
		t.Fatalf("milestone family is not dynamically completable: %d %s %s", code, &out, &stderr)
	}
}

func TestIssueListMilestoneFilterIsAccepted(t *testing.T) {
	a, err := Parse([]string{"issues", "list", "--project", "feat/demo", "--milestone", "M1", "--state", "open"})
	if err != nil {
		t.Fatal(err)
	}
	if got := a.Groups[0]["milestone"]; len(got) != 1 || got[0] != "M1" {
		t.Fatalf("milestone filter not retained: %#v", a.Groups)
	}
}

func TestIssueReferencesRepeatOnlyForMilestoneCreation(t *testing.T) {
	if _, err := Parse([]string{"comments", "list", "--issue", "Owner A", "--issue", "Owner B"}); err == nil {
		t.Fatal("comments list accepted duplicate owning issue selectors")
	}
}

func TestMilestoneListHasUsefulMarkdownHumanTable(t *testing.T) {
	a := App{Args: Args{Command: "milestones", Verb: "list"}, Format: "cli", Out: &bytes.Buffer{}}
	var out bytes.Buffer
	result := Result{Items: []any{map[string]any{"title": "M1", "issue_ids": []any{"a", "b"}, "progress": map[string]any{"total": 2, "open": 1, "closed": 1}}}}
	raw, err := a.cliResultWidth(result, 0)
	if err != nil {
		t.Fatal(err)
	}
	out.Write(raw)
	if !strings.Contains(out.String(), "TITLE") || !strings.Contains(out.String(), "PROGRESS") || !strings.Contains(out.String(), "M1") {
		t.Fatalf("milestone list is not a concise table: %s", &out)
	}
	a.Format = "markdown"
	markdown, err := a.markdownResult(result)
	if err != nil || !strings.Contains(string(markdown), "1/2 closed; 1 open; 0 blocked") || strings.Contains(string(markdown), "map[closed:") {
		t.Fatalf("markdown milestone progress is not concise: %s (%v)", markdown, err)
	}
}

func TestMilestoneListFileScopeOverridesSessionFallback(t *testing.T) {
	projectFromFile, selected := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	for _, testCase := range []struct {
		name, body string
		wantErr    bool
	}{
		{"valid file project overrides session", `{"type":"milestones","project_id":"11111111-1111-4111-8111-111111111111"}`, false},
		{"invalid typed project is rejected", `{"type":"milestones","project_id":null}`, true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "query.json")
			if err := os.WriteFile(file, []byte(testCase.body), 0600); err != nil {
				t.Fatal(err)
			}
			called := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Fatal(err)
				}
				query := request["query"].(map[string]any)
				if query["project_id"] != projectFromFile {
					t.Fatalf("session fallback replaced query-file scope: %#v", query)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"items": []any{}, "next_cursor": nil}})
			}))
			defer srv.Close()
			args, err := Parse([]string{"milestones", "list", "--file", file})
			if err != nil {
				t.Fatal(err)
			}
			app := App{Session: litclient.Session{Endpoint: srv.URL, HTTP: srv.Client(), Context: context.Background(), StateDir: dir, Meta: protocol.Meta{ServiceID: protocol.UUID()}, Client: protocol.Client{ProjectID: &selected}}, Args: args}
			_, err = app.records()
			if (err != nil) != testCase.wantErr {
				t.Fatalf("records error=%v want error=%v", err, testCase.wantErr)
			}
			if testCase.wantErr && called {
				t.Fatal("invalid project_id reached the service")
			}
			if !testCase.wantErr && !called {
				t.Fatal("valid typed query did not reach the service")
			}
		})
	}
}

func TestMilestoneUpdateResolvesMembersInEachTargetProject(t *testing.T) {
	projectA, projectB := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	issueA, issueB := "33333333-3333-4333-8333-333333333333", "44444444-4444-4444-8444-444444444444"
	targets := []struct{ id, project string }{{"55555555-5555-4555-8555-555555555555", projectA}, {"66666666-6666-4666-8666-666666666666", projectB}}
	call, current := 0, -1
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call++
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		target := request["targets"].([]any)[0].(map[string]any)
		var item map[string]any
		if target["type"] == "milestones" {
			idx := 0
			if target["selector"] == "M2" {
				idx = 1
			}
			if target["selector"] != []string{"M1", "M2"}[idx] {
				t.Fatalf("unexpected milestone lookup %d: %#v", call, request)
			}
			current = idx
			item = map[string]any{"id": targets[idx].id, "project_id": targets[idx].project}
		} else {
			if target["type"] != "issues" || current < 0 || target["project"] != targets[current].project || target["selector"] != "Shared title" {
				t.Fatalf("issue selector did not use owning project: %#v", target)
			}
			id := issueA
			if current == 1 {
				id = issueB
			}
			item = map[string]any{"id": id}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"items": []any{item}, "next_cursor": nil}, "server_time": time.Now().UTC().Format(time.RFC3339Nano)})
	}))
	defer srv.Close()
	args, err := Parse([]string{"milestones", "update", "M1", "M2", "--add-issue", "Shared title"})
	if err != nil {
		t.Fatal(err)
	}
	app := App{Session: litclient.Session{Endpoint: srv.URL, HTTP: srv.Client(), Context: context.Background(), StateDir: t.TempDir(), Mapping: Mapping{ClientID: protocol.UUID()}, Meta: protocol.Meta{ServiceID: protocol.UUID()}}, Args: args}
	items, err := app.recordInputs()
	if err != nil {
		t.Fatal(err)
	}
	if call != 4 || len(items) != 2 || items[0].Add.IssueIDs[0] != issueA || items[1].Add.IssueIDs[0] != issueB {
		t.Fatalf("unexpected resolved membership: calls=%d items=%#v", call, items)
	}
}

func TestResolveIssueRefsAcceptsSnapshotDeduplication(t *testing.T) {
	issue := "33333333-3333-4333-8333-333333333333"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"items": []any{map[string]any{"id": issue}}, "next_cursor": nil}})
	}))
	defer srv.Close()
	app := App{Session: litclient.Session{Endpoint: srv.URL, HTTP: srv.Client(), Context: context.Background(), StateDir: t.TempDir(), Meta: protocol.Meta{ServiceID: protocol.UUID()}}}
	got, err := app.resolveIssueRefs([]string{"Alias A", "Alias B"}, protocol.UUID())
	if err != nil || len(got) != 1 || got[0] != issue {
		t.Fatalf("deduplicated target snapshot: ids=%v err=%v", got, err)
	}
}

func TestMilestoneHistoryKeepsExplicitProjectAcrossPages(t *testing.T) {
	project, selected := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	owner := "55555555-5555-4555-8555-555555555555"
	for _, args := range [][]string{
		{"milestones", "history", "M1", "--project", project, "--limit", "10"},
		{"milestones", "history", "M1", "--project", project, "--request-hash", "abcdef0123456789abcdef0123456789"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			requests := []string{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.URL.Path+"?"+r.URL.Query().Encode())
				if strings.HasSuffix(r.URL.Path, "/history") || strings.Contains(r.URL.Path, "/history/") {
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"items": []any{}, "next_cursor": nil}})
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": owner}})
			}))
			defer srv.Close()
			parsed, err := Parse(args)
			if err != nil {
				t.Fatal(err)
			}
			app := App{Session: litclient.Session{Endpoint: srv.URL, HTTP: srv.Client(), Context: context.Background(), StateDir: t.TempDir(), Mapping: Mapping{ClientID: protocol.UUID()}, Meta: protocol.Meta{ServiceID: protocol.UUID()}, Client: protocol.Client{ProjectID: &selected}}, Args: parsed}
			if _, err = app.records(); err != nil {
				t.Fatal(err)
			}
			if len(requests) != 2 || !strings.Contains(requests[0], "project_id="+project) || !strings.Contains(requests[1], "project_id="+project) {
				t.Fatalf("explicit project was lost from history requests: %#v", requests)
			}
		})
	}
}

func reflectEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
