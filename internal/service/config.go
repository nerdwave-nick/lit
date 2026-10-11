package service

import (
	"fmt"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
	"net"
	"os"
	"path/filepath"
	"strconv"
)

type Config struct {
	SchemaVersion int             `json:"schema_version"`
	DataDir       string          `json:"data_dir"`
	Listen        string          `json:"listen"`
	Limits        protocol.Limits `json:"limits"`
	TitlePrefixes []string        `json:"title_prefixes"`
	// ReadConcurrency caps concurrent read snapshots (LIT_READ_CONCURRENCY).
	ReadConcurrency int `json:"read_concurrency,omitempty"`
}

func xdg(key, fallback string) string {
	v := os.Getenv(key)
	if filepath.IsAbs(v) {
		return v
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, fallback)
}
func LoadConfig(args []string) (Config, error) {
	c := Config{SchemaVersion: 1, DataDir: filepath.Join(xdg("XDG_DATA_HOME", ".local/share"), "lit-server"), Listen: "127.0.0.1:7411", Limits: protocol.DefaultLimits(), TitlePrefixes: []string{"feat", "fix", "refactor", "docs", "test", "chore", "decision", "research", "spec"}, ReadConcurrency: storage.DefaultReadConcurrency}
	f := map[string]string{}
	for i := 0; i < len(args); i++ {
		k := args[i]
		if k != "--config" && k != "--data-dir" && k != "--listen" {
			return c, fmt.Errorf("unknown server flag %s", k)
		}
		if _, ok := f[k]; ok {
			return c, fmt.Errorf("duplicate flag %s", k)
		}
		i++
		if i == len(args) {
			return c, fmt.Errorf("missing value for %s", k)
		}
		f[k] = args[i]
	}
	path := filepath.Join(xdg("XDG_CONFIG_HOME", ".config"), "lit-server", "config.json")
	explicit := false
	if v := os.Getenv("LIT_CONFIG_FILE"); v != "" {
		path = v
		explicit = true
	}
	if v, ok := f["--config"]; ok {
		path = v
		explicit = true
	}
	b, e := os.ReadFile(path)
	if e != nil {
		if explicit || !os.IsNotExist(e) {
			return c, fmt.Errorf("config: %w", e)
		}
	} else {
		c.SchemaVersion = 0
		if e = protocol.Decode(b, &c); e != nil {
			return c, fmt.Errorf("config: %w", e)
		}
	}
	if v := os.Getenv("LIT_DATA_DIR"); v != "" {
		c.DataDir = v
	}
	if v := os.Getenv("LIT_LISTEN"); v != "" {
		c.Listen = v
	}
	if v := os.Getenv("LIT_READ_CONCURRENCY"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil {
			return c, fmt.Errorf("invalid LIT_READ_CONCURRENCY %q", v)
		}
		c.ReadConcurrency = n
	}
	if v, ok := f["--data-dir"]; ok {
		c.DataDir = v
	}
	if v, ok := f["--listen"]; ok {
		c.Listen = v
	}
	if c.SchemaVersion != 1 {
		return c, fmt.Errorf("unsupported config schema_version")
	}
	if c.DataDir == "" {
		return c, fmt.Errorf("data_dir must not be empty")
	}
	if c.ReadConcurrency < 1 {
		return c, fmt.Errorf("read_concurrency must be a positive integer")
	}
	host, port, e := net.SplitHostPort(c.Listen)
	if e != nil {
		return c, fmt.Errorf("invalid listen address: %w", e)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return c, fmt.Errorf("listen must be an explicit loopback IP address")
	}
	n, e := strconv.Atoi(port)
	if e != nil || n < 0 || n > 65535 {
		return c, fmt.Errorf("invalid listen port")
	}
	l := c.Limits
	if l.ExplicitItems < 1 || l.ExpandedRecords < 1 || l.RequestBytes < 1 || l.SnapshotBytes < 1 || l.DefaultPage < 1 || l.MaxPage < l.DefaultPage {
		return c, fmt.Errorf("invalid limits")
	}
	seen := map[string]bool{}
	if len(c.TitlePrefixes) == 0 {
		return c, fmt.Errorf("empty title_prefixes")
	}
	for _, v := range c.TitlePrefixes {
		if v == "" || seen[v] {
			return c, fmt.Errorf("invalid title prefix")
		}
		for _, r := range v {
			if r < 'a' || r > 'z' {
				return c, fmt.Errorf("invalid title prefix %q", v)
			}
		}
		seen[v] = true
	}
	return c, nil
}
