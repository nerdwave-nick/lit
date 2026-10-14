package integration

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	workflowskills "github.com/nerdwave-nick/lit/internal/workflow-skills"
)

// The harness of the installed new-conversation walkthrough. Everything runs
// the public binaries built by go install, from a PATH that holds only them,
// against temporary lit-servers: no command can reach the developer's own
// server, home, agent configurations or model CLIs, so the walkthrough makes
// no model calls and shows nothing about how a native model follows the
// guidance. What it does show is that the guidance delivered into temporary
// Codex and Claude destinations teaches commands that behave as described.

type project struct{ id, title string }

type installed struct {
	t            *testing.T
	root, bin    string
	a, b         *server
	dataA, dataB string
}

// variant is one installed setup of one host: skills only (local scope), or
// skills plus hooks (an explicit configuration directory).
type variant struct {
	*installed
	t                 *testing.T // the subtest's, shadowing the walkthrough's
	name, host        string
	hooks             bool
	dir, config       string // the variant's temp root; the agent configuration that holds the delivered skills
	workflow, private string
	main, linked      string // two worktrees of one repository
	clients           map[string]bool
}

func (s *installed) litBin() string { return filepath.Join(s.bin, "lit") }

func (v *variant) env(endpoint string) []string {
	return []string{"PATH=" + v.bin, "HOME=" + filepath.Join(v.dir, "home"),
		"XDG_STATE_HOME=" + filepath.Join(v.dir, "xdg-state"), "XDG_CONFIG_HOME=" + filepath.Join(v.dir, "xdg-config"), "XDG_DATA_HOME=" + filepath.Join(v.dir, "xdg-data"),
		"LIT_WORKFLOW_STATE_DIR=" + v.workflow, "LIT_STATE_DIR=" + v.private, "LIT_SESSION=no-such-session", "LIT_ENDPOINT=" + endpoint}
}

// run executes a program in cwd with the variant's environment, naming
// endpoint as the only server it may reach, and returns its exit code and output.
func (v *variant) run(cwd, endpoint, stdin, program string, args ...string) (int, string, string) {
	v.t.Helper()
	if endpoint == "" {
		v.t.Fatal("every command names its endpoint: the walkthrough never reaches the default server")
	}
	cmd := exec.Command(program, args...)
	cmd.Dir, cmd.Env, cmd.Stdin = cwd, v.env(endpoint), strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok {
			v.t.Fatal(err)
		}
		code = exit.ExitCode()
	}
	return code, stdout.String(), stderr.String()
}

func (v *variant) lit(cwd, endpoint string, args ...string) (int, string, string) {
	v.t.Helper()
	return v.run(cwd, endpoint, "", v.litBin(), args...)
}

// litJSON runs lit, requires success and returns the JSON it printed.
func (v *variant) litJSON(cwd, endpoint string, args ...string) map[string]any {
	v.t.Helper()
	code, out, errOut := v.lit(cwd, endpoint, args...)
	if code != 0 {
		v.t.Fatalf("lit %v: exit %d\n%s%s", args, code, out, errOut)
	}
	return parseJSON(v.t, out)
}

func parseJSON(t *testing.T, text string) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("JSON: %v\n%s", err, text)
	}
	return v
}

// delivered reads a reference of the lit skill as setup delivered it.
func (v *variant) delivered(rel string) string {
	v.t.Helper()
	b, err := os.ReadFile(filepath.Join(v.config, "skills", "lit", "references", filepath.FromSlash(rel)))
	if err != nil {
		v.t.Fatal(err)
	}
	return string(b)
}

