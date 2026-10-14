package workflowskills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// journalVersion is the journal schema written by this installer. Version 1
// journals were written by setup-skills and covered the instruction file and
// the old ownership file. Version 2 (the first setup-workflows) covers the
// instruction file and the new record, plus the old record when it adopts an
// install. Version 3 adds the host file the hook installation edits
// (settings.json for Claude, hooks.json for Codex) and lets the touched
// directories include the hooks plugin. Versions 1 and 2 still recover.
const journalVersion = 3

type journal struct {
	Version     int                `json:"schema_version"`
	Touched     []string           `json:"touched"`
	Instruction string             `json:"instruction"`
	Backups     map[string]*string `json:"backups"`
	Files       map[string]*string `json:"files"`
	AfterFiles  map[string]*string `json:"after_files"`
	AfterSkills map[string]*string `json:"after_skills"`
}

// recoverInstallation accepts only states reachable by the recorded transaction.
// It validates all evidence before making any recovery writes.
func recoverInstallation(root string, dry bool) error {
	tx := filepath.Join(root, transactionName)
	if !exists(tx) {
		return nil
	}
	if e := safe(tx); e != nil {
		return e
	}
	marker := filepath.Join(tx, "committed.json")
	if exists(marker) {
		var m map[string]any
		if e := readJSON(marker, &m); e != nil {
			return e
		}
		if len(m) != 2 || m["schema_version"] != float64(1) || m["committed"] != true {
			return errors.New("invalid installation commit marker")
		}
		if dry {
			return nil
		}
		return os.RemoveAll(tx)
	}
	var j journal
	if e := readJSON(filepath.Join(tx, "journal.json"), &j); e != nil {
		return fmt.Errorf("interrupted staging: %w", e)
	}
	names, ok := journalFiles(j)
	if !ok || j.Touched == nil || !exact(j.Backups, j.Touched) || !exact(j.AfterSkills, j.Touched) {
		return errors.New("invalid recovery inventory")
	}
	seen := map[string]bool{}
	for _, n := range j.Touched {
		if (!allowed(n) && n != pluginName) || seen[n] {
			return errors.New("invalid touched skills")
		}
		seen[n] = true
	}
	backup := filepath.Join(tx, "backup")
	if e := safe(backup); e != nil {
		return e
	}
	ents, e := os.ReadDir(backup)
	if e != nil {
		return e
	}
	count := 0
	for _, v := range j.Backups {
		if v != nil {
			count++
		}
	}
	if count != len(ents) {
		return errors.New("recovery backup inventory mismatch")
	}
	for _, ent := range ents {
		if v, ok := j.Backups[ent.Name()]; !ok || v == nil {
			return errors.New("unexpected recovery backup")
		}
	}
	for n, v := range j.Backups {
		if v == nil {
			continue
		}
		got, e := fingerprint(filepath.Join(backup, n))
		if e != nil || got != *v {
			return fmt.Errorf("missing or damaged recovery backup: %s", n)
		}
	}
	for _, n := range names {
		got, e := fileHash(filepath.Join(tx, n))
		if e != nil || !same(got, j.Files[n]) {
			return fmt.Errorf("damaged recovery file: %s", n)
		}
		got, e = fileHash(filepath.Join(root, n))
		if e != nil {
			return e
		}
		if !same(got, j.Files[n]) && !same(got, j.AfterFiles[n]) {
			return fmt.Errorf("host file changed since installation: %s", n)
		}
	}
	if e := safe(filepath.Join(root, "skills")); e != nil {
		return e
	}
	for _, n := range j.Touched {
		p := filepath.Join(root, "skills", n)
		if e := safe(p); e != nil {
			return e
		}
		if exists(p) {
			v, e := fingerprint(p)
			if e != nil {
				return e
			}
			if !same(ptr(v), j.Backups[n]) && !same(ptr(v), j.AfterSkills[n]) {
				return fmt.Errorf("managed skill changed since installation: %s", n)
			}
		}
	}
	if dry {
		return nil
	}
	for _, n := range j.Touched {
		p := filepath.Join(root, "skills", n)
		if e := os.RemoveAll(p); e != nil {
			return e
		}
		if j.Backups[n] != nil {
			if e := restoreTree(filepath.Join(backup, n), p); e != nil {
				return e
			}
		}
	}
	for _, n := range names {
		p := filepath.Join(root, n)
		if j.Files[n] == nil {
			if e := os.Remove(p); e != nil && !os.IsNotExist(e) {
				return e
			}
		} else {
			b, e := os.ReadFile(filepath.Join(tx, n))
			if e != nil {
				return e
			}
			if e = atomicWrite(p, b, modeOf(filepath.Join(tx, n))); e != nil {
				return e
			}
		}
	}
	return os.RemoveAll(tx)
}

// journalFiles returns the files a journal covers: the instruction file, the
// ownership records and, from version 3, the host file of the hook
// installation. Files and AfterFiles must name exactly one of the sets the
// version allows.
func journalFiles(j journal) ([]string, bool) {
	if j.Instruction != "AGENTS.md" && j.Instruction != "CLAUDE.md" {
		return nil, false
	}
	hostFile := hostFileOf(j.Instruction)
	var sets [][]string
	switch j.Version {
	case 1:
		sets = [][]string{{j.Instruction, legacyRecordName}}
	case 2:
		sets = [][]string{{j.Instruction, recordName}, {j.Instruction, recordName, legacyRecordName}}
	case journalVersion:
		sets = [][]string{{j.Instruction, recordName}, {j.Instruction, recordName, legacyRecordName},
			{j.Instruction, recordName, hostFile}, {j.Instruction, recordName, legacyRecordName, hostFile}}
	}
	for _, names := range sets {
		if exact(j.Files, names) && exact(j.AfterFiles, names) {
			return names, true
		}
	}
	return nil, false
}

// instructionOf is the instruction file of host.
func instructionOf(host string) string {
	if host == "claude" {
		return "CLAUDE.md"
	}
	return "AGENTS.md"
}

// hostFileOf is the file the hook installation edits for the host whose
// instruction file is instruction.
func hostFileOf(instruction string) string {
	if instruction == "CLAUDE.md" {
		return "settings.json"
	}
	return "hooks.json"
}

// Copy only managed recovery inputs, never unrelated host configuration trees.
func copyRecoveryView(root, dst, host string) error {
	instruction := "AGENTS.md"
	if host == "claude" {
		instruction = "CLAUDE.md"
	}
	for _, n := range []string{instruction, recordName, legacyRecordName, hostFileOf(instruction)} {
		p := filepath.Join(root, n)
		if !exists(p) {
			continue
		}
		if e := safe(p); e != nil {
			return e
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		if e = os.WriteFile(filepath.Join(dst, n), b, modeOf(p)); e != nil {
			return e
		}
	}
	if e := copyTree(filepath.Join(root, transactionName), filepath.Join(dst, transactionName)); e != nil {
		return e
	}
	for _, n := range append(append(append([]string{}, suite...), retired...), pluginName) {
		p := filepath.Join(root, "skills", n)
		if exists(p) {
			if e := copyTree(p, filepath.Join(dst, "skills", n)); e != nil {
				return e
			}
		}
	}
	return nil
}
