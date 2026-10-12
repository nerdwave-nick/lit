package integration

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// lit search end to end: the most relevant result first, comment hits under
// their issue, diacritics ignored, operators, scope, state and cursors, plain
// output without terminal escapes off a terminal, and the index across a
// restart.
func TestRankedSearchCLI(t *testing.T) {
	data, state, cwd := t.TempDir(), t.TempDir(), t.TempDir()
	s := start(t, data)
	run(t, cwd, state, 0, "connect", "--endpoint", s.endpoint, "--format", "json")
	run(t, cwd, state, 0, "projects", "create", "--project-title", "feat/rank", "--content", "Ranked search")
	run(t, cwd, state, 2, "search", "timeout")
	run(t, cwd, state, 0, "session", "set", "--project", "feat/rank")
	run(t, cwd, state, 0, "issues", "create", "--issue", "Connection timeout handling", "--content", "Retries.",
		"--issue", "Retry policy", "--content", "A connection timeout leads to a retry.",
		"--issue", "Café opening", "--content", "Uses C++ and net/http.")
	r := run(t, cwd, state, 0, "issues", "create", "--issue", "Unrelated", "--content", "Nothing.")
	unrelated := r["items"].([]any)[0].(map[string]any)["id"].(string)
	run(t, cwd, state, 0, "comments", "create", "--issue", unrelated, "--content", "Seen a connection timeout in CI.")
	run(t, cwd, state, 0, "issues", "close", "Retry policy")
	ids := func(r map[string]any) []string {
		out := []string{}
		for _, v := range r["items"].([]any) {
			out = append(out, v.(map[string]any)["title"].(string))
		}
		return out
	}
	r = run(t, cwd, state, 0, "search", "connection timeout")
	if got := strings.Join(ids(r), "|"); got != "Connection timeout handling|Retry policy|Unrelated" {
		t.Fatal(got, r)
	}
	match := r["items"].([]any)[2].(map[string]any)["matches"].([]any)[0].(map[string]any)
	if match["field"] != "comment" || match["comment_id"] == "" || match["snippet"] != "Seen a connection timeout in CI." {
		t.Fatal(match)
	}
	for query, want := range map[string]string{"cafe": "Café opening", "C++": "Café opening", "net/http": "Café opening", `"timeout handling`: "Connection timeout handling",
		"connection timeout -retry -ci": "Connection timeout handling", "handl*": "Connection timeout handling", "opening OR handling": "Connection timeout handling|Café opening"} {
		got := ids(run(t, cwd, state, 0, "search", "--", query))
		if strings.Join(got, "|") != want && !(strings.Contains(want, "|") && len(got) == 2) {
			t.Errorf("%q: %v, want %s", query, got, want)
		}
	}
	if got := ids(run(t, cwd, state, 0, "search", "timeout", "--state", "closed")); strings.Join(got, "|") != "Retry policy" {
		t.Fatal(got)
	}
	run(t, cwd, state, 0, "projects", "create", "--project-title", "feat/other", "--content", "Another connection timeout")
	if got := ids(run(t, cwd, state, 0, "search", "connection timeout", "--all-projects")); len(got) != 4 {
		t.Fatal(got)
	}
	r = run(t, cwd, state, 0, "search", "connection timeout", "--limit", "1")
	cursor := r["next_cursor"].(string)
	r = run(t, cwd, state, 0, "search", "connection timeout", "--limit", "1", "--cursor", cursor)
	if got := ids(r); strings.Join(got, "|") != "Retry policy" {
		t.Fatal(got)
	}
	run(t, cwd, state, 2, "search", "other", "--cursor", cursor)
	run(t, cwd, state, 2, "search", "-flaky")
	run(t, cwd, state, 2, "search", strings.Repeat("x", 1001))
	command := exec.Command(cliBin, "search", "connection timeout", "--format", "cli")
	command.Dir = cwd
	command.Env = append(os.Environ(), "LIT_SESSION=default", "LIT_STATE_DIR="+state, "GORACE=atexit_sleep_ms=0")
	b, e := command.CombinedOutput()
	if e != nil || !strings.Contains(string(b), "  title: Connection timeout handling\n") || strings.Contains(string(b), "\x1b") {
		t.Fatal(e, string(b))
	}
	s.stop(t)
	s = start(t, data)
	if got := ids(run(t, cwd, state, 0, "search", "cafe", "--endpoint", s.endpoint)); strings.Join(got, "|") != "Café opening" {
		t.Fatal(got)
	}
}
