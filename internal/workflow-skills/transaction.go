package workflowskills

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// installHost installs the suite, and the hooks of an agent configuration, into
// one directory in a single journaled transaction.
func installHost(stage string, t Target, o Options) (res Result, e error) {
	root, host := t.Dir, t.Host
	p, e := inspect(root, t, o)
	if e != nil {
		return res, e
	}
	hp := p.hooks
	skills := filepath.Join(root, "skills")
	if e = os.MkdirAll(skills, 0755); e != nil {
		return res, e
	}
	hostStage, e := os.MkdirTemp(root, ".lit-stage-")
	if e != nil {
		return res, e
	}
	defer os.RemoveAll(hostStage)
	for _, n := range suite {
		if e = copyTree(filepath.Join(stage, n), filepath.Join(hostStage, n)); e != nil {
			return res, e
		}
	}
	var plugin *pluginRecord
	if hp.plugin != nil {
		dir := filepath.Join(hostStage, pluginName)
		if e = writeFiles(dir, hp.plugin); e != nil {
			return res, e
		}
		fp, e := fingerprint(dir)
		if e != nil {
			return res, e
		}
		plugin = &pluginRecord{Name: pluginName, Fingerprint: fp}
	}
	crashAt("planned")
	tx := filepath.Join(root, transactionName)
	if e = os.Mkdir(tx, 0700); e != nil {
		return res, e
	}
	// Until the journal is complete nothing has been changed, so an error
	// leaves no half-written transaction behind.
	journaled := false
	defer func() {
		if e != nil && !journaled {
			os.RemoveAll(tx)
		}
	}()
	backup := filepath.Join(tx, "backup")
	if e = os.Mkdir(backup, 0700); e != nil {
		return res, e
	}
	touched := append([]string{}, suite...)
	for _, n := range retired {
		if _, ok := p.previous.skills[n]; ok {
			touched = append(touched, n)
		}
	}
	installed := append([]string{}, suite...)
	if plugin != nil {
		installed = append(installed, pluginName)
	}
	touched = append(touched, installed[len(suite):]...)
	sort.Strings(touched)
	j := journal{Version: journalVersion, Touched: touched, Instruction: p.instruction, Backups: map[string]*string{}, Files: map[string]*string{}, AfterFiles: map[string]*string{}, AfterSkills: map[string]*string{}}
	m := record{Version: 1, Host: host, Skills: map[string]string{}}
	for _, n := range touched {
		j.Backups[n] = nil
		j.AfterSkills[n] = nil
		if v, ok := p.previous.tree(n); ok {
			if e = copyTree(filepath.Join(skills, n), filepath.Join(backup, n)); e != nil {
				return res, e
			}
			j.Backups[n] = ptr(v)
		}
	}
	for _, n := range suite {
		v, e := fingerprint(filepath.Join(stage, n))
		if e != nil {
			return res, e
		}
		m.Skills[n] = v
		j.AfterSkills[n] = ptr(v)
	}
	if plugin != nil {
		j.AfterSkills[pluginName] = ptr(plugin.Fingerprint)
	}
	// A local scope leaves the hooks an earlier run installed as they are.
	m.Plugin, m.Hooks, m.StatusLine = p.previous.plugin, p.previous.hooks, p.previous.statusLine
	if hp.active {
		m.Plugin, m.Hooks, m.StatusLine = plugin, hp.hooks, hp.statusLine
	}
	// Files are written in this order and the setup-skills ownership file is
	// removed last; the journal covers every one of them.
	changes := []fileChange{{name: p.instruction, content: p.instructionBytes, before: p.instructionBefore, guard: true}, {name: recordName, content: jsonBytes(m)}}
	if hp.file != nil {
		changes = append(changes, *hp.file)
	}
	if p.previous.legacy {
		changes = append(changes, fileChange{name: legacyRecordName})
	}
	for _, c := range changes {
		path := filepath.Join(root, c.name)
		old, e := os.ReadFile(path)
		if e != nil && !os.IsNotExist(e) {
			return res, e
		}
		j.Files[c.name] = nil
		if e == nil {
			if c.guard && !same(ptr(hash(old)), c.before) {
				return res, fmt.Errorf("%s changed while setup was reading it; rerun setup-workflows", c.name)
			}
			if e = os.WriteFile(filepath.Join(tx, c.name), old, modeOf(path)); e != nil {
				return res, e
			}
			j.Files[c.name] = ptr(hash(old))
		} else if c.guard && c.before != nil {
			return res, fmt.Errorf("%s was removed while setup was reading it; rerun setup-workflows", c.name)
		}
		j.AfterFiles[c.name] = nil
		if !c.remove() {
			j.AfterFiles[c.name] = ptr(hash(c.content))
		}
	}
	if e = atomicWrite(filepath.Join(tx, "journal.json"), jsonBytes(j), 0600); e != nil {
		return res, e
	}
	journaled = true
	crashAt("journal")
	mutate := func() error {
		for _, n := range touched {
			if e := os.RemoveAll(filepath.Join(skills, n)); e != nil {
				return e
			}
		}
		for _, n := range installed {
			if e := os.Rename(filepath.Join(hostStage, n), filepath.Join(skills, n)); e != nil {
				return e
			}
		}
		crashAt("skills")
		for _, c := range changes {
			if e := c.apply(root); e != nil {
				return e
			}
			crashAt("file:" + c.name)
		}
		crashAt("applied")
		return atomicWrite(filepath.Join(tx, "committed.json"), []byte(`{"schema_version":1,"committed":true}`), 0600)
	}
	if e = mutate(); e != nil {
		re := recoverInstallation(root, false)
		if re != nil {
			return res, fmt.Errorf("%w; recovery retained: %v", e, re)
		}
		return res, e
	}
	res = Result{Target: t, Adopted: p.previous.legacy, Hooks: hp.active, StatusLine: hp.state, Warnings: hp.warnings, Notes: hp.notes}
	return res, os.RemoveAll(tx)
}

