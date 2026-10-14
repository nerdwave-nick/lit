package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestHookSubagentStartHelpDescribesItsRuntimeAndEndpoint(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"hook", "subagent-start", "--help"}, &out, &errOut); code != 0 || errOut.Len() != 0 {
		t.Fatalf("%d %s", code, &errOut)
	}
	for _, want := range []string{"SubagentStart", "agent_id", "session_id", "parent", "LIT_ENDPOINT is not read", "lit hook --host codex|claude subagent-start"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help lacks %q:\n%s", want, &out)
		}
	}
	if strings.Contains(out.String(), "runtime ID is the hook input's session_id") {
		t.Errorf("help describes session-start's identity:\n%s", &out)
	}
}

func TestHookCompletesSubagentStart(t *testing.T) {
	offlineCompletion(t)
	values, _ := completion(t, "hook", "")
	if !strings.Contains(strings.Join(values, " "), "subagent-start") {
		t.Errorf("the subagent-start event is not completed: %v", values)
	}
}
