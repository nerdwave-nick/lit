package workflowskills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type inventory struct {
	Version int               `json:"schema_version"`
	Host    string            `json:"host"`
	Entries map[string]string `json:"entries"`
}

type plan struct {
	previous         inventory
	instruction      string
	instructionBytes []byte
}

func inspect(root, host string) (plan, error) {
	p := plan{previous: inventory{Version: 1, Host: host, Entries: map[string]string{}}, instruction: "AGENTS.md"}
	if host == "claude" {
		p.instruction = "CLAUDE.md"
	}
	for _, f := range []string{root, filepath.Join(root, "skills"), filepath.Join(root, ".lit-skills.json"), filepath.Join(root, p.instruction)} {
		if e := safe(f); e != nil {
			return p, e
		}
	}
	manifest := filepath.Join(root, ".lit-skills.json")
	if exists(manifest) {
		p.previous = inventory{}
		if e := readJSON(manifest, &p.previous); e != nil {
			return p, e
		}
		if p.previous.Version != 1 || p.previous.Host != host || p.previous.Entries == nil {
			return p, errors.New("invalid installation manifest")
		}
	}
	for n, v := range p.previous.Entries {
		if !allowed(n) || !validHash(ptr(v)) {
			return p, errors.New("invalid manifest entry")
		}
		got, e := fingerprint(filepath.Join(root, "skills", n))
		if e != nil || got != v {
			return p, fmt.Errorf("managed skill removed/modified: %s", n)
		}
	}
	for _, n := range suite {
		if _, ok := p.previous.Entries[n]; !ok && exists(filepath.Join(root, "skills", n)) {
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
	original := string(b)
	if strings.Count(original, start) != strings.Count(original, end) || strings.Count(original, start) > 1 || strings.Index(original, start) > strings.Index(original, end) {
		return p, errors.New("malformed managed instruction block")
	}
	if strings.Contains(original, start) && !exists(manifest) {
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
	return p, nil
}
func preflight(root, host string) error {
	if e := safe(root); e != nil {
		return e
	}
	if exists(filepath.Join(root, ".lit-install.lock")) {
		return errors.New("installation busy or interrupted; inspect .lit-install.lock")
	}
	return preflightUnlocked(root, host)
}
func preflightUnlocked(root, host string) error {
	if e := recoverInstallation(root, true); e != nil {
		return e
	}
	if exists(filepath.Join(root, transactionName)) {
		tmp, e := os.MkdirTemp("", "lit-recovery-preflight-")
		if e != nil {
			return e
		}
		defer os.RemoveAll(tmp)
		if e = copyRecoveryView(root, tmp, host); e != nil {
			return e
		}
		if e = recoverInstallation(tmp, false); e != nil {
			return e
		}
		_, e = inspect(tmp, host)
		return e
	}
	_, e := inspect(root, host)
	return e
}
