package workflowskills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveRestoresAWrappedStatusLineVerbatim(t *testing.T) {
	target := configTarget(t, "claude")
	foreign := `{"type": "command", "command": "` + interimCommand + `", "padding": 1, "refreshInterval": 5, "extra": [1, {"a": null}]}`
	put(t, settingsPath(target), `{"model": "m", "statusLine": `+foreign+`, "z": 1}`)
	if res := install(t, target, Options{}); res.StatusLine != "wrapped" {
		t.Fatalf("install %+v", res)
	}
	// A second run rewrites the wrapper; the recorded original must survive it.
	install(t, target, Options{Binary: "/opt/lit"})
	res := removeOne(t, target)
	if res.StatusLine != "restored" || !res.Found || !res.Hooks {
		t.Errorf("result %+v", res)
	}
	got := readObject(t, settingsPath(target))
	want := parseAny(t, `{"model": "m", "statusLine": `+foreign+`, "z": 1}`)
	if !sameJSON(got, want) {
		t.Errorf("settings.json after removal:\n%s", readText(t, settingsPath(target)))
	}
	if keys := keysOf(t, readText(t, settingsPath(target))); strings.Join(keys, ",") != "model,statusLine,z" {
		t.Errorf("member order %v", keys)
	}
}

func TestRemoveDeletesLitsStatusLineWhenThereWasNone(t *testing.T) {
	target := configTarget(t, "claude")
	put(t, settingsPath(target), "{\n  \"model\": \"m\"\n}\n")
	if res := install(t, target, Options{}); res.StatusLine != "set" {
		t.Fatalf("install %+v", res)
	}
	res := removeOne(t, target)
	if res.StatusLine != "removed" {
		t.Errorf("result %+v", res)
	}
	if got := readText(t, settingsPath(target)); got != "{\n  \"model\": \"m\"\n}\n" {
		t.Errorf("settings.json %q", got)
	}
}

func TestRemoveDeletesASettingsFileSetupCreatedWhenOnlyLitsContentIsLeft(t *testing.T) {
	target := configTarget(t, "claude")
	install(t, target, Options{})
	removeOne(t, target)
	if exists(settingsPath(target)) {
		t.Errorf("settings.json created by setup is still there:\n%s", readText(t, settingsPath(target)))
	}
	// A file the user wrote before setup is never deleted, even when it ends up empty.
	put(t, settingsPath(target), "{}\n")
	install(t, target, Options{})
	removeOne(t, target)
	if got := readText(t, settingsPath(target)); got != "{}\n" {
		t.Errorf("a settings.json of the user's became %q", got)
	}
}

func TestRemoveKeepsASettingsFileTheUserAddedTo(t *testing.T) {
	target := configTarget(t, "claude")
	install(t, target, Options{})
	b := readObject(t, settingsPath(target))
	b["model"] = "added later"
	put(t, settingsPath(target), string(jsonBytes(b)))
	removeOne(t, target)
	got := readObject(t, settingsPath(target))
	if !sameJSON(got, map[string]any{"model": "added later"}) {
		t.Errorf("settings.json %v", got)
	}
}

func TestRemoveLeavesAStatusLineTheUserChangedAfterSetup(t *testing.T) {
	target := configTarget(t, "claude")
	put(t, settingsPath(target), `{"model": "m", "statusLine": {"type": "command", "command": "`+interimCommand+`"}}`)
	install(t, target, Options{})
	changed := `{"type": "command", "command": "~/mine-now.sh"}`
	doc := readObject(t, settingsPath(target))
	doc["statusLine"] = parseAny(t, changed)
	put(t, settingsPath(target), string(jsonBytes(doc)))
	res := removeOne(t, target)
	if res.StatusLine != "left" || !contains(res.Warnings, "statusLine") || !contains(res.Warnings, interimCommand) {
		t.Errorf("result %+v", res)
	}
	if got := readObject(t, settingsPath(target)); !sameJSON(got["statusLine"], parseAny(t, changed)) || got["model"] != "m" {
		t.Errorf("settings.json %v", got)
	}
	if exists(filepath.Join(target.Dir, recordName)) || exists(filepath.Join(target.Dir, "skills", pluginName)) {
		t.Error("the rest of the installation was not removed")
	}
}

