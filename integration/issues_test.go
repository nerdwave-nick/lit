package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssuesCommentsCLI(t *testing.T) {
	data, state, cwd := t.TempDir(), t.TempDir(), t.TempDir()
	s := start(t, data)
	run(t, cwd, state, 0, "connect", "--endpoint", s.endpoint, "--format", "json", "--actor-name", "Tester")
	run(t, cwd, state, 0, "projects", "create", "--project-title", "feat/issues")
	run(t, cwd, state, 0, "session", "set", "--project", "feat/issues")
	run(t, cwd, state, 0, "issues", "create", "--issue", "Straße", "--content", "# Exact\r\n雪\n", "--label", "ready", "--assignee", "owner")
	run(t, cwd, state, 0, "issues", "create", "--issue", "Child: discussion", "--parent", "STRASSE")
	r := run(t, cwd, state, 0, "issues", "get", "title:Child: discussion")
	child := r["items"].([]any)[0].(map[string]any)
	id := child["id"].(string)
	run(t, cwd, state, 0, "comments", "create", "--issue", id, "--content", "one", "--issue", id, "--content", "two", "--author", "Human")
	r = run(t, cwd, state, 0, "comments", "list", "--issue", id)
	if len(r["items"].([]any)) != 2 {
		t.Fatal(r)
	}
	for _, raw := range r["items"].([]any) {
		note := raw.(map[string]any)
		expectedAuthor := map[string]string{"one": "Tester", "two": "Human"}[note["body"].(string)]
		if expectedAuthor == "" || note["author"] != expectedAuthor || note["issue_id"] != id {
			t.Fatal("comment item association lost", note)
		}
	}
	comment := r["items"].([]any)[0].(map[string]any)["id"].(string)
	run(t, cwd, state, 0, "comments", "update", "--comment", comment, "--content", "updated")
	run(t, cwd, state, 0, "issues", "update", id, "--clear", "parent", "--add-label", "done")
	run(t, cwd, state, 0, "issues", "close", id)
	run(t, cwd, state, 0, "issues", "reopen", id)
	r = run(t, cwd, state, 0, "issues", "get", "feat/issues:title:STRASSE", id)
	if len(r["items"].([]any)) != 2 {
		t.Fatal(r)
	}
	// Creation, update, close and reopen; comments do not change the issue.
	r = run(t, cwd, state, 0, "issues", "history", id)
	if len(r["items"].([]any)) != 4 {
		t.Fatal(r)
	}
	s.stop(t)
	s = start(t, data)
	run(t, cwd, state, 0, "issues", "get", id, "--endpoint", s.endpoint)
	run(t, cwd, state, 0, "comments", "history", comment, "--endpoint", s.endpoint)
}

func TestIssueListScopeConflicts(t *testing.T) {
	data, state, cwd := t.TempDir(), t.TempDir(), t.TempDir()
	s := start(t, data)
	run(t, cwd, state, 0, "connect", "--endpoint", s.endpoint, "--format", "json")
	run(t, cwd, state, 0, "projects", "create", "--project-title", "feat/a", "--project-title", "feat/b")
	run(t, cwd, state, 0, "session", "set", "--project", "feat/a")
	run(t, cwd, state, 2, "issues", "list", "--project", "feat/a", "--all-projects")
	path := filepath.Join(cwd, "query.json")
	os.WriteFile(path, []byte(`{"type":"issues","project_id":"feat/b"}`), 0600)
	run(t, cwd, state, 2, "issues", "list", "--file", path, "--project", "feat/a")
	run(t, cwd, state, 0, "issues", "list", "--file", path, "--project", "feat/b")
	run(t, cwd, state, 0, "issues", "list", "--all-projects")
}

