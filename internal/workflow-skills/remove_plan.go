package workflowskills

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// removal is what taking one agent configuration's installation away changes,
// computed before anything is written.
type removal struct {
	found       bool
	instruction string
	// trees maps the owned directories below skills/ that exist (skills and the
	// hooks plugin and the mod) to their verified fingerprints; skills counts
	// the skills.
	trees  map[string]string
	skills int
	// changes are the file writes, the ownership record last.
	changes         []fileChange
	hooks, mod      bool
	statusLine      string
	warnings, notes []string
}

// inspectRemoval plans the removal of the installation in the configuration at
// root (the target's directory, or a copy of it during preflight). It refuses
// owned files the user changed and leaves everything the record does not list.
func inspectRemoval(root string, t Target) (r removal, err error) {
	r = removal{instruction: instructionOf(t.Host), trees: map[string]string{}}
	for _, f := range []string{root, filepath.Join(root, "skills"), filepath.Join(root, recordName), filepath.Join(root, legacyRecordName)} {
		if err = safe(f); err != nil {
			return r, err
		}
	}
	prev, err := readOwned(root, t.Host)
	if err != nil {
		return r, err
	}
	text, before, err := readHostFile(filepath.Join(root, r.instruction))
	if err != nil {
		return r, err
	}
	if !prev.exists {
		if strings.Contains(string(text), start) {
			r.warnings = append(r.warnings, fmt.Sprintf("%s holds a lit managed block but no ownership record (%s) lists it, so it was left as it is", r.instruction, recordName))
		}
		r.notes = append(r.notes, fmt.Sprintf("no ownership record (%s) in %s; nothing to remove", recordName, root))
		return r, nil
	}
	r.found = true
	if err = r.planTrees(root, prev); err != nil {
		return r, err
	}
	ic, err := r.planInstruction(text, before)
	if err != nil {
		return r, err
	}
	hr, err := planHostRemoval(root, t.Host, prev)
	if err != nil {
		return r, err
	}
	r.hooks = prev.plugin != nil || len(prev.hooks) > 0
	r.mod = prev.mod != nil
	r.statusLine = hr.statusLine
	r.warnings, r.notes = append(r.warnings, hr.warnings...), append(r.notes, hr.notes...)
	r.changes = []fileChange{ic}
	if hr.change != nil {
		r.changes = append(r.changes, *hr.change)
	}
	record := recordName
	if prev.legacy {
		record = legacyRecordName
	}
	r.changes = append(r.changes, fileChange{name: record})
	return r, nil
}

// planTrees verifies the skills, the hooks plugin and the mod the record lists: one
// that is gone needs no removal, one the user changed is refused.
func (r *removal) planTrees(root string, prev owned) error {
	names := []string{}
	for n, v := range prev.skills {
		if !allowed(n) || !validHash(ptr(v)) {
			return errors.New("invalid manifest entry")
		}
		names = append(names, n)
	}
	sort.Strings(names)
	if prev.plugin != nil {
		names = append(names, pluginName)
	}
	if prev.mod != nil {
		names = append(names, modName)
	}
	for _, n := range names {
		want, _ := prev.tree(n)
		kind := map[string]string{pluginName: "hooks plugin", modName: "mod"}[n]
		if kind == "" {
			kind = "skill"
		}
		dir := filepath.Join(root, "skills", n)
		if !exists(dir) {
			r.notes = append(r.notes, fmt.Sprintf("the %s %s was already gone", kind, n))
			continue
		}
		got, err := fingerprint(dir)
		if err != nil {
			return fmt.Errorf("managed %s %s: %w", kind, n, err)
		}
		if got != want {
			return fmt.Errorf("managed %s modified: %s; setup does not remove files you changed (restore or delete it yourself, then rerun)", kind, n)
		}
		r.trees[n] = want
		if n != pluginName && n != modName {
			r.skills++
		}
	}
	return nil
}

// planInstruction takes the managed block out of the instruction file (nil
// text when the file does not exist). A file with nothing else in it is deleted.
func (r *removal) planInstruction(text []byte, before *string) (fileChange, error) {
	c := fileChange{name: r.instruction, before: before, guard: true}
	if text == nil {
		return c, nil
	}
	out, had, err := dropBlock(string(text))
	if err != nil {
		return c, err
	}
	if !had {
		r.notes = append(r.notes, r.instruction+" had no managed block")
	}
	if had && out == "" {
		return c, nil
	}
	c.content = append(make([]byte, 0, len(out)), out...)
	return c, nil
}

// dropBlock removes the managed block and the line break that ends its line.
func dropBlock(text string) (out string, had bool, err error) {
	s, e := strings.Index(text, start), strings.Index(text, end)
	if strings.Count(text, start) != strings.Count(text, end) || strings.Count(text, start) > 1 || s > e {
		return "", false, errors.New("malformed managed instruction block")
	}
	if s < 0 {
		return text, false, nil
	}
	rest := text[e+len(end):]
	switch {
	case strings.HasPrefix(rest, "\r\n"):
		rest = rest[2:]
	case strings.HasPrefix(rest, "\n"):
		rest = rest[1:]
	}
	return text[:s] + rest, true, nil
}
