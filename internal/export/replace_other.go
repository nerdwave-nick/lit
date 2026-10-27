//go:build !linux && !darwin

package export

import (
	"errors"
	"os"
)

var errUnsupported = errors.New("the Markdown export is supported only on Linux and macOS, like lit-server")

func lock(f *os.File) error      { return errUnsupported }
func unlock(f *os.File) error    { return nil }
func exchange(a, b string) error { return errUnsupported }
