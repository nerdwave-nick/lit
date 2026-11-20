package workflowskills

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
)

// The lit-ui Claude Code mod: its source, embedded from mods/lit-ui. Only the
// files the mod runs from are embedded (the manifest, the hooks module and its
// type contract), never its tests, its tsconfig.json or the types the engine
// lays in .claude-plugin/types/ when it loads a development checkout.
//
//go:embed mods/lit-ui/.claude-plugin/plugin.json mods/lit-ui/hooks/*.json mods/lit-ui/hooks/*.ts mods/lit-ui/hooks/*.tsx mods/lit-ui/types/*.d.ts
var modFS embed.FS

const (
	// modName is the lit-owned mod directory below skills/, next to lit-hooks:
	// Claude Code loads it as a skills-directory plugin like the hooks plugin.
	modName = "lit-ui"
	modRoot = "mods/" + modName
	// modConfigName is the file setup generates in the mod directory; the mod
	// reads the status mode and the lit executable from it (hooks/config.ts).
	modConfigName = "config.json"
)

// The Claude status surfaces --status chooses between.
const (
	statusLine = "line"
	statusMod  = "mod"
	statusBoth = "both"
)

// status is the chosen Claude status surface; both when none was chosen.
func (o Options) status() string {
	if o.Status == "" {
		return statusBoth
	}
	return o.Status
}

func (o Options) checkStatus() error {
	switch o.status() {
	case statusLine, statusMod, statusBoth:
		return nil
	}
	return fmt.Errorf("unknown status %q: choose line, mod or both", o.Status)
}

// litPath is the lit executable the mod runs.
func (o Options) litPath() string {
	if o.LitPath != "" {
		return o.LitPath
	}
	return o.binary()
}

// modFiles returns the files of the mod directory, by relative path: the
// embedded source and the generated config.json naming mode and lit.
func modFiles(mode, lit string) (map[string][]byte, error) {
	files := map[string][]byte{}
	err := fs.WalkDir(modFS, modRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := modFS.ReadFile(p)
		if err != nil {
			return err
		}
		files[filepath.FromSlash(strings.TrimPrefix(p, modRoot+"/"))] = b
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("embedded mod: %w", err)
	}
	if _, ok := files[filepath.Join("hooks", "hooks.json")]; !ok {
		return nil, fmt.Errorf("embedded mod: %s is missing", path.Join(modRoot, "hooks", "hooks.json"))
	}
	config := newObject()
	config.set("schema_version", marshalRaw(1))
	config.set("status", marshalRaw(mode))
	config.set("lit", marshalRaw(lit))
	files[modConfigName] = config.bytes()
	return files, nil
}

func modDirOf(root string) string { return filepath.Join(root, "skills", modName) }

// planMod decides the mod directory of a Claude configuration: installed (with
// mode's config) unless the mode is line, in which case a mod setup installed
// earlier is removed. A recorded mod must be intact; an unrecorded lit-ui
// directory is the user's and blocks an installation.
func planMod(root string, o Options, prev owned, hp *hookPlan) error {
	dir, mode := modDirOf(root), o.status()
	switch {
	case prev.mod != nil:
		if got, err := fingerprint(dir); err != nil || got != prev.mod.Fingerprint {
			return fmt.Errorf("managed mod removed/modified: %s; setup does not replace files you changed (restore or delete it yourself, then rerun)", modName)
		}
	case mode != statusLine && exists(dir):
		return fmt.Errorf("unowned skill collision: %s", modName)
	}
	hp.status = mode
	hp.modRemoved = prev.mod != nil && mode == statusLine
	if mode == statusLine {
		return nil
	}
	files, err := modFiles(mode, o.litPath())
	hp.mod = files
	return err
}

// planModSettings is the settings.json change of mode mod: a status line setup
// installed is restored to the original it wrapped or removed, through the same
// path as --remove; a status line setup does not own stays as it is. A
// settings.json setup created is deleted once nothing else is left in it.
func planModSettings(root string, prev owned, hp *hookPlan) error {
	created := listed(prev.created, "settings.json")
	if prev.statusLine == nil && !created {
		return nil
	}
	hr, err := planClaudeRemoval(root, prev)
	if err != nil {
		return err
	}
	hp.file, hp.state = hr.change, hr.statusLine
	hp.warnings, hp.notes = append(hp.warnings, hr.warnings...), append(hp.notes, hr.notes...)
	if created && exists(filepath.Join(root, "settings.json")) && (hr.change == nil || !hr.change.remove()) {
		hp.created = []string{"settings.json"}
	}
	return nil
}
