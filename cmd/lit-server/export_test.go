package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/export"
	"github.com/nerdwave-nick/lit/internal/storage"
)

// isolate points configuration at an empty home so tests never read the
// owner's files.
func isolate(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	for _, k := range []string{"LIT_CONFIG_FILE", "LIT_DATA_DIR", "LIT_LISTEN"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	return root
}

func TestExportHelpIsOffline(t *testing.T) {
	root := isolate(t)
	t.Setenv("LIT_DATA_DIR", filepath.Join(root, "uncreated"))
	for _, args := range [][]string{{"export", "--help"}, {"export", "-h"}, {"export", "help"}, {"help", "export"}} {
		var out, stderr bytes.Buffer
		if code := runCLI(args, &out, &stderr); code != 0 || stderr.Len() != 0 {
			t.Fatalf("%v: %d %s", args, code, &stderr)
		}
		for _, want := range []string{"lit-server export [flags]", "--to DIRECTORY", "--data-dir", "--config", "one-way", "never read back", "owner lock", "while lit-server runs", "Examples:"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("%v missing %q:\n%s", args, want, &out)
			}
		}
	}
	if _, e := os.Stat(filepath.Join(root, "uncreated")); !os.IsNotExist(e) {
		t.Fatalf("help created data: %v", e)
	}
	var out, stderr bytes.Buffer
	if code := runCLI([]string{"--help"}, &out, &stderr); code != 0 || !strings.Contains(out.String(), "lit-server export --to DIRECTORY") {
		t.Fatalf("the server help does not mention the export: %d\n%s", code, &out)
	}
}

func TestExportArgumentErrors(t *testing.T) {
	root := isolate(t)
	t.Setenv("LIT_DATA_DIR", filepath.Join(root, "uncreated"))
	for _, args := range [][]string{{"export"}, {"export", "--to"}, {"export", "--to", "a", "--to", "b"}, {"export", "--to", "a", "extra"}, {"export", "--listen", "127.0.0.1:1", "--to", "a"}, {"export", "--bogus"}} {
		var out, stderr bytes.Buffer
		if code := runCLI(args, &out, &stderr); code != 2 || stderr.Len() == 0 || out.Len() != 0 {
			t.Fatalf("%v: %d %q %q", args, code, &out, &stderr)
		}
	}
	if _, e := os.Stat(filepath.Join(root, "uncreated")); !os.IsNotExist(e) {
		t.Fatalf("invalid arguments created data: %v", e)
	}
}

func TestExportRefusesADirectoryWithoutADatabase(t *testing.T) {
	root := isolate(t)
	data, to := filepath.Join(root, "missing"), filepath.Join(root, "export")
	var out, stderr bytes.Buffer
	if code := runCLI([]string{"export", "--data-dir", data, "--to", to}, &out, &stderr); code != 1 || !strings.Contains(stderr.String(), "no lit database") {
		t.Fatalf("%d %s", code, &stderr)
	}
	for _, p := range []string{data, to} {
		if _, e := os.Stat(p); !os.IsNotExist(e) {
			t.Fatalf("a failed export created %s: %v", p, e)
		}
	}
}

func TestExportReadsWhileTheServerOwnsTheDatabase(t *testing.T) {
	root := isolate(t)
	data, to := filepath.Join(root, "data"), filepath.Join(root, "export")
	owner, e := storage.Open(data)
	if e != nil {
		t.Fatal(e)
	}
	defer owner.Close()
	t.Setenv("LIT_DATA_DIR", data)
	var out, stderr bytes.Buffer
	if code := runCLI([]string{"export", "--to", to}, &out, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("export beside a running owner: %d %s", code, &stderr)
	}
	if !strings.Contains(out.String(), "exported 0 projects, 0 issues, 0 comments and 0 milestones to "+to) {
		t.Fatalf("summary %q", &out)
	}
	b, e := os.ReadFile(filepath.Join(to, "manifest.json"))
	if e != nil {
		t.Fatal(e)
	}
	var m export.Manifest
	if e = json.Unmarshal(b, &m); e != nil || m.ServiceID != owner.ServiceID() || m.LastTransaction != nil {
		t.Fatalf("manifest %s: %v", b, e)
	}
	// The export never took ownership: the owner still holds the lock.
	if _, e = storage.Open(data); e == nil || !strings.Contains(e.Error(), "already owned") {
		t.Fatalf("ownership after the export: %v", e)
	}
	// Running it again replaces the previous export.
	out.Reset()
	if code := runCLI([]string{"export", "--data-dir", data, "--to", to}, &out, &stderr); code != 0 {
		t.Fatalf("second export: %d %s", code, &stderr)
	}
}
