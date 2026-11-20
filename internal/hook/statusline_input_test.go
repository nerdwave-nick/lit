package hook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseStatusInputToleratesMissingFields(t *testing.T) {
	for name, c := range map[string]struct {
		body string
		want statusInput
	}{
		"everything":         {`{"cwd":"/a","model":{"id":"claude-x","display_name":"Opus"},"effort":{"level":"high"},"workspace":{"current_dir":"/b"},"context_window":{"used_percentage":23.9}}`, statusInput{"Opus", "high", "/b", 23}},
		"no workspace":       {`{"cwd":"/a","model":{"display_name":"Opus"}}`, statusInput{Model: "Opus", Dir: "/a"}},
		"model id only":      {`{"model":{"id":"claude-x"}}`, statusInput{Model: "claude-x"}},
		"no effort":          {`{"model":{"display_name":"Haiku"},"context_window":{"used_percentage":7}}`, statusInput{Model: "Haiku", Percent: 7}},
		"null percentage":    {`{"model":{"display_name":"Opus"},"context_window":{"used_percentage":null,"current_usage":null}}`, statusInput{Model: "Opus"}},
		"no context window":  {`{"model":{"display_name":"Opus"}}`, statusInput{Model: "Opus"}},
		"negative":           {`{"context_window":{"used_percentage":-4}}`, statusInput{}},
		"over a hundred":     {`{"context_window":{"used_percentage":101.5}}`, statusInput{Percent: 101}},
		"wrong types":        {`{"model":"Opus","effort":"high","workspace":[],"context_window":{"used_percentage":"lots"},"cwd":"/a"}`, statusInput{Dir: "/a"}},
		"one wrong type":     {`{"effort":"high","model":{"display_name":"Opus"},"context_window":{"used_percentage":50}}`, statusInput{Model: "Opus", Percent: 50}},
		"not json":           {`status`, statusInput{}},
		"empty":              {``, statusInput{}},
		"not an object":      {`[1]`, statusInput{}},
		"unknown fields too": {`{"future":{"a":1},"model":{"display_name":"Opus"}}`, statusInput{Model: "Opus"}},
	} {
		if got := parseStatusInput([]byte(c.body)); got != c.want {
			t.Errorf("%s: got %+v, want %+v", name, got, c.want)
		}
	}
}

func fixtureBody(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "claude", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRecordedStatusLineInputsAreReadAndStateTheirProvenance(t *testing.T) {
	set := loadFixtures(t)
	want := map[string]statusInput{
		"claude/statusline-early.json":   {Model: "Opus 5.5", Effort: "medium", Dir: "/home/example/project"},
		"claude/statusline-session.json": {Model: "Opus 5.5", Effort: "high", Dir: "/home/example/project", Percent: 23},
	}
	seen := 0
	for _, f := range set.Fixtures {
		if f.Event != "statusline" {
			continue
		}
		seen++
		note := set.Provenance[f.Provenance]
		if !strings.HasPrefix(note, "Captured") && !strings.HasPrefix(note, "Not captured") {
			t.Errorf("%s: the provenance must say whether the input was captured: %q", f.File, note)
		}
		body, err := os.ReadFile(filepath.Join("testdata", f.File))
		if err != nil {
			t.Fatal(err)
		}
		if got := parseStatusInput(body); got != want[f.File] {
			t.Errorf("%s: got %+v, want %+v", f.File, got, want[f.File])
		}
		if f.Host != "claude" {
			t.Errorf("%s: only Claude has a custom status line", f.File)
		}
	}
	if seen != len(want) {
		t.Fatalf("%d status line fixtures in the manifest, want %d", seen, len(want))
	}
	if _, id := input(t, "claude", "startup"); !strings.Contains(string(fixtureBody(t, "statusline-session.json")), id) {
		t.Error("the status line fixtures must carry the session ID of the recorded session start")
	}
}
