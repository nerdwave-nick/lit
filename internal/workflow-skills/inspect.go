package workflowskills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type plan struct {
	previous          owned
	instruction       string
	instructionBytes  []byte
	instructionBefore *string // hash of the instruction file the plan is based on; nil when absent
	hooks             hookPlan
}

// inspect plans the installation into t, reading the configuration at root
// (the target's directory, or a copy of it during preflight) and refusing
// anything setup does not own.
func inspect(root string, t Target, o Options) (plan, error) {
	host := t.Host
	p := plan{instruction: "AGENTS.md"}
	if host == "claude" {
		p.instruction = "CLAUDE.md"
	}
	for _, f := range []string{root, filepath.Join(root, "skills"), filepath.Join(root, recordName), filepath.Join(root, legacyRecordName), filepath.Join(root, p.instruction)} {
		if e := safe(f); e != nil {
			return p, e
		}
	}
	var e error
	if p.previous, e = readOwned(root, host); e != nil {
		return p, e
	}
	for n, v := range p.previous.skills {
		if !allowed(n) || !validHash(ptr(v)) {
			return p, errors.New("invalid manifest entry")
		}
		got, e := fingerprint(filepath.Join(root, "skills", n))
		if e != nil || got != v {
			return p, fmt.Errorf("managed skill removed/modified: %s", n)
		}
	}
	for _, n := range suite {
		if _, ok := p.previous.skills[n]; !ok && exists(filepath.Join(root, "skills", n)) {
			return p, fmt.Errorf("unowned skill collision: %s", n)
		}
	}
	b, e := os.ReadFile(filepath.Join(root, p.instruction))
	if e != nil && !os.IsNotExist(e) {
		return p, e
	}
	if !utf8.Valid(b) {
		return p, errors.New("instruction is not UTF-8")
	}
	if e == nil {
		p.instructionBefore = ptr(hash(b))
	}
	original := string(b)
	if strings.Count(original, start) != strings.Count(original, end) || strings.Count(original, start) > 1 || strings.Index(original, start) > strings.Index(original, end) {
		return p, errors.New("malformed managed instruction block")
	}
	if strings.Contains(original, start) && !p.previous.exists {
		return p, errors.New("unowned managed instruction block")
	}
	template, e := assets.ReadFile("AGENTS_MD_TEMPLATE.md")
	if e != nil {
		return p, e
	}
	block := strings.TrimSuffix(string(template), "\n")
	if strings.Contains(original, start) {
		original = original[:strings.Index(original, start)] + block + original[strings.Index(original, end)+len(end):]
	} else {
		if original != "" && !strings.HasSuffix(original, "\n") {
			original += "\n"
		}
		original += block + "\n"
	}
	p.instructionBytes = []byte(original)
	p.hooks, e = planHooks(root, t, o, p.previous)
	return p, e
}
func preflight(t Target, o Options) error {
	if e := safe(t.Dir); e != nil {
		return e
	}
	if exists(filepath.Join(t.Dir, ".lit-install.lock")) {
		return errors.New("installation busy or interrupted; inspect .lit-install.lock")
	}
	return preflightUnlocked(t, o)
}
func preflightUnlocked(t Target, o Options) error {
	root := t.Dir
	if e := recoverInstallation(root, true); e != nil {
		return e
	}
	if exists(filepath.Join(root, transactionName)) {
		tmp, e := os.MkdirTemp("", "lit-recovery-preflight-")
		if e != nil {
			return e
		}
		defer os.RemoveAll(tmp)
		if e = copyRecoveryView(root, tmp, t.Host); e != nil {
			return e
		}
		if e = recoverInstallation(tmp, false); e != nil {
			return e
		}
		_, e = inspect(tmp, t, o)
		return e
	}
	_, e := inspect(root, t, o)
	return e
}
