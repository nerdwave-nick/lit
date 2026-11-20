package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestHookStatuslineTakesTheOriginalCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"hook", "statusline", "--help"}, &out, &errOut); code != 0 || errOut.Len() != 0 {
		t.Fatalf("%d %s", code, &errOut)
	}
	for _, want := range []string{
		"lit hook --host claude statusline [--original COMMAND] < status-line-input.json",
		"--original COMMAND",
		"Required flag: --host claude",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the statusline help lacks %q:\n%s", want, &out)
		}
	}
}

func TestOnlyTheStatuslineTakesTheOriginalCommand(t *testing.T) {
	offlineCompletion(t)
	for _, args := range [][]string{
		{"hook", "--host", "claude", "session-start", "--original", "x"},
		{"hook", "--host", "claude", "statusline", "--original"},
		{"hook", "--host", "claude", "statusline", "--original", "a", "--original", "b"},
		{"hook", "--host", "claude", "statusline", "extra"},
	} {
		var out, errOut bytes.Buffer
		if code := Run(args, &out, &errOut); code != 2 || out.Len() != 0 || errOut.Len() == 0 {
			t.Errorf("%v: %d out=%q err=%q", args, code, &out, &errOut)
		}
	}
	values, _ := completion(t, "hook", "--host", "claude", "statusline", "--")
	if !strings.Contains(strings.Join(values, " "), "--original") {
		t.Errorf("--original is not completed: %v", values)
	}
}
