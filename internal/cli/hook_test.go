package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/hook"
)

func TestHookCommandsComeFromTheEventRegistry(t *testing.T) {
	events := hook.Events()
	if len(events) == 0 {
		t.Fatal("no hook events registered")
	}
	var out, errOut bytes.Buffer
	if code := Run([]string{"hook", "--help"}, &out, &errOut); code != 0 || errOut.Len() != 0 {
		t.Fatalf("%d %s", code, &errOut)
	}
	for _, e := range events {
		if grammar.Find("hook", e.Name) == nil {
			t.Errorf("event %s is not a lit hook command", e.Name)
		}
		if !strings.Contains(out.String(), e.Name+" ") || !strings.Contains(out.String(), e.Summary) {
			t.Errorf("hook help omits %s:\n%s", e.Name, &out)
		}
	}
}

func TestHookCommandRequiresAHostAndKnowsNoSessionFlags(t *testing.T) {
	offlineCompletion(t)
	for _, args := range [][]string{
		{"hook", "session-start"},
		{"hook", "--host", "gemini", "session-start"},
		{"hook", "--host", "claude", "session-start", "--session", "x"},
		{"hook", "--host", "claude", "session-start", "--endpoint", "http://127.0.0.1:1"},
	} {
		var out, errOut bytes.Buffer
		if code := Run(args, &out, &errOut); code != 2 || out.Len() != 0 || errOut.Len() == 0 {
			t.Errorf("%v: %d out=%q err=%q", args, code, &out, &errOut)
		}
	}
}

func TestHookCompletion(t *testing.T) {
	offlineCompletion(t)
	values, _ := completion(t, "hook", "")
	if !strings.Contains(strings.Join(values, " "), "session-start") {
		t.Errorf("hook events are not completed: %v", values)
	}
	values, _ = completion(t, "hook", "--host", "")
	if strings.Join(values, " ") != "codex claude" && strings.Join(values, " ") != "claude codex" {
		t.Errorf("hosts are not completed: %v", values)
	}
}
