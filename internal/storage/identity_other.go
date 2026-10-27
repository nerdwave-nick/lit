//go:build !linux && !darwin

package storage

import "os"

// statIdentity leaves the identity to size and modification time where
// storage is unsupported (see lock_other.go).
func statIdentity(os.FileInfo) fileIdentity { return fileIdentity{} }
