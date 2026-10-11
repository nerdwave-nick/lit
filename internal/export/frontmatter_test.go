package export

import (
	"reflect"
	"strings"
	"testing"
	"testing/quick"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// decodeFrontMatter parses a front-matter block with a YAML parser and returns
// its keys in order with their values.
func decodeFrontMatter(t *testing.T, text string) ([]string, map[string]any) {
	t.Helper()
	inner, ok := strings.CutPrefix(text, "---\n")
	if !ok || !strings.HasSuffix(inner, "---\n") {
		t.Fatalf("not a front-matter block: %q", text)
	}
	inner = strings.TrimSuffix(inner, "---\n")
	var doc yaml.Node
	if e := yaml.Unmarshal([]byte(inner), &doc); e != nil {
		t.Fatalf("YAML parser refuses %q: %v", inner, e)
	}
	values := map[string]any{}
	if e := yaml.Unmarshal([]byte(inner), &values); e != nil {
		t.Fatal(e)
	}
	var keys []string
	if len(doc.Content) == 1 {
		for i := 0; i < len(doc.Content[0].Content); i += 2 {
			keys = append(keys, doc.Content[0].Content[i].Value)
		}
	}
	return keys, values
}

func TestFrontMatterRoundTripsThroughAYAMLParser(t *testing.T) {
	tricky := []string{
		"plain", "", " leading and trailing ", "quote \" and backslash \\", "colon: hash # dash -",
		"- looks like a list", "[looks, like] {a: map}", "&anchor *alias !tag %directive @at `tick`",
		"yes", "no", "null", "~", "1e3", "0x1F", "true", "line\nbreak", "tab\there", "cr\r\nlf",
		"nul\x00bell\x07esc\x1bdel\x7f", "c1 \u0085\u0080\u009f", "line sep \u2028 para sep \u2029",
		"bom \ufeff", "nonchar \ufffe\uffff", "Straße ünïcode 日本語 🙂", "e\u0301 combining",
	}
	var f frontMatter
	f.str("title", "Title")
	f.int("revision", 42)
	f.list("labels", tricky)
	for i, s := range tricky {
		f.str("k"+string(rune('a'+i)), s)
	}
	f.list("empty", nil)
	f.optional("absent", nil)
	keys, values := decodeFrontMatter(t, f.String())
	wantKeys := []string{"title", "revision", "labels"}
	for i := range tricky {
		wantKeys = append(wantKeys, "k"+string(rune('a'+i)))
	}
	if !reflect.DeepEqual(keys, wantKeys) {
		t.Fatalf("keys %q, want %q (empty and null fields are omitted, order kept)", keys, wantKeys)
	}
	if values["revision"] != 42 || values["title"] != "Title" {
		t.Fatalf("scalars %v %v", values["revision"], values["title"])
	}
	labels, _ := values["labels"].([]any)
	if len(labels) != len(tricky) {
		t.Fatalf("labels %v", values["labels"])
	}
	for i, s := range tricky {
		if labels[i] != s {
			t.Errorf("list item %d = %q, want %q", i, labels[i], s)
		}
		if got := values["k"+string(rune('a'+i))]; got != s {
			t.Errorf("string %d = %q, want %q", i, got, s)
		}
	}
	// Every line is a key, a block sequence item or a delimiter, and every
	// string is double quoted.
	for _, line := range strings.Split(strings.TrimSuffix(f.String(), "\n"), "\n") {
		switch {
		case line == "---", strings.HasPrefix(line, "  - \""), strings.HasSuffix(line, ":"),
			strings.Contains(line, ": \""), line == "revision: 42":
		default:
			t.Errorf("unexpected line %q", line)
		}
	}
}

func TestFrontMatterQuotingRoundTripsAnyValidText(t *testing.T) {
	check := func(s string) bool {
		if !utf8.ValidString(s) {
			return true
		}
		var f frontMatter
		f.str("v", s)
		var got map[string]string
		inner := strings.TrimSuffix(strings.TrimPrefix(f.String(), "---\n"), "---\n")
		if e := yaml.Unmarshal([]byte(inner), &got); e != nil {
			t.Logf("%q: %v", s, e)
			return false
		}
		return got["v"] == s
	}
	if e := quick.Check(check, &quick.Config{MaxCount: 2000}); e != nil {
		t.Fatal(e)
	}
}

func TestFrontMatterReplacesInvalidUTF8(t *testing.T) {
	var f frontMatter
	f.str("v", "a\xffb")
	_, values := decodeFrontMatter(t, f.String())
	if values["v"] != "a\uFFFDb" {
		t.Fatalf("invalid UTF-8 became %q", values["v"])
	}
}
