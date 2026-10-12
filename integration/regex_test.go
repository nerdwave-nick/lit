package integration

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// lit grep -E end to end: the user story's pattern across all projects with
// line numbers and context, case-sensitivity, RE2's simple folding (ss is
// not ß in regex mode), a pattern without literals, and an invalid pattern
// (validation_failed, exit 2).
func TestGrepRegexCLI(t *testing.T) {
	data, state, cwd := t.TempDir(), t.TempDir(), t.TempDir()
	s := start(t, data)
	run(t, cwd, state, 0, "connect", "--endpoint", s.endpoint, "--format", "json")
	run(t, cwd, state, 0, "projects", "create", "--project-title", "feat/net", "--content", "Network notes")
	run(t, cwd, state, 0, "projects", "create", "--project-title", "feat/db", "--content", "Database notes\nsee ECONNRESET too")
	run(t, cwd, state, 0, "session", "set", "--project", "feat/net")
	run(t, cwd, state, 0, "issues", "create", "--issue", "Dial failures", "--content", "first line\ndial tcp: connect: econnrefused\nlast line")
	run(t, cwd, state, 0, "issues", "create", "--issue", "Die Straße", "--content", "nothing else")
	items := func(r map[string]any) []any { return r["items"].([]any) }

	r := run(t, cwd, state, 0, "grep", "-E", "econn(refused|reset)", "--all-projects", "-C", "1")
	if len(items(r)) != 2 {
		t.Fatal(r)
	}
	hit := items(r)[0].(map[string]any)
	if hit["type"] != "issues" {
		hit = items(r)[1].(map[string]any)
	}
	ex := hit["excerpts"].([]any)[0].(map[string]any)
	if hit["type"] != "issues" || ex["text"] != "first line\ndial tcp: connect: econnrefused\nlast line" || ex["start_line"] != float64(1) || ex["match_line"] != float64(2) {
		t.Fatal(r)
	}
	if r = run(t, cwd, state, 0, "grep", "--regex", "ECONN(REFUSED|RESET)", "--case-sensitive", "--all-projects"); len(items(r)) != 1 || items(r)[0].(map[string]any)["type"] != "projects" {
		t.Fatal(r)
	}
	if r = run(t, cwd, state, 0, "grep", "-E", "^dial"); len(items(r)) != 1 {
		t.Fatal(r)
	}
	if r = run(t, cwd, state, 0, "grep", "-E", "strasse"); len(items(r)) != 0 {
		t.Fatal("regex folding matched ss to ß:", r)
	}
	if r = run(t, cwd, state, 0, "grep", "strasse"); len(items(r)) != 1 {
		t.Fatal("literal grep lost full folding:", r)
	}
	if r = run(t, cwd, state, 0, "grep", "-E", `\w+ \w+`, "--limit", "1"); len(items(r)) != 1 || r["next_cursor"] == nil {
		t.Fatal(r)
	}
	if r = run(t, cwd, state, 0, "grep", "-E", `\w+ \w+`, "--limit", "1", "--cursor", r["next_cursor"].(string)); len(items(r)) != 1 {
		t.Fatal(r)
	}
	if e := run(t, cwd, state, 2, "grep", "-E", "(unclosed")["error"].(map[string]any); e["code"] != "validation_failed" || !strings.Contains(e["message"].(string), "RE2") {
		t.Fatal(e)
	}

	// Human output with line numbers, as literal grep prints it.
	command := exec.Command(cliBin, "grep", "-E", "econn(refused|reset)", "--all-projects", "-n", "-C", "1", "--format", "cli")
	command.Dir = cwd
	command.Env = append(os.Environ(), "LIT_SESSION=default", "LIT_STATE_DIR="+state, "GORACE=atexit_sleep_ms=0")
	b, e := command.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(b))
	}
	for _, want := range []string{"1: Database notes\n2: see ECONNRESET too\n", "1: first line\n2: dial tcp: connect: econnrefused\n3: last line\n"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %q in\n%s", want, b)
		}
	}
}
