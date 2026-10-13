package cli

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/service"
	"github.com/nerdwave-nick/lit/internal/storage"
)

func TestMilestoneCLIRoundtripThroughService(t *testing.T) {
	t.Chdir(t.TempDir())
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	backend, err := service.New(db, service.Config{Limits: protocol.DefaultLimits(), TitlePrefixes: []string{"feat"}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(backend)
	t.Cleanup(server.Close)
	t.Setenv("LIT_STATE_DIR", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("LIT_SESSION", "")
	t.Setenv("LIT_ENDPOINT", "")
	session, endpoint := "milestone-cli-roundtrip", server.URL
	run := func(args ...string) (string, int) {
		t.Helper()
		base := []string{"--session", session, "--endpoint", endpoint}
		argv := append(base, args...)
		var out, errOut bytes.Buffer
		code := Run(argv, &out, &errOut)
		if code != 0 {
			t.Logf("command %v failed: %s %s", args, out.String(), errOut.String())
			return errOut.String() + out.String(), code
		}
		return out.String(), code
	}
	if _, code := run("connect", "--actor-name", "CLI test", "--actor-kind", "agent", "--format", "json"); code != 0 {
		t.Fatal("connect failed")
	}
	if _, code := run("projects", "create", "--project-title", "feat/milestones", "--content", "CLI milestone integration test", "--format", "json"); code != 0 {
		t.Fatal("project creation failed")
	}
	if _, code := run("session", "set", "--project", "feat/milestones", "--format", "json"); code != 0 {
		t.Fatal("project selection failed")
	}
	if _, code := run("issues", "create", "--issue", "Alpha", "--content", "alpha", "--issue", "Beta", "--content", "beta", "--format", "json"); code != 0 {
		t.Fatal("issue creation failed")
	}
	body := "## Objective\n\n  preserve exact body bytes\n"
	bodyFile := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(bodyFile, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if _, code := run("milestones", "create", "--milestone", "M1", "--content-file", bodyFile, "--issue", "Alpha", "--format", "json"); code != 0 {
		t.Fatal("milestone creation with membership failed")
	}
	if _, code := run("milestones", "create", "--file", writeQueryFile(t, `[ {"project":"feat/milestones","title":"M2","content":"second","issue_ids":[]} ]`), "--format", "json"); code != 0 {
		t.Fatal("typed JSON milestone creation failed")
	}
	atomicFile := writeQueryFile(t, `[
  {"target":"M1","expected_revision":1,"set":{"title":"M1 must roll back"}},
  {"target":"M2","expected_revision":99,"set":{"title":"M2 stale"}}
]`)
	if output, code := run("milestones", "update", "--file", atomicFile, "--format", "json"); code != 3 || !strings.Contains(output, "revision_conflict") {
		t.Fatalf("stale typed update was not rejected atomically: code=%d output=%s", code, output)
	}
	if output, code := run("milestones", "get", "M1", "M2", "--format", "json"); code != 0 {
		t.Fatalf("get after atomic rejection: code=%d %s", code, output)
	} else {
		var result struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal([]byte(output), &result); err != nil || len(result.Items) != 2 || result.Items[0]["title"] != "M1" || result.Items[1]["title"] != "M2" {
			t.Fatalf("stale batch partially applied: %s (%v)", output, err)
		}
	}
	export, code := run("milestones", "get", "M1", "--format", "markdown")
	if code != 0 || !strings.HasPrefix(export, "---\n") || !strings.HasSuffix(export, body) || strings.Contains(export, "progress:") {
		t.Fatalf("milestone markdown did not preserve the raw body/durable metadata: code=%d %s", code, export)
	}
	list, code := run("milestones", "list", "--format", "cli", "--sort", "title")
	if code != 0 || !strings.Contains(list, "MEMBERS") || !strings.Contains(list, "PROGRESS") || !strings.Contains(list, "0/1 closed; 1 open; 0 blocked") {
		t.Fatalf("milestone table omitted members or useful progress: code=%d %s", code, list)
	}
	filtered, code := run("issues", "list", "--milestone", "M1", "--format", "json")
	if code != 0 || !strings.Contains(filtered, `"title":"Alpha"`) || strings.Contains(filtered, `"title":"Beta"`) {
		t.Fatalf("issue milestone filter mismatch: code=%d %s", code, filtered)
	}
	updated, code := run("milestones", "update", "M1", "--title", "M1 renamed", "--content", "Updated objective", "--add-issue", "Beta", "--remove-issue", "Alpha", "--revision", "1", "--format", "json")
	if code != 0 {
		t.Fatal("milestone membership update failed")
	}
	var receipt struct {
		RequestHash string `json:"request_hash"`
	}
	if err := json.Unmarshal([]byte(updated), &receipt); err != nil || receipt.RequestHash == "" {
		t.Fatalf("missing update request hash: %s (%v)", updated, err)
	}
	if _, code := run("projects", "create", "--project-title", "feat/elsewhere", "--content", "Other selected project", "--format", "json"); code != 0 {
		t.Fatal("second project creation failed")
	}
	if _, code := run("session", "set", "--project", "feat/elsewhere", "--format", "json"); code != 0 {
		t.Fatal("different project selection failed")
	}
	history, code := run("milestones", "history", "M1 renamed", "--project", "feat/milestones", "--limit", "10", "--format", "json")
	if code != 0 || !strings.Contains(history, "create") || !strings.Contains(history, "update") {
		t.Fatalf("milestone history failed: code=%d %s", code, history)
	}
	for _, args := range [][]string{
		{"milestones", "history", "M1 renamed", "--project", "feat/milestones", "--all", "--format", "json"},
		{"milestones", "history", "M1 renamed", "--project", "feat/milestones", "--request-hash", receipt.RequestHash, "--format", "json"},
	} {
		if output, code := run(args...); code != 0 || !strings.Contains(output, receipt.RequestHash) {
			t.Fatalf("scoped history form %v failed: code=%d %s", args, code, output)
		}
	}
	if _, code := run("milestones", "update", "M1 renamed", "--project", "feat/milestones", "--clear", "issues", "--revision", "2", "--format", "json"); code != 0 {
		t.Fatal("clearing membership failed")
	}
	final, code := run("milestones", "get", "M1 renamed", "--project", "feat/milestones", "--format", "json")
	if code != 0 || !strings.Contains(final, `"issue_ids":[]`) || !strings.Contains(final, `"total":0`) {
		t.Fatalf("empty milestone projection mismatch: code=%d %s", code, final)
	}
}

func writeQueryFile(t *testing.T, value string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(path, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
