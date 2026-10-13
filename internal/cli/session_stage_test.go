package cli

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/service"
	"github.com/nerdwave-nick/lit/internal/storage"
)

// stageHarness runs lit commands against a temporary service and state.
type stageHarness struct {
	t *testing.T
}

func newStageHarness(t *testing.T) stageHarness {
	t.Helper()
	t.Chdir(t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("LIT_STATE_DIR", t.TempDir())
	t.Setenv("LIT_SESSION", "")
	data, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { data.Close() })
	handler, err := service.New(data, service.Config{Limits: protocol.DefaultLimits(), TitlePrefixes: []string{"test"}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	t.Setenv("LIT_ENDPOINT", server.URL)
	return stageHarness{t}
}

// run executes lit and returns its exit code, stdout and stderr.
func (h stageHarness) run(args ...string) (int, string, string) {
	h.t.Helper()
	var out, stderr bytes.Buffer
	code := Run(args, &out, &stderr)
	return code, out.String(), stderr.String()
}

// item runs a command that must succeed in JSON and returns its one item.
func (h stageHarness) item(args ...string) map[string]any {
	h.t.Helper()
	code, out, stderr := h.run(append(args, "--format", "json")...)
	if code != 0 {
		h.t.Fatalf("%v: %d %s", args, code, stderr)
	}
	var result struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil || len(result.Items) != 1 {
		h.t.Fatalf("%v: %s %v", args, out, err)
	}
	return result.Items[0]
}

// The workflow stage is set, read in every output format, and cleared through
// the session commands.
func TestSessionStageSetGetUnset(t *testing.T) {
	h := newStageHarness(t)
	if code, _, stderr := h.run("connect", "--session", "stage", "--session-id-only"); code != 0 {
		t.Fatal(stderr)
	}
	item := func(args ...string) map[string]any { return h.item(append([]string{"--session", "stage"}, args...)...) }

	if got, present := item("session", "get")["stage"]; !present || got != nil {
		t.Fatalf("a new session shows stage %v (present %v), want null", got, present)
	}
	if got := item("session", "set", "--stage", "impl"); got["stage"] != "impl" {
		t.Fatalf("set: %v", got)
	}
	if got := item("session", "get"); got["stage"] != "impl" {
		t.Fatalf("get: %v", got)
	}
	if got := item("session", "get", "--stage"); len(got) != 1 || got["stage"] != "impl" {
		t.Fatalf("get --stage selects only the stage: %v", got)
	}
	if got := item("session", "get", "--project", "--stage"); len(got) != 2 || got["stage"] != "impl" {
		t.Fatalf("get --project --stage: %v", got)
	}
	for format, want := range map[string]string{"cli": "Stage: impl", "markdown": "| Stage | impl |"} {
		code, out, stderr := h.run("--session", "stage", "session", "get", "--format", format)
		if code != 0 || !strings.Contains(out, want) {
			t.Errorf("%s: %d %q %s", format, code, out, stderr)
		}
	}
	// Overwriting replaces the stage; other properties are left alone.
	if got := item("session", "set", "--stage", "to-spec", "--output-format", "json"); got["stage"] != "to-spec" || got["output_format"] != "json" {
		t.Fatalf("overwrite: %v", got)
	}
	if got := item("session", "set", "--output-format", "cli"); got["stage"] != "to-spec" {
		t.Fatalf("setting another property changed the stage: %v", got)
	}
	// Any well-formed label is stored; membership in the standard list is not checked.
	if got := item("session", "set", "--stage", "ship"); got["stage"] != "ship" {
		t.Fatalf("custom stage: %v", got)
	}
	// Malformed labels are refused by the service and change nothing.
	for _, bad := range []string{"Not Valid", "UPPER", "to_spec", "", strings.Repeat("a", 33)} {
		code, out, stderr := h.run("--session", "stage", "session", "set", "--stage", bad, "--format", "json")
		if code != 2 || out != "" || !strings.Contains(stderr, "validation_failed") {
			t.Errorf("--stage %q: %d %q %s", bad, code, out, stderr)
		}
	}
	if got := item("session", "get"); got["stage"] != "ship" {
		t.Fatalf("a refused stage changed it: %v", got)
	}
	// Unset clears the stage and only the stage.
	if got := item("session", "unset", "--stage"); got["stage"] != nil {
		t.Fatalf("unset: %v", got)
	}
	if got, present := item("session", "get")["stage"]; !present || got != nil {
		t.Fatalf("after unset: %v (present %v)", got, present)
	}
	// Clearing a cleared stage is harmless.
	if got := item("session", "unset", "--stage"); got["stage"] != nil {
		t.Fatalf("unset again: %v", got)
	}
	// A value is required to set, and none is accepted to clear.
	for _, args := range [][]string{{"session", "set", "--stage"}, {"session", "unset", "--stage", "impl"}, {"session", "set"}} {
		if code, out, _ := h.run(append([]string{"--session", "stage"}, args...)...); code == 0 || out != "" {
			t.Errorf("%v accepted: %d %q", args, code, out)
		}
	}
}

// Two sessions keep their own stages, and a stage lives on the service: a
// session opened again later (as workflow-session resume does) still has it.
func TestSessionStageIsPerSessionAndSurvivesReconnect(t *testing.T) {
	h := newStageHarness(t)
	for _, name := range []string{"a", "b"} {
		if code, _, stderr := h.run("connect", "--session", name, "--session-id-only"); code != 0 {
			t.Fatal(stderr)
		}
	}
	h.item("--session", "a", "session", "set", "--stage", "orchestrate")
	if got := h.item("--session", "b", "session", "get"); got["stage"] != nil {
		t.Fatalf("another session shows %v", got["stage"])
	}
	client := h.item("--session", "a", "session", "get")["client_id"].(string)
	// connect --client-id is how workflow-session resumes a client.
	if got := h.item("--session", "a", "connect", "--client-id", client); got["stage"] != "orchestrate" {
		t.Fatalf("resume: %v", got)
	}
	if got := h.item("--session", "a", "session", "get"); got["stage"] != "orchestrate" {
		t.Fatalf("after resume: %v", got)
	}
}
