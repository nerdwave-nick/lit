package service

import (
	"fmt"
	"os"
	"time"
)

// exportEnvironment applies LIT_EXPORT_DIR and LIT_EXPORT_INTERVAL over the
// values from the JSON file.
func (c *Config) exportEnvironment() error {
	if v := os.Getenv("LIT_EXPORT_DIR"); v != "" {
		c.ExportDir = v
	}
	if v := os.Getenv("LIT_EXPORT_INTERVAL"); v != "" {
		d, e := time.ParseDuration(v)
		if e != nil {
			return fmt.Errorf("invalid LIT_EXPORT_INTERVAL %q: a Go duration such as 10m, or 0 for no interval exports", v)
		}
		c.ExportInterval = Duration(d)
	}
	return nil
}

// validateExports checks the export settings: interval exports, off with
// the default interval 0, need a directory.
func (c *Config) validateExports() error {
	if c.ExportInterval < 0 {
		return fmt.Errorf("export_interval must not be negative (0 turns interval exports off)")
	}
	if c.ExportInterval > 0 && c.ExportDir == "" {
		return fmt.Errorf("export_interval needs export_dir (or LIT_EXPORT_DIR)")
	}
	return nil
}