func TestRemoveHandlesAStatusLineThatIsGone(t *testing.T) {
	target := configTarget(t, "claude")
	put(t, settingsPath(target), `{"model": "m"}`)
	install(t, target, Options{})
	doc := readObject(t, settingsPath(target))
	delete(doc, "statusLine")
	put(t, settingsPath(target), string(jsonBytes(doc)))
	res := removeOne(t, target)
	if !res.Found || res.StatusLine != "" || len(res.Warnings) != 0 {
		t.Errorf("result %+v", res)
	}
	if got := readObject(t, settingsPath(target)); !sameJSON(got, map[string]any{"model": "m"}) {
		t.Errorf("settings.json %v", got)
	}
	// A settings.json deleted altogether is no obstacle either.
	install(t, target, Options{})
	if err := os.Remove(settingsPath(target)); err != nil {
		t.Fatal(err)
	}
	if res = removeOne(t, target); !res.Found || exists(settingsPath(target)) {
		t.Errorf("result %+v", res)
	}
}

func TestRemoveRefusesHostFilesItCannotEdit(t *testing.T) {
	for name, tc := range map[string]struct {
		host, file string
		corrupt    func(t *testing.T, path string)
		want       string
	}{
		"settings.json that is not JSON": {"claude", "settings.json", func(t *testing.T, p string) { put(t, p, "{oops") }, "settings.json"},
		"settings.json that is a list":   {"claude", "settings.json", func(t *testing.T, p string) { put(t, p, "[]") }, "settings.json"},
		"hooks.json that is not JSON":    {"codex", "hooks.json", func(t *testing.T, p string) { put(t, p, "nope") }, "hooks.json"},
		"hooks that is not an object":    {"codex", "hooks.json", func(t *testing.T, p string) { put(t, p, `{"hooks": []}`) }, "hooks.json"},
		"symlinked settings.json": {"claude", "settings.json", func(t *testing.T, p string) {
			other := filepath.Join(t.TempDir(), "settings.json")
			put(t, other, `{"statusLine": {}}`)
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(other, p); err != nil {
				t.Fatal(err)
			}
		}, "symlink"},
	} {
		t.Run(name, func(t *testing.T) {
			target := configTarget(t, tc.host)
			install(t, target, Options{})
			tc.corrupt(t, filepath.Join(target.Dir, tc.file))
			before := filesOnly(t, target.Dir)
			_, err := Remove([]Target{target})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want one mentioning %q", err, tc.want)
			}
			if filesOnly(t, target.Dir) != before || exists(filepath.Join(target.Dir, transactionName)) {
				t.Error("a refused removal changed the configuration")
			}
		})
	}
}

func eventGroups(t *testing.T, target Target) map[string][]any {
	t.Helper()
	out := map[string][]any{}
	hooks, _ := readObject(t, hooksPath(target))["hooks"].(map[string]any)
	for event, v := range hooks {
		out[event], _ = v.([]any)
	}
	return out
}

func TestRemoveTakesOnlyLitsGroupsOutOfHooksJson(t *testing.T) {
	target := configTarget(t, "codex")
	user := `{"matcher": "^startup$", "hooks": [{"type": "command", "command": "~/mine.sh"}]}`
	put(t, hooksPath(target), `{"hooks": {"Stop": [`+user+`], "PreToolUse": [`+user+`]}, "other": true}`)
	install(t, target, Options{})
	res := removeOne(t, target)
	if !res.Found || !res.Hooks || res.StatusLine != "" {
		t.Errorf("result %+v", res)
	}
	got := eventGroups(t, target)
	if len(got["Stop"]) != 1 || !sameJSON(got["Stop"][0], parseAny(t, user)) || len(got["PreToolUse"]) != 1 {
		t.Errorf("the user's entries changed: %v", got)
	}
	// The groups lit appended to events the file did not have are gone; the
	// emptied lists stay, because setup cannot tell whether the user wrote them.
	for _, event := range []string{"SessionStart", "SubagentStart"} {
		if l, ok := got[event]; !ok || len(l) != 0 {
			t.Errorf("%s: %v", event, l)
		}
	}
	if readObject(t, hooksPath(target))["other"] != true {
		t.Error("an unrelated member changed")
	}
}

