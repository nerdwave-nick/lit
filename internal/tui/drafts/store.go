package drafts

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// Store is the drafts directory of one client state directory.
type Store struct {
	// Root holds one directory per project.
	Root string
}

// New returns the store in stateDir (litclient.StateDir), beside the
// instance registry: <state>/tui/drafts.
func New(stateDir string) Store { return Store{Root: filepath.Join(stateDir, "tui", "drafts")} }

// Dir is project's drafts directory.
func (s Store) Dir(project string) string { return filepath.Join(s.Root, project) }

const sidecars = ".lit" // the sidecar directory inside a project's directory

func (s Store) sidecar(project string, kind Kind, record string) string {
	return filepath.Join(s.Dir(project), sidecars, string(kind)+"-"+record+".json")
}

func valid(d Draft) error {
	switch {
	case d.Kind == "" || strings.ContainsAny(string(d.Kind), `/\.`):
		return protocol.E(400, "invalid_arguments", "a draft needs a kind")
	case !protocol.ValidUUID(d.ProjectID) || !protocol.ValidUUID(d.RecordID) || !protocol.ValidUUID(d.IssueID):
		return protocol.E(400, "invalid_arguments", "a draft needs project, record and issue UUIDs")
	}
	return nil
}

func localError(what string, err error) error {
	return protocol.E(500, "local_state_error", what+": "+err.Error())
}

// Create records d and writes its file with exactly content. It never
// overwrites: a record that already has a draft of d's kind is refused with
// draft_exists, and a file that already has the draft's name is left alone.
// It sets the file name, schema version and times.
func (s Store) Create(d Draft, content string) (Draft, error) {
	if err := valid(d); err != nil {
		return d, err
	}
	d.dir, d.SchemaVersion = s.Dir(d.ProjectID), SchemaVersion
	d.CreatedAt = protocol.Now()
	d.UpdatedAt = d.CreatedAt
	if err := os.MkdirAll(filepath.Join(d.dir, sidecars), 0o700); err != nil {
		return d, localError("cannot create the drafts directory", err)
	}
	base := Slug(d.Title) + "--"
	d.File = base + d.RecordID[:8] + ".md"
	if _, err := os.Lstat(d.Path()); err == nil {
		d.File = base + d.RecordID + ".md"
	}
	side := s.sidecar(d.ProjectID, d.Kind, d.RecordID)
	if err := writeNew(side, d); err != nil {
		if errors.Is(err, os.ErrExist) {
			return d, errExists("this record")
		}
		return d, localError("cannot record the draft", err)
	}
	f, err := os.OpenFile(d.Path(), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		_, err = f.WriteString(content)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(d.Path())
		}
	}
	if err != nil {
		_ = os.Remove(side)
		return d, localError("cannot write the draft file", err)
	}
	return d, nil
}

// writeNew writes v to path atomically, refusing (os.ErrExist) when path
// exists: the complete file is linked into place, which fails rather than
// replaces.
func writeNew(path string, v any) error {
	tmp := filepath.Join(filepath.Dir(path), ".tmp-"+protocol.UUID())
	if err := litclient.WriteJSON(tmp, v); err != nil {
		return err
	}
	defer os.Remove(tmp)
	return os.Link(tmp, path)
}

// Find returns the draft of kind for record in project.
func (s Store) Find(project string, kind Kind, record string) (Draft, bool, error) {
	if err := valid(Draft{Kind: kind, ProjectID: project, RecordID: record, IssueID: record}); err != nil {
		return Draft{}, false, err
	}
	d, err := s.read(s.sidecar(project, kind, record))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return Draft{}, false, nil
	case err != nil:
		return Draft{}, false, err
	}
	return d, true, nil
}

func (s Store) read(path string) (Draft, error) {
	var d Draft
	b, err := os.ReadFile(path)
	if err != nil {
		return d, err
	}
	if err := protocol.Decode(b, &d); err != nil {
		return d, fmt.Errorf("draft sidecar %s: %w", path, err)
	}
	want := string(d.Kind) + "-" + d.RecordID + ".json"
	if d.SchemaVersion != SchemaVersion || valid(d) != nil || filepath.Base(path) != want || d.File == "" || d.File != filepath.Base(d.File) ||
		filepath.Base(filepath.Dir(filepath.Dir(path))) != d.ProjectID {
		return d, fmt.Errorf("draft sidecar %s: unsupported or invalid", path)
	}
	d.dir = filepath.Dir(filepath.Dir(path))
	return d, nil
}

