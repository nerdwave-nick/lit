package workflowskills

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestObjectKeepsKeyOrderAndValueTexts(t *testing.T) {
	src := `{"zeta": 1.0, "hooks": {"Stop": [ ]}, "alpha": [1, 2, 3], "html": "<b>&</b>", "big": 1E3, "uni": "é\u00e9"}`
	o, err := parseObject([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"zeta", "hooks", "alpha", "html", "big", "uni"}; !reflect.DeepEqual(o.keys, want) {
		t.Fatalf("keys %v", o.keys)
	}
	out := string(o.bytes())
	if !strings.HasSuffix(out, "}\n") {
		t.Errorf("no trailing newline: %q", out)
	}
	for _, want := range []string{`"zeta": 1.0`, `"big": 1E3`, `"<b>&</b>"`, `"é\u00e9"`, "\n  \"hooks\": {\n    \"Stop\": []\n  }"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lost %q:\n%s", want, out)
		}
	}
	if got := keysOf(t, out); !reflect.DeepEqual(got, o.keys) {
		t.Errorf("round trip keys %v", got)
	}
}

func TestObjectEdits(t *testing.T) {
	o, err := parseObject([]byte(`{"a":1,"b":2,"c":3}`))
	if err != nil {
		t.Fatal(err)
	}
	o.set("b", json.RawMessage(`{"x":true}`))
	o.set("d", json.RawMessage(`"new"`))
	o.del("a")
	o.del("missing")
	if want := []string{"b", "c", "d"}; !reflect.DeepEqual(o.keys, want) {
		t.Fatalf("keys %v", o.keys)
	}
	if v, ok := o.get("b"); !ok || string(v) != `{"x":true}` {
		t.Errorf("b = %s %v", v, ok)
	}
	if _, ok := o.get("a"); ok {
		t.Error("a was deleted")
	}
	if got := parseAny(t, string(o.bytes())); !sameJSON(got, parseAny(t, `{"b":{"x":true},"c":3,"d":"new"}`)) {
		t.Errorf("document %v", got)
	}
	if got := keysOf(t, string(o.bytes())); !reflect.DeepEqual(got, []string{"b", "c", "d"}) {
		t.Errorf("written key order %v", got)
	}
}

func TestParseObjectRefusesWhatIsNotAnObject(t *testing.T) {
	for name, src := range map[string]string{
		"empty":     ``,
		"array":     `[1]`,
		"string":    `"x"`,
		"null":      `null`,
		"truncated": `{"a":1`,
		"trailing":  `{"a":1} {"b":2}`,
		"duplicate": `{"a":1,"a":2}`,
		"comment":   "{\"a\":1} // note",
		"not json":  `hooks`,
	} {
		t.Run(name, func(t *testing.T) {
			if o, err := parseObject([]byte(src)); err == nil {
				t.Fatalf("accepted: %+v", o)
			}
		})
	}
}

func TestNewObjectIsEmptyAndEditable(t *testing.T) {
	o := newObject()
	o.set("hooks", json.RawMessage(`{}`))
	if got := string(o.bytes()); got != "{\n  \"hooks\": {}\n}\n" {
		t.Errorf("%q", got)
	}
}
