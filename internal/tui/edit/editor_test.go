package edit

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestEditorPrefersVisualThenEditorThenThePlatformDefault(t *testing.T) {
	none := func(string) bool { return false }
	cases := []struct {
		env    map[string]string
		goos   string
		want   []string
		source string
	}{
		{map[string]string{"VISUAL": "nvim --clean", "EDITOR": "vim"}, "linux", []string{"nvim", "--clean"}, "VISUAL"},
		{map[string]string{"VISUAL": "  ", "EDITOR": "vim -u NONE"}, "linux", []string{"vim", "-u", "NONE"}, "EDITOR"},
		{map[string]string{}, "linux", []string{"vi"}, "default"},
		{map[string]string{}, "darwin", []string{"vi"}, "default"},
		{map[string]string{}, "windows", []string{"notepad"}, "default"},
	}
	for _, c := range cases {
		argv, source, err := Lookup(func(k string) string { return c.env[k] }, c.goos, none)
		if err != nil || !slices.Equal(argv, c.want) || source != c.source {
			t.Errorf("%v on %s: %q from %s, %v; want %q from %s", c.env, c.goos, argv, source, err, c.want, c.source)
		}
	}
	if _, _, err := Lookup(func(k string) string { return map[string]string{"EDITOR": `vim "unterminated`}[k] }, "linux", none); err == nil ||
		!strings.Contains(err.Error(), "EDITOR") {
		t.Errorf("an unparsable EDITOR is not reported: %v", err)
	}
}

// An editor path with spaces works quoted, escaped, or unquoted when the
// whole value is an existing file.
func TestEditorPathsWithSpaces(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my editors")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "fancy edit")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	argv, _, err := Lookup(func(k string) string { return map[string]string{"EDITOR": exe}[k] }, "linux", IsFile)
	if err != nil || !slices.Equal(argv, []string{exe}) {
		t.Fatalf("unquoted existing path: %q %v", argv, err)
	}
	argv, _, err = Lookup(func(k string) string { return map[string]string{"VISUAL": `"` + exe + `" -n`}[k] }, "linux", IsFile)
	if err != nil || !slices.Equal(argv, []string{exe, "-n"}) {
		t.Fatalf("quoted path: %q %v", argv, err)
	}
}

func TestSplitParsesWordsAndQuotesWithoutAShell(t *testing.T) {
	cases := []struct {
		in, goos string
		want     []string
	}{
		{"nvim", "linux", []string{"nvim"}},
		{"  code   --wait\t-n ", "linux", []string{"code", "--wait", "-n"}},
		{`'/opt/My Editor/bin/ed' -c 'set ft=markdown'`, "linux", []string{"/opt/My Editor/bin/ed", "-c", "set ft=markdown"}},
		{`"/opt/My Editor/ed" "say \"hi\" \$HOME \x"`, "linux", []string{"/opt/My Editor/ed", `say "hi" $HOME \x`}},
		{`/opt/My\ Editor/ed -c\ x`, "linux", []string{"/opt/My Editor/ed", "-c x"}},
		{`ed ''`, "linux", []string{"ed", ""}},
		{`ed a"b c"d`, "linux", []string{"ed", "ab cd"}},
		{`vim $HOME; rm -rf / | cat`, "linux", []string{"vim", "$HOME;", "rm", "-rf", "/", "|", "cat"}},
		{`C:\Tools\vim.exe -n`, "windows", []string{`C:\Tools\vim.exe`, "-n"}},
		{`"C:\Program Files\Notepad++\notepad++.exe" -multiInst`, "windows", []string{`C:\Program Files\Notepad++\notepad++.exe`, "-multiInst"}},
	}
	for _, c := range cases {
		got, err := Split(c.in, c.goos)
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("Split(%q, %s) = %q, %v; want %q", c.in, c.goos, got, err, c.want)
		}
	}
	for _, bad := range []string{`vim "open`, `vim 'open`, `vim \`, "", "   "} {
		if got, err := Split(bad, "linux"); err == nil {
			t.Errorf("Split(%q) = %q, want an error", bad, got)
		}
	}
}

// The editor runs from its arguments with the draft path appended as one
// argument, spaces included, and no shell interprets either.
func TestCommandRunsWithoutAShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX script as the editor")
	}
	dir := filepath.Join(t.TempDir(), "with space")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "args")
	script := filepath.Join(dir, "fake editor")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done > \""+out+"\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	draft := filepath.Join(dir, "my draft--9fd871a2.md")
	argv, err := Split(`'`+script+`' --flag '$(touch pwned)'`, "linux")
	if err != nil {
		t.Fatal(err)
	}
	cmd := Command(argv, draft)
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out)
	if got := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n"); !slices.Equal(got, []string{"--flag", "$(touch pwned)", draft}) {
		t.Fatalf("editor arguments %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil {
		t.Fatal("a shell ran the command")
	}
	if _, err := exec.LookPath(argv[0]); err != nil {
		t.Fatal(err)
	}
}
