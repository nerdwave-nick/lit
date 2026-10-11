package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Backups default to <data dir>/backup, every 24h, keeping 14 snapshots; each
// setting follows the usual precedence (environment over the JSON file over
// the default) and is validated.
func TestConfigBackups(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	for _, k := range []string{"LIT_CONFIG_FILE", "LIT_DATA_DIR", "LIT_LISTEN", "LIT_READ_CONCURRENCY", "LIT_BACKUP_DIR", "LIT_BACKUP_INTERVAL", "LIT_BACKUP_KEEP"} {
		t.Setenv(k, "")
	}
	check := func(name string, args []string, dir string, every time.Duration, keep int) {
		t.Helper()
		c, e := LoadConfig(args)
		if e != nil || c.BackupDir != dir || time.Duration(c.BackupInterval) != every || c.BackupKeep != keep {
			t.Fatalf("%s: dir %q interval %v keep %d (%v), want %q %v %d", name, c.BackupDir, time.Duration(c.BackupInterval), c.BackupKeep, e, dir, every, keep)
		}
	}
	check("default", nil, filepath.Join(root, ".local/share/lit-server", "backup"), 24*time.Hour, 14)
	check("follows --data-dir", []string{"--data-dir", "/srv/lit"}, "/srv/lit/backup", 24*time.Hour, 14)
	t.Setenv("LIT_DATA_DIR", "/env/lit")
	check("follows LIT_DATA_DIR", nil, "/env/lit/backup", 24*time.Hour, 14)

	p := filepath.Join(root, "config.json")
	t.Setenv("LIT_CONFIG_FILE", p)
	os.WriteFile(p, []byte(`{"schema_version":1,"backup_dir":"/file/backup","backup_interval":"90m","backup_keep":3}`), 0600)
	check("file", nil, "/file/backup", 90*time.Minute, 3)
	t.Setenv("LIT_BACKUP_DIR", "/env/backup")
	t.Setenv("LIT_BACKUP_INTERVAL", "1h30m15s")
	t.Setenv("LIT_BACKUP_KEEP", "7")
	check("environment", nil, "/env/backup", 90*time.Minute+15*time.Second, 7)
	t.Setenv("LIT_BACKUP_INTERVAL", "0")
	check("zero turns scheduled backups off", nil, "/env/backup", 0, 7)

	for k, values := range map[string][]string{
		"LIT_BACKUP_INTERVAL": {"-1h", "daily", "24"},
		"LIT_BACKUP_KEEP":     {"0", "-1", "two", "1.5"},
	} {
		for _, v := range values {
			t.Setenv(k, v)
			if _, e := LoadConfig(nil); e == nil {
				t.Fatalf("accepted %s=%s", k, v)
			}
		}
		t.Setenv(k, "")
	}
	t.Setenv("LIT_BACKUP_DIR", "")
	for _, body := range []string{
		`{"schema_version":1,"backup_keep":0}`,
		`{"schema_version":1,"backup_interval":"-5m"}`,
		`{"schema_version":1,"backup_interval":3600}`,
		`{"schema_version":1,"backup_interval":"soon"}`,
	} {
		os.WriteFile(p, []byte(body), 0600)
		if _, e := LoadConfig(nil); e == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}

// The interval is written and read as a Go duration string.
func TestDurationJSON(t *testing.T) {
	b, e := json.Marshal(Duration(36 * time.Hour))
	if e != nil || string(b) != `"36h0m0s"` {
		t.Fatal(string(b), e)
	}
	var d Duration
	if e = json.Unmarshal(b, &d); e != nil || time.Duration(d) != 36*time.Hour {
		t.Fatal(d, e)
	}
}
