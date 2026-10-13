package workflowsession

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyKeys(t *testing.T) {
	for _, tc := range []struct {
		parts   []string
		encoded string
	}{
		{[]string{"codex", "main"}, `["codex", "main"]`},
		{[]string{"claude", "é😀<&\n"}, `["claude", "\u00e9\ud83d\ude00<&\n"]`},
	} {
		want := fmt.Sprintf("%x", sha256.Sum256([]byte(tc.encoded)))
		if got := key(tc.parts...); got != want {
			t.Fatalf("legacy hash %s != %s", got, want)
		}
	}
}
func TestOfflineValidation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LIT_WORKFLOW_STATE_DIR", filepath.Join(root, "absent"))
	for _, args := range [][]string{{"--help"}, {"--host", "codex", "--runtime-id", "x", "fresh", "--endpoint", "not-an-origin"}, {"--host", "invalid", "--runtime-id", "x", "resume"}, {"--host", "codex", "--runtime-id", " ", "resume"}} {
		var out, err bytes.Buffer
		code := Run(args, &out, &err)
		if args[0] == "--help" {
			if code != 0 {
				t.Fatal(err.String())
			}
		} else if code == 0 {
			t.Fatal("accepted", args)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "absent")); !os.IsNotExist(err) {
		t.Fatal("invalid/help created state")
	}
}

func TestMain(m *testing.M) {
	if expected := os.Getenv("LIT_ENDPOINT_SUBPROCESS_FIXTURE"); expected != "" {
		if len(os.Args) < 5 || os.Args[3] != "--endpoint" || os.Args[4] != expected || os.Getenv("LIT_ENDPOINT") != "" {
			fmt.Fprintln(os.Stderr, "unexpected endpoint arguments", os.Args)
			os.Exit(9)
		}
		fmt.Fprintln(os.Stdout, `{"items":[{"client_id":"11111111-1111-4111-8111-111111111111","service_id":"22222222-2222-4222-8222-222222222222","project_id":null}]}`)
		os.Exit(0)
	}

	if os.Getenv("LIT_SESSION_SUBPROCESS_FIXTURE") == "1" {
		if os.Getenv("LIT_SESSION") != "" || os.Getenv("LIT_ENDPOINT") != "" {
			fmt.Fprintln(os.Stderr, "inherited identity leaked")
			os.Exit(9)
		}
		fmt.Fprintln(os.Stdout, "controlled stdout")
		fmt.Fprintln(os.Stderr, "controlled stderr")
		os.Exit(7)
	}
	os.Exit(m.Run())
}
func TestSubprocessIsolationAndExit(t *testing.T) {
	t.Setenv("LIT_SESSION_SUBPROCESS_FIXTURE", "1")
	t.Setenv("LIT_SESSION", "parent")
	t.Setenv("LIT_ENDPOINT", "http://wrong")
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	out, stderr, code, e := invoke(executable, record{Session: "agent-explicit", Endpoint: "http://explicit"}, []string{"session", "get"})
	if e != nil || code != 7 || string(out) != "controlled stdout\n" || string(stderr) != "controlled stderr\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q error=%v", code, out, stderr, e)
	}
}

func TestFreshEndpointSelectionAndResumeBinding(t *testing.T) {
	for _, tc := range []struct{ name, environment, explicit, want string }{
		{name: "default", want: "http://127.0.0.1:7411"},
		{name: "environment", environment: "https://env.example", want: "https://env.example"},
		{name: "explicit", environment: "https://env.example", explicit: "https://explicit.example/", want: "https://explicit.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LIT_WORKFLOW_STATE_DIR", t.TempDir())
			t.Setenv("LIT_ENDPOINT", tc.environment)
			t.Setenv("LIT_ENDPOINT_SUBPROCESS_FIXTURE", tc.want)
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			call := func(id, action string, extra ...string) {
				t.Helper()
				args := []string{"--host", "codex", "--runtime-id", id, "--cli", executable, action}
				args = append(args, extra...)
				var out, stderr bytes.Buffer
				if code := Run(args, &out, &stderr); code != 0 {
					t.Fatalf("%v: %d %s", args, code, &stderr)
				}
				if !strings.Contains(out.String(), tc.want) {
					t.Fatalf("missing selected endpoint: %s", &out)
				}
			}
			var flags []string
			if tc.explicit != "" {
				flags = []string{"--endpoint", tc.explicit}
			}
			call("parent", "fresh", flags...)
			childFlags := append([]string{"--parent-runtime-id", "parent"}, flags...)
			call("child", "subagent", childFlags...)
			t.Setenv("LIT_ENDPOINT", "https://changed.example")
			call("parent", "resume")
			call("child", "resume")
		})
	}
}

func TestOverrideGuardAllowsLiteralContentAndPositionals(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("LIT_WORKFLOW_STATE_DIR", t.TempDir())
	for _, command := range [][]string{
		{"issues", "create", "--issue", "Test", "--content=--session"},
		{"issues", "create", "--issue", "Test", "--content=--endpoint"},
		{"issues", "create", "--issue", "Test", "--content=--format"},
		{"grep", "--", "--session"},
	} {
		args := append([]string{"--host", "codex", "--runtime-id", "missing", "run", "--"}, command...)
		var out, stderr bytes.Buffer
		code := Run(args, &out, &stderr)
		if code == 0 || strings.Contains(stderr.String(), "cannot override") {
			t.Fatalf("%v: %d %s", command, code, &stderr)
		}
		if !strings.Contains(stderr.String(), "no such file") {
			t.Fatalf("expected missing session evidence instead of guard rejection: %s", &stderr)
		}
	}
}
