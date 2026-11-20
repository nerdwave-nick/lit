package checkoutcontext

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/nerdwave-nick/lit/internal/clientendpoint"
	"github.com/nerdwave-nick/lit/internal/protocol"
)

const (
	schemaVersion  = 1
	maxRecordBytes = 64 << 10
	maxTitleBytes  = 512
)

// Preference is the project remembered for a checkout. ServiceID is the
// identity of the lit-server it belongs to and ProjectID the identity of the
// project there; Endpoint is only an address (a different address reaching the
// same service is the same preference) and ProjectTitle a display name that
// the project's owner may since have renamed. None of them is validated
// against a service here.
type Preference struct {
	ServiceID    string
	ProjectID    string
	ProjectTitle string
	Endpoint     string
}

// Record is a stored preference with its checkout and save time.
type Record struct {
	Checkout Checkout
	Preference
	SavedAt time.Time
}

var (
	// ErrCorrupt marks a stored record that cannot be decoded or fails validation.
	ErrCorrupt = errors.New("checkout preference is damaged")
	// ErrUnsupported marks a stored record with an unsupported schema version.
	ErrUnsupported = errors.New("checkout preference has an unsupported version")
	// ErrLocked marks a preference another invocation holds past the timeout.
	ErrLocked = errors.New("checkout preference is locked by another invocation")
	// ErrInvalid marks a Preference or Checkout that cannot be stored.
	ErrInvalid = errors.New("invalid checkout preference")
)

// RecordError describes a damaged or unsupported stored record. Kind is
// ErrCorrupt or ErrUnsupported, which errors.Is finds; the record itself is
// left in place for inspection until it is cleared.
type RecordError struct {
	Path   string
	Kind   error
	Reason string
}

func (e *RecordError) Error() string { return fmt.Sprintf("%s: %s: %s", e.Kind, e.Path, e.Reason) }
func (e *RecordError) Unwrap() error { return e.Kind }

// diskRecord is version 1 of the stored record. It holds project context only:
// no client identity, claim token or session name.
type diskRecord struct {
	SchemaVersion int    `json:"schema_version"`
	Checkout      string `json:"checkout"`
	ServiceID     string `json:"service_id"`
	ProjectID     string `json:"project_id"`
	ProjectTitle  string `json:"project_title"`
	Endpoint      string `json:"endpoint"`
	SavedAt       string `json:"saved_at"`
}

// normalize validates p and returns it with its endpoint in canonical form.
func (p Preference) normalize() (Preference, error) {
	bad := func(format string, args ...any) (Preference, error) {
		return Preference{}, fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
	}
	switch {
	case !protocol.ValidUUID(p.ServiceID):
		return bad("service_id must be a lower-case UUID")
	case !protocol.ValidUUID(p.ProjectID):
		return bad("project_id must be a lower-case UUID")
	case p.ProjectTitle == "" || len(p.ProjectTitle) > maxTitleBytes || !utf8.ValidString(p.ProjectTitle):
		return bad("project_title must be nonempty UTF-8 of at most %d bytes", maxTitleBytes)
	}
	for _, r := range p.ProjectTitle {
		if unicode.IsControl(r) {
			return bad("project_title must not contain control characters")
		}
	}
	endpoint, err := clientendpoint.Resolve(p.Endpoint, true, "", "")
	if err != nil {
		return bad("endpoint: %v", err)
	}
	p.Endpoint = endpoint
	return p, nil
}

// decode reads a stored record for c. A record of another schema version is
// unsupported whatever else it holds; everything else that is not exactly
// version 1 is corrupt.
func decode(path string, c Checkout, b []byte) (Record, error) {
	fail := func(kind error, format string, args ...any) (Record, error) {
		return Record{}, &RecordError{Path: path, Kind: kind, Reason: fmt.Sprintf(format, args...)}
	}
	var fields map[string]json.RawMessage
	if err := protocol.Decode(b, &fields); err != nil {
		return fail(ErrCorrupt, "%v", err)
	}
	raw, ok := fields["schema_version"]
	if !ok {
		return fail(ErrCorrupt, "missing schema_version")
	}
	var version int
	if err := json.Unmarshal(raw, &version); err != nil {
		return fail(ErrCorrupt, "schema_version must be an integer")
	}
	if version != schemaVersion {
		return fail(ErrUnsupported, "schema version %d (this lit reads version %d)", version, schemaVersion)
	}
	var d diskRecord
	if err := protocol.Decode(b, &d); err != nil {
		return fail(ErrCorrupt, "%v", err)
	}
	if d.Checkout != c.Path {
		return fail(ErrCorrupt, "record belongs to checkout %q, not %q", d.Checkout, c.Path)
	}
	p, err := Preference{ServiceID: d.ServiceID, ProjectID: d.ProjectID, ProjectTitle: d.ProjectTitle, Endpoint: d.Endpoint}.normalize()
	if err != nil || p.Endpoint != d.Endpoint {
		if err == nil {
			err = errors.New("endpoint is not in canonical form")
		}
		return fail(ErrCorrupt, "%v", err)
	}
	saved, err := time.Parse(time.RFC3339Nano, d.SavedAt)
	if err != nil {
		return fail(ErrCorrupt, "saved_at: %v", err)
	}
	return Record{Checkout: c, Preference: p, SavedAt: saved.UTC()}, nil
}

func (r Record) encode() diskRecord {
	return diskRecord{SchemaVersion: schemaVersion, Checkout: r.Checkout.Path, ServiceID: r.ServiceID, ProjectID: r.ProjectID,
		ProjectTitle: r.ProjectTitle, Endpoint: r.Endpoint, SavedAt: r.SavedAt.UTC().Format(time.RFC3339Nano)}
}

// valid reports whether c can name a record: an absolute path.
func (c Checkout) valid() error {
	if !filepath.IsAbs(c.Path) {
		return fmt.Errorf("%w: checkout path must be absolute: %q", ErrInvalid, c.Path)
	}
	return nil
}
