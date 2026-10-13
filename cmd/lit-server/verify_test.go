package main

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/storage"
)

func verifyCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	return serverCLI(t, append([]string{"verify"}, args...)...)
}

// serverCLI runs lit-server's command line with an isolated environment.
func serverCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("LIT_CONFIG_FILE", "")
	t.Setenv("LIT_LISTEN", "")
	t.Setenv("LIT_DATA_DIR", "")
	var out, errOut bytes.Buffer
	code := runCLI(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestVerifyHelpIsOffline(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent")
	for _, args := range [][]string{{"--help"}, {"-h"}, {"help"}, {"--data-dir", missing, "--help"}} {
		code, out, errOut := verifyCLI(t, args...)
		if code != 0 || errOut != "" {
			t.Fatalf("%v: %d %s", args, code, errOut)
		}
		for _, want := range []string{"lit-server verify [flags]", "--data-dir", "--config", "replays", "integrity_check", "stopped", "Exit status"} {
			if !strings.Contains(out, want) {
				t.Errorf("%v: help lacks %q:\n%s", args, want, out)
			}
		}
	}
	if code, out, errOut := serverCLI(t, "help", "verify"); code != 0 || errOut != "" || !strings.Contains(out, "lit-server verify [flags]") {
		t.Fatalf("help verify: %d %s %s", code, out, errOut)
	}
	if _, e := os.Stat(missing); !os.IsNotExist(e) {
		t.Fatal("help touched the data directory", e)
	}
}

// The root help names the verify subcommand, as it names export.
func TestRootHelpNamesVerify(t *testing.T) {
	if code, out, _ := serverCLI(t, "--help"); code != 0 || !strings.Contains(out, "lit-server verify checks") || !strings.Contains(out, "see lit-server verify --help") {
		t.Fatal(code, out)
	}
}

func TestVerifyArgumentErrors(t *testing.T) {
	for _, args := range [][]string{{"extra"}, {"--listen", "127.0.0.1:0"}, {"--bogus"}, {"--data-dir"}} {
		if code, out, errOut := verifyCLI(t, args...); code != 2 || out != "" || errOut == "" {
			t.Fatalf("%v: %d %q %q", args, code, out, errOut)
		}
	}
}

// A database that verified is left clean: the next start skips the full
// integrity scan that verify just ran.
func TestVerifyPassesOnAConsistentDatabase(t *testing.T) {
	dir := fresh(t, func(*sql.DB) {})
	code, out, errOut := verifyCLI(t, "--data-dir", dir)
	if code != 0 || !strings.Contains(out, "verified") || errOut != "" {
		t.Fatal(code, out, errOut)
	}
	if !markerPresent(t, dir) {
		t.Fatal("verify left no clean-shutdown marker")
	}
}

// A database that failed verify keeps no marker, even one left by a clean
// shutdown before the damage, so the next start runs the full scan.
func TestVerifyFailureLeavesNoMarker(t *testing.T) {
	dir := fresh(t, func(o *sql.DB) { execSQL(t, o, projectRow) })
	markClean(t, dir)
	if code, out, _ := verifyCLI(t, "--data-dir", dir); code != 1 || markerPresent(t, dir) {
		t.Fatal(code, out, markerPresent(t, dir))
	}
}

func TestVerifyFailsOnAMismatch(t *testing.T) {
	// A project row that no request created: it has no history.
	dir := fresh(t, func(o *sql.DB) { execSQL(t, o, projectRow) })
	code, out, _ := verifyCLI(t, "--data-dir", dir)
	if code != 1 || !strings.Contains(out, "00000000-0000-4000-8000-000000000001: has no history") || !strings.Contains(out, "1 problem") {
		t.Fatal(code, out)
	}
}

func TestVerifyFailsOnAFailedStartupCheck(t *testing.T) {
	dir := fresh(t, func(o *sql.DB) {
		execSQL(t, o, "INSERT INTO project_repositories (project_id, repository_ref) VALUES ('00000000-0000-4000-8000-000000000009', 'x')")
	})
	code, out, _ := verifyCLI(t, "--data-dir", dir)
	if code != 1 || !strings.Contains(out, "foreign_key_check") {
		t.Fatal(code, out)
	}
}

func TestVerifyNeedsAStoppedServerAndAnExistingDatabase(t *testing.T) {
	dir := t.TempDir()
	db, e := storage.Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	code, out, errOut := verifyCLI(t, "--data-dir", dir)
	if code != 1 || out != "" || !strings.Contains(errOut, "already owned") || !strings.Contains(errOut, "stop lit-server") {
		t.Fatal(code, out, errOut)
	}
	missing := filepath.Join(t.TempDir(), "absent")
	code, out, errOut = verifyCLI(t, "--data-dir", missing)
	if code != 1 || out != "" || !strings.Contains(errOut, "no lit database") {
		t.Fatal(code, out, errOut)
	}
	if _, e := os.Stat(missing); !os.IsNotExist(e) {
		t.Fatal("verify created the data directory", e)
	}
}
