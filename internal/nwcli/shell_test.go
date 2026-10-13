package nwcli

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/nwcli/shelltest"
)

const awkward = `it's "quoted" $HOME; (x)`

// shellApp is app with the value kinds the shell scripts must handle.
func shellApp() *Command {
	root := app()
	create := root.Find("items", "create")
	create.Flags = append(create.Flags,
		Flag{Name: "dir", Usage: "Work `DIR`", Value: Value{Kind: Dir}},
		Flag{Name: "when", Usage: "Lease `DURATION`", Value: Value{Kind: Dynamic, Completer: "durations"}})
	return root
}

var shellCompleters = map[string]Completer{
	"items": func(ctx Context) ([]Candidate, Directive) {
		var out []Candidate
		for _, v := range []string{awkward, "item two", "feat/api:Fix it"} {
			if strings.HasPrefix(v, ctx.Prefix) {
				out = append(out, Candidate{v, "Record " + v})
			}
		}
		return out, NoFiles
	},
	"durations": func(Context) ([]Candidate, Directive) {
		return []Candidate{{Value: "15m"}, {Value: "30m"}, {Value: "45m"}, {Value: "1h"}}, NoFiles | KeepOrder
	},
}

// TestShellProgram is the program the shell tests complete: this test binary,
// run again with NWCLI_SHELL_PROGRAM=1.
func TestShellProgram(t *testing.T) {
	if os.Getenv("NWCLI_SHELL_PROGRAM") != "1" {
		return
	}
	args := os.Args[slices.Index(os.Args, "--")+1:]
	if len(args) > 0 && args[0] == "__complete" {
		cands, d := shellApp().Complete(args[1:], shellCompleters)
		WriteCompletion(os.Stdout, cands, d, true)
	}
	os.Exit(0)
}

func completeIn(t *testing.T, shell, line string) shelltest.Result {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var script strings.Builder
	if err := WriteScript(&script, shell, "app", "__complete"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "subdir"), 0o700)
	os.WriteFile(filepath.Join(dir, "file.txt"), nil, 0o600)
	return shelltest.Complete(t, shell, script.String(), shelltest.Program{
		Name: "app", Path: executable, Args: []string{"-test.run=^TestShellProgram$", "--"},
		Env: []string{"NWCLI_SHELL_PROGRAM=1"}, Dir: dir,
	}, line)
}

// unquote returns what bash passes for a COMPREPLY entry once inserted after prefix.
func unquote(t *testing.T, prefix, entry string) string {
	t.Helper()
	out, err := exec.Command("bash", "--norc", "-c", "printf %s "+prefix+entry).Output()
	if err != nil {
		t.Fatalf("bash rejects %q: %v", prefix+entry, err)
	}
	return string(out)
}

func TestShellScripts(t *testing.T) {
	for _, shell := range Shells {
		t.Run(shell, func(t *testing.T) {
			t.Parallel()
			r := completeIn(t, shell, "app it")
			if !slices.Equal(r.Values, []string{"items"}) {
				t.Errorf("subcommands: %+v", r)
			}

			r = completeIn(t, shell, "app items create ")
			if !slices.Contains(r.Values, "--item") || !slices.Contains(r.Values, "--session") {
				t.Errorf("bare-Tab flags: %+v", r)
			}
			if shell != "bash" && !slices.ContainsFunc(r.Descriptions, func(d string) bool { return strings.Contains(d, "Item `TITLE`") }) {
				t.Errorf("flag descriptions: %+v", r.Descriptions)
			}

			r = completeIn(t, shell, "app items get ")
			got := r.Values
			if shell == "bash" {
				got = nil
				for _, entry := range r.Values {
					got = append(got, unquote(t, "", entry))
				}
			}
			if !slices.Contains(got, awkward) || !slices.Contains(got, "item two") {
				t.Errorf("quoted record values: %q", got)
			}

			for _, line := range []string{"app items get feat/api:F", "app items get feat/api:"} {
				r = completeIn(t, shell, line)
				if shell == "bash" && len(r.Values) == 1 {
					r.Values = []string{unquote(t, "feat/api:", r.Values[0])} // readline replaces the text after ':'
				}
				if !slices.Equal(r.Values, []string{"feat/api:Fix it"}) {
					t.Errorf("%q: value with a colon: %q", line, r.Values)
				}
			}

			r = completeIn(t, shell, "app items create --dir ")
			if !slices.ContainsFunc(r.Values, func(v string) bool { return strings.HasPrefix(v, "subdir") }) || slices.ContainsFunc(r.Values, func(v string) bool { return strings.HasPrefix(v, "file") }) {
				t.Errorf("directories only: %+v", r)
			}

			r = completeIn(t, shell, "app search ")
			if len(r.Values) != 0 || slices.Contains(r.Options, "default") || slices.Contains(r.Options, "files") {
				t.Errorf("file names offered for a free-form operand: %+v", r)
			}

			r = completeIn(t, shell, "app items create --when ")
			ordered := map[string]string{"bash": "nosort", "zsh": "-V"}[shell]
			if !slices.Equal(r.Values, []string{"15m", "30m", "45m", "1h"}) || ordered != "" && !slices.Contains(r.Options, ordered) {
				t.Errorf("keep order: %+v", r)
			}
		})
	}
}