// tree returns the recorded fingerprint of a directory below skills/ that the
// earlier installation owns: a skill or the hooks plugin.
func (o owned) tree(name string) (string, bool) {
	if name == pluginName {
		if o.plugin == nil {
			return "", false
		}
		return o.plugin.Fingerprint, true
	}
	v, ok := o.skills[name]
	return v, ok
}

// writeFiles writes the files (by relative path) below dir.
func writeFiles(dir string, files map[string][]byte) error {
	for name, content := range files {
		path := filepath.Join(dir, name)
		if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
			return e
		}
		if e := os.WriteFile(path, content, 0644); e != nil {
			return e
		}
	}
	return nil
}

// modeOf returns the permission bits of a file, or the default for a new one.
func modeOf(path string) fs.FileMode {
	if info, e := os.Stat(path); e == nil {
		return info.Mode().Perm()
	}
	return 0644
}

// fileChange is one write to a file of the configuration directory; nil
// content removes the file. A guarded change belongs to a file the user or the
// host also writes: it is applied only to the content the plan was computed
// from (before; nil when the file did not exist).
type fileChange struct {
	name    string
	content []byte
	before  *string
	guard   bool
}

func (c fileChange) remove() bool { return c.content == nil }

func (c fileChange) apply(root string) error {
	p := filepath.Join(root, c.name)
	if c.guard {
		cur, e := fileHash(p)
		if e != nil {
			return e
		}
		if !same(cur, c.before) {
			return fmt.Errorf("%s changed while setup was writing; refusing to overwrite it", c.name)
		}
	}
	if c.remove() {
		if e := os.Remove(p); e != nil && !os.IsNotExist(e) {
			return e
		}
		return nil
	}
	return atomicWrite(p, c.content, modeOf(p))
}
