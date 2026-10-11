//go:build !linux

// Frozen copy of internal/store at c5af73e; see store.go.

package legacystore

import (
	"fmt"
	"os"
)

func lock(f *os.File) error   { return fmt.Errorf("lit service storage is supported only on Linux") }
func unlock(f *os.File) error { return nil }
