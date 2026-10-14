package workflowskills

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const (
	litStatusLine  = "lit hook --host claude statusline"
	interimCommand = "~/.claude/statusline.sh"
)

func settingsPath(target Target) string { return filepath.Join(target.Dir, "settings.json") }

func statusLineOf(t *testing.T, target Target) map[string]any {
	t.Helper()
	m, ok := readObject(t, settingsPath(target))["statusLine"].(map[string]any)
	if !ok {
		t.Fatalf("no statusLine in settings.json:\n%s", readText(t, settingsPath(target)))
	}
	return m
}

func TestClaudeInstallWritesTheHooksPlugin(t *testing.T) {
	target := configTarget(t, "claude")
	res := install(t, target, Options{Binary: "lit"})
	if !res.Hooks {
		t.Error("the result does not report the hooks")
	}
	plugin := filepath.Join(target.Dir, "skills", "lit-hooks")
	if m := readObject(t, filepath.Join(plugin, ".claude-plugin", "plugin.json")); m["name"] != "lit-hooks" {
		t.Errorf("plugin manifest %v", m)
	}
	hooks, _ := readObject(t, filepath.Join(plugin, "hooks", "hooks.json"))["hooks"].(map[string]any)
	wantEvents := map[string]string{"SessionStart": "session-start", "SubagentStart": "subagent-start", "Stop": "stop"}
	if len(hooks) != len(wantEvents) {
		t.Fatalf("events %v", hooks)
	}
	for event, sub := range wantEvents {
		groups, _ := hooks[event].([]any)
		if len(groups) != 1 {
			t.Fatalf("%s: %v", event, hooks[event])
		}
		group := groups[0].(map[string]any)
		if _, matcher := group["matcher"]; matcher {
			t.Errorf("%s: a matcher would hide some start sources", event)
		}
		handlers, _ := group["hooks"].([]any)
		if len(handlers) != 1 {
			t.Fatalf("%s: %v", event, group)
		}
		h := handlers[0].(map[string]any)
		if h["type"] != "command" || h["command"] != "lit hook --host claude "+sub {
			t.Errorf("%s: %v", event, h)
		}
		if timeout, _ := h["timeout"].(float64); timeout < 1 || timeout > 30 {
			t.Errorf("%s: timeout %v", event, h["timeout"])
		}
	}
	rec := readRecordFile(t, target.Dir)
	fp, err := fingerprint(plugin)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Plugin == nil || rec.Plugin.Name != "lit-hooks" || rec.Plugin.Fingerprint != fp {
		t.Errorf("record plugin %+v, installed fingerprint %s", rec.Plugin, fp)
	}
	if rec.Hooks != nil {
		t.Errorf("a claude record holds codex hook entries: %v", rec.Hooks)
	}
}

