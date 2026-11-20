package integration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	workflowskills "github.com/nerdwave-nick/lit/internal/workflow-skills"
)

// TestGoDistributionWalkthrough exercises the installed public executables from
// source-free directories. No Python, vendor CLI, shell, or source asset path is
// present on their PATH. The source tree is used only by normal go install/build.
func TestGoDistributionWalkthrough(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("lit-server service runtime is supported on Linux and macOS")
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
	claudeConfig := filepath.Join(root, "claude work")
	codexConfig := filepath.Join(root, "codex personal")
	for _, p := range []string{home, repo1, repo2, claudeConfig, codexConfig} {
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
		// stderr carries setup's warnings and notes; stdout is the JSON result.
		var stderr bytes.Buffer
		c.Stderr = &stderr
		out, err := c.Output()
		if err != nil {
			t.Fatalf("%v: %v: %s%s", args, err, out, &stderr)
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
	// Both binaries report the same version: "dev", or the version go install
	// recorded from the checkout's VCS state (for example v0.7.1+dirty).
	litVersion, err := exec.Command(cli, "version").CombinedOutput()
	if err != nil || strings.TrimSpace(string(litVersion)) == "" {
		t.Fatalf("lit version: %v: %s", err, litVersion)
	}
	if out, err := exec.Command(filepath.Join(bin, "lit-tui"), "--version").CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != strings.TrimSpace(string(litVersion)) {
		t.Fatalf("lit-tui --version: %v: %s, lit version: %s", err, out, litVersion)
	}
	// The claude configuration holds an install of the removed setup-skills
	// command, which setup-workflows adopts in its first run.
	legacySkill := []byte("an older lit skill\n")
	legacyFingerprint := sha256.New()
	legacyFingerprint.Write(append(append([]byte("SKILL.md\x00"), legacySkill...), 0))
	for name, content := range map[string]string{
		filepath.Join("skills", "lit", "SKILL.md"): string(legacySkill),
		".lit-skills.json":                         `{"schema_version":1,"host":"claude","entries":{"lit":"` + hex.EncodeToString(legacyFingerprint.Sum(nil)) + `"}}`,
		"CLAUDE.md":                                "mine\n<!-- BEGIN lit managed -->\nold\n<!-- END lit managed -->\n",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(claudeConfig, name)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(claudeConfig, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Setup has no service available and no usable inherited tracker selection.
	// hooks: the target is an agent configuration (user scope or --config-dir),
	// which also gets the lit hooks; a local scope never does.
	type target struct {
		host, root string
		hooks      bool
	}
	targets := []target{}
	installs := []struct {
		args    []string
		targets []target
		adopt   bool
	}{
		{[]string{"--scope", "local", "--agent", "both"}, []target{{"codex", filepath.Join(repo1, ".codex"), false}, {"claude", filepath.Join(repo1, ".claude"), false}}, false},
		{[]string{"--scope", "user", "--agent", "both"}, []target{{"codex", filepath.Join(home, ".codex"), true}, {"claude", filepath.Join(home, ".claude"), true}}, false},
		{[]string{"--agent", "claude", "--config-dir", claudeConfig}, []target{{"claude", claudeConfig, true}}, true},
		{[]string{"--agent", "codex", "--config-dir", codexConfig}, []target{{"codex", codexConfig, true}}, false},
	}
	for _, tc := range installs {
		for repeat := 0; repeat < 2; repeat++ {
			receipt := call(append([]string{"setup-workflows"}, tc.args...)...)["installed"].([]any)
			if len(receipt) != len(tc.targets) {
				t.Fatalf("%v receipt: %v", tc.args, receipt)
			}
			for i, want := range tc.targets {
				got := receipt[i].(map[string]any)
				if got["agent"] != want.host || got["directory"] != want.root || got["adopted"] != (tc.adopt && repeat == 0) || got["hooks"] != want.hooks {
					t.Fatalf("%v run %d receipt %v, want %v (adopted %v)", tc.args, repeat, got, want, tc.adopt && repeat == 0)
				}
			}
		}
		targets = append(targets, tc.targets...)
	}
	expected := []string{"lit", "planner", "clarify", "modeling", "challenge", "consult", "research", "prototype", "to-spec", "to-tickets", "impl", "triage", "codebase", "what", "i-have-adhd", "orchestrate"}
	for _, tg := range targets {
		host, hostRoot := tg.host, tg.root
		skills := filepath.Join(hostRoot, "skills")
		var record struct {
			Host   string            `json:"host"`
			Skills map[string]string `json:"skills"`
		}
		recordJSON, err := os.ReadFile(filepath.Join(hostRoot, ".lit-workflows.json"))
		if err != nil || json.Unmarshal(recordJSON, &record) != nil || record.Host != host || len(record.Skills) != len(expected) {
			t.Fatalf("ownership record of %s: %v %s", hostRoot, err, recordJSON)
		}
		if _, err := os.Stat(filepath.Join(hostRoot, ".lit-skills.json")); !os.IsNotExist(err) {
			t.Fatalf("%s keeps the old ownership file: %v", hostRoot, err)
		}
		entries, err := os.ReadDir(skills)
		wantEntries := len(expected)
		if tg.hooks && host == "claude" {
			wantEntries++ // the lit-owned hooks plugin directory
		}
		if err != nil || len(entries) != wantEntries {
			t.Fatalf("suite %s: %d %v", skills, len(entries), err)
		}
		checkHooks(t, tg.host, tg.root, tg.hooks, bin, env, repo1)
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
	// setup-workflows --remove undoes every installation above with the same
	// flags, leaving only what the user wrote.
	for _, tc := range installs {
		for repeat := 0; repeat < 2; repeat++ {
			receipt := call(append([]string{"setup-workflows", "--remove"}, tc.args...)...)["removed"].([]any)
			if len(receipt) != len(tc.targets) {
				t.Fatalf("remove %v receipt: %v", tc.args, receipt)
			}
			for i, want := range tc.targets {
				got := receipt[i].(map[string]any)
				// The second run finds nothing left to remove.
				first, skills := repeat == 0, 0
				if first {
					skills = len(expected)
				}
				if got["agent"] != want.host || got["directory"] != want.root || got["found"] != first || got["hooks"] != (first && want.hooks) || got["skills"] != float64(skills) {
					t.Fatalf("remove %v run %d receipt %v, want %v (found %v)", tc.args, repeat, got, want, first)
				}
			}
		}
	}
	for _, tg := range targets {
		var left []string
		if err := filepath.WalkDir(tg.root, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				left = append(left, strings.TrimPrefix(path, tg.root+string(filepath.Separator)))
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		// The claude configuration keeps the line the user wrote before setup.
		if tg.root == claudeConfig {
			if b, err := os.ReadFile(filepath.Join(claudeConfig, "CLAUDE.md")); err != nil || string(b) != "mine\n" {
				t.Fatalf("CLAUDE.md after removal: %q %v", b, err)
			}
			left = slices.DeleteFunc(left, func(f string) bool { return f == "CLAUDE.md" })
		}
		if len(left) != 0 {
			t.Fatalf("removal left files in %s: %v", tg.root, left)
		}
	}
	t.Log("PASS: standard Go install (lit, lit-server, lit-tui); non-terminal lit/lit tui dispatch; source-free offline setup all scopes/both hosts/16 skills; hooks and status line in agent configurations only (installed commands run); real service; four isolated sessions; cross-checkout resume/mappings; claims/checkpoints/closure; setup-workflows --remove leaves only the user's files")
}

// checkHooks verifies the hook installation of one target and runs the installed
// hook commands the way the host does (a shell, the host's input on stdin, a
// PATH that holds only the installed lit). lit-server is unreachable at the
// walkthrough's LIT_ENDPOINT, so the commands answer that lit is unavailable.
func checkHooks(t *testing.T, host, root string, hooks bool, bin string, env []string, cwd string) {
	t.Helper()
	present := func(p string) bool { _, err := os.Stat(filepath.Join(root, p)); return err == nil }
	plugin := filepath.Join("skills", "lit-hooks")
	if present("settings.json") != (hooks && host == "claude") || present(plugin) != (hooks && host == "claude") || present("hooks.json") != (hooks && host == "codex") {
		t.Fatalf("%s: hook files: settings.json=%v plugin=%v hooks.json=%v, want hooks=%v", root, present("settings.json"), present(plugin), present("hooks.json"), hooks)
	}
	if !hooks {
		return
	}
	hooksFile := "hooks.json"
	if host == "claude" {
		hooksFile = filepath.Join(plugin, "hooks", "hooks.json")
	}
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Type, Command string
				Timeout       int
			}
		}
	}
	b, err := os.ReadFile(filepath.Join(root, hooksFile))
	if err != nil || json.Unmarshal(b, &doc) != nil || len(doc.Hooks) != 3 {
		t.Fatalf("%s: %v %s", hooksFile, err, b)
	}
	run := func(command, input string) string {
		t.Helper()
		c := exec.Command("sh", "-c", command)
		c.Dir, c.Env, c.Stdin = cwd, append(append([]string{}, env...), "COLUMNS=100"), strings.NewReader(input)
		var stdout, stderr bytes.Buffer
		c.Stdout, c.Stderr = &stdout, &stderr
		if err := c.Run(); err != nil {
			t.Fatalf("%s: %v: %s%s", command, err, &stdout, &stderr)
		}
		return stdout.String()
	}
	for event, input := range map[string]string{
		"SessionStart":  `{"session_id":"walkthrough-hooks-` + host + `","hook_event_name":"SessionStart","source":"startup","cwd":"` + cwd + `"}`,
		"SubagentStart": `{"session_id":"walkthrough-hooks-` + host + `","agent_id":"walkthrough-agent","agent_type":"general-purpose","hook_event_name":"SubagentStart","cwd":"` + cwd + `"}`,
		"Stop":          `{"session_id":"walkthrough-hooks-` + host + `","hook_event_name":"Stop","stop_hook_active":false,"cwd":"` + cwd + `"}`,
	} {
		groups := doc.Hooks[event]
		if len(groups) != 1 || len(groups[0].Hooks) != 1 || groups[0].Hooks[0].Type != "command" || !strings.Contains(groups[0].Hooks[0].Command, "hook --host "+host+" ") {
			t.Fatalf("%s %s: %+v", root, event, groups)
		}
		out := run(groups[0].Hooks[0].Command, input)
		if event == "SessionStart" && !strings.Contains(out, "unavailable") {
			t.Errorf("%s SessionStart answered %q, want the lit-unavailable note", root, out)
		}
	}
	if host != "claude" {
		return
	}
	var settings struct {
		StatusLine struct {
			Type, Command   string
			RefreshInterval int
		} `json:"statusLine"`
	}
	b, err = os.ReadFile(filepath.Join(root, "settings.json"))
	if err != nil || json.Unmarshal(b, &settings) != nil || settings.StatusLine.Type != "command" || settings.StatusLine.RefreshInterval != 2 || !strings.Contains(settings.StatusLine.Command, "hook --host claude statusline") {
		t.Fatalf("settings.json: %v %s", err, b)
	}
	if line := run(settings.StatusLine.Command, `{"session_id":"walkthrough-hooks-claude","cwd":"`+cwd+`","model":{"display_name":"Opus"}}`); !strings.Contains(line, "not lit") {
		t.Errorf("%s status line %q, want the not-lit state", root, line)
	}
}
