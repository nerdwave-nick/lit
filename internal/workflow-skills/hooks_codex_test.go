package workflowskills

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func hooksPath(target Target) string { return filepath.Join(target.Dir, "hooks.json") }

// litGroup is the group lit appends for a Codex event.
func litGroup(binary, sub string, timeout int) any {
	return map[string]any{"hooks": []any{map[string]any{"type": "command", "command": binary + " hook --host codex " + sub, "timeout": float64(timeout)}}}
}

func eventList(t *testing.T, target Target, event string) []any {
	t.Helper()
	hooks, _ := readObject(t, hooksPath(target))["hooks"].(map[string]any)
	list, _ := hooks[event].([]any)
	return list
}

func TestCodexInstallCreatesHooksJson(t *testing.T) {
	target := configTarget(t, "codex")
	res := install(t, target, Options{Binary: "lit"})
	if !res.Hooks || res.StatusLine != "" {
		t.Errorf("result %+v", res)
	}
	want := map[string]any{"hooks": map[string]any{
		"SessionStart":  []any{litGroup("lit", "session-start", 10)},
		"SubagentStart": []any{litGroup("lit", "subagent-start", 5)},
		"Stop":          []any{litGroup("lit", "stop", 5)},
	}}
	if got := readAny(t, hooksPath(target)); !sameJSON(got, want) {
		t.Fatalf("hooks.json:\n%s", readText(t, hooksPath(target)))
	}
	rec := readRecordFile(t, target.Dir)
	if len(rec.Hooks) != 3 || rec.Plugin != nil || rec.StatusLine != nil {
		t.Fatalf("record %+v", rec)
	}
	for _, e := range rec.Hooks {
		list := eventList(t, target, e.Event)
		if len(list) != 1 || !sameJSON(parseAny(t, string(e.Group)), list[0]) {
			t.Errorf("record entry %s %s is not what hooks.json holds: %v", e.Event, e.Group, list)
		}
	}
	for _, sub := range []string{"session-start", "subagent-start", "stop"} {
		if !contains(res.Notes, "lit hook --host codex "+sub) {
			t.Errorf("the approval note does not name the %s hook: %v", sub, res.Notes)
		}
	}
	if !contains(res.Notes, "/hooks") || !contains(res.Notes, "approve") {
		t.Errorf("notes %v", res.Notes)
	}
	for _, n := range []string{"config.toml", "settings.json"} {
		if exists(filepath.Join(target.Dir, n)) {
			t.Errorf("setup wrote %s: hooks are never pre-trusted and Codex has no settings.json", n)
		}
	}
	if exists(filepath.Join(target.Dir, "skills", pluginName)) {
		t.Error("a codex installation holds the claude plugin directory")
	}
}

func TestCodexHooksAreAppendedAfterTheUsersEntries(t *testing.T) {
	target := configTarget(t, "codex")
	user := `{"matcher": "^startup$", "hooks": [{"type": "command", "command": "~/mine.sh"}]}`
	put(t, hooksPath(target), `{
  "note": "keep",
  "hooks": {
    "SessionStart": [ `+user+` ],
    "PreToolUse": [],
    "Stop": [
    ]
  }
}`)
	install(t, target, Options{})
	text := readText(t, hooksPath(target))
	if got := keysOf(t, text); !reflect.DeepEqual(got, []string{"note", "hooks"}) {
		t.Errorf("top-level order %v", got)
	}
	hooksRaw, _ := parseObject([]byte(text))
	raw, _ := hooksRaw.get("hooks")
	inner, err := parseObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(inner.keys, []string{"SessionStart", "PreToolUse", "Stop", "SubagentStart"}) {
		t.Errorf("event order %v", inner.keys)
	}
	if got := eventList(t, target, "SessionStart"); len(got) != 2 || !sameJSON(got[0], parseAny(t, user)) || !sameJSON(got[1], litGroup("lit", "session-start", 10)) {
		t.Errorf("SessionStart %v", got)
	}
	if got := eventList(t, target, "PreToolUse"); len(got) != 0 {
		t.Errorf("PreToolUse %v", got)
	}
	if got := eventList(t, target, "Stop"); len(got) != 1 || !sameJSON(got[0], litGroup("lit", "stop", 5)) {
		t.Errorf("Stop %v", got)
	}
	if readObject(t, hooksPath(target))["note"] != "keep" {
		t.Error("an unrelated member changed")
	}
}

