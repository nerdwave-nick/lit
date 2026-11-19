package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkflowSessionHelp(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "file")
	if e := os.WriteFile(blocked, []byte("blocked"), 0600); e != nil {
		t.Fatal(e)
	}
	t.Setenv("LIT_STATE_DIR", filepath.Join(blocked, "state"))
	t.Setenv("LIT_WORKFLOW_STATE_DIR", filepath.Join(blocked, "workflow"))
	for _, args := range [][]string{{"--help"}, {"workflow-session", "--help"}} {
		var out, stderr bytes.Buffer
		if code := Run(args, &out, &stderr); code != 0 || !strings.Contains(out.String(), "workflow-session") {
			t.Fatalf("%v: code %d out %s err %s", args, code, &out, &stderr)
		}
	}
}

// TestWorkflowSessionShowRoutesThroughTheGrammar checks that show is a
// workflow-session action, reports a missing association with its own exit
// status, and accepts none of the bound flags.
func TestWorkflowSessionShowRoutesThroughTheGrammar(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LIT_STATE_DIR", filepath.Join(dir, "state"))
	t.Setenv("LIT_WORKFLOW_STATE_DIR", filepath.Join(dir, "workflow"))
	var out, stderr bytes.Buffer
	code := Run([]string{"workflow-session", "--host", "claude", "--runtime-id", "absent", "show"}, &out, &stderr)
	if code != 3 || stderr.Len() != 0 || !strings.Contains(out.String(), `"code":"association_not_found"`) {
		t.Fatalf("show: %d out %s err %s", code, &out, &stderr)
	}
	for _, extra := range [][]string{{"--endpoint", "http://127.0.0.1:1"}, {"--project", "feat/x"}, {"operand"}} {
		out.Reset()
		stderr.Reset()
		args := append([]string{"workflow-session", "--host", "claude", "--runtime-id", "absent", "show"}, extra...)
		if code := Run(args, &out, &stderr); code != 2 {
			t.Fatalf("%v: %d out %s err %s", extra, code, &out, &stderr)
		}
	}
	if _, e := os.Stat(filepath.Join(dir, "workflow")); !os.IsNotExist(e) {
		t.Fatal("show created workflow state")
	}
}
