package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Interval exports are off by default and have no directory; export_dir and
// export_interval follow the usual precedence (environment over the JSON
// file over the default) and are validated.
func TestConfigExports(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	for _, k := range []string{"LIT_CONFIG_FILE", "LIT_DATA_DIR", "LIT_LISTEN", "LIT_READ_CONCURRENCY", "LIT_BACKUP_DIR", "LIT_BACKUP_INTERVAL", "LIT_BACKUP_KEEP", "LIT_EXPORT_DIR", "LIT_EXPORT_INTERVAL"} {
		t.Setenv(k, "")
	}
	check := func(name string, dir string, every time.Duration) {
		t.Helper()
		c, e := LoadConfig(nil)
		if e != nil || c.ExportDir != dir || time.Duration(c.ExportInterval) != every {
			t.Fatalf("%s: dir %q interval %v (%v), want %q %v", name, c.ExportDir, time.Duration(c.ExportInterval), e, dir, every)
		}
	}
	check("off by default", "", 0)

	p := filepath.Join(root, "config.json")
	t.Setenv("LIT_CONFIG_FILE", p)
	os.WriteFile(p, []byte(`{"schema_version":1,"export_dir":"/file/export","export_interval":"10m"}`), 0600)
	check("file", "/file/export", 10*time.Minute)
	t.Setenv("LIT_EXPORT_DIR", "/env/export")
	t.Setenv("LIT_EXPORT_INTERVAL", "1m30s")
	check("environment", "/env/export", 90*time.Second)
	t.Setenv("LIT_EXPORT_INTERVAL", "0")
	check("zero turns interval exports off", "/env/export", 0)
	os.WriteFile(p, []byte(`{"schema_version":1,"export_dir":"/file/export"}`), 0600)
	t.Setenv("LIT_EXPORT_INTERVAL", "")
	check("a directory alone exports nothing", "/env/export", 0)

	for _, v := range []string{"-1m", "hourly", "60"} {
		t.Setenv("LIT_EXPORT_INTERVAL", v)
		if _, e := LoadConfig(nil); e == nil {
			t.Fatalf("accepted LIT_EXPORT_INTERVAL=%s", v)
		}
	}
	t.Setenv("LIT_EXPORT_INTERVAL", "")
	t.Setenv("LIT_EXPORT_DIR", "")
	for _, body := range []string{
		`{"schema_version":1,"export_dir":"/x","export_interval":"-5m"}`,
		`{"schema_version":1,"export_dir":"/x","export_interval":600}`,
		`{"schema_version":1,"export_interval":"10m"}`,
	} {
		os.WriteFile(p, []byte(body), 0600)
		if _, e := LoadConfig(nil); e == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}
