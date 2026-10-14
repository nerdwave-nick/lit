package workflowskills

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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

// Options tune an installation; the zero value is the production default.
type Options struct {
	// Binary is how the installed hook commands name the lit executable: a
	// command word found through PATH ("lit", the default) or an absolute path.
	Binary string
	// Home and WorkDir are where setup looks for lit hooks it did not install
	// (other agent configurations, the project's .claude and .codex); they
	// default to the user's home directory and the current directory.
	Home, WorkDir string
}

// Install is InstallWith with the default Options.
func Install(bundle fs.FS, targets []Target) ([]Result, error) {
	return InstallWith(bundle, targets, Options{})
}

// InstallWith extracts the complete embedded bundle without starting a service
// and, into agent configurations (not the local scope), the hooks and the
// Claude status line. Every target is preflighted before changes; each target
// commits separately.
func InstallWith(bundle fs.FS, targets []Target, opt Options) ([]Result, error) {
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
		if e := preflight(t, opt); e != nil {
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
		if e := preflightUnlocked(t, opt); e != nil {
			return nil, e
		}
	}
	results := []Result{}
	for _, t := range targets {
		if e := recoverInstallation(t.Dir, false); e != nil {
			return results, e
		}
		res, e := installHost(stage, t, opt)
		if e != nil {
			done := []string{}
			for _, r := range results {
				done = append(done, r.Target.Host)
			}
			return results, fmt.Errorf("%s setup failed (completed agents: %v; inspect retained transaction and rerun): %w", t.Host, done, e)
		}
		if res.Hooks {
			res.Warnings = append(res.Warnings, scanElsewhere(t, opt)...)
		}
		results = append(results, res)
	}
	return results, nil
}

// crashAt is called at the steps of a transaction; tests replace it to stop
// the installer where a killed process would stop.
var crashAt = func(step string) {}
