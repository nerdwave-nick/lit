package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type brokenSetupOutput struct{}

func (brokenSetupOutput) Write([]byte) (int, error) { return 0, errors.New("test output unavailable") }

func TestSetupWorkflowsReportsOutputFailureAfterInstallation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LIT_STATE_DIR", t.TempDir())
	t.Chdir(t.TempDir())
	destination := t.TempDir()
	var stderr bytes.Buffer
	code := Run([]string{"setup-workflows", "--agent", "codex", "--config-dir", destination}, brokenSetupOutput{}, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "workflows installed") || !strings.Contains(stderr.String(), "write result") {
		t.Fatalf("code=%d stderr=%s", code, &stderr)
	}
	if _, err := os.Stat(filepath.Join(destination, "skills", "lit", "SKILL.md")); err != nil {
		t.Fatalf("successful installation was not retained: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, ".lit-workflows.json")); err != nil {
		t.Fatal(err)
	}
}

func TestHelperOutputHelpAndDirectoryCompletion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("LIT_STATE_DIR", t.TempDir())
	t.Setenv("LIT_WORKFLOW_STATE_DIR", t.TempDir())
	t.Setenv("LIT_ENDPOINT", "http://127.0.0.1:1")
	t.Chdir(t.TempDir())
	for _, path := range []string{"setup-workflows", "workflow-session", "workflow-session fresh", "workflow-session subagent", "workflow-session resume", "workflow-session reconcile", "workflow-session checkout", "workflow-session run"} {
		var out, stderr bytes.Buffer
		if code := Run(append(strings.Fields(path), "--help"), &out, &stderr); code != 0 || !(strings.Contains(out.String(), "JSON") || strings.Contains(out.String(), "--format json")) {
			t.Errorf("%s code=%d out=%s err=%s", path, code, &out, &stderr)
		}
	}
	for _, path := range []string{"setup-workflows", "workflow-session checkout"} {
		var out, stderr bytes.Buffer
		args := append([]string{"__complete"}, strings.Fields(path)...)
		args = append(args, map[string]string{"setup-workflows": "--config-dir", "workflow-session checkout": "--path"}[path], "")
		if code := Run(args, &out, &stderr); code != 0 || !strings.Contains(out.String(), ":16\n") {
			t.Errorf("directory completion %s code=%d out=%s err=%s", path, code, &out, &stderr)
		}
	}
}
