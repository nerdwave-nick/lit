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
// Every selected host is preflighted before changes; each host commits separately.
func Install(bundle fs.FS, parent string, hosts []string) error {
	if len(hosts) == 0 {
		return errors.New("no agents selected")
	}
	seen := map[string]bool{}
	for _, h := range hosts {
		if (h != "codex" && h != "claude") || seen[h] {
			return errors.New("invalid agent selection")
		}
		seen[h] = true
	}
	parent, e := filepath.Abs(parent)
	if e != nil {
		return e
	}
	stage, e := os.MkdirTemp("", "lit-skills-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(stage)
	for _, n := range suite {
		if _, e := fs.ReadFile(bundle, "skills/"+n+"/SKILL.md"); e != nil {
			return fmt.Errorf("incomplete embedded suite: %w", e)
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
		return e
	}
	roots := []string{}
	for _, h := range hosts {
		r := filepath.Join(parent, "."+h)
		if e := preflight(r, h); e != nil {
			return fmt.Errorf("%s preflight: %w", h, e)
		}
		roots = append(roots, r)
	}
	locks := []string{}
	defer func() {
		for _, p := range locks {
			os.Remove(p)
		}
	}()
	for _, r := range roots {
		if e := os.MkdirAll(r, 0755); e != nil {
			return e
		}
		l := filepath.Join(r, ".lit-install.lock")
		if e := os.Mkdir(l, 0700); e != nil {
			return fmt.Errorf("installation busy or interrupted: %w", e)
		}
		locks = append(locks, l)
	}
	for i, r := range roots {
		if e := preflightUnlocked(r, hosts[i]); e != nil {
			return e
		}
	}
	completed := []string{}
	for i, r := range roots {
		if e := recoverInstallation(r, false); e != nil {
			return e
		}
		if e := installHost(stage, r, hosts[i]); e != nil {
			return fmt.Errorf("%s setup failed (completed agents: %v; inspect retained transaction and rerun): %w", hosts[i], completed, e)
		}
		completed = append(completed, hosts[i])
	}
	return nil
}
func installHost(stage, root, host string) error {
	p, e := inspect(root, host)
	if e != nil {
		return e
	}
	skills := filepath.Join(root, "skills")
	if e = os.MkdirAll(skills, 0755); e != nil {
		return e
	}
	hostStage, e := os.MkdirTemp(root, ".lit-stage-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(hostStage)
	for _, n := range suite {
		if e = copyTree(filepath.Join(stage, n), filepath.Join(hostStage, n)); e != nil {
			return e
		}
	}
	tx := filepath.Join(root, transactionName)
	if e = os.Mkdir(tx, 0700); e != nil {
		return e
	}
	backup := filepath.Join(tx, "backup")
	if e = os.Mkdir(backup, 0700); e != nil {
		return e
	}
	touched := append([]string{}, suite...)
	for _, n := range retired {
		if _, ok := p.previous.Entries[n]; ok {
			touched = append(touched, n)
		}
	}
	sort.Strings(touched)
	j := journal{Version: 1, Touched: touched, Instruction: p.instruction, Backups: map[string]*string{}, Files: map[string]*string{}, AfterFiles: map[string]*string{}, AfterSkills: map[string]*string{}}
	m := inventory{Version: 1, Host: host, Entries: map[string]string{}}
	for _, n := range touched {
		j.Backups[n] = nil
		j.AfterSkills[n] = nil
		if v, ok := p.previous.Entries[n]; ok {
			if e = copyTree(filepath.Join(skills, n), filepath.Join(backup, n)); e != nil {
				return e
			}
			j.Backups[n] = ptr(v)
		}
	}
	for _, n := range suite {
		v, e := fingerprint(filepath.Join(stage, n))
		if e != nil {
			return e
		}
		m.Entries[n] = v
		j.AfterSkills[n] = ptr(v)
	}
	contents := map[string][]byte{p.instruction: p.instructionBytes, ".lit-skills.json": jsonBytes(m)}
	for n, b := range contents {
		old, e := os.ReadFile(filepath.Join(root, n))
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		j.Files[n] = nil
		if e == nil {
			if e = os.WriteFile(filepath.Join(tx, n), old, 0644); e != nil {
				return e
			}
			j.Files[n] = ptr(hash(old))
		}
		j.AfterFiles[n] = ptr(hash(b))
	}
	if e = atomicWrite(filepath.Join(tx, "journal.json"), jsonBytes(j), 0600); e != nil {
		return e
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
		for n, b := range contents {
			if e := atomicWrite(filepath.Join(root, n), b, 0644); e != nil {
				return e
			}
		}
		return atomicWrite(filepath.Join(tx, "committed.json"), []byte(`{"schema_version":1,"committed":true}`), 0600)
	}
	if e = mutate(); e != nil {
		re := recoverInstallation(root, false)
		if re != nil {
			return fmt.Errorf("%w; recovery retained: %v", e, re)
		}
		return e
	}
	return os.RemoveAll(tx)
}
