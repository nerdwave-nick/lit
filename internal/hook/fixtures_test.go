package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// check validates v, a decoded JSON value, against the subset of JSON Schema
// the embedded Codex hook schemas use.
func check(root, schema map[string]any, v any, path string) []string {
	var problems []string
	if ref, ok := schema["$ref"].(string); ok {
		name := strings.TrimPrefix(ref, "#/definitions/")
		return check(root, root["definitions"].(map[string]any)[name].(map[string]any), v, path)
	}
	for _, sub := range asList(schema["allOf"]) {
		problems = append(problems, check(root, sub.(map[string]any), v, path)...)
	}
	if want, ok := schema["const"]; ok && v != want {
		problems = append(problems, path+": want "+want.(string))
	}
	if enum, ok := schema["enum"]; ok {
		found := false
		for _, e := range enum.([]any) {
			found = found || e == v
		}
		if !found {
			problems = append(problems, path+": not an allowed value")
		}
	}
	if types, ok := schema["type"]; ok {
		matched := false
		for _, name := range asList(types) {
			matched = matched || jsonType(v) == name
		}
		if !matched {
			problems = append(problems, path+": wrong type "+jsonType(v))
		}
	}
	object, isObject := v.(map[string]any)
	if !isObject {
		return problems
	}
	properties, _ := schema["properties"].(map[string]any)
	for _, name := range asList(schema["required"]) {
		if _, ok := object[name.(string)]; !ok {
			problems = append(problems, path+": missing "+name.(string))
		}
	}
	for name, value := range object {
		sub, known := properties[name].(map[string]any)
		switch {
		case known:
			problems = append(problems, check(root, sub, value, path+"."+name)...)
		case schema["additionalProperties"] == false:
			problems = append(problems, path+": unexpected "+name)
		}
	}
	return problems
}

func asList(v any) []any {
	switch v := v.(type) {
	case []any:
		return v
	case nil:
		return nil
	}
	return []any{v}
}

func jsonType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case map[string]any:
		return "object"
	}
	return "array"
}

func loadSchema(t *testing.T, name string) map[string]any {
	t.Helper()
	b, e := os.ReadFile(filepath.Join("testdata", "codex", name))
	if e != nil {
		t.Fatal(e)
	}
	var schema map[string]any
	if e = json.Unmarshal(b, &schema); e != nil {
		t.Fatal(e)
	}
	return schema
}

func TestRecordedInputsMatchTheirHostsAndStateTheirProvenance(t *testing.T) {
	set := loadFixtures(t)
	schema := loadSchema(t, "session-start.input.schema.json")
	covered := map[string]bool{}
	for _, f := range set.Fixtures {
		if f.Event != "session-start" {
			continue // each other event's inputs are checked by that event's own test
		}
		t.Run(f.File, func(t *testing.T) {
			if set.Provenance[f.Provenance] == "" {
				t.Fatalf("no provenance %q", f.Provenance)
			}
			if !strings.Contains(set.Provenance[f.Provenance], "Not captured") && !strings.Contains(set.Provenance[f.Provenance], "Captured") {
				t.Fatal("provenance must say whether the input was captured")
			}
			b, e := os.ReadFile(filepath.Join("testdata", f.File))
			if e != nil {
				t.Fatal(e)
			}
			var in map[string]any
			if e = json.Unmarshal(b, &in); e != nil {
				t.Fatal(e)
			}
			if in["hook_event_name"] != "SessionStart" || in["source"] != f.Source {
				t.Fatalf("manifest and file disagree: %v", in)
			}
			for _, name := range []string{"session_id", "cwd", "transcript_path"} {
				if _, ok := in[name]; !ok {
					t.Errorf("missing %s", name)
				}
			}
			if f.Host == "codex" {
				if problems := check(schema, schema, in, "input"); len(problems) != 0 {
					t.Fatalf("the Codex schema rejects it: %v", problems)
				}
			}
			covered[f.Host+"/"+f.Source] = true
		})
	}
	for _, host := range hosts {
		for _, source := range []string{"startup", "resume", "clear", "fork", "compact"} {
			if !covered[host+"/"+source] {
				t.Errorf("no recorded %s input for source %s", host, source)
			}
		}
	}
}

func TestOutputsMatchTheHostSchemas(t *testing.T) {
	schema := loadSchema(t, "session-start.output.schema.json")
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			g := newRig(t, nil)
			in, _ := input(t, host, "startup")
			r := g.hook(host, "session-start", in, Options{})
			var out map[string]any
			if e := json.Unmarshal([]byte(r.Stdout), &out); e != nil {
				t.Fatal(e)
			}
			if problems := check(schema, schema, out, "output"); len(problems) != 0 {
				t.Fatalf("the schema rejects %s: %v", r.Stdout, problems)
			}
			if out["hookSpecificOutput"].(map[string]any)["additionalContext"] == "" {
				t.Fatal("no additionalContext")
			}
		})
	}
}
