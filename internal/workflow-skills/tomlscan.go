package workflowskills

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	tableHeader   = regexp.MustCompile(`^\[\[?\s*([^\]]*?)\s*\]\]?\s*(#.*)?$`)
	featureOff    = regexp.MustCompile(`^(?:features\s*\.\s*)?(?:hooks|codex_hooks)\s*=\s*false\b`)
	featureOffKey = regexp.MustCompile(`^(?:hooks|codex_hooks)\s*=\s*false\b`)
)

// codexConfigWarnings looks at the config.toml of a Codex configuration, which
// setup never edits, for what undermines the hooks it installs in hooks.json:
// hooks defined there too (Codex warns about a layer that has both) and hooks
// switched off. It reads lines, not TOML, which is enough for these two
// settings.
func codexConfigWarnings(root string) []string {
	path := filepath.Join(root, "config.toml")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var warnings []string
	inTable, inFeatures, hooksTable, disabled := false, false, false, false
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if m := tableHeader.FindStringSubmatch(line); strings.HasPrefix(line, "[") && m != nil {
			keys := splitTableKeys(m[1])
			inTable = true
			inFeatures = len(keys) == 1 && keys[0] == "features"
			if len(keys) > 0 && keys[0] == "hooks" && (len(keys) < 2 || keys[1] != "state") {
				hooksTable = true
			}
			continue
		}
		if (inFeatures && featureOffKey.MatchString(line)) || (!inTable && featureOff.MatchString(line)) {
			disabled = true
		}
	}
	if hooksTable {
		warnings = append(warnings, path+": [hooks] is defined here too; Codex warns about a layer that has both hooks.json and [hooks], and setup edits only hooks.json, so keep the hooks in one of the two")
	}
	if disabled {
		warnings = append(warnings, path+": hooks are disabled ([features] hooks = false), so the lit hooks in hooks.json will not run until that is removed")
	}
	return warnings
}

// splitTableKeys splits the dotted key of a table header, unquoting parts.
func splitTableKeys(s string) []string {
	var keys []string
	var cur strings.Builder
	quote := rune(0)
	flush := func() {
		keys = append(keys, strings.TrimSpace(cur.String()))
		cur.Reset()
	}
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
		case r == '.':
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return keys
}
