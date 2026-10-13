package cli

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"go.yaml.in/yaml/v3"
)

func TestMarkdownRecordDocuments(t *testing.T) {
	body := " \t\r\n# Original | heading\n\n<script>keep()</script>\n```go\nraw\\text\n```\n---\n  end  "
	for _, tc := range []struct {
		command, bodyKey string
		record           any
	}{
		{"projects", "description", protocol.Project{SchemaVersion: 1, ID: "project", Revision: 9007199254740993, Title: "test/poc", Description: body, RepositoryRefs: []string{"a: b", "line\none", "null", "2026-09-30"}, IssueIDs: []string{"issue"}}},
		{"issues", "body", protocol.Issue{SchemaVersion: 1, ID: "issue", Revision: 9007199254740993, Title: "quote: \"hi\"", Body: body, State: "open", Labels: []string{}, ProjectID: "project", ChildIDs: []string{"child"}, CommentIDs: []string{"comment"}, Blocks: []string{"blocked"}, BlockedBy: []string{"blocker"}, Related: []string{"related"}}},
		{"comments", "body", protocol.Comment{SchemaVersion: 1, ID: "comment", Revision: 9007199254740993, Body: body, Author: "name\nmultiline", IssueID: "issue"}},
	} {
		t.Run(tc.command, func(t *testing.T) {
			var out bytes.Buffer
			a := App{Session: litclient.Session{ServerTime: "transport-time"}, Args: Args{Command: tc.command, Verb: "get"}, Format: "markdown", Out: &out, Err: &out}
			if a.Print(Result{Items: []any{tc.record}}) != 0 {
				t.Fatal(out.String())
			}
			data := out.Bytes()
			if !bytes.HasPrefix(data, []byte("---\n")) {
				t.Fatalf("missing frontmatter: %s", data)
			}
			parts := bytes.SplitN(data[4:], []byte("\n---\n"), 2)
			if len(parts) != 2 {
				t.Fatal(string(data))
			}
			if string(parts[1]) != body {
				t.Fatalf("body changed: %q", parts[1])
			}
			var metadata map[string]any
			if err := yaml.Unmarshal(parts[0], &metadata); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(tc.record)
			var want map[string]any
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.UseNumber()
			_ = dec.Decode(&want)
			delete(want, tc.bodyKey)
			// Re-encode YAML values as JSON and compare JSON-model values with UseNumber.
			raw, err := json.Marshal(metadata)
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			dec = json.NewDecoder(bytes.NewReader(raw))
			dec.UseNumber()
			_ = dec.Decode(&got)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("metadata differs\ngot %#v\nwant %#v", got, want)
			}
		})
	}
}
func TestMarkdownDocumentOnlyForCompleteSingleGet(t *testing.T) {
	p := protocol.Project{SchemaVersion: 1, ID: "p", Title: "test/poc", Description: "# body"}
	for _, tc := range []struct {
		name, command, verb string
		items               []any
	}{
		{"list", "projects", "list", []any{p}}, {"multi", "projects", "get", []any{p, p}},
		{"history", "projects", "history", []any{p}}, {"projection", "projects", "get", []any{map[string]any{"id": "p", "description": "body"}}},
		{"receipt", "projects", "create", []any{protocol.ChangedObject{ID: "p", Revision: 1}}}, {"session", "session", "get", []any{p}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := App{Args: Args{Command: tc.command, Verb: tc.verb}}
			b, e := a.markdownResult(Result{Items: tc.items})
			if e != nil || strings.HasPrefix(string(b), "---\n") {
				t.Fatalf("%s %v", b, e)
			}
			if tc.name == "list" && !strings.Contains(string(b), "| TITLE | ISSUES |") {
				t.Fatal(string(b))
			}
		})
	}
}
func TestMarkdownDocumentEmptyBody(t *testing.T) {
	a := App{Args: Args{Command: "comments", Verb: "get"}}
	b, e := a.markdownResult(Result{Items: []any{protocol.Comment{ID: "c"}}})
	if e != nil || !bytes.HasSuffix(b, []byte("\n---\n")) {
		t.Fatalf("%q %v", b, e)
	}
}

func TestRecordGetHelpExplainsMarkdownExport(t *testing.T) {
	for _, resource := range []string{"projects", "issues", "comments"} {
		help := commandLong(resource, "get")
		for _, want := range []string{"--format markdown", "one complete returned record", "YAML frontmatter", "multiple returned records", "Export each separately", "--content-file"} {
			if !strings.Contains(help, want) {
				t.Errorf("%s missing %q: %s", resource, want, help)
			}
		}
	}
}
