package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/cmd/lit-migrate/internal/legacy"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
)

func isolate(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("LIT_CONFIG_FILE", "")
	return root
}

func cli(args ...string) (code int, out, errOut string) {
	var o, e bytes.Buffer
	code = runCLI(args, &o, &e)
	return code, o.String(), e.String()
}

func TestHelpPage(t *testing.T) {
	isolate(t)
	want, err := os.ReadFile(filepath.Join("testdata", "help.golden"))
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--help"}, {"-h"}, {"help"}} {
		if code, out, errOut := cli(args...); code != 0 || out != string(want) || errOut != "" {
			t.Errorf("%v: %d %s\n%s", args, code, errOut, out)
		}
	}
	if code, out, _ := cli("--version"); code != 0 || strings.TrimSpace(out) == "" {
		t.Fatalf("version: %d %q", code, out)
	}
}

func TestArgumentErrorsTouchNothing(t *testing.T) {
	root := isolate(t)
	to := filepath.Join(root, "new")
	for _, args := range [][]string{
		{},
		{"--from", root},
		{"--to", to},
		{"--from", root, "--to", to, "--to", to},
		{"--from", root, "--to", to, "extra"},
		{"--bogus"},
		{"--from", root, "--to", to, "--config", filepath.Join(root, "missing.json")},
	} {
		if code, _, errOut := cli(args...); code != 2 || errOut == "" {
			t.Fatalf("%v: %d %s", args, code, errOut)
		}
	}
	if _, err := os.Stat(to); !os.IsNotExist(err) {
		t.Fatalf("invalid arguments created the target: %v", err)
	}
}

// legacyStore writes a small legacy store with the frozen legacy writer: one
// client and one project titled feat/one.
func legacyStore(t *testing.T, dir string) string {
	t.Helper()
	s, err := legacy.Open(dir, legacy.Config{SchemaVersion: 1, Limits: protocol.DefaultLimits(), TitlePrefixes: []string{"feat"}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := protocol.Client{SchemaVersion: 1, ClientID: protocol.UUID(), StateRevision: 1, Status: "connected", Actor: protocol.Actor{Name: "me", Kind: "agent"}}
	if err = s.SaveClient(c); err != nil {
		t.Fatal(err)
	}
	title, id := "feat/one", protocol.UUID()
	i := protocol.Intent{SchemaVersion: 1, Operation: "transaction", ServiceID: s.ServiceID(), Actor: protocol.DurableActor{ClientID: c.ClientID, Name: "me", Kind: "agent"},
		Operations: []protocol.Operation{{Type: "projects", ID: id, Kind: "create", Set: protocol.ProjectSet{Title: &title}}},
		Targets:    []protocol.ObjectRef{{Type: "projects", ID: id}}}
	if r, err := s.Apply(i, c); err != nil || r.Outcome != "applied" {
		t.Fatal(r, err)
	}
	return s.ServiceID()
}

func writeConfig(t *testing.T, path string, prefixes string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(`{"schema_version": 1, "title_prefixes": `+prefixes+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDryRunThenImport(t *testing.T) {
	root := isolate(t)
	from, to, config := filepath.Join(root, "old"), filepath.Join(root, "new"), filepath.Join(root, "config.json")
	service := legacyStore(t, from)
	writeConfig(t, config, `["feat", "docs"]`)

	code, out, errOut := cli("--from", from, "--to", to, "--config", config, "--dry-run")
	if code != 0 || !strings.Contains(out, "dry run passed all 9 checks in ") || !strings.Contains(out, "service "+service+": 1 projects") || !strings.Contains(errOut, "check 9 passed") {
		t.Fatalf("dry run: %d\n%s\n%s", code, out, errOut)
	}
	if _, err := os.Stat(to); !os.IsNotExist(err) {
		t.Fatalf("the dry run created the target: %v", err)
	}
	code, out, errOut = cli("--from", from, "--to", to, "--config", config)
	if code != 0 || !strings.Contains(out, "imported into "+filepath.Join(to, storage.DatabaseFile)) {
		t.Fatalf("import: %d\n%s\n%s", code, out, errOut)
	}
	db, err := storage.Open(to)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if db.ServiceID() != service {
		t.Fatalf("service %s, want %s", db.ServiceID(), service)
	}
	// The target is no longer empty: a second import is refused.
	if code, _, errOut = cli("--from", from, "--to", to, "--config", config); code != 1 || !strings.Contains(errOut, "not empty") {
		t.Fatalf("second import: %d %s", code, errOut)
	}
}

// Legacy verification validates the current projects' title prefixes, so the
// legacy server's configuration matters.
func TestConfigTitlePrefixesAreChecked(t *testing.T) {
	root := isolate(t)
	from, to, config := filepath.Join(root, "old"), filepath.Join(root, "new"), filepath.Join(root, "config.json")
	legacyStore(t, from)
	writeConfig(t, config, `["docs"]`)
	code, _, errOut := cli("--from", from, "--to", to, "--config", config)
	if code != 1 || !strings.Contains(errOut, "check 1") || !strings.Contains(errOut, "no database was written") {
		t.Fatalf("%d %s", code, errOut)
	}
	if _, err := os.Stat(to); !os.IsNotExist(err) {
		t.Fatalf("the failed import left the target: %v", err)
	}
}
