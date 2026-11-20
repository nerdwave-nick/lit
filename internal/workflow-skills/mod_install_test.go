package workflowskills

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const testLit = "/opt/lit/bin/lit"

func modDir(target Target) string { return filepath.Join(target.Dir, "skills", "lit-ui") }

// modFiles lists the files of the installed mod, relative to its directory.
func modFilesOf(t *testing.T, target Target) []string {
	t.Helper()
	var names []string
	err := filepath.WalkDir(modDir(target), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(modDir(target), p)
		names = append(names, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	return names
}

func wantModConfig(t *testing.T, target Target, mode string) {
	t.Helper()
	got := readAny(t, filepath.Join(modDir(target), "config.json"))
	want := parseAny(t, `{"schema_version":1,"status":"`+mode+`","lit":"`+testLit+`"}`)
	if !sameJSON(got, want) {
		t.Errorf("config.json %v, want %v", got, want)
	}
}

func wantModRecorded(t *testing.T, target Target) {
	t.Helper()
	rec := readRecordFile(t, target.Dir)
	fp, err := fingerprint(modDir(target))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Mod == nil || rec.Mod.Name != "lit-ui" || rec.Mod.Fingerprint != fp {
		t.Errorf("record mod %+v, installed fingerprint %s", rec.Mod, fp)
	}
}

func hasStatusLine(t *testing.T, target Target) bool {
	t.Helper()
	if !exists(settingsPath(target)) {
		return false
	}
	_, ok := readObject(t, settingsPath(target))["statusLine"]
	return ok
}

func TestDefaultStatusInstallsTheModAndTheStatusLine(t *testing.T) {
	target := configTarget(t, "claude")
	res := install(t, target, Options{LitPath: testLit})
	if res.Status != "both" || res.Mod != "installed" || res.StatusLine != "set" {
		t.Errorf("result status %q mod %q status line %q", res.Status, res.Mod, res.StatusLine)
	}
	// The mod's source minus its development files (tests, tsconfig, the
	// engine's generated types), plus the generated config.json.
	got := modFilesOf(t, target)
	for _, want := range []string{".claude-plugin/plugin.json", "config.json", "hooks/hooks.json", "hooks/register.tsx", "types/index.d.ts"} {
		if !contains(got, want) {
			t.Errorf("mod file %s missing: %v", want, got)
		}
	}
	for _, name := range got {
		if strings.HasPrefix(name, "tests/") || name == "tsconfig.json" || strings.HasPrefix(name, ".claude-plugin/types/") {
			t.Errorf("development file installed: %s", name)
		}
	}
	src, err := modFS.ReadFile("mods/lit-ui/hooks/register.tsx")
	if err != nil || readText(t, filepath.Join(modDir(target), "hooks", "register.tsx")) != string(src) {
		t.Errorf("register.tsx is not the embedded source: %v", err)
	}
	if m := readObject(t, filepath.Join(modDir(target), ".claude-plugin", "plugin.json")); m["name"] != "lit-ui" {
		t.Errorf("plugin manifest %v", m)
	}
	wantModConfig(t, target, "both")
	wantModRecorded(t, target)
	if !hasStatusLine(t, target) {
		t.Error("both: no status line")
	}
}

func TestStatusLineInstallsNoMod(t *testing.T) {
	target := configTarget(t, "claude")
	res := install(t, target, Options{LitPath: testLit, Status: "line"})
	if res.Status != "line" || res.Mod != "" || res.StatusLine != "set" {
		t.Errorf("result status %q mod %q status line %q", res.Status, res.Mod, res.StatusLine)
	}
	if exists(modDir(target)) {
		t.Error("line: the mod was installed")
	}
	if rec := readRecordFile(t, target.Dir); rec.Mod != nil {
		t.Errorf("line: record mod %+v", rec.Mod)
	}
	if !hasStatusLine(t, target) {
		t.Error("line: no status line")
	}
}

func TestStatusModInstallsNoStatusLine(t *testing.T) {
	target := configTarget(t, "claude")
	res := install(t, target, Options{LitPath: testLit, Status: "mod"})
	if res.Status != "mod" || res.Mod != "installed" || res.StatusLine != "" {
		t.Errorf("result status %q mod %q status line %q", res.Status, res.Mod, res.StatusLine)
	}
	if exists(settingsPath(target)) {
		t.Error("mod: setup created settings.json")
	}
	wantModConfig(t, target, "mod")
	wantModRecorded(t, target)
	if rec := readRecordFile(t, target.Dir); rec.StatusLine != nil || len(rec.CreatedFiles) != 0 {
		t.Errorf("mod: record status line %+v, created %v", rec.StatusLine, rec.CreatedFiles)
	}
}

func TestStatusModLeavesAForeignStatusLine(t *testing.T) {
	target := configTarget(t, "claude")
	original := "{\"statusLine\": {\"type\": \"command\", \"command\": \"" + interimCommand + "\"}}\n"
	put(t, settingsPath(target), original)
	res := install(t, target, Options{LitPath: testLit, Status: "mod"})
	if res.StatusLine != "" {
		t.Errorf("status line result %q", res.StatusLine)
	}
	if readText(t, settingsPath(target)) != original {
		t.Errorf("settings.json changed:\n%s", readText(t, settingsPath(target)))
	}
}

func TestSwitchingToModRestoresTheWrappedStatusLine(t *testing.T) {
	target := configTarget(t, "claude")
	original := "{\"model\": \"m\", \"statusLine\": {\"type\": \"command\", \"command\": \"" + interimCommand + "\", \"padding\": 1}}\n"
	put(t, settingsPath(target), original)
	if res := install(t, target, Options{LitPath: testLit}); res.StatusLine != "wrapped" {
		t.Fatalf("both: status line %q", res.StatusLine)
	}
	res := install(t, target, Options{LitPath: testLit, Status: "mod"})
	if res.StatusLine != "restored" || res.Mod != "installed" {
		t.Errorf("result status line %q mod %q", res.StatusLine, res.Mod)
	}
	if got := readAny(t, settingsPath(target)); !sameJSON(got, parseAny(t, original)) {
		t.Errorf("settings.json %v", got)
	}
	wantModConfig(t, target, "mod")
	wantModRecorded(t, target)
	if rec := readRecordFile(t, target.Dir); rec.StatusLine != nil {
		t.Errorf("record status line %+v", rec.StatusLine)
	}
}

func TestSwitchingToModRemovesLitsOwnStatusLine(t *testing.T) {
	target := configTarget(t, "claude")
	install(t, target, Options{LitPath: testLit, Status: "line"})
	res := install(t, target, Options{LitPath: testLit, Status: "mod"})
	if res.StatusLine != "removed" || res.Mod != "installed" {
		t.Errorf("result status line %q mod %q", res.StatusLine, res.Mod)
	}
	if hasStatusLine(t, target) {
		t.Error("lit's status line is still there")
	}
	// settings.json was setup's and holds nothing else, so it goes too.
	if exists(settingsPath(target)) {
		t.Error("the settings.json setup created is still there")
	}
	if rec := readRecordFile(t, target.Dir); len(rec.CreatedFiles) != 0 {
		t.Errorf("created files %v", rec.CreatedFiles)
	}
}

func TestSwitchingToModKeepsASettingsFileSetupCreatedWhileTheUserAddsToIt(t *testing.T) {
	target := configTarget(t, "claude")
	install(t, target, Options{LitPath: testLit})
	settings := readObject(t, settingsPath(target))
	put(t, settingsPath(target), `{"model":"m","statusLine":`+string(marshalRaw(settings["statusLine"]))+`}`)
	install(t, target, Options{LitPath: testLit, Status: "mod"})
	if got := readAny(t, settingsPath(target)); !sameJSON(got, parseAny(t, `{"model":"m"}`)) {
		t.Errorf("settings.json %v", got)
	}
	if rec := readRecordFile(t, target.Dir); len(rec.CreatedFiles) != 1 {
		t.Errorf("created files %v", rec.CreatedFiles)
	}
	if _, err := Remove([]Target{target}); err != nil {
		t.Fatal(err)
	}
	if got := readAny(t, settingsPath(target)); !sameJSON(got, parseAny(t, `{"model":"m"}`)) {
		t.Errorf("remove changed the user's settings: %v", got)
	}
}

func TestSwitchingToModLeavesAStatusLineTheUserChanged(t *testing.T) {
	target := configTarget(t, "claude")
	install(t, target, Options{LitPath: testLit})
	changed := "{\"statusLine\": {\"type\": \"command\", \"command\": \"mine.sh\"}}\n"
	put(t, settingsPath(target), changed)
	res := install(t, target, Options{LitPath: testLit, Status: "mod"})
	if res.StatusLine != "left" || !contains(res.Warnings, "changed after setup wrote it") {
		t.Errorf("result status line %q warnings %v", res.StatusLine, res.Warnings)
	}
	if readText(t, settingsPath(target)) != changed {
		t.Error("a status line the user changed was edited")
	}
}

func TestSwitchingToLineRemovesTheMod(t *testing.T) {
	target := configTarget(t, "claude")
	install(t, target, Options{LitPath: testLit, Status: "mod"})
	res := install(t, target, Options{LitPath: testLit, Status: "line"})
	if res.Mod != "removed" || res.StatusLine != "set" {
		t.Errorf("result mod %q status line %q", res.Mod, res.StatusLine)
	}
	if exists(modDir(target)) {
		t.Error("the mod is still installed")
	}
	if rec := readRecordFile(t, target.Dir); rec.Mod != nil || rec.StatusLine == nil {
		t.Errorf("record mod %+v status line %+v", rec.Mod, rec.StatusLine)
	}
	// And back: the mod returns and the status line goes.
	install(t, target, Options{LitPath: testLit, Status: "mod"})
	if !exists(modDir(target)) || hasStatusLine(t, target) {
		t.Error("line -> mod did not converge")
	}
}

func TestSwitchingBetweenBothAndModRewritesTheConfig(t *testing.T) {
	target := configTarget(t, "claude")
	install(t, target, Options{LitPath: testLit, Status: "mod"})
	res := install(t, target, Options{LitPath: testLit, Status: "both"})
	if res.Mod != "installed" || res.StatusLine != "set" {
		t.Errorf("result mod %q status line %q", res.Mod, res.StatusLine)
	}
	wantModConfig(t, target, "both")
	wantModRecorded(t, target)
}

func TestRerunningTheSameStatusIsStable(t *testing.T) {
	for _, mode := range []string{"line", "mod", "both"} {
		t.Run(mode, func(t *testing.T) {
			target := configTarget(t, "claude")
			install(t, target, Options{LitPath: testLit, Status: mode})
			before := snapshot(t, target.Dir)
			install(t, target, Options{LitPath: testLit, Status: mode})
			if after := snapshot(t, target.Dir); after != before {
				t.Errorf("a rerun changed the configuration:\n%s\n---\n%s", before, after)
			}
		})
	}
}

func TestAnUnownedLitUIDirectoryIsACollision(t *testing.T) {
	for _, mode := range []string{"mod", "both"} {
		t.Run(mode, func(t *testing.T) {
			target := configTarget(t, "claude")
			put(t, filepath.Join(modDir(target), "mine.txt"), "mine")
			before := snapshot(t, target.Dir)
			_, err := InstallWith(Bundle(), []Target{target}, Options{LitPath: testLit, Status: mode})
			if err == nil || !strings.Contains(err.Error(), "collision: lit-ui") {
				t.Fatalf("error %v", err)
			}
			if snapshot(t, target.Dir) != before {
				t.Error("a refused install changed the configuration")
			}
		})
	}
	t.Run("line", func(t *testing.T) {
		target := configTarget(t, "claude")
		put(t, filepath.Join(modDir(target), "mine.txt"), "mine")
		install(t, target, Options{LitPath: testLit, Status: "line"})
		if readText(t, filepath.Join(modDir(target), "mine.txt")) != "mine" {
			t.Error("line touched a lit-ui directory it does not own")
		}
	})
}

func TestAModifiedModIsRefused(t *testing.T) {
	for _, mode := range []string{"line", "mod", "both"} {
		t.Run(mode, func(t *testing.T) {
			target := configTarget(t, "claude")
			install(t, target, Options{LitPath: testLit})
			put(t, filepath.Join(modDir(target), "hooks", "register.tsx"), "edited")
			before := snapshot(t, target.Dir)
			_, err := InstallWith(Bundle(), []Target{target}, Options{LitPath: testLit, Status: mode})
			if err == nil || !strings.Contains(err.Error(), "lit-ui") {
				t.Fatalf("error %v", err)
			}
			if snapshot(t, target.Dir) != before {
				t.Error("a refused install changed the configuration")
			}
		})
	}
}

func TestUnknownStatusIsRefused(t *testing.T) {
	target := configTarget(t, "claude")
	if _, err := InstallWith(Bundle(), []Target{target}, Options{Status: "pane"}); err == nil || !strings.Contains(err.Error(), "status") {
		t.Fatalf("error %v", err)
	}
	if exists(filepath.Join(target.Dir, recordName)) {
		t.Error("a refused install wrote a record")
	}
}

func TestLocalScopeAndCodexInstallNoMod(t *testing.T) {
	parent := t.TempDir()
	for _, mode := range []string{"mod", "both"} {
		if _, err := InstallWith(Bundle(), localTargets(parent, "claude", "codex"), Options{LitPath: testLit, Status: mode}); err != nil {
			t.Fatal(err)
		}
	}
	for _, host := range []string{"claude", "codex"} {
		dir := filepath.Join(parent, "."+host)
		if exists(filepath.Join(dir, "skills", "lit-ui")) || exists(filepath.Join(dir, "settings.json")) {
			t.Errorf("%s local: mod or status line installed", host)
		}
	}
	codex := configTarget(t, "codex")
	res := install(t, codex, Options{LitPath: testLit, Status: "mod"})
	if exists(filepath.Join(codex.Dir, "skills", "lit-ui")) || res.Mod != "" || res.Status != "" {
		t.Errorf("codex: mod installed or reported: %+v", res)
	}
	if rec := readRecordFile(t, codex.Dir); rec.Mod != nil {
		t.Errorf("codex record mod %+v", rec.Mod)
	}
}

func TestALocalRunKeepsTheRecordedMod(t *testing.T) {
	parent := t.TempDir()
	fp := strings.Repeat("ab", 32)
	put(t, filepath.Join(parent, ".claude", recordName), `{"schema_version":1,"host":"claude","skills":{},"mod":{"name":"lit-ui","fingerprint":"`+fp+`"}}`)
	if _, err := Install(Bundle(), localTargets(parent, "claude")); err != nil {
		t.Fatal(err)
	}
	if rec := readRecordFile(t, filepath.Join(parent, ".claude")); rec.Mod == nil || rec.Mod.Fingerprint != fp {
		t.Errorf("a local run dropped the mod: %+v", rec.Mod)
	}
}

func TestMalformedModEntriesOfTheOwnershipRecord(t *testing.T) {
	fp := strings.Repeat("ab", 32)
	for name, tc := range map[string]struct{ host, body string }{
		"mod on codex":           {"codex", `"skills":{},"mod":{"name":"lit-ui","fingerprint":"` + fp + `"}`},
		"mod with another name":  {"claude", `"skills":{},"mod":{"name":"lit-hooks","fingerprint":"` + fp + `"}`},
		"mod with bad hash":      {"claude", `"skills":{},"mod":{"name":"lit-ui","fingerprint":"zz"}`},
		"mod with unknown field": {"claude", `"skills":{},"mod":{"name":"lit-ui","fingerprint":"` + fp + `","x":1}`},
	} {
		t.Run(name, func(t *testing.T) {
			target := configTarget(t, tc.host)
			put(t, filepath.Join(target.Dir, recordName), `{"schema_version":1,"host":"`+tc.host+`",`+tc.body+`}`)
			before := snapshot(t, target.Dir)
			if _, err := InstallWith(Bundle(), []Target{target}, Options{}); err == nil {
				t.Fatal("accepted")
			}
			if snapshot(t, target.Dir) != before {
				t.Fatal("a refused install changed the configuration")
			}
		})
	}
}

func TestRemoveTakesTheModAway(t *testing.T) {
	target := configTarget(t, "claude")
	install(t, target, Options{LitPath: testLit, Status: "mod"})
	res, err := Remove([]Target{target})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || !res[0].Found || !res[0].Mod || !res[0].Hooks {
		t.Errorf("removal %+v", res)
	}
	if exists(modDir(target)) || exists(filepath.Join(target.Dir, "skills", pluginName)) || exists(filepath.Join(target.Dir, recordName)) {
		t.Error("remove left lit's files")
	}
}

func TestRemoveRefusesAModifiedMod(t *testing.T) {
	target := configTarget(t, "claude")
	install(t, target, Options{LitPath: testLit})
	put(t, filepath.Join(modDir(target), "config.json"), "{}")
	before := snapshot(t, target.Dir)
	if _, err := Remove([]Target{target}); err == nil || !strings.Contains(err.Error(), "lit-ui") {
		t.Fatalf("error %v", err)
	}
	if snapshot(t, target.Dir) != before {
		t.Error("a refused removal changed the configuration")
	}
}

func TestInterruptedStatusSwitchIsRolledBack(t *testing.T) {
	for _, tc := range []struct{ from, to string }{{"both", "mod"}, {"mod", "line"}, {"line", "both"}} {
		// Only a switch to or from mod changes the status line in settings.json.
		steps := []string{"journal", "skills", "file:CLAUDE.md", "file:" + recordName, "applied"}
		if tc.to == "mod" || tc.from == "mod" {
			steps = append(steps, "file:settings.json")
		}
		for _, step := range steps {
			t.Run(tc.from+"-"+tc.to+"/"+step, func(t *testing.T) {
				target := configTarget(t, "claude")
				userConfiguration(t, target)
				install(t, target, Options{LitPath: testLit, Status: tc.from})
				before := filesOnly(t, target.Dir)
				crashAtStep(t, target, step, Options{LitPath: testLit, Status: tc.to})
				if err := recoverInstallation(target.Dir, false); err != nil {
					t.Fatal(err)
				}
				if after := filesOnly(t, target.Dir); after != before {
					t.Fatalf("recovery did not restore the configuration:\n%s\n---\n%s", before, after)
				}
				control := configTarget(t, "claude")
				userConfiguration(t, control)
				install(t, control, Options{LitPath: testLit, Status: tc.from})
				install(t, control, Options{LitPath: testLit, Status: tc.to})
				install(t, target, Options{LitPath: testLit, Status: tc.to})
				if got, want := filesOnly(t, target.Dir), filesOnly(t, control.Dir); got != want {
					t.Fatalf("the recovered switch differs from an uninterrupted one:\n%s\n---\n%s", got, want)
				}
			})
		}
	}
}

func TestOnlyAVersion4JournalMayTouchTheMod(t *testing.T) {
	target := configTarget(t, "claude")
	crashAtStep(t, target, "skills", Options{LitPath: testLit})
	path := filepath.Join(target.Dir, transactionName, "journal.json")
	j := readObject(t, path)
	if j["schema_version"] != float64(4) || !contains(toStrings(j["touched"]), "lit-ui") {
		t.Fatalf("journal %v", j)
	}
	j["schema_version"] = 3
	put(t, path, string(jsonBytes(j)))
	if err := recoverInstallation(target.Dir, false); err == nil || !strings.Contains(err.Error(), "invalid touched skills") {
		t.Fatalf("a version 3 journal touching the mod was accepted: %v", err)
	}
}

func toStrings(v any) []string {
	out := []string{}
	list, _ := v.([]any)
	for _, s := range list {
		str, _ := s.(string)
		out = append(out, str)
	}
	return out
}
