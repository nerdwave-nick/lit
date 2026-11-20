package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The help of workflow-session fresh and subagent tells a skill how the
// project is chosen, what the JSON says about it and how to recover from a
// remembered project that cannot be adopted.
func TestWorkflowSessionHelpDescribesRememberedProjectAdoption(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("LIT_STATE_DIR", t.TempDir())
	t.Setenv("LIT_SESSION", "")
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("help must not contact the service") }))
	defer server.Close()
	t.Setenv("LIT_ENDPOINT", server.URL)
	for _, tc := range []struct {
		path  string
		wants []string
	}{
		{"workflow-session fresh", []string{
			"remembered for the current checkout", "lit context set",
			"project_selection", "explicit", "remembered", "none",
			"saved_title", "checkout",
			"before any association or client is created", "lit context clear", "--project REF",
			"never falls back",
			"--endpoint, then LIT_ENDPOINT, then the endpoint saved with the remembered project",
			"same service", "leaves the remembered project untouched",
		}},
		{"workflow-session subagent", []string{"never adopts a remembered project", "explicit --project", "project_selection"}},
		{"workflow-session resume", []string{"project_selection", "kept as it was"}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			var out, err bytes.Buffer
			if code := Run(append(strings.Fields(tc.path), "--help"), &out, &err); code != 0 || err.Len() != 0 {
				t.Fatalf("%d %s", code, &err)
			}
			for _, want := range tc.wants {
				if !strings.Contains(out.String(), want) {
					t.Errorf("missing %q in\n%s", want, &out)
				}
			}
		})
	}
}

// --project says what it does in each command that has it.
func TestWorkflowSessionProjectFlagUsage(t *testing.T) {
	for _, tc := range []struct{ path, want string }{
		{"workflow-session fresh", "adopted"},
		{"workflow-session subagent", "never adopted"},
	} {
		var out, err bytes.Buffer
		if code := Run(append(strings.Fields(tc.path), "--help"), &out, &err); code != 0 {
			t.Fatalf("%d %s", code, &err)
		}
		var line string
		for _, l := range strings.Split(out.String(), "\n") {
			if strings.Contains(l, "--project REF") && strings.Contains(l, "Optional.") {
				line = l
			}
		}
		if !strings.Contains(line, tc.want) {
			t.Errorf("%s --project usage %q lacks %q", tc.path, line, tc.want)
		}
	}
}
