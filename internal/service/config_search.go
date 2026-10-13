package service

import (
	"fmt"
	"os"
	"time"
)

// DefaultSearchTimeout is the deadline of every search request.
const DefaultSearchTimeout = 10 * time.Second

// searchEnvironment applies LIT_SEARCH_TIMEOUT over the value from the JSON
// file.
func (c *Config) searchEnvironment() error {
	if v := os.Getenv("LIT_SEARCH_TIMEOUT"); v != "" {
		d, e := time.ParseDuration(v)
		if e != nil {
			return fmt.Errorf("invalid LIT_SEARCH_TIMEOUT %q: a positive Go duration such as 10s", v)
		}
		c.SearchTimeout = Duration(d)
	}
	return nil
}

// validateSearch requires a positive search deadline: no search runs without
// one.
func (c *Config) validateSearch() error {
	if c.SearchTimeout <= 0 {
		return fmt.Errorf("search_timeout must be a positive duration such as \"10s\"")
	}
	return nil
}