func TestCodexSecondRunChangesNothing(t *testing.T) {
	target := configTarget(t, "codex")
	install(t, target, Options{})
	files, record := readText(t, hooksPath(target)), readText(t, filepath.Join(target.Dir, recordName))
	install(t, target, Options{})
	if readText(t, hooksPath(target)) != files || readText(t, filepath.Join(target.Dir, recordName)) != record {
		t.Error("a second run changed the installation")
	}
}

func TestCodexRerunWithAnotherBinaryReplacesOnlyLitsEntries(t *testing.T) {
	target := configTarget(t, "codex")
	install(t, target, Options{Binary: "lit"})
	// The user adds entries after lit's (SessionStart) and before them (Stop).
	doc := readObject(t, hooksPath(target))
	hooks := doc["hooks"].(map[string]any)
	after := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "after.sh"}}}
	before := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "before.sh"}}}
	hooks["SessionStart"] = append(hooks["SessionStart"].([]any), after)
	hooks["Stop"] = append([]any{before}, hooks["Stop"].([]any)...)
	put(t, hooksPath(target), string(jsonBytes(doc)))

	install(t, target, Options{Binary: "/opt/lit"})
	if got := eventList(t, target, "SessionStart"); len(got) != 2 || !sameJSON(got[0], after) || !sameJSON(got[1], litGroup("/opt/lit", "session-start", 10)) {
		t.Errorf("SessionStart %v", got)
	}
	if got := eventList(t, target, "Stop"); len(got) != 2 || !sameJSON(got[0], before) || !sameJSON(got[1], litGroup("/opt/lit", "stop", 5)) {
		t.Errorf("Stop %v", got)
	}
	if got := eventList(t, target, "SubagentStart"); len(got) != 1 || !sameJSON(got[0], litGroup("/opt/lit", "subagent-start", 5)) {
		t.Errorf("SubagentStart %v", got)
	}
	rec := readRecordFile(t, target.Dir)
	if len(rec.Hooks) != 3 {
		t.Fatalf("record %+v", rec.Hooks)
	}
	for _, e := range rec.Hooks {
		if !strings.Contains(string(e.Group), "/opt/lit hook --host codex") {
			t.Errorf("record still holds %s", e.Group)
		}
	}
}

func TestCodexEditedLitEntryIsLeftAlone(t *testing.T) {
	target := configTarget(t, "codex")
	install(t, target, Options{})
	doc := readObject(t, hooksPath(target))
	stop := doc["hooks"].(map[string]any)["Stop"].([]any)
	stop[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)["timeout"] = float64(99)
	put(t, hooksPath(target), string(jsonBytes(doc)))

	res := install(t, target, Options{})
	got := eventList(t, target, "Stop")
	if len(got) != 2 || got[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)["timeout"] != float64(99) || !sameJSON(got[1], litGroup("lit", "stop", 5)) {
		t.Errorf("Stop %v", got)
	}
	if !contains(res.Warnings, "Stop") || !contains(res.Warnings, "edited") {
		t.Errorf("warnings %v", res.Warnings)
	}
	for _, e := range readRecordFile(t, target.Dir).Hooks {
		if e.Event == "Stop" && !sameJSON(parseAny(t, string(e.Group)), litGroup("lit", "stop", 5)) {
			t.Errorf("record holds the edited entry: %s", e.Group)
		}
	}
}

func TestCodexIdenticalEntryAlreadyPresentIsNotDuplicated(t *testing.T) {
	target := configTarget(t, "codex")
	handwritten := litGroup("lit", "stop", 5)
	put(t, hooksPath(target), string(jsonBytes(map[string]any{"hooks": map[string]any{"Stop": []any{handwritten}}})))
	res := install(t, target, Options{})
	if got := eventList(t, target, "Stop"); len(got) != 1 || !sameJSON(got[0], handwritten) {
		t.Errorf("Stop %v", got)
	}
	if !contains(res.Warnings, "Stop") || !contains(res.Warnings, "already") {
		t.Errorf("warnings %v", res.Warnings)
	}
	for _, e := range readRecordFile(t, target.Dir).Hooks {
		if e.Event == "Stop" {
			t.Errorf("setup claims an entry it did not add: %s", e.Group)
		}
	}
}

