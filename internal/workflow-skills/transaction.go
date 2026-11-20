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
	plugin, e := stageTree(hostStage, pluginName, hp.plugin)
	if e != nil {
		return res, e
	}
	mod, e := stageTree(hostStage, modName, hp.mod)
	if e != nil {
		return res, e
	}
	crashAt("planned")
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
	if mod != nil {
		installed = append(installed, modName)
	}
	touched = append(touched, installed[len(suite):]...)
	if mod == nil && hp.modRemoved {
		touched = append(touched, modName)
	}
	sort.Strings(touched)
	x, e := beginTxn(root, p.instruction, touched)
	if e != nil {
		return res, e
	}
	defer func() {
		if e != nil {
			x.abort()
		}
	}()
	m := record{Version: 1, Host: host, Skills: map[string]string{}}
	for _, n := range touched {
		var before *string
		if v, ok := p.previous.tree(n); ok {
			before = ptr(v)
		}
		if e = x.track(skills, n, before, nil); e != nil {
			return res, e
		}
	}
	for _, n := range suite {
		v, e := fingerprint(filepath.Join(stage, n))
		if e != nil {
			return res, e
		}
		m.Skills[n] = v
		x.j.AfterSkills[n] = ptr(v)
	}
	for _, r := range []*pluginRecord{plugin, mod} {
		if r != nil {
			x.j.AfterSkills[r.Name] = ptr(r.Fingerprint)
		}
	}
	// A local scope leaves the hooks an earlier run installed as they are.
	m.Plugin, m.Mod, m.Hooks, m.StatusLine, m.CreatedFiles = p.previous.plugin, p.previous.mod, p.previous.hooks, p.previous.statusLine, p.previous.created
	if hp.active {
		m.Plugin, m.Mod, m.Hooks, m.StatusLine, m.CreatedFiles = plugin, mod, hp.hooks, hp.statusLine, hp.created
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
	if e = x.journalChanges(changes); e != nil {
		return res, e
	}
	if e = x.seal(); e != nil {
		return res, e
	}
	e = x.run(func() error {
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
		return applyChanges(root, changes)
	})
	if e != nil {
		return res, e
	}
	res = Result{Target: t, Adopted: p.previous.legacy, Hooks: hp.active, StatusLine: hp.state, Status: hp.status, Warnings: hp.warnings, Notes: hp.notes}
	switch {
	case mod != nil:
		res.Mod = "installed"
	case hp.modRemoved:
		res.Mod = "removed"
	}
	return res, x.finish()
}

// stageTree writes the files of a lit-owned directory below skills/ (the hooks
// plugin or the mod) into the stage and returns its record; nil files stage
// nothing.
func stageTree(stage, name string, files map[string][]byte) (*pluginRecord, error) {
	if files == nil {
		return nil, nil
	}
	dir := filepath.Join(stage, name)
	if e := writeFiles(dir, files); e != nil {
		return nil, e
	}
	fp, e := fingerprint(dir)
	if e != nil {
		return nil, e
	}
	return &pluginRecord{Name: name, Fingerprint: fp}, nil
}

// tree returns the recorded fingerprint of a directory below skills/ that the
// earlier installation owns: a skill, the hooks plugin or the mod.
func (o owned) tree(name string) (string, bool) {
	for n, r := range map[string]*pluginRecord{pluginName: o.plugin, modName: o.mod} {
		if name == n {
			if r == nil {
				return "", false
			}
			return r.Fingerprint, true
		}
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
