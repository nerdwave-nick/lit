//go:build !linux

package importer

import (
	"fmt"
	"os"
)

func lock(f *os.File) error   { return fmt.Errorf("lit-migrate is supported only on Linux") }
func unlock(f *os.File) error { return nil }
