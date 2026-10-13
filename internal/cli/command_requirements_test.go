package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/nwcli"
)

func TestCommandRequirementsHelp(t *testing.T) {
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
		{"connect", []string{"Required flags: none", "random throwaway session name", "Valid forms:", "lit connect\n", "--client-id UUID", "system username", "--output-format"}},
		{"projects history", []string{"history [REF]", "pagination flags are ignored"}},
		{"projects get", []string{"get [REF...]", "session selection"}},
		{"projects update", []string{"--project-id REF", "positional targets", "--file PATH", "--content and --content-file"}},
		{"comments update", []string{"REF... [CHANGE_FLAGS]", "Comment references are UUIDs"}},
		{"issues create", []string{"--issue TITLE", "--file PATH", "project selection", "Conditional (see valid forms)"}},
		{"issues link", []string{"--from REF --to REF --relation KIND", "--file PATH"}},
		{"issues list", []string{"--all-projects", "--file PATH", "cannot combine --file", "--all conflicts"}},
		{"comments list", []string{"--issue REF", "Required: an owning issue"}},
		{"issues history", []string{"--request-hash HASH", "conflicts with --limit, --cursor and --all"}},
		{"claims renew", []string{"--all", "--for DURATION | --until RFC3339", "30m", "--project"}},
		{"session unset", []string{"at least one", "cannot be unset", "--runtime-vendor"}},
		{"transactions status", []string{"Required flags: none", "LIT_SESSION does not filter", "retained request"}},
		{"setup-skills", []string{"Required flags: --scope and --agent", "Required with --scope custom", "--scope local|user", "--scope custom --path DIRECTORY"}},
		{"workflow-session subagent", []string{"Required flags: --host, --runtime-id and --parent-runtime-id", "distinct"}},
		{"workflow-session checkout", []string{"Required flags: --host, --runtime-id, --repository and --path", "selected project"}},
		{"grep", []string{"Required: one nonempty PATTERN", "--project REF | --all-projects"}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			var baseline string
			for _, suffix := range []string{"--help", "-h", "help"} {
				var out, err bytes.Buffer
				args := append(strings.Fields(tc.path), suffix)
				if suffix == "help" && (tc.path == "grep" || strings.HasSuffix(tc.path, " run")) {
					args = append([]string{"help"}, strings.Fields(tc.path)...)
				}
				if code := Run(args, &out, &err); code != 0 || err.Len() != 0 {
					t.Fatalf("%v: %d %s", args, code, &err)
				}
				for _, want := range tc.wants {
					if !strings.Contains(out.String(), want) {
						t.Errorf("missing %q in\n%s", want, &out)
					}
				}
				if baseline != "" && baseline != out.String() {
					t.Errorf("help alias %s differs", suffix)
				}
				baseline = out.String()
			}
		})
	}
}

func TestEveryApplicationLeafHasRequirementsAndForms(t *testing.T) {
	grammar.Walk(func(path []string, c *nwcli.Command) {
		if len(c.Commands) > 0 || path[0] == "completion" {
			return
		}
		for _, want := range []string{"Requirements:", "Valid forms:"} {
			if !strings.Contains(c.Description, want) {
				t.Errorf("lit %s lacks %s", strings.Join(path, " "), want)
			}
		}
	})
}

// Validate representative advertised alternatives with the actual domain parser
// and input builders, without a service, installed config, or persistent state.
func TestAdvertisedInputAlternatives(t *testing.T) {
	for _, args := range [][]string{
		{"projects", "create", "--project-title", "test/one", "--project-title", "test/two", "--content", "Two"},
		{"projects", "update", "test/one", "--revision", "1"},
		{"projects", "update", "--project-id", "test/one"},
		{"issues", "create", "--project", "test/poc", "--issue", "One", "--issue", "Two"},
		{"issues", "update", "One", "Two"},
		{"issues", "close", "--issue", "One", "--issue", "Two"},
		{"issues", "link", "--from", "One", "--to", "Two", "--relation", "blocks", "--to", "Three"},
		{"comments", "create", "--issue", "One"},
		{"comments", "update", "01234567-89ab-4cde-8fab-0123456789ab"},
		{"claims", "renew", "--all", "--for", "30m"},
		{"claims", "release", "One", "Two", "--project", "test/poc"},
		{"session", "set", "--project", "test/poc", "--output-format", "cli"},
		{"session", "unset", "--runtime-vendor", "--runtime-session-id"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			parsed, err := Parse(args)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateArgs(parsed); err != nil {
				t.Fatal(err)
			}
			app := App{Args: parsed}
			switch parsed.Command {
			case "projects":
				_, err = app.projectInputs()
			case "issues", "comments":
				_, err = app.recordInputs()
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReferenceGrammarHelp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("LIT_STATE_DIR", t.TempDir())
	t.Setenv("LIT_SESSION", "")
	t.Chdir(t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("reference help must not contact the service") }))
	defer server.Close()
	t.Setenv("LIT_ENDPOINT", server.URL)
	for _, tc := range []struct {
		path   string
		wants  []string
		absent string
	}{
		{"projects get", []string{"title:TITLE", "id:UUID_PREFIX", "unique UUID prefix"}, "PROJECT_TITLE:ISSUE_REF"},
		{"issues get", []string{"title:TITLE", "id:UUID_PREFIX", "PROJECT_TITLE:ISSUE_REF", "Refactor a/b: cleanup", "unqualified UUID"}, ""},
		{"issues create", []string{"existing issue references", "new --issue title"}, ""},
		{"comments get", []string{"id:UUID_PREFIX", "Comments have no title selector"}, "title:TITLE"},
		{"comments create", []string{"PROJECT_TITLE:ISSUE_REF", "owning issue"}, "Comments have no title selector"},
		{"claims acquire", []string{"PROJECT_TITLE:ISSUE_REF", "title:TITLE"}, ""},
		{"session set", []string{"Project REF", "id:UUID_PREFIX"}, "PROJECT_TITLE:ISSUE_REF"},
		{"workflow-session fresh", []string{"Project REF", "title:TITLE"}, "PROJECT_TITLE:ISSUE_REF"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			var out, err bytes.Buffer
			if code := Run(append(strings.Fields(tc.path), "--help"), &out, &err); code != 0 {
				t.Fatalf("%d %s", code, &err)
			}
			for _, want := range tc.wants {
				if !strings.Contains(out.String(), want) {
					t.Errorf("missing %q in %s", want, &out)
				}
			}
			if tc.absent != "" && strings.Contains(out.String(), tc.absent) {
				t.Errorf("inapplicable reference syntax %q in %s", tc.absent, &out)
			}
		})
	}
}
