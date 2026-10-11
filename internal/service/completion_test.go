package service

import (
	"crypto/sha256"
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

func TestCompletionMetadataScopeAndReferences(t *testing.T) {
	root := t.TempDir()
	s, client := projectTestServer(t, root)
	defer s.DB.Close()
	const secret = "CONTENT_MUST_NEVER_BE_EXPORTED"
	projects := []string{protocol.UUID(), protocol.UUID()}
	for i, title := range []string{"feat/alpha", "feat/zulu"} {
		executeRecordTest(t, s, client, prepareProjectTest(t, s, client, "create", protocol.ProjectInput{ID: projects[i], Title: textPointer(title), Content: textPointer(secret)}))
		issue := protocol.UUID()
		executeRecordTest(t, s, client, prepareRecordTest(t, s, client, projects[i], "issue.create", protocol.ProjectInput{ID: issue, Title: textPointer("Fix timeout"), Content: textPointer(secret)}))
		executeRecordTest(t, s, client, prepareRecordTest(t, s, client, projects[i], "comment.create", protocol.ProjectInput{ID: protocol.UUID(), Issue: issue, Content: textPointer(secret)}))
		executeRecordTest(t, s, client, prepareRecordTest(t, s, client, projects[i], "milestone.create", protocol.ProjectInput{ID: protocol.UUID(), Title: textPointer("Release one"), Content: textPointer(secret)}))
	}
	client.ProjectID = &projects[0]
	if err := s.SaveClient(client); err != nil {
		t.Fatal(err)
	}
	before := completionTree(t, s, client)
	for _, kind := range []string{"projects", "issues", "comments", "milestones"} {
		code, body := directRequest(t, s, protocol.Client{}, "GET", "/v1/completions/"+kind, nil)
		if code != 200 {
			t.Fatalf("%s: %d %v", kind, code, body)
		}
		data := body["data"].(map[string]any)
		if data["service_id"] != s.DB.ServiceID() || data["api_major"] != float64(1) {
			t.Fatal(data)
		}
		items := data["items"].([]any)
		if len(items) != 2 {
			t.Fatalf("%s: %v", kind, data)
		}
		encoded, _ := json.Marshal(body)
		if strings.Contains(string(encoded), secret) {
			t.Fatal("content leaked", string(encoded))
		}
		for _, entry := range items {
			item := entry.(map[string]any)
			for key := range item {
				switch key {
				case "value", "id", "title", "project_id", "project_title", "issue_id", "state":
				default:
					t.Fatalf("unapproved completion field: %s", key)
				}
			}
			resolved, err := resolveRecordOf(s, kind, item["value"].(string), "")
			if err != nil {
				t.Fatal(kind, item, err)
			}
			_, id, _, _, _ := recordIdentity(resolved)
			if id != item["id"] {
				t.Fatal("candidate resolved to wrong record", item)
			}
		}
	}
	for _, tc := range []struct {
		query  string
		client protocol.Client
		want   string
		count  int
	}{
		{"", client, projects[0], 1},
		{"?project=feat/zulu", client, projects[1], 1},
		{"?prefix=" + url.QueryEscape("feat/zulu:Fi"), client, projects[1], 1},
		{"", protocol.Client{ClientID: protocol.UUID()}, "", 2},
		{"?prefix=feat/z&limit=1", protocol.Client{}, projects[1], 1},
		{"?prefix=" + secret, protocol.Client{}, "", 0},
	} {
		code, body := directRequest(t, s, tc.client, "GET", "/v1/completions/issues"+tc.query, nil)
		if code != 200 {
			t.Fatal(code, body)
		}
		items := body["data"].(map[string]any)["items"].([]any)
		if len(items) != tc.count {
			t.Fatalf("%s: %v", tc.query, items)
		}
		for _, item := range items {
			if tc.want != "" && item.(map[string]any)["project_id"] != tc.want {
				t.Fatal(item)
			}
		}
	}
	code, body := directRequest(t, s, protocol.Client{}, "GET", "/v1/completions/projects?limit=1", nil)
	if code != 200 || body["data"].(map[string]any)["has_more"] != true {
		t.Fatal(code, body)
	}
	code, body = directRequest(t, s, protocol.Client{}, "GET", "/v1/completions/projects?prefix=feat/z&limit=1", nil)
	items := body["data"].(map[string]any)["items"].([]any)
	if code != 200 || len(items) != 1 || items[0].(map[string]any)["id"] != projects[1] {
		t.Fatal(code, body)
	}
	if !reflect.DeepEqual(before, completionTree(t, s, client)) {
		t.Fatal("completion changed durable state")
	}
	client.Status = "disconnected"
	if err := s.SaveClient(client); err != nil {
		t.Fatal(err)
	}
	code, body = directRequest(t, s, client, "GET", "/v1/completions/issues", nil)
	if code != 200 || len(body["data"].(map[string]any)["items"].([]any)) != 2 {
		t.Fatal(code, body)
	}
}

func TestCompletionValidationAndContentReadGate(t *testing.T) {
	s, _ := projectTestServer(t, t.TempDir())
	defer s.DB.Close()
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"POST", "/v1/completions/projects", 405},
		{"GET", "/v1/completions/history", 404},
		{"GET", "/v1/completions/projects?content=true", 422},
		{"GET", "/v1/completions/issues?prefix=a&prefix=b", 422},
		{"GET", "/v1/completions/issues?project=", 422},
		{"GET", "/v1/completions/issues?project=feat/missing", 404},
		{"GET", "/v1/completions/projects?project=feat/a", 422},
		{"GET", "/v1/completions/projects?limit=0", 422},
		{"GET", "/v1/completions/projects?limit=1001", 422},
		{"GET", "/v1/completions/projects?prefix=%09", 422},
		{"GET", "/v1/completions/projects?prefix=%ff", 422},
		{"GET", "/v1/projects", 400},
		{"GET", "/v1/issues?all_projects=true", 400},
	} {
		code, body := directRequest(t, s, protocol.Client{}, tc.method, tc.path, nil)
		if code != tc.status {
			t.Fatalf("%s %s: %d %v", tc.method, tc.path, code, body)
		}
	}
}

