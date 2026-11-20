package workflowskills

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// An installation is recorded in the root of its agent configuration. The
// record lists everything setup-workflows owns there, so a later run can tell
// its own files from the user's: the version-1 record holds the skills (the
// managed instruction block is owned wherever a record exists).
const (
	recordName = ".lit-workflows.json"
	// legacyRecordName is the ownership file of the removed setup-skills
	// command. It is adopted into the record and removed in the same transaction.
	legacyRecordName = ".lit-skills.json"
)

type record struct {
	Version int               `json:"schema_version"`
	Host    string            `json:"host"`
	Skills  map[string]string `json:"skills"` // skill name -> tree fingerprint
	// The hook installation, present only in agent configurations that got one
	// (never in a local scope): the Claude plugin directory below skills/, the
	// Claude lit-ui mod directory beside it, the hook groups lit appended to a
	// Codex hooks.json, and the Claude status line.
	Plugin     *pluginRecord     `json:"plugin,omitempty"`
	Mod        *pluginRecord     `json:"mod,omitempty"`
	Hooks      []hookEntry       `json:"hooks,omitempty"`
	StatusLine *statusLineRecord `json:"status_line,omitempty"`
	// CreatedFiles lists the host files (settings.json, hooks.json) that did not
	// exist before setup wrote them, so removal can delete one again when
	// nothing but lit's content is left in it.
	CreatedFiles []string `json:"created_files,omitempty"`
}

// pluginRecord is the lit-owned Claude plugin directory skills/<Name>.
type pluginRecord struct {
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
}

// hookEntry is one hook group lit appended to an event list of the Codex
// hooks.json, recorded exactly as written so it can be found again by value.
type hookEntry struct {
	Event string          `json:"event"`
	Group json.RawMessage `json:"group"`
}

// statusLineRecord is the Claude statusLine value lit wrote to settings.json
// and, when it wrapped a foreign status line, that status line as it was.
type statusLineRecord struct {
	Installed json.RawMessage `json:"installed"`
	Original  json.RawMessage `json:"original,omitempty"`
}

// legacyRecord is the setup-skills ownership file.
type legacyRecord struct {
	Version int               `json:"schema_version"`
	Host    string            `json:"host"`
	Entries map[string]string `json:"entries"`
}

// Result reports what Install did for one target.
type Result struct {
	Target  Target
	Adopted bool // the target held a setup-skills install, now owned by the record
	// Hooks reports that the hooks belong to this installation (user scope and
	// configuration directories only); StatusLine is what happened to the Claude
	// status line: "set", "wrapped", "unchanged" or "kept" (left alone).
	Hooks      bool
	StatusLine string
	// Status is the Claude status surface of a configuration with hooks ("line",
	// "mod" or "both"); Mod is "installed" when the lit-ui mod is installed after
	// the run, "removed" when the run took it away, empty otherwise.
	Status, Mod string
	// Warnings are problems the user should look at, Notes what to do next.
	Warnings []string
	Notes    []string
}

// owned is what a target's installation owns before this run.
type owned struct {
	skills     map[string]string
	plugin     *pluginRecord
	mod        *pluginRecord
	hooks      []hookEntry
	statusLine *statusLineRecord
	created    []string
	exists     bool // a record, new or legacy, exists
	legacy     bool // it is the setup-skills ownership file
}

// readOwned reads the record of an existing installation, the legacy
// ownership file when only that exists. A record is read strictly: an unknown
// field means a newer lit wrote it, and dropping it would lose ownership.
func readOwned(root, host string) (owned, error) {
	o := owned{skills: map[string]string{}}
	current, old := filepath.Join(root, recordName), filepath.Join(root, legacyRecordName)
	switch {
	case exists(current) && exists(old):
		return o, fmt.Errorf("both %s and %s exist; keep %s and remove the stale %s only after checking it lists nothing else", recordName, legacyRecordName, recordName, legacyRecordName)
	case exists(current):
		var r record
		if e := readStrictJSON(current, &r); e != nil {
			return o, fmt.Errorf("invalid ownership record: %w", e)
		}
		if r.Version != 1 || r.Host != host || r.Skills == nil {
			return o, errors.New("invalid ownership record")
		}
		if e := validHookSections(r); e != nil {
			return o, fmt.Errorf("invalid ownership record: %w", e)
		}
		o.skills, o.exists = r.Skills, true
		o.plugin, o.mod, o.hooks, o.statusLine, o.created = r.Plugin, r.Mod, r.Hooks, r.StatusLine, r.CreatedFiles
	case exists(old):
		var r legacyRecord
		if e := readJSON(old, &r); e != nil {
			return o, e
		}
		if r.Version != 1 || r.Host != host || r.Entries == nil {
			return o, errors.New("invalid installation manifest")
		}
		o.skills, o.exists, o.legacy = r.Entries, true, true
	}
	return o, nil
}

func readStrictJSON(p string, v any) error {
	if e := safe(p); e != nil {
		return e
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return e
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	return d.Decode(v)
}

// validHookSections checks the parts of a record that describe the hook
// installation: each belongs to one host and must be well-formed, because
// removal acts on exactly what they say.
func validHookSections(r record) error {
	if r.Plugin != nil {
		if r.Host != "claude" || r.Plugin.Name != pluginName || !validHash(&r.Plugin.Fingerprint) {
			return errors.New("invalid plugin entry")
		}
	}
	if r.Mod != nil {
		if r.Host != "claude" || r.Mod.Name != modName || !validHash(&r.Mod.Fingerprint) {
			return errors.New("invalid mod entry")
		}
	}
	if len(r.Hooks) > 0 && r.Host != "codex" {
		return errors.New("hook entries belong to a codex configuration")
	}
	for _, h := range r.Hooks {
		known := false
		for _, d := range hookDefs {
			known = known || d.event == h.Event
		}
		if _, e := parseObject(h.Group); !known || e != nil {
			return fmt.Errorf("invalid hook entry for %q", h.Event)
		}
	}
	if len(r.CreatedFiles) > 1 || (len(r.CreatedFiles) == 1 && r.CreatedFiles[0] != hostFileOf(instructionOf(r.Host))) {
		return errors.New("invalid created files")
	}
	if s := r.StatusLine; s != nil {
		if r.Host != "claude" {
			return errors.New("a status line belongs to a claude configuration")
		}
		if _, e := parseObject(s.Installed); e != nil {
			return errors.New("invalid status line entry")
		}
		if s.Original != nil {
			if _, e := parseObject(s.Original); e != nil {
				return errors.New("invalid original status line")
			}
		}
	}
	return nil
}