func TestCodexWarnsAboutHooksDefinedInConfigToml(t *testing.T) {
	for name, tc := range map[string]struct {
		toml string
		want string // a fragment of the expected warning; empty for none
	}{
		"hooks table":       {"[hooks]\nfoo = 1\n", "[hooks]"},
		"event table":       {"model = \"x\"\n\n[[hooks.SessionStart]]\nmatcher = \"^startup$\"\n", "[hooks]"},
		"event handler":     {"[[hooks.Stop.hooks]]\ntype = \"command\"\n", "[hooks]"},
		"indented":          {"  [ hooks ]\n", "[hooks]"},
		"features off":      {"[features]\nhooks = false\n", "disabled"},
		"features alias":    {"[features]\ncodex_hooks = false\n", "disabled"},
		"features dotted":   {"features.hooks = false\n", "disabled"},
		"features spaced":   {"[features]\nfoo = true\nhooks   =   false # off\n", "disabled"},
		"features on":       {"[features]\nhooks = true\n", ""},
		"other table":       {"[other]\nhooks = false\n", ""},
		"only trust state":  {"[hooks.state.\"/x/hooks.json:stop:0:0\"]\ntrusted_hash = \"sha256:abc\"\n", ""},
		"commented table":   {"# [hooks]\n# hooks = false\n", ""},
		"unrelated":         {"model = \"gpt\"\n[projects.\"/x\"]\ntrust_level = \"trusted\"\n", ""},
		"string with table": {"note = \"[hooks]\"\n", ""},
	} {
		t.Run(name, func(t *testing.T) {
			target := configTarget(t, "codex")
			put(t, filepath.Join(target.Dir, "config.toml"), tc.toml)
			res := install(t, target, Options{})
			if readText(t, filepath.Join(target.Dir, "config.toml")) != tc.toml {
				t.Error("setup edited config.toml")
			}
			if tc.want == "" {
				if contains(res.Warnings, "config.toml") {
					t.Errorf("unexpected warning %v", res.Warnings)
				}
				return
			}
			if !contains(res.Warnings, "config.toml") || !contains(res.Warnings, tc.want) {
				t.Errorf("warnings %v, want one naming config.toml and %q", res.Warnings, tc.want)
			}
		})
	}
}

func TestCodexHooksRefusals(t *testing.T) {
	for name, body := range map[string]string{
		"not json":              `{"hooks": `,
		"array":                 `[]`,
		"hooks is a list":       `{"hooks": []}`,
		"event is an object":    `{"hooks": {"Stop": {}}}`,
		"event is a string":     `{"hooks": {"SessionStart": "x"}}`,
		"hooks is null":         `{"hooks": null}`,
		"duplicate event":       `{"hooks": {"Stop": [], "Stop": []}}`,
		"duplicate top level":   `{"hooks": {}, "hooks": {}}`,
		"trailing second value": `{"hooks": {}} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			target := configTarget(t, "codex")
			put(t, hooksPath(target), body)
			before := snapshot(t, target.Dir)
			if _, err := InstallWith(Bundle(), []Target{target}, Options{}); err == nil {
				t.Fatal("accepted")
			}
			if after := snapshot(t, target.Dir); after != before {
				t.Fatalf("a refused install changed the configuration:\n%s\n---\n%s", before, after)
			}
		})
	}
}

func TestHostFilesKeepTheirPermissions(t *testing.T) {
	for host, name := range map[string]string{"claude": "settings.json", "codex": "hooks.json"} {
		t.Run(host, func(t *testing.T) {
			target := configTarget(t, host)
			path := filepath.Join(target.Dir, name)
			put(t, path, `{}`)
			if err := os.Chmod(path, 0o600); err != nil {
				t.Fatal(err)
			}
			install(t, target, Options{})
			if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
				t.Errorf("%s: %v %v", name, info, err)
			}
			if !strings.Contains(readText(t, path), "lit hook") {
				t.Errorf("%s was not edited", name)
			}
		})
	}
}

func TestEverythingTheRecordListsIsInstalled(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		t.Run(host, func(t *testing.T) {
			target := configTarget(t, host)
			install(t, target, Options{})
			text := readText(t, filepath.Join(target.Dir, recordName))
			var generic map[string]json.RawMessage
			if err := json.Unmarshal([]byte(text), &generic); err != nil {
				t.Fatal(err)
			}
			want := map[string][]string{"claude": {"schema_version", "host", "skills", "plugin", "mod", "status_line", "created_files"}, "codex": {"schema_version", "host", "skills", "hooks", "created_files"}}[host]
			if len(generic) != len(want) {
				t.Errorf("record members %v, want %v", keysOf(t, text), want)
			}
			for _, k := range want {
				if _, ok := generic[k]; !ok {
					t.Errorf("record has no %s", k)
				}
			}
		})
	}
}