func TestClaudeHookCommandsQuoteTheBinaryPath(t *testing.T) {
	target := configTarget(t, "claude")
	install(t, target, Options{Binary: "/opt/my tools/lit"})
	hooks := readObject(t, filepath.Join(target.Dir, "skills", "lit-hooks", "hooks", "hooks.json"))["hooks"].(map[string]any)
	h := hooks["Stop"].([]any)[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
	if h["command"] != `'/opt/my tools/lit' hook --host claude stop` {
		t.Errorf("stop command %v", h["command"])
	}
	if got := statusLineOf(t, target)["command"]; got != `'/opt/my tools/lit' hook --host claude statusline` {
		t.Errorf("status line command %v", got)
	}
}

func TestClaudeStatusLineIsSetWhenUnset(t *testing.T) {
	target := configTarget(t, "claude")
	res := install(t, target, Options{})
	if res.StatusLine != "set" {
		t.Errorf("status line result %q", res.StatusLine)
	}
	want := parseAny(t, `{"type":"command","command":"`+litStatusLine+`","padding":0,"refreshInterval":2}`)
	if got := statusLineOf(t, target); !sameJSON(got, want) {
		t.Errorf("statusLine %v", got)
	}
	rec := readRecordFile(t, target.Dir)
	if rec.StatusLine == nil || rec.StatusLine.Original != nil || !sameJSON(parseAny(t, string(rec.StatusLine.Installed)), want) {
		t.Errorf("record status line %+v", rec.StatusLine)
	}
}

func TestClaudeInstallEditsOnlyItsOwnMembersOfSettings(t *testing.T) {
	target := configTarget(t, "claude")
	original := `{
    "zeta": 1.0,
    "hooks": {"PostToolUse": [{"matcher": "Skill", "hooks": [{"type": "command", "command": "~/hooks/other.sh"}]}]},
    "alpha": ["<b>", 2, {"x": null}],
    "env": {"A": "a & b"}
}`
	put(t, settingsPath(target), original)
	install(t, target, Options{})
	text := readText(t, settingsPath(target))
	if got := keysOf(t, text); !reflect.DeepEqual(got, []string{"zeta", "hooks", "alpha", "env", "statusLine"}) {
		t.Errorf("member order %v", got)
	}
	for _, want := range []string{`"zeta": 1.0`, `"<b>"`, `"a & b"`, `"~/hooks/other.sh"`} {
		if !strings.Contains(text, want) {
			t.Errorf("settings.json lost %s:\n%s", want, text)
		}
	}
	after := readObject(t, settingsPath(target))
	before := parseAny(t, original).(map[string]any)
	for k, v := range before {
		if !sameJSON(after[k], v) {
			t.Errorf("member %s changed: %v", k, after[k])
		}
	}
}

func TestClaudeStatusLineWrapsAForeignOne(t *testing.T) {
	target := configTarget(t, "claude")
	foreign := `{"type": "command", "command": "` + interimCommand + `", "padding": 1, "refreshInterval": 5}`
	put(t, settingsPath(target), `{"model": "m", "statusLine": `+foreign+`}`)
	res := install(t, target, Options{})
	if res.StatusLine != "wrapped" {
		t.Errorf("status line result %q", res.StatusLine)
	}
	got := statusLineOf(t, target)
	if got["command"] != litStatusLine+" --original '"+interimCommand+"'" || got["type"] != "command" || got["padding"] != float64(1) || got["refreshInterval"] != float64(2) {
		t.Errorf("wrapped statusLine %v", got)
	}
	rec := readRecordFile(t, target.Dir)
	if rec.StatusLine == nil || !sameJSON(parseAny(t, string(rec.StatusLine.Original)), parseAny(t, foreign)) {
		t.Fatalf("the original status line is not recorded exactly: %+v", rec.StatusLine)
	}
	if !sameJSON(parseAny(t, string(rec.StatusLine.Installed)), got) {
		t.Errorf("record installed %s, settings %v", rec.StatusLine.Installed, got)
	}
	if readObject(t, settingsPath(target))["model"] != "m" {
		t.Error("unrelated settings changed")
	}
}

func TestClaudeStatusLineWrapsACommandWithQuotes(t *testing.T) {
	target := configTarget(t, "claude")
	command := `echo 'it'"'"'s' | cat`
	put(t, settingsPath(target), `{"statusLine": {"type":"command","command":`+string(encodeString(command))+`}}`)
	install(t, target, Options{})
	original, has, ok := parseLitStatusLine(statusLineOf(t, target)["command"].(string))
	if !ok || !has || original != command {
		t.Fatalf("wrapped command parsed as %q %v %v", original, has, ok)
	}
}

func TestClaudeStatusLineIsUpdatedWhenAlreadyLits(t *testing.T) {
	target := configTarget(t, "claude")
	foreign := `{"type":"command","command":"` + interimCommand + `","padding":1}`
	put(t, settingsPath(target), `{"statusLine": `+foreign+`}`)
	install(t, target, Options{Binary: "lit"})
	first := readRecordFile(t, target.Dir).StatusLine

	// The user tunes lit's status line; a later run with another binary path
	// rewrites the command, keeps the tuning and keeps the recorded original.
	settings := readObject(t, settingsPath(target))
	sl := settings["statusLine"].(map[string]any)
	sl["refreshInterval"] = float64(7)
	put(t, settingsPath(target), string(jsonBytes(settings)))
	res := install(t, target, Options{Binary: "/opt/lit"})
	if res.StatusLine != "wrapped" {
		t.Errorf("status line result %q", res.StatusLine)
	}
	got := statusLineOf(t, target)
	if got["command"] != "/opt/lit hook --host claude statusline --original '"+interimCommand+"'" || got["refreshInterval"] != float64(7) {
		t.Errorf("statusLine %v", got)
	}
	rec := readRecordFile(t, target.Dir).StatusLine
	if !sameJSON(parseAny(t, string(rec.Original)), parseAny(t, string(first.Original))) || !sameJSON(parseAny(t, string(rec.Installed)), got) {
		t.Errorf("record %+v", rec)
	}

	// An unchanged installation reports that and rewrites nothing.
	before := readText(t, settingsPath(target))
	if res = install(t, target, Options{Binary: "/opt/lit"}); res.StatusLine != "unchanged" {
		t.Errorf("status line result %q", res.StatusLine)
	}
	if readText(t, settingsPath(target)) != before {
		t.Error("settings.json was rewritten without a change")
	}
}

func TestClaudeStatusLineIsAdoptedWhenHandWrittenAsLits(t *testing.T) {
	target := configTarget(t, "claude")
	put(t, settingsPath(target), `{"statusLine": {"type":"command","command":"lit hook --host claude statusline --original 'my-line.sh'"}}`)
	install(t, target, Options{})
	got := statusLineOf(t, target)
	if got["command"] != litStatusLine+" --original my-line.sh" || got["refreshInterval"] != float64(2) || got["padding"] != float64(0) {
		t.Errorf("statusLine %v", got)
	}
	rec := readRecordFile(t, target.Dir).StatusLine
	if rec == nil || !sameJSON(parseAny(t, string(rec.Original)), parseAny(t, `{"type":"command","command":"my-line.sh"}`)) {
		t.Errorf("record %+v", rec)
	}
}

func TestClaudeStatusLineNullCountsAsUnset(t *testing.T) {
	target := configTarget(t, "claude")
	put(t, settingsPath(target), `{"statusLine": null}`)
	if res := install(t, target, Options{}); res.StatusLine != "set" {
		t.Errorf("status line result %q", res.StatusLine)
	}
}

func TestClaudeStatusLineThatCannotBeWrappedIsKept(t *testing.T) {
	for name, foreign := range map[string]string{
		"string":       `"just text"`,
		"no command":   `{"type": "command"}`,
		"other type":   `{"type": "text", "text": "hello"}`,
		"empty":        `{"type": "command", "command": ""}`,
		"number":       `7`,
		"nonstring":    `{"type": "command", "command": 7}`,
		"whitespace":   `{"type": "command", "command": "   "}`,
		"command list": `{"type": "command", "command": ["a"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			target := configTarget(t, "claude")
			put(t, settingsPath(target), `{"statusLine": `+foreign+`}`)
			res := install(t, target, Options{})
			if res.StatusLine != "kept" || !contains(res.Notes, "status line") {
				t.Errorf("result %q, notes %v", res.StatusLine, res.Notes)
			}
			if got := readObject(t, settingsPath(target))["statusLine"]; !sameJSON(got, parseAny(t, foreign)) {
				t.Errorf("statusLine changed to %v", got)
			}
			if rec := readRecordFile(t, target.Dir); rec.StatusLine != nil || rec.Plugin == nil {
				t.Errorf("record %+v", rec)
			}
		})
	}
}

func TestClaudeSecondRunChangesNothing(t *testing.T) {
	target := configTarget(t, "claude")
	put(t, settingsPath(target), `{"statusLine": {"type":"command","command":"`+interimCommand+`"}}`)
	install(t, target, Options{})
	before := snapshot(t, target.Dir)
	settings, record := readText(t, settingsPath(target)), readText(t, filepath.Join(target.Dir, recordName))
	install(t, target, Options{})
	if snapshot(t, target.Dir) != before || readText(t, settingsPath(target)) != settings || readText(t, filepath.Join(target.Dir, recordName)) != record {
		t.Error("a second run changed the installation")
	}
}

func TestLocalScopeInstallsNoHooks(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		t.Run(host, func(t *testing.T) {
			parent := t.TempDir()
			res, err := Install(Bundle(), localTargets(parent, host))
			if err != nil {
				t.Fatal(err)
			}
			if res[0].Hooks || res[0].StatusLine != "" {
				t.Errorf("result %+v", res[0])
			}
			root := filepath.Join(parent, "."+host)
			for _, p := range []string{"settings.json", "hooks.json", filepath.Join("skills", "lit-hooks")} {
				if exists(filepath.Join(root, p)) {
					t.Errorf("a local install wrote %s", p)
				}
			}
			if rec := readRecordFile(t, root); rec.Plugin != nil || rec.Hooks != nil || rec.StatusLine != nil {
				t.Errorf("record %+v", rec)
			}
		})
	}
}

func TestSkillsOnlyRunKeepsTheInstalledHooksAndTheirRecord(t *testing.T) {
	for _, host := range []string{"claude", "codex"} {
		t.Run(host, func(t *testing.T) {
			// A project at the home directory makes the local scope the user
			// configuration: the skills update, the hooks stay as they are.
			parent := t.TempDir()
			target := Target{Host: host, Dir: filepath.Join(parent, "."+host), Scope: ScopeUser}
			install(t, target, Options{})
			hookFile := map[string]string{"claude": "settings.json", "codex": "hooks.json"}[host]
			before, recBefore := readText(t, filepath.Join(target.Dir, hookFile)), readRecordFile(t, target.Dir)
			if _, err := Install(Bundle(), localTargets(parent, host)); err != nil {
				t.Fatal(err)
			}
			if readText(t, filepath.Join(target.Dir, hookFile)) != before {
				t.Errorf("%s changed", hookFile)
			}
			if rec := readRecordFile(t, target.Dir); !reflect.DeepEqual(rec, recBefore) {
				t.Errorf("record %+v, was %+v", rec, recBefore)
			}
			if host == "claude" && !exists(filepath.Join(target.Dir, "skills", "lit-hooks")) {
				t.Error("the plugin was removed")
			}
		})
	}
}

func TestClaudeHooksRefusals(t *testing.T) {
	for _, kind := range []string{"modified plugin", "unowned plugin directory", "settings is an array", "settings is not json", "settings has a duplicate key", "settings is a symlink"} {
		t.Run(kind, func(t *testing.T) {
			target := configTarget(t, "claude")
			switch kind {
			case "modified plugin":
				install(t, target, Options{})
				put(t, filepath.Join(target.Dir, "skills", "lit-hooks", "hooks", "hooks.json"), `{"hooks":{}}`)
			case "unowned plugin directory":
				put(t, filepath.Join(target.Dir, "skills", "lit-hooks", "notes.txt"), "mine")
			case "settings is an array":
				put(t, settingsPath(target), `[1]`)
			case "settings is not json":
				put(t, settingsPath(target), `{"a": 1,}`)
			case "settings has a duplicate key":
				put(t, settingsPath(target), `{"a": 1, "a": 2}`)
			case "settings is a symlink":
				outside := filepath.Join(t.TempDir(), "real.json")
				put(t, outside, `{}`)
				if err := os.Symlink(outside, settingsPath(target)); err != nil {
					t.Skip(err)
				}
			}
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

func TestClaudeInstallCreatesSettingsWhenTheHostNeverWroteOne(t *testing.T) {
	// A user who never ran Claude has no settings.json; setup creates it.
	target := configTarget(t, "claude")
	install(t, target, Options{})
	if !exists(settingsPath(target)) {
		t.Fatal("settings.json was not created")
	}
	m := readObject(t, settingsPath(target))
	if len(m) != 1 || m["statusLine"] == nil {
		t.Errorf("settings.json holds more than the status line: %v", m)
	}
}
