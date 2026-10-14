package workflowskills

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
)

// Codex has no plugin for lit's hooks: setup appends lit's groups at the end
// of the event lists of <config>/hooks.json, records them exactly and, on a
// later run, replaces them by exact match. Other entries are never touched.
// Trust is never written: Codex skips a new or changed hook until the user
// approves it in /hooks.

func planCodex(root string, o Options, prev owned) (hookPlan, error) {
	hp := hookPlan{active: true}
	path := filepath.Join(root, "hooks.json")
	old, before, err := readHostFile(path)
	if err != nil {
		return hp, err
	}
	edit, err := editHooksJSON(old, o.binary(), prev.hooks)
	if err != nil {
		return hp, fmt.Errorf("hooks.json: %w; fix or move it, then rerun", err)
	}
	hp.file = hostFileChange("hooks.json", old, edit.content, before)
	hp.created = createdFiles(prev.created, "hooks.json", before)
	hp.hooks = edit.entries
	for _, w := range edit.warnings {
		hp.warnings = append(hp.warnings, path+": "+w)
	}
	hp.warnings = append(hp.warnings, codexConfigWarnings(root)...)
	hp.notes = append(hp.notes, "Codex skips hooks it has not been told to trust: open /hooks in Codex and approve these lit hooks (setup never trusts them for you):")
	for _, d := range hookDefs {
		hp.notes = append(hp.notes, "  "+d.event+": "+hookCommand(o.binary(), "codex", d.command))
	}
	return hp, nil
}

type hooksEdit struct {
	content  []byte
	entries  []hookEntry
	warnings []string
}

// editHooksJSON appends lit's groups to the content of hooks.json (nil when the
// file does not exist) and returns the groups it owns afterwards.
func editHooksJSON(old []byte, binary string, prev []hookEntry) (hooksEdit, error) {
	doc, hooks := newObject(), newObject()
	if old != nil {
		var err error
		if doc, err = parseObject(old); err != nil {
			return hooksEdit{}, err
		}
	}
	if raw, ok := doc.get("hooks"); ok {
		var err error
		if hooks, err = parseObject(raw); err != nil {
			return hooksEdit{}, errors.New(`"hooks" is not an object`)
		}
	}
	var edit hooksEdit
	changed := false
	for _, d := range hookDefs {
		want := hookGroupFor("codex", binary, d)
		var list []json.RawMessage
		if raw, ok := hooks.get(d.event); ok {
			if string(compactJSON(raw)) == "null" || json.Unmarshal(raw, &list) != nil {
				return hooksEdit{}, fmt.Errorf("the %s entry is not a list", d.event)
			}
		}
		owned := false
		for _, e := range prev {
			if e.Event != d.event {
				continue
			}
			i := indexOfJSON(list, e.Group)
			switch {
			case i < 0:
				edit.warnings = append(edit.warnings, fmt.Sprintf("the %s entry setup added was edited or removed; setup left what is there", d.event))
			case equalJSON(e.Group, want):
				owned = true
			default:
				list = append(list[:i:i], list[i+1:]...)
				changed = true
				hooks.set(d.event, marshalRaw(list))
			}
		}
		switch {
		case owned:
		case indexOfJSON(list, want) >= 0:
			edit.warnings = append(edit.warnings, fmt.Sprintf("a %s entry identical to lit's is already there and was not added by setup; setup did not add another", d.event))
			continue
		default:
			list = append(list, want)
			changed = true
			hooks.set(d.event, marshalRaw(list))
		}
		edit.entries = append(edit.entries, hookEntry{Event: d.event, Group: want})
	}
	if !changed {
		edit.content = old
		return edit, nil
	}
	doc.set("hooks", hooks.raw())
	edit.content = doc.bytes()
	return edit, nil
}

func indexOfJSON(list []json.RawMessage, v json.RawMessage) int {
	for i, item := range list {
		if equalJSON(item, v) {
			return i
		}
	}
	return -1
}
