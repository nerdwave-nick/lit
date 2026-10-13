package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The search deadline defaults to 10s and follows the usual precedence:
// environment over the JSON file over the default. It must be positive, so
// no search can run without one.
func TestConfigSearchTimeout(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	for _, k := range []string{"LIT_CONFIG_FILE", "LIT_DATA_DIR", "LIT_LISTEN", "LIT_READ_CONCURRENCY", "LIT_SEARCH_TIMEOUT"} {
		t.Setenv(k, "")
	}
	check := func(name string, want time.Duration) {
		t.Helper()
		c, e := LoadConfig(nil)
		if e != nil || time.Duration(c.SearchTimeout) != want {
			t.Fatalf("%s: search timeout %v (%v), want %v", name, time.Duration(c.SearchTimeout), e, want)
		}
	}
	check("default", 10*time.Second)
	p := filepath.Join(root, "config.json")
	t.Setenv("LIT_CONFIG_FILE", p)
	os.WriteFile(p, []byte(`{"schema_version":1,"search_timeout":"2500ms"}`), 0600)
	check("file", 2500*time.Millisecond)
	t.Setenv("LIT_SEARCH_TIMEOUT", "1m30s")
	check("environment", 90*time.Second)

	for _, v := range []string{"0", "-1s", "10", "soon"} {
		t.Setenv("LIT_SEARCH_TIMEOUT", v)
		if _, e := LoadConfig(nil); e == nil {
			t.Fatalf("accepted LIT_SEARCH_TIMEOUT=%s", v)
		}
	}
	t.Setenv("LIT_SEARCH_TIMEOUT", "")
	for _, body := range []string{
		`{"schema_version":1,"search_timeout":"0s"}`,
		`{"schema_version":1,"search_timeout":"-5s"}`,
		`{"schema_version":1,"search_timeout":10}`,
	} {
		os.WriteFile(p, []byte(body), 0600)
		if _, e := LoadConfig(nil); e == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}
