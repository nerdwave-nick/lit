package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	workflowskills "github.com/nerdwave-nick/lit/internal/workflow-skills"
)

// TestGoDistributionWalkthrough exercises the installed public executables from
// source-free directories. No Python, vendor CLI, shell, or source asset path is
// present on their PATH. The source tree is used only by normal go install/build.
func TestGoDistributionWalkthrough(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("lit-server service runtime is supported on Linux")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	build := exec.Command("go", "install", "../cmd/lit-server", "../cmd/lit", "../cmd/lit-tui")
	build.Env = append(os.Environ(), "GOBIN="+bin)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("standard go install: %v: %s", err, out)
	}
	for _, name := range []string{"lit", "lit-server", "lit-tui"} {
		if info, err := os.Stat(filepath.Join(bin, name)); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("missing installed %s: %v", name, err)
		}
	}
	for _, obsolete := range []string{"lit-cli", "litd"} {
		if _, err := os.Stat(filepath.Join(bin, obsolete)); !os.IsNotExist(err) {
			t.Fatalf("obsolete executable %s installed: %v", obsolete, err)
		}
	}
	oldService := litBin
	litBin = filepath.Join(bin, "lit-server")
	defer func() { litBin = oldService }()
	cli := filepath.Join(bin, "lit")
	home := filepath.Join(root, "home")
	repo1 := filepath.Join(root, "producer")
	repo2 := filepath.Join(root, "consumer")
	custom := filepath.Join(root, "custom parent")
	for _, p := range []string{home, repo1, repo2, custom} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	env := []string{"PATH=" + bin, "HOME=" + home, "USERPROFILE=" + home, "LIT_STATE_DIR=" + filepath.Join(root, "private"), "LIT_WORKFLOW_STATE_DIR=" + filepath.Join(root, "workflow"), "LIT_SESSION=wrong-inherited-session", "LIT_ENDPOINT=http://127.0.0.1:1"}
	cwd := repo1
	call := func(args ...string) map[string]any {
		t.Helper()
		c := exec.Command(cli, args...)
		c.Dir = cwd
		c.Env = env
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v: %s", args, err, out)
		}
		var result map[string]any
		if err := json.Unmarshal(out, &result); err != nil {
			t.Fatalf("%v JSON: %v: %s", args, err, out)
		}
		return result
	}
	// Outside a terminal, bare lit keeps its usage error for scripts and agents,
	// while lit tui hands over to the installed lit-tui, which needs a terminal.
	for _, tc := range []struct {
		args         []string
		code         int
		stderr, from string
	}{
		{nil, 2, `{"error":{"code":"invalid_arguments","message":"a subcommand is required for lit; run 'lit --help'","details":{}}}`, "lit"},
		{[]string{"tui"}, 2, "lit-tui needs an interactive terminal: stdin and stdout must be terminals", "lit-tui"},
		{[]string{"tui", "--endpoint", "http://127.0.0.1:1"}, 2, "lit-tui needs an interactive terminal: stdin and stdout must be terminals", "lit-tui"},
	} {
		c := exec.Command(cli, tc.args...)
		c.Dir, c.Env = cwd, env
		var stdout, stderr bytes.Buffer
		c.Stdout, c.Stderr = &stdout, &stderr
		_ = c.Run()
		if c.ProcessState.ExitCode() != tc.code || stdout.Len() != 0 || strings.TrimSpace(stderr.String()) != tc.stderr {
			t.Fatalf("lit %v outside a terminal (want the %s result): %d, stdout %q, stderr %q", tc.args, tc.from, c.ProcessState.ExitCode(), &stdout, &stderr)
		}
	}
	if out, err := exec.Command(filepath.Join(bin, "lit-tui"), "--version").CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "dev" {
		t.Fatalf("lit-tui --version: %v: %s", err, out)
	}
	// Setup has no service available and no usable inherited tracker selection.
	for _, args := range [][]string{{"--scope", "local", "--agent", "both"}, {"--scope", "user", "--agent", "both"}, {"--scope", "custom", "--path", custom, "--agent", "both"}} {
		for repeat := 0; repeat < 2; repeat++ {
			call(append([]string{"setup-skills"}, args...)...)
		}
	}
	expected := []string{"lit", "planner", "clarify", "modeling", "challenge", "consult", "research", "prototype", "to-spec", "to-tickets", "impl", "triage", "codebase", "what", "i-have-adhd", "orchestrate"}
	for _, parent := range []string{repo1, home, custom} {
		for _, host := range []string{"codex", "claude"} {
			hostRoot := filepath.Join(parent, "."+host)
			skills := filepath.Join(hostRoot, "skills")
			entries, err := os.ReadDir(skills)
			if err != nil || len(entries) != len(expected) {
				t.Fatalf("suite %s: %d %v", skills, len(entries), err)
			}
			for _, name := range expected {
				b, err := os.ReadFile(filepath.Join(skills, name, "SKILL.md"))
				if err != nil || len(b) == 0 {
					t.Fatalf("skill %s: %v", name, err)
				}
			}
			if err := fs.WalkDir(workflowskills.Bundle(), "skills/orchestrate", func(name string, entry fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if entry.IsDir() {
					return nil
				}
				rel := strings.TrimPrefix(name, "skills/orchestrate/")
				want, err := fs.ReadFile(workflowskills.Bundle(), name)
				if err != nil {
					return err
				}
				got, err := os.ReadFile(filepath.Join(skills, "orchestrate", filepath.FromSlash(rel)))
				if err != nil {
					return err
				}
				if !bytes.Equal(got, want) {
					return fmt.Errorf("installed orchestrate resource differs from bundle: %s", rel)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"what", "i-have-adhd"} {
				b, err := os.ReadFile(filepath.Join(skills, name, "SKILL.md"))
				if err != nil || !bytes.Contains(b, []byte("disable-model-invocation: true")) {
					t.Fatalf("Claude policy %s: %v", name, err)
				}
				b, err = os.ReadFile(filepath.Join(skills, name, "agents", "openai.yaml"))
				if err != nil || !bytes.Contains(b, []byte("allow_implicit_invocation: false")) {
					t.Fatalf("Codex policy %s: %v", name, err)
				}
			}
			for _, name := range []string{"planner", "to-spec", "to-tickets", "triage"} {
				b, err := os.ReadFile(filepath.Join(skills, name, "SKILL.md"))
				if err != nil || bytes.Contains(b, []byte("disable-model-invocation: true")) || !bytes.Contains(b, []byte("Run this skill only when the user requested it.")) {
					t.Fatalf("Claude confirmation-gated policy %s: %v", name, err)
				}
				b, err = os.ReadFile(filepath.Join(skills, name, "agents", "openai.yaml"))
				if err != nil || bytes.Contains(b, []byte("allow_implicit_invocation: false")) {
					t.Fatalf("Codex confirmation-gated policy %s: %v", name, err)
				}
			}
			if err := filepath.WalkDir(hostRoot, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if strings.HasSuffix(path, ".py") || strings.Contains(path, "__pycache__") {
					t.Errorf("installed Python dependency %s", path)
				}
				if !d.IsDir() && (strings.HasSuffix(path, ".md") || strings.HasSuffix(path, ".yaml")) {
					content, err := os.ReadFile(path)
					if err != nil {
						return err
					}
					for _, obsolete := range []string{"lit-cli", "litd"} {
						if bytes.Contains(content, []byte(obsolete)) {
							t.Errorf("installed active instruction %s names obsolete executable %s", path, obsolete)
						}
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	s := start(t, filepath.Join(root, "data"), "HOME="+home, "PATH="+bin, "XDG_CONFIG_HOME="+filepath.Join(root, "config"), "LIT_CONFIG_FILE=", "LIT_DATA_DIR=", "LIT_LISTEN=")
	clients := map[any]bool{}
	for _, host := range []string{"codex", "claude"} {
		cwd = repo1
		session := func(args ...string) map[string]any {
			t.Helper()
			return call(append([]string{"workflow-session", "--host", host, "--runtime-id", "main"}, args...)...)
		}
		tracker := func(args ...string) map[string]any {
			t.Helper()
			return session(append([]string{"run", "--"}, args...)...)
		}
		fresh := session("fresh", "--endpoint", s.endpoint, "--discussion-id", "go-distribution-walkthrough")
		if clients[fresh["client_id"]] {
			t.Fatal("hosts share a logical client")
		}
		clients[fresh["client_id"]] = true
		project := item(tracker("projects", "create", "--project-title", "feat/distribution-"+host))["id"].(string)
		tracker("session", "set", "--project", project)
		issue := item(tracker("issues", "create", "--issue", "Verify installed workflow", "--assignee", host, "--content", "Use embedded lit skill: claim, checkpoint, resume, and close after checks."))["id"].(string)
		tracker("claims", "acquire", issue)
		tracker("comments", "create", "--issue", issue, "--content", "Checkpoint: both host bundles installed offline; seven-binary delivery verification follows.")
		session("checkout", "--repository", "https://example.invalid/producer", "--path", repo1)
		cwd = repo2
		resumed := session("resume")
		if resumed["client_id"] != fresh["client_id"] || resumed["project_id"] != project {
			t.Fatal("cross-checkout resume changed identity/project", resumed)
		}
		session("checkout", "--repository", "https://example.invalid/consumer", "--path", repo2)
		child := call("workflow-session", "--host", host, "--runtime-id", "child", "subagent", "--parent-runtime-id", "main", "--endpoint", s.endpoint, "--project", project)
		if clients[child["client_id"]] {
			t.Fatal("child inherited a logical client")
		}
		clients[child["client_id"]] = true
		childResume := call("workflow-session", "--host", host, "--runtime-id", "child", "resume")
		if childResume["client_id"] != child["client_id"] {
			t.Fatal("child resume changed identity")
		}
		if item(tracker("claims", "get", issue))["claim"] == nil {
			t.Fatal("resume lost active claim")
		}
		cwd = repo1
		tracker("comments", "create", "--issue", issue, "--content", "Verification: explicit parent/child sessions and cross-checkout resume passed.")
		tracker("issues", "close", issue)
		if item(tracker("issues", "get", issue))["state"] != "closed" || item(tracker("claims", "get", issue))["claim"] != nil {
			t.Fatal("closure did not close/release")
		}
		if len(tracker("comments", "list", "--issue", issue)["items"].([]any)) != 2 {
			t.Fatal("checkpoint comments missing")
		}
	}
	mappings, err := filepath.Glob(filepath.Join(root, "workflow", "checkouts", "*.json"))
	if err != nil || len(mappings) != 4 {
		t.Fatal("checkout mappings", mappings, err)
	}
	t.Log("PASS: standard Go install (lit, lit-server, lit-tui); non-terminal lit/lit tui dispatch; source-free offline setup all scopes/both hosts/16 skills; real service; four isolated sessions; cross-checkout resume/mappings; claims/checkpoints/closure")
}
