//go:build !linux && !darwin

package storage

import (
	"fmt"
	"os"
)

func lock(f *os.File) error {
	return fmt.Errorf("lit service storage is supported only on Linux and macOS")
}
func unlock(f *os.File) error { return nil }
