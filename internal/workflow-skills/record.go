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
}

// owned is what a target's installation owns before this run.
type owned struct {
	skills map[string]string
	exists bool // a record, new or legacy, exists
	legacy bool // it is the setup-skills ownership file
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
		o.skills, o.exists = r.Skills, true
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