// List returns project's drafts, oldest first. Only sidecars identify
// drafts: other files, such as editor swap and backup files, are ignored. A
// sidecar that cannot be read is reported in the error; the others are still
// returned.
func (s Store) List(project string) ([]Draft, error) {
	if !protocol.ValidUUID(project) {
		return nil, protocol.E(400, "invalid_arguments", "a project UUID is required")
	}
	entries, err := os.ReadDir(filepath.Join(s.Dir(project), sidecars))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, localError("cannot list drafts", err)
	}
	var out []Draft
	var problems []error
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".json") {
			continue
		}
		d, err := s.read(filepath.Join(s.Dir(project), sidecars, name))
		if err != nil {
			problems = append(problems, err)
			continue
		}
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return out[i].key() < out[j].key()
	})
	return out, errors.Join(problems...)
}

// ListAll returns the drafts of every project.
func (s Store) ListAll() ([]Draft, error) {
	entries, err := os.ReadDir(s.Root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, localError("cannot list drafts", err)
	}
	var out []Draft
	var problems []error
	for _, e := range entries {
		if e.IsDir() && protocol.ValidUUID(e.Name()) {
			got, err := s.List(e.Name())
			out = append(out, got...)
			if err != nil {
				problems = append(problems, err)
			}
		}
	}
	return out, errors.Join(problems...)
}

// Read returns the draft file's content, exactly.
func (s Store) Read(d Draft) (string, error) {
	b, err := os.ReadFile(d.Path())
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "", errMissing(d.Path())
	case err != nil:
		return "", localError("cannot read the draft", err)
	}
	return string(b), nil
}

// Save rewrites d's sidecar (after a reload or a claim change); its content
// file is left alone.
func (s Store) Save(d Draft) (Draft, error) {
	if err := valid(d); err != nil {
		return d, err
	}
	d.UpdatedAt = protocol.Now()
	if err := litclient.WriteJSON(s.sidecar(d.ProjectID, d.Kind, d.RecordID), d); err != nil {
		return d, localError("cannot record the draft", err)
	}
	return d, nil
}

// Discard removes the draft: its file, its editor leftovers (swap, backup
// and lock files named after it) and then its sidecar. Discarding a draft
// that is already gone succeeds.
func (s Store) Discard(d Draft) error {
	if err := valid(d); err != nil {
		return err
	}
	dir := s.Dir(d.ProjectID)
	var problems []error
	remove := func(path string) {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			problems = append(problems, err)
		}
	}
	if d.File != "" && d.File == filepath.Base(d.File) {
		remove(filepath.Join(dir, d.File))
		if entries, err := os.ReadDir(dir); err == nil {
			for _, e := range entries {
				if leftover(d.File, e.Name()) {
					remove(filepath.Join(dir, e.Name()))
				}
			}
		}
	}
	if len(problems) == 0 {
		remove(s.sidecar(d.ProjectID, d.Kind, d.RecordID))
	}
	if err := errors.Join(problems...); err != nil {
		return localError("cannot discard the draft", err)
	}
	return nil
}

// leftover reports whether name is an editor's file for the draft file: a
// vim swap file (.<file>.swp, .swo, …), a backup (<file>~, <file>.bak,
// <file>.orig) or an Emacs auto-save or lock file (#<file>#, .#<file>).
func leftover(file, name string) bool {
	switch name {
	case file + "~", file + ".bak", file + ".orig", "#" + file + "#", ".#" + file:
		return true
	}
	return vimSwap.MatchString(name) && name[1:len(name)-4] == file
}

var vimSwap = regexp.MustCompile(`^\..+\.s[a-w][a-z]$`)

// MaxSlug bounds the title part of a draft file name.
const MaxSlug = 48

// Slug is the cosmetic title part of a draft file name: lower-case ASCII
// letters and digits, every other run of characters one hyphen, at most
// MaxSlug long; "draft" when nothing is left. The record ID in the name and
// the sidecar identify the draft.
func Slug(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
		default:
			dash = true
		}
	}
	s := b.String()
	if len(s) > MaxSlug {
		s = strings.TrimRight(s[:MaxSlug], "-")
	}
	if s == "" {
		return "draft"
	}
	return s
}

func join(dir, file string) string { return filepath.Join(dir, file) }
