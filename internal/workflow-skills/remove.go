package workflowskills

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Removal reports what Remove did for one target.
type Removal struct {
	Target Target
	// Found reports that the target held an installation (an ownership record)
	// and that everything the record lists was removed.
	Found bool
	// Skills counts the skill directories removed, Hooks reports that the hooks
	// (the Claude plugin directory, the Codex hooks.json groups) were removed.
	Skills int
	Hooks  bool
	// StatusLine is what became of the Claude status line: "restored" (the
	// wrapped original is back), "removed" (lit's was deleted) or "left" (it was
	// changed after setup wrote it, so it stays).
	StatusLine string
	// Warnings are problems the user should look at, Notes what else happened.
	Warnings []string
	Notes    []string
}

// Remove undoes setup-workflows in the targets: it removes exactly what each
// target's ownership record lists (the skills, the managed instruction block,
// the hooks plugin or hook groups, lit's status line, restoring a wrapped
// original) and the record itself, in one journaled transaction per target
// with the same lock, backups and recovery as installing. Files the record
// lists but the user changed are refused, and content setup does not own is
// never touched. A target without an installation is not an error.
// Every target is preflighted before any is changed.
func Remove(targets []Target) ([]Removal, error) {
	if e := checkTargets(targets); e != nil {
		return nil, e
	}
	present := []Target{}
	for _, t := range targets {
		if exists(t.Dir) {
			present = append(present, t)
		}
	}
	inspectTarget := func(t Target) func(string) error {
		return func(root string) error { _, e := inspectRemoval(root, t); return e }
	}
	for _, t := range present {
		if e := preflight(t, inspectTarget(t)); e != nil {
			return nil, fmt.Errorf("%s preflight: %w", t.Host, e)
		}
	}
	unlock, e := lockTargets(present, false)
	defer unlock()
	if e != nil {
		return nil, e
	}
	for _, t := range present {
		if e := preflightUnlocked(t, inspectTarget(t)); e != nil {
			return nil, e
		}
	}
	done := map[string]Removal{}
	results := func() []Removal {
		out := []Removal{}
		for _, t := range targets {
			if r, ok := done[t.Dir]; ok {
				out = append(out, r)
			} else if !exists(t.Dir) {
				out = append(out, Removal{Target: t, Notes: []string{t.Dir + " does not exist; nothing to remove"}})
			}
		}
		return out
	}
	for _, t := range present {
		if e := recoverInstallation(t.Dir, false); e != nil {
			return results(), e
		}
		res, e := removeHost(t)
		if e != nil {
			names := []string{}
			for _, r := range results() {
				names = append(names, r.Target.Host)
			}
			return results(), fmt.Errorf("%s removal failed (completed agents: %v; inspect retained transaction and rerun): %w", t.Host, names, e)
		}
		done[t.Dir] = res
	}
	return results(), nil
}

// removeHost removes the installation of one agent configuration in a single
// journaled transaction.
func removeHost(t Target) (res Removal, err error) {
	root := t.Dir
	r, err := inspectRemoval(root, t)
	res = Removal{Target: t, Warnings: r.warnings, Notes: r.notes}
	if err != nil || !r.found {
		return res, err
	}
	crashAt("planned")
	touched := []string{}
	for n := range r.trees {
		touched = append(touched, n)
	}
	sort.Strings(touched)
	x, err := beginTxn(root, r.instruction, touched)
	if err != nil {
		return res, err
	}
	defer func() {
		if err != nil {
			x.abort()
		}
	}()
	skills := filepath.Join(root, "skills")
	for _, n := range touched {
		if err = x.track(skills, n, ptr(r.trees[n]), nil); err != nil {
			return res, err
		}
	}
	if err = x.journalChanges(r.changes); err != nil {
		return res, err
	}
	if err = x.seal(); err != nil {
		return res, err
	}
	err = x.run(func() error {
		for _, n := range touched {
			if e := os.RemoveAll(filepath.Join(skills, n)); e != nil {
				return e
			}
		}
		crashAt("skills")
		return applyChanges(root, r.changes)
	})
	if err != nil {
		return res, err
	}
	res.Found, res.Skills, res.Hooks, res.StatusLine = true, r.skills, r.hooks, r.statusLine
	return res, x.finish()
}