func TestIssueCommentGroupedFilesAndStdin(t *testing.T) {
	data, state, cwd := t.TempDir(), t.TempDir(), t.TempDir()
	s := start(t, data)
	run(t, cwd, state, 0, "connect", "--endpoint", s.endpoint, "--format", "json")
	run(t, cwd, state, 0, "projects", "create", "--project-title", "feat/files")
	run(t, cwd, state, 0, "session", "set", "--project", "feat/files")
	command := exec.Command(cliBin, "issues", "create", "--file", "-")
	command.Dir = cwd
	command.Env = append(os.Environ(), "LIT_SESSION=default", "LIT_STATE_DIR="+state, "GORACE=atexit_sleep_ms=0")
	command.Stdin = strings.NewReader(`[{"title":"First","content":"# Byte\\r\\n雪","labels":["ready"]},{"title":"Second"}]`)
	if b, e := command.CombinedOutput(); e != nil {
		t.Fatal(e, string(b))
	}
	r := run(t, cwd, state, 0, "issues", "get", "First", "Second")
	first := r["items"].([]any)[0].(map[string]any)["id"].(string)
	second := r["items"].([]any)[1].(map[string]any)["id"].(string)
	run(t, cwd, state, 0, "issues", "update", "--issue", first, "--content", "first", "--add-label", "a", "--issue", second, "--content", "second", "--assignee", "someone")
	run(t, cwd, state, 0, "issues", "update", "--issue", first, "--add-label", "b", "--issue", first, "--add-label", "c")
	run(t, cwd, state, 2, "issues", "update", "--issue", first, "--content", "one", "--content", "two")
	run(t, cwd, state, 2, "issues", "update", "--issue", first, "--add-label", "a", "--issue", first, "--add-label", "b", "--add-label", "b")
	run(t, cwd, state, 2, "issues", "update", first, "--clear", "labels", "--add-label", "x")
	run(t, cwd, state, 2, "issues", "create", "--issue", "bad", "--content", "literal", "--content-file", "missing")
	run(t, cwd, state, 2, "issues", "create", "--issue", "bad", "--content-file", "-", "--issue", "other", "--content-file", "-")
	file := filepath.Join(cwd, "update.json")
	os.WriteFile(file, []byte(`[{"target":"Second","set":{"assignee":null,"parent_id":null,"body":"# Updated\r\n雪"}}]`), 0600)
	run(t, cwd, state, 0, "issues", "update", "--file", file)
	r = run(t, cwd, state, 0, "issues", "get", second)
	issue := r["items"].([]any)[0].(map[string]any)
	if issue["assignee"] != nil || issue["parent_id"] != nil || issue["body"] != "# Updated\r\n雪" {
		t.Fatal(issue)
	}
	run(t, cwd, state, 0, "comments", "create", "--issue", first, "--content", "one", "--author", "Alice", "--issue", second, "--content", "two", "--author", "Bob")
	for n, owner := range []string{first, second} {
		notes := run(t, cwd, state, 0, "comments", "list", "--issue", owner)["items"].([]any)
		if len(notes) != 1 {
			t.Fatal(notes)
		}
		note := notes[0].(map[string]any)
		if note["issue_id"] != owner || note["body"] != []string{"one", "two"}[n] || note["author"] != []string{"Alice", "Bob"}[n] {
			t.Fatal("comment owner/content/author mismatch", note)
		}
	}
	r = run(t, cwd, state, 0, "comments", "list", "--issue", first)
	comment := r["items"].([]any)[0].(map[string]any)["id"].(string)
	os.WriteFile(file, []byte(`[{"target":"`+comment+`","set":{"author":"Descriptive author"},"clear":["content"]}]`), 0600)
	run(t, cwd, state, 0, "comments", "update", "--file", file)
	r = run(t, cwd, state, 0, "comments", "get", comment)
	note := r["items"].([]any)[0].(map[string]any)
	if note["body"] != "" || note["author"] != "Descriptive author" {
		t.Fatal(note)
	}
	for _, raw := range []string{`[{"target":"` + comment + `","set":{"author":null}}]`, `[{"target":"` + comment + `","set":{"state":"closed"}}]`, `[{"target":"` + comment + `","add":{}}]`, `[{"target":"` + comment + `","set":{"body":"a","body":"b"}}]`} {
		os.WriteFile(file, []byte(raw), 0600)
		run(t, cwd, state, 2, "comments", "update", "--file", file)
	}
	page := run(t, cwd, state, 0, "issues", "list", "--sort", "title", "--limit", "1")
	if page["next_cursor"] == nil {
		t.Fatal(page)
	}
	run(t, cwd, state, 0, "issues", "list", "--sort", "title", "--limit", "1", "--cursor", page["next_cursor"].(string))
	run(t, cwd, state, 2, "issues", "list", "--sort", "updated-at", "--limit", "1", "--cursor", page["next_cursor"].(string))
}
