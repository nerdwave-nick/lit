package litclient

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

// Mapping binds a logical session name to its client, service and endpoint.
type Mapping struct {
	SchemaVersion int    `json:"schema_version"`
	ClientID      string `json:"client_id"`
	ServiceID     string `json:"service_id"`
	Endpoint      string `json:"endpoint"`
}

// Pending is the retained evidence of one mutation, written to
// StateDir/pending/<request_id>.json before the request is sent.
type Pending struct {
	SchemaVersion int               `json:"schema_version"`
	RequestID     string            `json:"request_id"`
	ServiceID     string            `json:"service_id"`
	ClientID      string            `json:"client_id"`
	Endpoint      string            `json:"endpoint"`
	Method        string            `json:"method"`
	Path          string            `json:"path"`
	Body          json.RawMessage   `json:"body"`
	Headers       map[string]string `json:"headers"`
	CreatedAt     string            `json:"created_at"`
}

// StateDir returns the private client state directory: LIT_STATE_DIR, or the
// platform default.
func StateDir() (string, error) {
	if v := os.Getenv("LIT_STATE_DIR"); v != "" {
		return v, nil
	}
	home, e := os.UserHomeDir()
	if e != nil {
		return "", e
	}
	return stateDir(runtime.GOOS, home, os.Getenv("XDG_STATE_HOME"), os.Getenv("LOCALAPPDATA"))
}

func stateDir(platform, home, xdgState, localAppData string) (string, error) {
	switch platform {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "lit"), nil
	case "windows":
		v := localAppData
		if v == "" {
			return "", fmt.Errorf("LOCALAPPDATA is unavailable")
		}
		return filepath.Join(v, "lit"), nil
	default:
		v := xdgState
		if !filepath.IsAbs(v) {
			v = filepath.Join(home, ".local", "state")
		}
		return filepath.Join(v, "lit"), nil
	}
}

// MappingPath returns where the named logical session's mapping lives in dir.
func MappingPath(dir, name string) string {
	return filepath.Join(dir, "sessions", base64.RawURLEncoding.EncodeToString([]byte(name))+".json")
}

// ReadMapping reads and validates a session mapping.
func ReadMapping(path string) (Mapping, error) {
	var m Mapping
	b, e := os.ReadFile(path)
	if e != nil {
		return m, e
	}
	if e = protocol.Decode(b, &m); e != nil {
		return m, fmt.Errorf("invalid session mapping: %w", e)
	}
	if m.SchemaVersion != 1 || !protocol.ValidUUID(m.ClientID) || !protocol.ValidUUID(m.ServiceID) || m.Endpoint == "" {
		return m, fmt.Errorf("unsupported or invalid session mapping")
	}
	return m, nil
}

// WriteJSON durably and atomically replaces path with v's JSON encoding.
func WriteJSON(path string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	dir := filepath.Dir(path)
	if e = durableDirectory(dir); e != nil {
		return e
	}
	f, e := os.CreateTemp(dir, ".lit-tmp-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	x := f.Close()
	if e == nil {
		e = x
	}
	if e != nil {
		return e
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return e
	}
	return syncDirectory(dir)
}

// AcquireLock takes an exclusive advisory lock on path, retrying until
// deadline. The returned function releases it.
func AcquireLock(path string, deadline time.Time) (func(), error) {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	for {
		if e = localLock(f); e == nil {
			return func() { localUnlock(f); f.Close() }, nil
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("session mapping is locked by another invocation: %w", e)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func durableDirectory(path string) error {
	if info, e := os.Stat(path); e == nil {
		if !info.IsDir() {
			return fmt.Errorf("not a directory: %s", path)
		}
		return nil
	} else if !os.IsNotExist(e) {
		return e
	}
	parent := filepath.Dir(path)
	if e := durableDirectory(parent); e != nil {
		return e
	}
	if e := os.Mkdir(path, 0700); e != nil && !os.IsExist(e) {
		return e
	}
	if e := syncDirectory(path); e != nil {
		return e
	}
	return syncDirectory(parent)
}
