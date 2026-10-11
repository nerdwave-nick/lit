package integration

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestGrepCLI(t *testing.T) {
	data, state, cwd := t.TempDir(), t.TempDir(), t.TempDir()
	s := start(t, data)
	run(t, cwd, state, 0, "connect", "--endpoint", s.endpoint, "--format", "json")
	run(t, cwd, state, 0, "projects", "create", "--project-title", "feat/search", "--content", "Project needle")
	run(t, cwd, state, 2, "grep", "needle")
	run(t, cwd, state, 0, "session", "set", "--project", "feat/search")
	run(t, cwd, state, 0, "issues", "create", "--issue", "A Straße", "--content", "old-only")
	r := run(t, cwd, state, 0, "issues", "get", "A Straße")
	id := r["items"].([]any)[0].(map[string]any)["id"].(string)
	run(t, cwd, state, 0, "comments", "create", "--issue", id, "--content", "before\n\nUnique.* Straße\nafter")
	run(t, cwd, state, 0, "issues", "update", id, "--content", "new-only")
	run(t, cwd, state, 0, "issues", "create", "--issue", "double-hit title", "--content", "double-hit body")
	both := run(t, cwd, state, 0, "grep", "double-hit")
	if len(both["items"].([]any)) != 1 || len(both["items"].([]any)[0].(map[string]any)["excerpts"].([]any)) != 2 {
		t.Fatal(both)
	}
	for _, pattern := range []string{"old-only", "does-not-match"} {
		r = run(t, cwd, state, 0, "grep", pattern)
		if len(r["items"].([]any)) != 0 {
			t.Fatal(r)
		}
	}
	r = run(t, cwd, state, 0, "grep", "Unique.*", "-C", "1", "-n")
	hits := r["items"].([]any)
	if len(hits) != 1 {
		t.Fatal(r)
	}
	hit := hits[0].(map[string]any)
	ex := hit["excerpts"].([]any)[0].(map[string]any)
	if hit["type"] != "comments" || hit["issue_id"] != id || hit["issue_title"] != "A Straße" || ex["text"] != "\nUnique.* Straße\nafter" || ex["start_line"] != float64(2) || ex["match_line"] != float64(3) {
		t.Fatal(r)
	}
	r = run(t, cwd, state, 0, "grep", "STRASSE")
	if len(r["items"].([]any)) != 2 {
		t.Fatal(r)
	}
	r = run(t, cwd, state, 0, "grep", "STRASSE", "--case-sensitive")
	if len(r["items"].([]any)) != 0 {
		t.Fatal(r)
	}
	run(t, cwd, state, 0, "projects", "create", "--project-title", "feat/other", "--content", "Unique.*")
	r = run(t, cwd, state, 0, "grep", "Unique.*", "--all-projects")
	if len(r["items"].([]any)) != 2 {
		t.Fatal(r)
	}
	r = run(t, cwd, state, 0, "grep", "Unique.*", "--project", "feat/other")
	if len(r["items"].([]any)) != 1 {
		t.Fatal(r)
	}
	run(t, cwd, state, 2, "grep", "Unique.*", "--all-projects", "--project", "feat/search")
	r = run(t, cwd, state, 0, "grep", "STRASSE", "--limit", "1")
	cursor := r["next_cursor"].(string)
	if r["server_time"] == "" {
		t.Fatal(r)
	}
	r = run(t, cwd, state, 0, "grep", "STRASSE", "--limit", "1", "--cursor", cursor)
	if len(r["items"].([]any)) != 1 || r["next_cursor"] != nil {
		t.Fatal(r)
	}
	run(t, cwd, state, 2, "grep", "other", "--limit", "1", "--cursor", cursor)
	for _, numbered := range []bool{false, true} {
		args := []string{"grep", "Unique.*", "--format", "cli"}
		if numbered {
			args = append(args, "-n")
		}
		command := exec.Command(cliBin, args...)
		command.Dir = cwd
		command.Env = append(os.Environ(), "LIT_SESSION=default", "LIT_STATE_DIR="+state, "GORACE=atexit_sleep_ms=0")
		b, e := command.CombinedOutput()
		if e != nil {
			t.Fatal(e, string(b))
		}
		if strings.Contains(string(b), "3: Unique") != numbered {
			t.Fatal(string(b))
		}
	}
	s.stop(t)
	s = start(t, data)
	r = run(t, cwd, state, 0, "grep", "Unique.*", "--endpoint", s.endpoint)
	if len(r["items"].([]any)) != 1 {
		t.Fatal(r)
	}
}

// Search limits end to end: with LIT_SEARCH_TIMEOUT set below any search's
// duration, lit grep fails with search_timeout (exit 1) and the advice to
// narrow it. A server restarted with the default deadline answers the same
// search, and refuses a pattern over 1,000 bytes with validation_failed
// (exit 2).
func TestGrepLimitsCLI(t *testing.T) {
	data, state, cwd := t.TempDir(), t.TempDir(), t.TempDir()
	s := start(t, data, "LIT_SEARCH_TIMEOUT=1ns")
	run(t, cwd, state, 0, "connect", "--endpoint", s.endpoint, "--format", "json")
	run(t, cwd, state, 0, "projects", "create", "--project-title", "feat/limits", "--content", "Project needle")
	failure := func(r map[string]any) (string, string) {
		e, _ := r["error"].(map[string]any)
		code, _ := e["code"].(string)
		msg, _ := e["message"].(string)
		return code, msg
	}
	for _, scope := range [][]string{{"--all-projects"}, {"--project", "feat/limits"}} {
		code, msg := failure(run(t, cwd, state, 1, append([]string{"grep", "needle"}, scope...)...))
		if code != "search_timeout" || !strings.Contains(msg, "narrow the query or its scope") {
			t.Fatal(scope, code, msg)
		}
	}
	s.stop(t)
	s = start(t, data)
	r := run(t, cwd, state, 0, "grep", "needle", "--all-projects", "--endpoint", s.endpoint)
	if len(r["items"].([]any)) != 1 {
		t.Fatal(r)
	}
	run(t, cwd, state, 0, "grep", strings.Repeat("x", 1000), "--all-projects", "--endpoint", s.endpoint)
	if code, msg := failure(run(t, cwd, state, 2, "grep", strings.Repeat("x", 1001), "--all-projects", "--endpoint", s.endpoint)); code != "validation_failed" || !strings.Contains(msg, "1000 bytes") {
		t.Fatal(code, msg)
	}
}