// guided returns the arguments of a command that the delivered guidance in rel
// spells exactly as line, with the placeholder pairs in repl replaced. The
// walkthrough runs the commands the way the guidance teaches them, so a
// guidance edit that no longer matches the executable fails here. The default
// server's address in the guidance's examples is replaced with the first
// temporary server, and a command that still names it is refused.
func (v *variant) guided(rel, line string, repl ...string) []string {
	v.t.Helper()
	found := false
	for _, l := range strings.Split(v.delivered(rel), "\n") {
		found = found || strings.TrimSpace(l) == line
	}
	if !found {
		v.t.Fatalf("the delivered %s (%s) does not teach %q", rel, v.config, line)
	}
	words := strings.Fields(strings.NewReplacer(append(repl, "--host codex", "--host "+v.host, "http://127.0.0.1:7411", v.a.endpoint)...).Replace(line))
	if words[0] != "lit" || strings.Contains(strings.Join(words, " "), ":7411") {
		v.t.Fatalf("refusing to run %v", words)
	}
	return words[1:]
}

// hookCommand is the installed command for a host event, found in the
// hook definitions that setup wrote.
func (v *variant) hookCommand(event string) string {
	v.t.Helper()
	file := filepath.Join(v.config, "hooks.json")
	if v.host == "claude" {
		file = filepath.Join(v.config, "skills", "lit-hooks", "hooks", "hooks.json")
	}
	b, err := os.ReadFile(file)
	if err != nil {
		v.t.Fatal(err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct{ Command string }
		}
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		v.t.Fatalf("%s: %v", file, err)
	}
	for _, group := range doc.Hooks[event] {
		for _, hook := range group.Hooks {
			if strings.Contains(hook.Command, "hook --host "+v.host+" ") {
				return hook.Command
			}
		}
	}
	v.t.Fatalf("%s has no lit %s hook", file, event)
	return ""
}

// runHook runs an installed hook command the way the host does (a shell, the
// event's JSON on stdin) and returns the context it injects for the agent.
func (v *variant) runHook(cwd, endpoint, event string, input map[string]any) string {
	v.t.Helper()
	input["hook_event_name"] = event
	input["cwd"] = cwd
	stdin, _ := json.Marshal(input)
	code, out, errOut := v.run(cwd, endpoint, string(stdin), "sh", "-c", v.hookCommand(event))
	if code != 0 {
		v.t.Fatalf("hook %s: exit %d\n%s%s", event, code, out, errOut)
	}
	var printed struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &printed); err != nil || printed.HookSpecificOutput.AdditionalContext == "" {
		v.t.Fatalf("hook %s printed %q (%v), want additionalContext", event, out, err)
	}
	return printed.HookSpecificOutput.AdditionalContext
}

// contextLine is the value of the first "prefix value" line of a hook context.
func contextLine(text, prefix string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix)
		}
	}
	return ""
}

