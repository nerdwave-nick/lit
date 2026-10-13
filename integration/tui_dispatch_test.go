package integration

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeTUI installs a lit-tui stand-in that reports which copy ran, its process
// ID and its arguments.
func fakeTUI(t *testing.T, dir, tag string) {
	t.Helper()
	script := fmt.Sprintf("#!/bin/sh\necho \"%s pid=$$ args=$*\"\n", tag)
	if err := os.WriteFile(filepath.Join(dir, "lit-tui"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func copyExecutable(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestTUIDispatch covers how lit finds and starts lit-tui: next to lit first,
// then PATH, by process replacement, with the endpoint passed through; a
// missing binary is a clear error; bare lit dispatches only on a terminal.
func TestTUIDispatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix process replacement; Windows dispatch is checked separately")
	}
	root := t.TempDir()
	litDir, pathDir, emptyDir, home := filepath.Join(root, "lit dir"), filepath.Join(root, "path"), filepath.Join(root, "empty"), filepath.Join(root, "home")
	for _, d := range []string{litDir, pathDir, emptyDir, home} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	lit := filepath.Join(litDir, "lit")
	copyExecutable(t, cliBin, lit)
	baseEnv := []string{"HOME=" + home, "USERPROFILE=" + home, "LIT_STATE_DIR=" + filepath.Join(root, "state"), "LIT_WORKFLOW_STATE_DIR=" + filepath.Join(root, "workflow"), "GORACE=atexit_sleep_ms=0"}
	run := func(path string, args ...string) (code, pid int, stdout, stderr string) {
		t.Helper()
		cmd := exec.Command(lit, args...)
		cmd.Env = append([]string{"PATH=" + path}, baseEnv...)
		var out, errOut strings.Builder
		cmd.Stdout, cmd.Stderr = &out, &errOut
		err := cmd.Run()
		var exit *exec.ExitError
		if err != nil && !errors.As(err, &exit) {
			t.Fatalf("%v: %v", args, err)
		}
		return cmd.ProcessState.ExitCode(), cmd.Process.Pid, out.String(), errOut.String()
	}
	errorCode := func(stderr string) (string, string) {
		var envelope struct {
			Error struct{ Code, Message string }
		}
		if err := json.Unmarshal([]byte(stderr), &envelope); err != nil {
			t.Fatalf("error envelope: %v: %q", err, stderr)
		}
		return envelope.Error.Code, envelope.Error.Message
	}

	fakeTUI(t, pathDir, "path")
	t.Run("bare lit outside a terminal keeps the usage error", func(t *testing.T) {
		code, _, stdout, stderr := run(pathDir)
		if c, msg := errorCode(stderr); code != 2 || stdout != "" || c != "invalid_arguments" || msg != "a subcommand is required for lit; run 'lit --help'" {
			t.Fatalf("code %d, stdout %q, stderr %q", code, stdout, stderr)
		}
	})
	t.Run("PATH, by process replacement, endpoint passed through", func(t *testing.T) {
		code, pid, stdout, stderr := run(pathDir, "tui", "--endpoint", "http://127.0.0.1:1")
		if want := fmt.Sprintf("path pid=%d args=--endpoint http://127.0.0.1:1\n", pid); code != 0 || stdout != want || stderr != "" {
			t.Fatalf("code %d, stdout %q (want %q), stderr %q", code, stdout, want, stderr)
		}
	})
	fakeTUI(t, litDir, "sibling")
	t.Run("the sibling of lit before PATH", func(t *testing.T) {
		code, pid, stdout, _ := run(pathDir, "--endpoint=http://127.0.0.1:2", "tui")
		if want := fmt.Sprintf("sibling pid=%d args=--endpoint http://127.0.0.1:2\n", pid); code != 0 || stdout != want {
			t.Fatalf("code %d, stdout %q (want %q)", code, stdout, want)
		}
		if code, _, stdout, _ := run(pathDir, "tui"); code != 0 || !strings.HasPrefix(stdout, "sibling pid=") || !strings.HasSuffix(stdout, "args=\n") {
			t.Fatalf("without flags: code %d, stdout %q", code, stdout)
		}
	})
	t.Run("a missing lit-tui names the binary and how to install it", func(t *testing.T) {
		if err := os.Remove(filepath.Join(litDir, "lit-tui")); err != nil {
			t.Fatal(err)
		}
		code, _, stdout, stderr := run(emptyDir, "tui")
		c, msg := errorCode(stderr)
		if code != 1 || stdout != "" || c != "tui_not_found" {
			t.Fatalf("code %d, stdout %q, stderr %q", code, stdout, stderr)
		}
		for _, want := range []string{"lit-tui", litDir, "PATH", "go install github.com/nerdwave-nick/lit/cmd/lit-tui@latest"} {
			if !strings.Contains(msg, want) {
				t.Errorf("message lacks %q: %s", want, msg)
			}
		}
	})
	t.Run("bare lit on a terminal launches lit-tui", func(t *testing.T) {
		scriptBin, err := exec.LookPath("script")
		if err != nil || runtime.GOOS != "linux" {
			t.Skip("util-linux script provides the terminal on Linux")
		}
		fakeTUI(t, litDir, "sibling")
		onTerminal := func(args string) string {
			t.Helper()
			cmd := exec.Command(scriptBin, "-qec", "'"+lit+"' "+args, os.DevNull)
			cmd.Env = append([]string{"PATH=" + emptyDir + ":/usr/bin:/bin"}, baseEnv...)
			out, _ := cmd.CombinedOutput()
			return strings.ReplaceAll(string(out), "\r\n", "\n")
		}
		if out := onTerminal("--endpoint http://127.0.0.1:3"); !strings.Contains(out, "sibling pid=") || !strings.Contains(out, "args=--endpoint http://127.0.0.1:3\n") {
			t.Fatalf("bare lit on a terminal: %q", out)
		}
		// Arguments that lit tui does not accept keep the usage error; help stays help.
		if out := onTerminal("--session s"); strings.Contains(out, "sibling") || !strings.Contains(out, "a subcommand is required for lit") {
			t.Fatalf("bare lit --session on a terminal: %q", out)
		}
		if out := onTerminal("--help"); strings.Contains(out, "sibling") || !strings.Contains(out, "Usage:") {
			t.Fatalf("lit --help on a terminal: %q", out)
		}
	})
}
