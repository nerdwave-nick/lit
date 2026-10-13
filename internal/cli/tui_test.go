package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeExecutable creates a file named lit-tui (lit-tui.exe on Windows) in dir.
func writeExecutable(t *testing.T, dir string, mode os.FileMode) string {
	t.Helper()
	name := "lit-tui"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil { // WriteFile keeps an existing file's mode
		t.Fatal(err)
	}
	return path
}

func TestFindTUIPrefersTheSiblingOfLitThenPATH(t *testing.T) {
	litDir, pathDir, empty := t.TempDir(), t.TempDir(), t.TempDir()
	lit := filepath.Join(litDir, "lit")
	onPath := writeExecutable(t, pathDir, 0o755)
	t.Setenv("PATH", pathDir)

	if got, err := findTUI(lit); err != nil || got != onPath {
		t.Fatalf("without a sibling: %q %v, want PATH entry %q", got, err, onPath)
	}
	if runtime.GOOS != "windows" {
		writeExecutable(t, litDir, 0o644) // not executable: ignored
		if got, err := findTUI(lit); err != nil || got != onPath {
			t.Fatalf("with a non-executable sibling: %q %v, want PATH entry %q", got, err, onPath)
		}
	}
	sibling := writeExecutable(t, litDir, 0o755)
	if got, err := findTUI(lit); err != nil || got != sibling {
		t.Fatalf("with a sibling: %q %v, want %q", got, err, sibling)
	}

	t.Setenv("PATH", empty)
	_, err := findTUI(filepath.Join(empty, "lit"))
	if err == nil {
		t.Fatal("missing lit-tui was found")
	}
	for _, want := range []string{"lit-tui", empty, "PATH", "go install github.com/nerdwave-nick/lit/cmd/lit-tui@latest"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing-binary error lacks %q: %v", want, err)
		}
	}
}

// The tui command accepts only the endpoint; argument errors stay offline
// invalid_arguments errors and never look for or start lit-tui.
func TestTUIArgumentErrors(t *testing.T) {
	t.Setenv("LIT_STATE_DIR", "/dev/null/no-state")
	t.Setenv("PATH", t.TempDir())
	for _, tc := range []struct {
		argv []string
		msg  string
	}{
		{[]string{"tui", "extra"}, `unexpected argument "extra" for lit tui`},
		{[]string{"tui", "--session", "s"}, "--session does not apply to lit tui"},
		{[]string{"tui", "--format", "json"}, "--format does not apply to lit tui"},
		{[]string{"tui", "--timeout", "5s"}, "--timeout does not apply to lit tui"},
		{[]string{"tui", "--endpoint"}, "missing value for --endpoint"},
	} {
		var out, errOut bytes.Buffer
		code := Run(tc.argv, &out, &errOut)
		var envelope struct {
			Error struct{ Code, Message string }
		}
		json.Unmarshal(bytes.TrimSpace(append(out.Bytes(), errOut.Bytes()...)), &envelope)
		if code != 2 || envelope.Error.Code != "invalid_arguments" || envelope.Error.Message != tc.msg {
			t.Errorf("%v: code %d, %s%s; want %q", tc.argv, code, &out, &errOut, tc.msg)
		}
	}
}

// A missing lit-tui is a clear tui_not_found error naming the binary and how
// to install it.
func TestTUIMissingBinary(t *testing.T) {
	t.Setenv("LIT_STATE_DIR", "/dev/null/no-state")
	t.Setenv("PATH", t.TempDir())
	if exe, err := os.Executable(); err == nil {
		if _, err := os.Stat(filepath.Join(filepath.Dir(exe), "lit-tui")); err == nil {
			t.Skip("a lit-tui sits next to the test binary")
		}
	}
	var out, errOut bytes.Buffer
	code := Run([]string{"tui", "--endpoint", "http://127.0.0.1:1"}, &out, &errOut)
	var envelope struct {
		Error struct{ Code, Message string }
	}
	if err := json.Unmarshal(errOut.Bytes(), &envelope); err != nil {
		t.Fatalf("%v: %s", err, &errOut)
	}
	if code != 1 || out.Len() != 0 || envelope.Error.Code != "tui_not_found" || !strings.Contains(envelope.Error.Message, "go install github.com/nerdwave-nick/lit/cmd/lit-tui@latest") {
		t.Fatalf("code %d, out %q, err %s", code, &out, &errOut)
	}
}
