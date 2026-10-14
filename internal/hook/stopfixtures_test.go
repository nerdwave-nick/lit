package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecordedStopInputsMatchTheirHostsAndStateTheirProvenance(t *testing.T) {
	set := loadFixtures(t)
	schema := loadSchema(t, "stop.input.schema.json")
	covered := map[string]bool{}
	for _, f := range set.Fixtures {
		if f.Event != "stop" {
			continue
		}
		t.Run(f.File, func(t *testing.T) {
			provenance := set.Provenance[f.Provenance]
			if !strings.Contains(provenance, "Not captured") && !strings.Contains(provenance, "Captured") {
				t.Fatalf("provenance %q must say whether the input was captured", f.Provenance)
			}
			b, e := os.ReadFile(filepath.Join("testdata", f.File))
			if e != nil {
				t.Fatal(e)
			}
			var in map[string]any
			if e = json.Unmarshal(b, &in); e != nil {
				t.Fatal(e)
			}
			active, isBool := in["stop_hook_active"].(bool)
			if in["hook_event_name"] != "Stop" || !isBool {
				t.Fatalf("not a Stop input: %v", in)
			}
			for _, name := range []string{"session_id", "cwd", "transcript_path", "last_assistant_message", "permission_mode"} {
				if _, ok := in[name]; !ok {
					t.Errorf("missing %s", name)
				}
			}
			if f.Host == "codex" {
				if problems := check(schema, schema, in, "input"); len(problems) != 0 {
					t.Fatalf("the Codex schema rejects it: %v", problems)
				}
			}
			covered[f.Host+"/"+map[bool]string{false: "inactive", true: "active"}[active]] = true
		})
	}
	for _, host := range hosts {
		for _, state := range []string{"inactive", "active"} {
			if !covered[host+"/"+state] {
				t.Errorf("no recorded %s Stop input with stop_hook_active %s", host, state)
			}
		}
	}
}
