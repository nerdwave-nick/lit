package workflowskills

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
)

// hostRemoval is what taking the hook installation away changes in the host
// file (settings.json or hooks.json); change is nil when it stays as it is.
type hostRemoval struct {
	change          *fileChange
	statusLine      string
	warnings, notes []string
}

func planHostRemoval(root, host string, prev owned) (hostRemoval, error) {
	if host == "claude" {
		return planClaudeRemoval(root, prev)
	}
	return planCodexRemoval(root, prev)
}

func listed(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

// removalChange is the change of a host file: the edited content, or the
// deletion of a file setup created when nothing but its own content is left.
func removalChange(name string, doc *object, before *string, edited, drop bool) *fileChange {
	switch {
	case drop:
		return &fileChange{name: name, before: before, guard: true}
	case edited:
		return &fileChange{name: name, content: doc.bytes(), before: before, guard: true}
	}
	return nil
}

// planClaudeRemoval puts the status line back: lit's statusLine is replaced by
// the original it wrapped, or deleted when there was none. A status line that
// no longer is what setup wrote is the user's now and stays.
func planClaudeRemoval(root string, prev owned) (hr hostRemoval, err error) {
	created := listed(prev.created, "settings.json")
	if prev.statusLine == nil && !created {
		return hr, nil
	}
	path := filepath.Join(root, "settings.json")
	old, before, err := readHostFile(path)
	if err != nil {
		return hr, err
	}
	if old == nil {
		hr.notes = append(hr.notes, "settings.json no longer exists, so there is no status line to restore")
		return hr, nil
	}
	doc, err := parseObject(old)
	if err != nil {
		return hr, fmt.Errorf("settings.json: %w; fix or move it, then rerun", err)
	}
	if s := prev.statusLine; s != nil {
		hr.statusLine = restoreStatusLine(doc, s, &hr)
	}
	edited := hr.statusLine == "restored" || hr.statusLine == "removed"
	hr.change = removalChange("settings.json", doc, before, edited, created && len(doc.keys) == 0)
	return hr, nil
}

// restoreStatusLine edits doc and reports what became of the status line:
// "restored", "removed" or "left" (empty when there was none to act on).
func restoreStatusLine(doc *object, s *statusLineRecord, hr *hostRemoval) string {
	cur, has := doc.get("statusLine")
	switch {
	case !has || string(compactJSON(cur)) == "null":
		hr.notes = append(hr.notes, "settings.json has no statusLine any more, so there was nothing to restore")
	case !equalJSON(cur, s.Installed):
		msg := "the statusLine in settings.json was changed after setup wrote it, so it was left as it is"
		if s.Original != nil {
			msg += "; the status line setup wrapped was " + string(s.Original)
		}
		hr.warnings = append(hr.warnings, msg)
		return "left"
	case s.Original != nil:
		doc.set("statusLine", s.Original)
		return "restored"
	default:
		doc.del("statusLine")
		return "removed"
	}
	return ""
}

// planCodexRemoval takes lit's recorded groups out of hooks.json: for each, the
// first group equal to it by meaning. A group the user edited no longer matches
// and stays; so do the lists that setup emptied, because the file does not say
// whether the user wrote them.
func planCodexRemoval(root string, prev owned) (hr hostRemoval, err error) {
	created := listed(prev.created, "hooks.json")
	if len(prev.hooks) == 0 && !created {
		return hr, nil
	}
	path := filepath.Join(root, "hooks.json")
	old, before, err := readHostFile(path)
	if err != nil {
		return hr, err
	}
	if old == nil {
		hr.notes = append(hr.notes, "hooks.json no longer exists, so there are no hook groups to remove")
		return hr, nil
	}
	doc, err := parseObject(old)
	if err != nil {
		return hr, fmt.Errorf("hooks.json: %w; fix or move it, then rerun", err)
	}
	hooks := newObject()
	if raw, ok := doc.get("hooks"); ok {
		if hooks, err = parseObject(raw); err != nil {
			return hr, errors.New(`hooks.json: "hooks" is not an object; fix or move it, then rerun`)
		}
	}
	edited := false
	for _, e := range prev.hooks {
		var list []json.RawMessage
		raw, ok := hooks.get(e.Event)
		if !ok || json.Unmarshal(raw, &list) != nil || indexOfJSON(list, e.Group) < 0 {
			hr.warnings = append(hr.warnings, fmt.Sprintf("%s: the %s entry setup added was edited or removed; setup left what is there", path, e.Event))
			continue
		}
		i := indexOfJSON(list, e.Group)
		hooks.set(e.Event, marshalRaw(append(list[:i:i], list[i+1:]...)))
		edited = true
	}
	if edited {
		doc.set("hooks", hooks.raw())
	}
	hr.change = removalChange("hooks.json", doc, before, edited, created && onlyEmptyLists(doc))
	return hr, nil
}

// onlyEmptyLists reports that hooks.json holds nothing but event lists without
// entries: what a hooks.json setup created looks like once its groups are gone.
func onlyEmptyLists(doc *object) bool {
	for _, k := range doc.keys {
		if k != "hooks" {
			return false
		}
	}
	raw, ok := doc.get("hooks")
	if !ok {
		return true
	}
	hooks, err := parseObject(raw)
	if err != nil {
		return false
	}
	for _, k := range hooks.keys {
		var list []json.RawMessage
		if json.Unmarshal(hooks.vals[k], &list) != nil || len(list) != 0 {
			return false
		}
	}
	return true
}
