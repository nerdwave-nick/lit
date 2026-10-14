package workflowskills

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestMalformedHookSectionsOfTheOwnershipRecord(t *testing.T) {
	fp := strings.Repeat("ab", 32)
	plugin := `"plugin":{"name":"lit-hooks","fingerprint":"` + fp + `"}`
	group := `{"hooks":[{"type":"command","command":"lit hook --host codex stop","timeout":5}]}`
	for name, tc := range map[string]struct{ host, body string }{
		"plugin on codex":           {"codex", `"skills":{},` + plugin},
		"hooks on claude":           {"claude", `"skills":{},"hooks":[{"event":"Stop","group":` + group + `}]`},
		"status line on codex":      {"codex", `"skills":{},"status_line":{"installed":{"type":"command"}}`},
		"plugin with another name":  {"claude", `"skills":{},"plugin":{"name":"other","fingerprint":"` + fp + `"}`},
		"plugin without name":       {"claude", `"skills":{},"plugin":{"fingerprint":"` + fp + `"}`},
		"plugin with bad hash":      {"claude", `"skills":{},"plugin":{"name":"lit-hooks","fingerprint":"zz"}`},
		"plugin without hash":       {"claude", `"skills":{},"plugin":{"name":"lit-hooks"}`},
		"plugin with unknown field": {"claude", `"skills":{},"plugin":{"name":"lit-hooks","fingerprint":"` + fp + `","x":1}`},
		"unknown event":             {"codex", `"skills":{},"hooks":[{"event":"PreToolUse","group":` + group + `}]`},
		"group is a list":           {"codex", `"skills":{},"hooks":[{"event":"Stop","group":[]}]`},
		"group is a string":         {"codex", `"skills":{},"hooks":[{"event":"Stop","group":"x"}]`},
		"group is missing":          {"codex", `"skills":{},"hooks":[{"event":"Stop"}]`},
		"entry with unknown field":  {"codex", `"skills":{},"hooks":[{"event":"Stop","group":` + group + `,"x":1}]`},
		"status line empty":         {"claude", `"skills":{},"status_line":{}`},
		"status line is a string":   {"claude", `"skills":{},"status_line":{"installed":"x"}`},
		"original is a string":      {"claude", `"skills":{},"status_line":{"installed":{"type":"command"},"original":"x"}`},
		"status line unknown field": {"claude", `"skills":{},"status_line":{"installed":{"type":"command"},"x":1}`},
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

func TestWellFormedHookSectionsAreAccepted(t *testing.T) {
	fp := strings.Repeat("ab", 32)
	group := `{"hooks":[{"type":"command","command":"lit hook --host codex stop","timeout":5}]}`
	for name, tc := range map[string]struct{ host, body string }{
		"claude": {"claude", `"skills":{},"plugin":{"name":"lit-hooks","fingerprint":"` + fp + `"},"status_line":{"installed":{"type":"command","command":"lit hook --host claude statusline"},"original":{"type":"command","command":"x.sh"}}`},
		"codex":  {"codex", `"skills":{},"hooks":[{"event":"Stop","group":` + group + `}]`},
	} {
		t.Run(name, func(t *testing.T) {
			// A local run does not look at the hooks, so only the record is
			// validated; a user-scope run would check the plugin directory.
			parent := t.TempDir()
			put(t, filepath.Join(parent, "."+tc.host, recordName), `{"schema_version":1,"host":"`+tc.host+`",`+tc.body+`}`)
			if _, err := Install(Bundle(), localTargets(parent, tc.host)); err != nil {
				t.Fatal(err)
			}
			rec := readRecordFile(t, filepath.Join(parent, "."+tc.host))
			if (tc.host == "claude") != (rec.Plugin != nil) || (tc.host == "codex") != (len(rec.Hooks) == 1) {
				t.Errorf("a local run dropped the hook sections: %+v", rec)
			}
		})
	}
}

func TestUserScopeRunNeedsTheRecordedPluginToBeIntact(t *testing.T) {
	target := configTarget(t, "claude")
	install(t, target, Options{})
	if err := removeAll(filepath.Join(target.Dir, "skills", pluginName)); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, target.Dir)
	if _, err := InstallWith(Bundle(), []Target{target}, Options{}); err == nil || !strings.Contains(err.Error(), "removed/modified") {
		t.Fatalf("error %v", err)
	}
	if snapshot(t, target.Dir) != before {
		t.Error("a refused install changed the configuration")
	}
}