func TestRemoveDeletesAHooksFileSetupCreated(t *testing.T) {
	target := configTarget(t, "codex")
	install(t, target, Options{})
	removeOne(t, target)
	if exists(hooksPath(target)) {
		t.Errorf("hooks.json created by setup is still there:\n%s", readText(t, hooksPath(target)))
	}
	// One the user wrote stays, with its own content.
	put(t, hooksPath(target), `{"hooks": {}}`)
	install(t, target, Options{})
	removeOne(t, target)
	if got := eventGroups(t, target); len(got["Stop"]) != 0 || !exists(hooksPath(target)) {
		t.Errorf("hooks.json %v", got)
	}
}

func TestRemoveKeepsAHooksFileTheUserAddedTo(t *testing.T) {
	target := configTarget(t, "codex")
	install(t, target, Options{})
	doc := readObject(t, hooksPath(target))
	hooks := doc["hooks"].(map[string]any)
	hooks["Stop"] = append(hooks["Stop"].([]any), parseAny(t, `{"hooks": [{"type": "command", "command": "~/later.sh"}]}`))
	put(t, hooksPath(target), string(jsonBytes(doc)))
	removeOne(t, target)
	got := eventGroups(t, target)
	if len(got["Stop"]) != 1 || !sameJSON(got["Stop"][0], parseAny(t, `{"hooks": [{"type": "command", "command": "~/later.sh"}]}`)) {
		t.Errorf("hooks.json %v", got)
	}
}

func TestRemoveLeavesAnEditedLitEntryAndSaysSo(t *testing.T) {
	target := configTarget(t, "codex")
	install(t, target, Options{})
	doc := readObject(t, hooksPath(target))
	stop := doc["hooks"].(map[string]any)["Stop"].([]any)
	stop[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)["timeout"] = float64(99)
	put(t, hooksPath(target), string(jsonBytes(doc)))
	res := removeOne(t, target)
	if !res.Found || !contains(res.Warnings, "Stop") {
		t.Errorf("result %+v", res)
	}
	got := eventGroups(t, target)
	if len(got["Stop"]) != 1 || len(got["SessionStart"]) != 0 || len(got["SubagentStart"]) != 0 {
		t.Errorf("hooks.json %v", got)
	}
	// What is left is the user's now: the file stays although setup created it.
	if !exists(hooksPath(target)) {
		t.Error("hooks.json with an entry of the user's was deleted")
	}
}

func TestRemoveDoesNotTakeAnIdenticalEntryTheUserOwned(t *testing.T) {
	target := configTarget(t, "codex")
	mine := litGroup("lit", "stop", 5)
	put(t, hooksPath(target), string(jsonBytes(map[string]any{"hooks": map[string]any{"Stop": []any{mine}}})))
	install(t, target, Options{Binary: "lit"})
	removeOne(t, target)
	if got := eventGroups(t, target); len(got["Stop"]) != 1 || !sameJSON(got["Stop"][0], mine) {
		t.Errorf("an entry setup did not add was removed: %v", got)
	}
}

func TestRemoveTakesTheFirstOfEqualGroups(t *testing.T) {
	target := configTarget(t, "codex")
	install(t, target, Options{Binary: "lit"})
	doc := readObject(t, hooksPath(target))
	hooks := doc["hooks"].(map[string]any)
	hooks["Stop"] = append(hooks["Stop"].([]any), litGroup("lit", "stop", 5))
	put(t, hooksPath(target), string(jsonBytes(doc)))
	removeOne(t, target)
	if got := eventGroups(t, target); len(got["Stop"]) != 1 {
		t.Errorf("exactly one of two equal groups should go: %v", got)
	}
}

func TestRemoveWorksWhenHooksJsonIsGone(t *testing.T) {
	target := configTarget(t, "codex")
	install(t, target, Options{})
	if err := os.Remove(hooksPath(target)); err != nil {
		t.Fatal(err)
	}
	if res := removeOne(t, target); !res.Found || exists(hooksPath(target)) {
		t.Errorf("result %+v", res)
	}
}
