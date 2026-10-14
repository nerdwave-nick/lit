package workflowskills

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var suite = strings.Fields("lit planner clarify modeling challenge consult research prototype to-spec to-tickets impl triage codebase what i-have-adhd orchestrate")
var retired = strings.Fields("wayfinder grilling domain-modeling codebase-design grill-with-docs")

const start = "<!-- BEGIN lit managed -->"
const end = "<!-- END lit managed -->"
const transactionName = ".lit-install-transaction"

func allowed(n string) bool {
	for _, v := range append(append([]string{}, suite...), retired...) {
		if n == v {
			return true
		}
	}
	return false
}

// Install extracts the complete embedded bundle without starting a service.
// Every target is preflighted before changes; each target commits separately.
func Install(bundle fs.FS, targets []Target) ([]Result, error) {
	if len(targets) == 0 {
		return nil, errors.New("no agents selected")
	}
	dirs := map[string]bool{}
	for _, t := range targets {
		if (t.Host != "codex" && t.Host != "claude") || t.Dir == "" || dirs[t.Dir] {
			return nil, errors.New("invalid agent selection")
		}
		dirs[t.Dir] = true
	}
	stage, e := os.MkdirTemp("", "lit-skills-")
	if e != nil {
		return nil, e
	}
	defer os.RemoveAll(stage)
	for _, n := range suite {
		if _, e := fs.ReadFile(bundle, "skills/"+n+"/SKILL.md"); e != nil {
			return nil, fmt.Errorf("incomplete embedded suite: %w", e)
		}
	}
	e = fs.WalkDir(bundle, "skills", func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("symlink bundle entry")
		}
		rel := strings.TrimPrefix(p, "skills")
		dst := filepath.Join(stage, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(dst, 0755)
		}
		b, e := fs.ReadFile(bundle, p)
		if e != nil {
			return e
		}
		return os.WriteFile(dst, b, 0644)
	})
	if e != nil {
		return nil, e
	}
	for _, t := range targets {
		if e := preflight(t.Dir, t.Host); e != nil {
			return nil, fmt.Errorf("%s preflight: %w", t.Host, e)
		}
	}
	locks := []string{}
	defer func() {
		for _, p := range locks {
			os.Remove(p)
		}
	}()
	for _, t := range targets {
		if e := os.MkdirAll(t.Dir, 0755); e != nil {
			return nil, e
		}
		l := filepath.Join(t.Dir, ".lit-install.lock")
		if e := os.Mkdir(l, 0700); e != nil {
			return nil, fmt.Errorf("installation busy or interrupted: %w", e)
		}
		locks = append(locks, l)
	}
	for _, t := range targets {
		if e := preflightUnlocked(t.Dir, t.Host); e != nil {
			return nil, e
		}
	}
	results := []Result{}
	for _, t := range targets {
		if e := recoverInstallation(t.Dir, false); e != nil {
			return results, e
		}
		adopted, e := installHost(stage, t.Dir, t.Host)
		if e != nil {
			done := []string{}
			for _, r := range results {
				done = append(done, r.Target.Host)
			}
			return results, fmt.Errorf("%s setup failed (completed agents: %v; inspect retained transaction and rerun): %w", t.Host, done, e)
		}
		results = append(results, Result{Target: t, Adopted: adopted})
	}
	return results, nil
}

// installHost installs the suite into one configuration directory in a single
// journaled transaction and reports whether it adopted a setup-skills install.
func installHost(stage, root, host string) (adopted bool, e error) {
	p, e := inspect(root, host)
	if e != nil {
		return false, e
	}
	skills := filepath.Join(root, "skills")
	if e = os.MkdirAll(skills, 0755); e != nil {
		return false, e
	}
	hostStage, e := os.MkdirTemp(root, ".lit-stage-")
	if e != nil {
		return false, e
	}
	defer os.RemoveAll(hostStage)
	for _, n := range suite {
		if e = copyTree(filepath.Join(stage, n), filepath.Join(hostStage, n)); e != nil {
			return false, e
		}
	}
	tx := filepath.Join(root, transactionName)
	if e = os.Mkdir(tx, 0700); e != nil {
		return false, e
	}
	backup := filepath.Join(tx, "backup")
	if e = os.Mkdir(backup, 0700); e != nil {
		return false, e
	}
	touched := append([]string{}, suite...)
	for _, n := range retired {
		if _, ok := p.previous.skills[n]; ok {
			touched = append(touched, n)
		}
	}
	sort.Strings(touched)
	j := journal{Version: journalVersion, Touched: touched, Instruction: p.instruction, Backups: map[string]*string{}, Files: map[string]*string{}, AfterFiles: map[string]*string{}, AfterSkills: map[string]*string{}}
	m := record{Version: 1, Host: host, Skills: map[string]string{}}
	for _, n := range touched {
		j.Backups[n] = nil
		j.AfterSkills[n] = nil
		if v, ok := p.previous.skills[n]; ok {
			if e = copyTree(filepath.Join(skills, n), filepath.Join(backup, n)); e != nil {
				return false, e
			}
			j.Backups[n] = ptr(v)
		}
	}
	for _, n := range suite {
		v, e := fingerprint(filepath.Join(stage, n))
		if e != nil {
			return false, e
		}
		m.Skills[n] = v
		j.AfterSkills[n] = ptr(v)
	}
	// Files are written in this order and the setup-skills ownership file is
	// removed last; the journal covers every one of them.
	changes := []fileChange{{name: p.instruction, content: p.instructionBytes}, {name: recordName, content: jsonBytes(m)}}
	if p.previous.legacy {
		changes = append(changes, fileChange{name: legacyRecordName})
	}
	for _, c := range changes {
		old, e := os.ReadFile(filepath.Join(root, c.name))
		if e != nil && !os.IsNotExist(e) {
			return false, e
		}
		j.Files[c.name] = nil
		if e == nil {
			if e = os.WriteFile(filepath.Join(tx, c.name), old, 0644); e != nil {
				return false, e
			}
			j.Files[c.name] = ptr(hash(old))
		}
		j.AfterFiles[c.name] = nil
		if !c.remove() {
			j.AfterFiles[c.name] = ptr(hash(c.content))
		}
	}
	if e = atomicWrite(filepath.Join(tx, "journal.json"), jsonBytes(j), 0600); e != nil {
		return false, e
	}
	mutate := func() error {
		for _, n := range touched {
			if e := os.RemoveAll(filepath.Join(skills, n)); e != nil {
				return e
			}
		}
		for _, n := range suite {
			if e := os.Rename(filepath.Join(hostStage, n), filepath.Join(skills, n)); e != nil {
				return e
			}
		}
		for _, c := range changes {
			if e := c.apply(root); e != nil {
				return e
			}
		}
		return atomicWrite(filepath.Join(tx, "committed.json"), []byte(`{"schema_version":1,"committed":true}`), 0600)
	}
	if e = mutate(); e != nil {
		re := recoverInstallation(root, false)
		if re != nil {
			return false, fmt.Errorf("%w; recovery retained: %v", e, re)
		}
		return false, e
	}
	return p.previous.legacy, os.RemoveAll(tx)
}

// fileChange is one write to a file of the configuration directory; nil
// content removes the file.
type fileChange struct {
	name    string
	content []byte
}

func (c fileChange) remove() bool { return c.content == nil }

func (c fileChange) apply(root string) error {
	p := filepath.Join(root, c.name)
	if c.remove() {
		if e := os.Remove(p); e != nil && !os.IsNotExist(e) {
			return e
		}
		return nil
	}
	return atomicWrite(p, c.content, 0644)
}
