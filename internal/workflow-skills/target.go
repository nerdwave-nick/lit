package workflowskills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Scopes name how a Target was selected.
const (
	ScopeLocal     = "local"      // .claude and .codex in the current directory
	ScopeUser      = "user"       // .claude and .codex in the home directory
	ScopeConfigDir = "config-dir" // one explicit agent configuration directory
)

// Target is one agent configuration an installation writes into: a host's
// configuration directory and the scope that selected it. Every target has its
// own lock, transaction and ownership record, so two configurations of the
// same host are two independent installs.
type Target struct {
	Host  string // "codex" or "claude"
	Dir   string // absolute configuration directory, for example /home/me/.claude
	Scope string
}

// Selection is what a user asked for: a scope and agent, or one agent and an
// explicit configuration directory.
type Selection struct {
	Scope     string // ScopeLocal or ScopeUser; empty when ConfigDir is set
	Agent     string // "codex", "claude" or, except with ConfigDir, "both"
	ConfigDir string
}

// ResolveTargets turns a Selection into the targets to install into. The
// vendor configuration overrides (CLAUDE_CONFIG_DIR, CODEX_HOME) are never
// read: a target is always named on the command line.
func ResolveTargets(sel Selection) ([]Target, error) {
	var hosts []string
	switch sel.Agent {
	case "codex", "claude":
		hosts = []string{sel.Agent}
	case "both":
		if sel.ConfigDir != "" {
			return nil, errors.New("--config-dir takes exactly one agent: --agent codex or --agent claude")
		}
		hosts = []string{"codex", "claude"}
	default:
		return nil, fmt.Errorf("--agent must be codex, claude, or both (codex or claude with --config-dir), not %q", sel.Agent)
	}
	if sel.ConfigDir != "" {
		if sel.Scope != "" {
			return nil, errors.New("choose --scope or --config-dir, not both")
		}
		dir, err := configDir(sel.ConfigDir, hosts[0])
		if err != nil {
			return nil, err
		}
		return []Target{{Host: hosts[0], Dir: dir, Scope: ScopeConfigDir}}, nil
	}
	var parent string
	var err error
	switch sel.Scope {
	case ScopeLocal:
		parent, err = os.Getwd()
	case ScopeUser:
		parent, err = os.UserHomeDir()
	case "":
		return nil, errors.New("choose --scope local|user or --config-dir DIRECTORY")
	default:
		return nil, fmt.Errorf("--scope must be local or user, not %q", sel.Scope)
	}
	if err != nil {
		return nil, err
	}
	targets := []Target{}
	for _, h := range hosts {
		targets = append(targets, Target{Host: h, Dir: filepath.Join(parent, "."+h), Scope: sel.Scope})
	}
	return targets, nil
}

// configDir checks that dir can be a host configuration directory. lit cannot
// verify that it really is one, so it refuses what is certainly a mistake: a
// path that is missing (a typo must not create a stray configuration; the host
// creates its own), is not a directory, or is the parent of a .claude/.codex
// directory (what --scope custom --path used to take).
func configDir(dir, host string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	switch {
	case os.IsNotExist(err):
		return "", fmt.Errorf("--config-dir %s does not exist; create it or check the spelling", abs)
	case err != nil:
		return "", err
	case !info.IsDir():
		return "", fmt.Errorf("--config-dir %s is not a directory", abs)
	}
	if child, err := os.Stat(filepath.Join(abs, "."+host)); err == nil && child.IsDir() {
		return "", fmt.Errorf("--config-dir %s is the parent of a .%s directory; name the configuration directory itself, for example %s", abs, host, filepath.Join(abs, "."+host))
	}
	return abs, nil
}
