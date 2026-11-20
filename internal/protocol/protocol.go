// Package protocol contains transport types shared by the service and CLI.
package protocol

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

const Version = 1

var ReleaseVersion = "dev"

type Actor struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}
type Runtime struct {
	Vendor    string `json:"vendor,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}
type Client struct {
	SchemaVersion int     `json:"schema_version"`
	ClientID      string  `json:"client_id"`
	StateRevision int64   `json:"state_revision"`
	Status        string  `json:"status"`
	Actor         Actor   `json:"actor"`
	ProjectID     *string `json:"project_id"`
	Runtime       Runtime `json:"runtime"`
	OutputFormat  *string `json:"output_format"`
	// Stage is the workflow stage this client is working in, a label for
	// display (see ValidStage), or nil when none is set.
	Stage *string `json:"stage"`
}
type Limits struct {
	ExplicitItems   int   `json:"explicit_items"`
	ExpandedRecords int   `json:"expanded_records"`
	RequestBytes    int64 `json:"request_bytes"`
	SnapshotBytes   int64 `json:"snapshot_bytes"`
	DefaultPage     int   `json:"default_page"`
	MaxPage         int   `json:"max_page"`
}

func DefaultLimits() Limits { return Limits{1000, 10000, 32 << 20, 32 << 20, 100, 1000} }

type Meta struct {
	ServiceID     string         `json:"service_id"`
	APIMajor      int            `json:"api_major"`
	Version       string         `json:"version"`
	Limits        Limits         `json:"limits"`
	Timing        map[string]int `json:"timing"`
	TitlePrefixes []string       `json:"title_prefixes"`
}
type Response struct {
	Data       any     `json:"data,omitempty"`
	Items      []any   `json:"items,omitempty"`
	NextCursor *string `json:"next_cursor"`
	ServerTime string  `json:"server_time"`
}
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details"`
	Status  int    `json:"-"`
}

func (e *Error) Error() string { return e.Message }
func E(status int, code, message string) *Error {
	return &Error{Code: code, Message: message, Status: status, Details: map[string]any{}}
}
func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
func UUID() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	s := hex.EncodeToString(b[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}
func ValidUUID(s string) bool {
	if len(s) != 36 || s != strings.ToLower(s) {
		return false
	}
	for _, i := range []int{8, 13, 18, 23} {
		if s[i] != '-' {
			return false
		}
	}
	_, e := hex.DecodeString(strings.ReplaceAll(s, "-", ""))
	return e == nil
}

// Decode rejects unknown fields, duplicate keys at every level, and trailing JSON.
func Decode(b []byte, v any) error {
	if !utf8.Valid(b) {
		return fmt.Errorf("JSON input must be valid UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	if e := unique(d); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	return d.Decode(v)
}
func unique(d *json.Decoder) error {
	t, e := d.Token()
	if e != nil {
		return e
	}
	if x, ok := t.(json.Delim); ok {
		switch x {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return e
				}
				s := k.(string)
				if seen[s] {
					return fmt.Errorf("duplicate JSON key %q", s)
				}
				seen[s] = true
				if e = unique(d); e != nil {
					return e
				}
			}
		case '[':
			for d.More() {
				if e = unique(d); e != nil {
					return e
				}
			}
		default:
			return fmt.Errorf("unexpected delimiter")
		}
		_, e = d.Token()
		return e
	}
	return nil
}
func Write(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Response{Data: data, ServerTime: Now()})
}
func WriteError(w http.ResponseWriter, e *Error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.Status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": e, "server_time": Now()})
}
