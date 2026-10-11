package service

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigPrecedenceAndValidation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_DATA_HOME", "relative-ignored")
	for _, k := range []string{"LIT_CONFIG_FILE", "LIT_DATA_DIR", "LIT_LISTEN", "LIT_READ_CONCURRENCY"} {
		t.Setenv(k, "")
	}
	c, e := LoadConfig(nil)
	if e != nil || c.Listen != "127.0.0.1:7411" || c.DataDir != filepath.Join(root, ".local/share/lit-server") {
		t.Fatal(c, e)
	}
	p := filepath.Join(root, "service.json")
	os.WriteFile(p, []byte(`{"schema_version":1,"data_dir":"/from-file","listen":"127.0.0.1:7400"}`), 0600)
	t.Setenv("LIT_CONFIG_FILE", p)
	t.Setenv("LIT_DATA_DIR", "/from-env")
	t.Setenv("LIT_LISTEN", "127.0.0.1:7401")
	c, e = LoadConfig(nil)
	if e != nil || c.DataDir != "/from-env" || c.Listen != "127.0.0.1:7401" {
		t.Fatal(c, e)
	}
	c, e = LoadConfig([]string{"--data-dir", "/from-flag", "--listen", "127.0.0.1:7402"})
	if e != nil || c.DataDir != "/from-flag" || c.Listen != "127.0.0.1:7402" {
		t.Fatal(c, e)
	}
	for _, body := range []string{`{}`, `{"schema_version":2}`, `{"schema_version":1,"unknown":true}`, `{"schema_version":1,"schema_version":1}`, `broken`, `{"schema_version":1,"limits":{"request_bytes":0}}`} {
		os.WriteFile(p, []byte(body), 0600)
		if _, e = LoadConfig(nil); e == nil {
			t.Fatalf("accepted invalid config %s", body)
		}
	}
	if _, e = LoadConfig([]string{"--config", filepath.Join(root, "absent")}); e == nil {
		t.Fatal("accepted explicitly missing config")
	}
}

// The read cap defaults to 8 and follows the usual precedence: environment
// over the JSON file over the default. It must be a positive integer.
func TestConfigReadConcurrency(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	for _, k := range []string{"LIT_CONFIG_FILE", "LIT_DATA_DIR", "LIT_LISTEN", "LIT_READ_CONCURRENCY"} {
		t.Setenv(k, "")
	}
	if c, e := LoadConfig(nil); e != nil || c.ReadConcurrency != 8 {
		t.Fatal("default", c.ReadConcurrency, e)
	}
	p := filepath.Join(root, "config.json")
	t.Setenv("LIT_CONFIG_FILE", p)
	os.WriteFile(p, []byte(`{"schema_version":1,"read_concurrency":3}`), 0600)
	if c, e := LoadConfig(nil); e != nil || c.ReadConcurrency != 3 {
		t.Fatal("file", c.ReadConcurrency, e)
	}
	t.Setenv("LIT_READ_CONCURRENCY", "5")
	if c, e := LoadConfig(nil); e != nil || c.ReadConcurrency != 5 {
		t.Fatal("environment", c.ReadConcurrency, e)
	}
	for _, v := range []string{"0", "-1", "two", "1.5"} {
		t.Setenv("LIT_READ_CONCURRENCY", v)
		if _, e := LoadConfig(nil); e == nil {
			t.Fatalf("accepted LIT_READ_CONCURRENCY=%s", v)
		}
	}
	t.Setenv("LIT_READ_CONCURRENCY", "")
	for _, body := range []string{`{"schema_version":1,"read_concurrency":0}`, `{"schema_version":1,"read_concurrency":-2}`} {
		os.WriteFile(p, []byte(body), 0600)
		if _, e := LoadConfig(nil); e == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}

func TestConfigDefaultDirectoryDiscovery(t *testing.T) {
	for _, relative := range []bool{false, true} {
		t.Run(fmt.Sprint(relative), func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("HOME", root)
			t.Setenv("USERPROFILE", root)
			for _, key := range []string{"LIT_CONFIG_FILE", "LIT_DATA_DIR", "LIT_LISTEN", "LIT_READ_CONCURRENCY"} {
				t.Setenv(key, "")
			}
			configBase, dataBase := filepath.Join(root, "config"), filepath.Join(root, "data")
			t.Setenv("XDG_CONFIG_HOME", configBase)
			t.Setenv("XDG_DATA_HOME", dataBase)
			if relative {
				t.Setenv("XDG_CONFIG_HOME", "relative")
				t.Setenv("XDG_DATA_HOME", "relative")
				configBase, dataBase = filepath.Join(root, ".config"), filepath.Join(root, ".local", "share")
			}
			// An unrelated/old config must not be discovered under the client's name.
			old := filepath.Join(configBase, "lit", "config.json")
			if err := os.MkdirAll(filepath.Dir(old), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(old, []byte("invalid config"), 0600); err != nil {
				t.Fatal(err)
			}
			c, err := LoadConfig(nil)
			if err != nil || c.DataDir != filepath.Join(dataBase, "lit-server") {
				t.Fatalf("defaults = %+v, %v", c, err)
			}
			current := filepath.Join(configBase, "lit-server", "config.json")
			if err := os.MkdirAll(filepath.Dir(current), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(current, []byte(`{"schema_version":1,"listen":"127.0.0.1:7422"}`), 0600); err != nil {
				t.Fatal(err)
			}
			c, err = LoadConfig(nil)
			if err != nil || c.Listen != "127.0.0.1:7422" || c.DataDir != filepath.Join(dataBase, "lit-server") {
				t.Fatalf("discovery = %+v, %v", c, err)
			}
			override := filepath.Join(root, "explicit.json")
			if err := os.WriteFile(override, []byte(`{"schema_version":1,"listen":"127.0.0.1:7423"}`), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("LIT_CONFIG_FILE", override)
			c, err = LoadConfig(nil)
			if err != nil || c.Listen != "127.0.0.1:7423" {
				t.Fatalf("env override = %+v, %v", c, err)
			}
			c, err = LoadConfig([]string{"--config", current})
			if err != nil || c.Listen != "127.0.0.1:7422" {
				t.Fatalf("flag override = %+v, %v", c, err)
			}
		})
	}
}