func TestCompletionAmbiguousTitlesRoundTrip(t *testing.T) {
	s, client := projectTestServer(t, t.TempDir())
	defer s.DB.Close()
	project := protocol.UUID()
	executeRecordTest(t, s, client, prepareProjectTest(t, s, client, "create", protocol.ProjectInput{ID: project, Title: textPointer("feat/refs")}))
	for _, title := range []string{"help", "deadbeef", "title:literal", "id:literal", "Refactor a/b: cleanup", "-flag", "Quotes 'and' \"spaces\" $HOME; (echo nope)"} {
		executeRecordTest(t, s, client, prepareRecordTest(t, s, client, project, "issue.create", protocol.ProjectInput{ID: protocol.UUID(), Title: textPointer(title)}))
	}
	for _, query := range []string{"", "?project=feat/refs", "?project=feat/refs&prefix=title:", "?prefix=feat/refs:title:"} {
		code, body := directRequest(t, s, protocol.Client{}, "GET", "/v1/completions/issues"+query, nil)
		if code != 200 {
			t.Fatal(code, body)
		}
		items := body["data"].(map[string]any)["items"].([]any)
		if len(items) == 0 {
			t.Fatal("missing candidates", query)
		}
		for _, raw := range items {
			item := raw.(map[string]any)
			got, err := s.ResolveIssue(item["value"].(string), project)
			if err != nil || got.ID != item["id"] {
				t.Fatal(item, got, err)
			}
		}
	}
}

// completionTree is the durable state a completion must not change: every
// record with its history, and the client.
func completionTree(t *testing.T, s *Server, c protocol.Client) [32]byte {
	t.Helper()
	records, err := allRecords(s)
	if err != nil {
		t.Fatal(err)
	}
	client, err := s.Client(c.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(mustJSON([]any{records, client}))
}
