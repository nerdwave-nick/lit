package cli

import (
	"bytes"
	"encoding/json"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicOutputFormats(t *testing.T) {
	for _, format := range []string{"cli", "markdown", "json", "human", "invalid"} {
		for _, argv := range [][]string{{"session", "get", "--format", format}, {"projects", "list", "--format", format}, {"issues", "list", "--format", format}, {"grep", "needle", "--format", format}, {"session", "set", "--output-format", format}, {"connect", "--output-format", format}} {
			a, err := Parse(argv)
			if err == nil {
				err = validateArgs(a)
			}
			valid := format == "cli" || format == "markdown" || format == "json"
			if (err == nil) != valid {
				t.Errorf("%v: %v", argv, err)
			}
		}
	}
}

func TestOutputFormatPreferencesAndLegacyCache(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("LIT_STATE_DIR", filepath.Join(root, "state"))
	t.Setenv("LIT_SESSION", "default")
	cwd, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(cwd) })
	m := Mapping{SchemaVersion: 1, ClientID: protocol.UUID(), ServiceID: protocol.UUID(), Endpoint: "http://127.0.0.1:7411"}
	if err := litclient.WriteJSON(litclient.MappingPath(filepath.Join(root, "state"), "default"), m); err != nil {
		t.Fatal(err)
	}
	for _, stored := range []string{"cli", "markdown", "json", "human"} {
		want := stored
		if want == "human" {
			want = "cli"
		}
		if err := litclient.WriteJSON(cachePath(m.ClientID), Cache{1, m.ClientID, m.ServiceID, stored}); err != nil {
			t.Fatal(err)
		}
		if got := offlineFormat(nil); got != want {
			t.Errorf("cache %s => %s, want %s", stored, got, want)
		}
		a := App{Session: litclient.Session{Client: protocol.Client{OutputFormat: &stored}}}
		if got := a.preference(); got != want {
			t.Errorf("preference %s => %s", stored, got)
		}
		for _, override := range []string{"cli", "markdown", "json"} {
			if got := offlineFormat([]string{"--format", override}); got != override {
				t.Errorf("override %s => %s", override, got)
			}
		}
	}
	if got := (&App{}).preference(); got != "cli" {
		t.Fatal("default", got)
	}
	if err := litclient.WriteJSON(cachePath(m.ClientID), Cache{1, m.ClientID, protocol.UUID(), "markdown"}); err != nil {
		t.Fatal(err)
	}
	if got := offlineFormat(nil); got != "" {
		t.Fatal("wrong service cache accepted", got)
	}
}

func TestPrintAndErrorsUseRequestedFormat(t *testing.T) {
	for _, format := range []string{"cli", "markdown", "json"} {
		var out, stderr bytes.Buffer
		a := App{Args: Args{Command: "projects", Verb: "list"}, Format: format, Out: &out, Err: &stderr}
		r := Result{Items: []any{map[string]any{"id": "project-id", "title": "test/poc", "revision": 1}}, Outcome: "applied", RequestHash: "proof"}
		if code := a.Print(r); code != 0 {
			t.Fatal(code, stderr.String())
		}
		switch format {
		case "cli":
			if strings.Contains(out.String(), "# Projects") || strings.Contains(out.String(), "```") || !strings.Contains(out.String(), "test/poc") {
				t.Fatal(out.String())
			}
		case "markdown":
			if !strings.HasPrefix(out.String(), "# Projects list\n") || !strings.Contains(out.String(), "| --- |") {
				t.Fatal(out.String())
			}
		case "json":
			var got Result
			if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.Outcome != "applied" || got.RequestHash != "proof" || len(got.Items) != 1 {
				t.Fatal(out.String(), err)
			}
		}
		if code := a.Error(protocol.E(409, "conflict", "unsafe \x1b[31m <tag>")); code != 3 {
			t.Fatal(code)
		}
		if strings.Contains(stderr.String(), "\x1b") {
			t.Fatal("raw terminal control", stderr.String())
		}
		if format == "json" && !json.Valid(stderr.Bytes()) {
			t.Fatal(stderr.String())
		}
		if format == "markdown" && strings.Contains(stderr.String(), "<tag>") {
			t.Fatal("raw markdown HTML", stderr.String())
		}
	}
}

func TestOfflineFormatRespectsLiteralDelimiter(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("LIT_STATE_DIR", root)
	t.Setenv("LIT_SESSION", "")
	if got := offlineFormat([]string{"issues", "get", "--format", "markdown", "--", "--format", "json"}); got != "markdown" {
		t.Fatalf("literal target changed format: %s", got)
	}
}
