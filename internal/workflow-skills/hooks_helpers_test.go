package workflowskills

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// configTarget is an empty agent configuration directory chosen with
// --config-dir, the way a user installs into ~/.claude-work.
func configTarget(t *testing.T, host string) Target {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "."+host+"-work")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return Target{Host: host, Dir: dir, Scope: ScopeConfigDir}
}

// install installs into one target with the default options and fails the test
// on an error.
func install(t *testing.T, target Target, opt Options) Result {
	t.Helper()
	res, err := InstallWith(Bundle(), []Target{target}, opt)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("results %+v", res)
	}
	return res[0]
}

func readAny(t *testing.T, path string) any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%s: %v\n%s", path, err, b)
	}
	return v
}

func readObject(t *testing.T, path string) map[string]any {
	t.Helper()
	m, ok := readAny(t, path).(map[string]any)
	if !ok {
		t.Fatalf("%s is not a JSON object", path)
	}
	return m
}

func parseAny(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("%v: %s", err, s)
	}
	return v
}

func sameJSON(a, b any) bool { return reflect.DeepEqual(a, b) }

func readText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// keysOf returns the keys of a JSON object text in document order.
func keysOf(t *testing.T, text string) []string {
	t.Helper()
	o, err := parseObject([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return o.keys
}

func contains(list []string, part string) bool {
	for _, s := range list {
		if strings.Contains(s, part) {
			return true
		}
	}
	return false
}

// crashAtStep runs an install that dies (panics) when it reaches step, leaving
// the files a killed process would leave. A real kill would also leave the lock
// directory; the panic unwinds through the installer's deferred cleanup instead,
// so the next run starts from the transaction alone.
func crashAtStep(t *testing.T, target Target, step string, opt Options) {
	t.Helper()
	crashed := false
	crashAt = func(s string) {
		if s == step {
			crashed = true
			panic("simulated crash at " + s)
		}
	}
	t.Cleanup(func() { crashAt = func(string) {} })
	func() {
		defer func() { _ = recover() }()
		_, _ = InstallWith(Bundle(), []Target{target}, opt)
	}()
	crashAt = func(string) {}
	if !crashed {
		t.Fatalf("the installer never reached step %q", step)
	}
}

func removeAll(path string) error { return os.RemoveAll(path) }