// seed writes a user's own file, which setup and removal must leave alone.
func seed(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// install seeds the user's own content in the destination, installs the
// embedded skills with the installed lit, and checks that the destination now
// holds exactly the embedded guidance next to the untouched user content.
func (v *variant) install() {
	t := v.t
	t.Helper()
	instruction, own := "CLAUDE.md", "mine\n"
	if v.host == "codex" {
		instruction = "AGENTS.md"
	}
	args := []string{"setup-workflows", "--agent", v.host, "--config-dir", v.config}
	cwd := v.dir
	if !v.hooks {
		cwd = v.main
		v.config = filepath.Join(v.main, "."+v.host)
		args = []string{"setup-workflows", "--scope", "local", "--agent", v.host}
	}
	seed(t, filepath.Join(v.config, "skills", "mine", "SKILL.md"), "my own skill\n")
	seed(t, filepath.Join(v.config, instruction), own)
	switch {
	case v.hooks && v.host == "claude":
		seed(t, filepath.Join(v.config, "settings.json"), `{"env":{"MINE":"1"}}`+"\n")
	case v.hooks:
		seed(t, filepath.Join(v.config, "hooks.json"), `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"echo mine"}]}]}}`+"\n")
	}
	receipt := v.litJSON(cwd, v.a.endpoint, args...)["installed"].([]any)[0].(map[string]any)
	if receipt["agent"] != v.host || receipt["directory"] != v.config || receipt["hooks"] != v.hooks {
		t.Fatalf("setup receipt %v", receipt)
	}
	// The destination holds the embedded guidance byte for byte.
	embedded := 0
	err := fs.WalkDir(workflowskills.Bundle(), "skills", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		want, err := fs.ReadFile(workflowskills.Bundle(), name)
		if err != nil {
			return err
		}
		embedded++
		if got := read(t, filepath.Join(v.config, filepath.FromSlash(name))); got != string(want) {
			t.Errorf("delivered %s differs from the embedded copy", name)
		}
		return nil
	})
	if err != nil || embedded == 0 {
		t.Fatalf("walking the embedded skills: %d files, %v", embedded, err)
	}
	// What the user wrote survives, and the managed block is added after it.
	if read(t, filepath.Join(v.config, "skills", "mine", "SKILL.md")) != "my own skill\n" {
		t.Error("setup changed the user's own skill")
	}
	if text := read(t, filepath.Join(v.config, instruction)); !strings.HasPrefix(text, own) || !strings.Contains(text, "<!-- BEGIN lit managed -->") {
		t.Errorf("%s after setup: %q", instruction, text)
	}
	if v.hooks && v.host == "codex" && !strings.Contains(read(t, filepath.Join(v.config, "hooks.json")), "echo mine") {
		t.Error("setup dropped the user's own Codex hook")
	}
	if v.hooks && v.host == "claude" && !strings.Contains(read(t, filepath.Join(v.config, "settings.json")), `"MINE"`) {
		t.Error("setup dropped the user's own Claude settings")
	}
	// Hooks only where an agent configuration was named; a repository never gets them.
	if _, err := os.Stat(filepath.Join(v.config, "hooks.json")); (err == nil) != (v.hooks && v.host == "codex") {
		t.Errorf("hooks.json present=%v with hooks=%v", err == nil, v.hooks)
	}
	// The guidance the agent reads names the context the hook injects.
	text := v.delivered("context.md")
	for _, want := range []string{"`remembered project:`", "`lit session not set up`", "project_selection"} {
		if !strings.Contains(text, want) {
			t.Errorf("delivered context guidance lacks %q", want)
		}
	}
}

// uninstall removes what setup installed and checks the user's content is intact.
func (v *variant) uninstall() {
	t := v.t
	t.Helper()
	args := []string{"setup-workflows", "--remove", "--agent", v.host, "--config-dir", v.config}
	cwd := v.dir
	if !v.hooks {
		cwd, args = v.main, []string{"setup-workflows", "--remove", "--scope", "local", "--agent", v.host}
	}
	if receipt := v.litJSON(cwd, v.a.endpoint, args...)["removed"].([]any)[0].(map[string]any); receipt["found"] != true {
		t.Fatalf("removal receipt %v", receipt)
	}
	if read(t, filepath.Join(v.config, "skills", "mine", "SKILL.md")) != "my own skill\n" {
		t.Error("removal changed the user's own skill")
	}
	if _, err := os.Stat(filepath.Join(v.config, "skills", "lit")); !os.IsNotExist(err) {
		t.Errorf("the lit skill is still installed after removal: %v", err)
	}
	instruction := "CLAUDE.md"
	if v.host == "codex" {
		instruction = "AGENTS.md"
	}
	if got := read(t, filepath.Join(v.config, instruction)); got != "mine\n" {
		t.Errorf("%s after removal: %q", instruction, got)
	}
	switch {
	case v.hooks && v.host == "claude":
		var settings map[string]any
		if err := json.Unmarshal([]byte(read(t, filepath.Join(v.config, "settings.json"))), &settings); err != nil || settings["env"] == nil || settings["statusLine"] != nil {
			t.Errorf("settings.json after removal: %v %v", settings, err)
		}
	case v.hooks:
		if text := read(t, filepath.Join(v.config, "hooks.json")); !strings.Contains(text, "echo mine") || strings.Contains(text, "lit hook") {
			t.Errorf("hooks.json after removal: %s", text)
		}
	}
}
