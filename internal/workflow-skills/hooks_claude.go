package workflowskills

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// Claude loads <config>/skills/lit-hooks/ as a skills-directory plugin, so the
// hooks are installed like skills and need no settings.json entry. The status
// line cannot come from a plugin; it is the one member of settings.json that
// setup edits.
const (
	// pluginVersion changes only when the hook definitions do.
	pluginVersion = "1.0.0"
	// statusLineRefresh is the refresh interval (seconds) setup configures, so
	// the lit part follows resizes and idle changes.
	statusLineRefresh = 2
)

type hookHandler struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

type hookGroup struct {
	Hooks []hookHandler `json:"hooks"`
}

// hookGroupFor is the hook group of event d for host: no matcher, so every
// start source of the event is served.
func hookGroupFor(host, binary string, d hookDef) json.RawMessage {
	return marshalRaw(hookGroup{[]hookHandler{{"command", hookCommand(binary, host, d.command), d.timeout}}})
}

// claudePlugin returns the files of the lit-hooks plugin.
func claudePlugin(binary string) map[string][]byte {
	manifest := newObject()
	manifest.set("name", marshalRaw(pluginName))
	manifest.set("version", marshalRaw(pluginVersion))
	manifest.set("description", marshalRaw("Session hooks of lit workflows, installed by lit setup-workflows. Do not edit: setup replaces this directory."))
	manifest.set("author", marshalRaw(map[string]string{"name": "lit setup-workflows"}))
	events := newObject()
	for _, d := range hookDefs {
		events.set(d.event, marshalRaw([]json.RawMessage{hookGroupFor("claude", binary, d)}))
	}
	hooks := newObject()
	hooks.set("hooks", events.raw())
	return map[string][]byte{
		filepath.Join(".claude-plugin", "plugin.json"): manifest.bytes(),
		filepath.Join("hooks", "hooks.json"):           hooks.bytes(),
	}
}

func planClaude(root string, o Options, prev owned) (hookPlan, error) {
	hp := hookPlan{active: true, plugin: claudePlugin(o.binary())}
	switch dir := pluginDir(root); {
	case prev.plugin != nil:
		if got, err := fingerprint(dir); err != nil || got != prev.plugin.Fingerprint {
			return hp, fmt.Errorf("managed hooks plugin removed/modified: %s", pluginName)
		}
	case exists(dir):
		return hp, fmt.Errorf("unowned skill collision: %s", pluginName)
	}
	old, before, err := readHostFile(filepath.Join(root, "settings.json"))
	if err != nil {
		return hp, err
	}
	edit, err := editSettings(old, o.binary(), prev.statusLine)
	if err != nil {
		return hp, fmt.Errorf("settings.json: %w; fix or move it, then rerun", err)
	}
	hp.file = hostFileChange("settings.json", old, edit.content, before)
	hp.created = createdFiles(prev.created, "settings.json", before)
	hp.statusLine, hp.state, hp.warnings, hp.notes = edit.statusLine, edit.state, edit.warnings, edit.notes
	return hp, nil
}

type settingsEdit struct {
	content    []byte
	statusLine *statusLineRecord
	state      string
	warnings   []string
	notes      []string
}

// editSettings sets or wraps the status line in the content of settings.json
// (nil when the file does not exist). Nothing else in the file changes.
func editSettings(old []byte, binary string, prev *statusLineRecord) (settingsEdit, error) {
	doc := newObject()
	if old != nil {
		var err error
		if doc, err = parseObject(old); err != nil {
			return settingsEdit{}, err
		}
	}
	var edit settingsEdit
	if v, ok := doc.get("disableAllHooks"); ok && string(compactJSON(v)) == "true" {
		edit.warnings = append(edit.warnings, "settings.json sets disableAllHooks, which also disables the lit hooks and the status line")
	}
	cur, has := doc.get("statusLine")
	if has && string(compactJSON(cur)) == "null" {
		has = false
	}
	if !has {
		value := statusLineValue(binary, "", nil)
		doc.set("statusLine", value)
		edit.content, edit.state = doc.bytes(), "set"
		edit.statusLine = &statusLineRecord{Installed: compactJSON(value)}
		return edit, nil
	}
	value, rec, state, ok := adaptStatusLine(cur, binary, prev)
	switch {
	case !ok:
		edit.content, edit.state = old, "kept"
		edit.notes = append(edit.notes, "the status line in settings.json is not a command lit can wrap, so it was left as it is and the lit part is not shown")
	case state == "unchanged":
		edit.content, edit.state, edit.statusLine = old, state, rec
	default:
		doc.set("statusLine", value)
		edit.content, edit.state, edit.statusLine = doc.bytes(), state, rec
	}
	return edit, nil
}

// statusLineValue is lit's status line value: the command, no padding and the
// refresh interval.
func statusLineValue(binary, original string, base *object) json.RawMessage {
	o := base
	if o == nil {
		o = newObject()
	}
	o.set("type", marshalRaw("command"))
	o.set("command", marshalRaw(statusLineCommand(binary, original)))
	if _, ok := o.get("padding"); !ok {
		o.set("padding", marshalRaw(0))
	}
	if _, ok := o.get("refreshInterval"); !ok {
		o.set("refreshInterval", marshalRaw(statusLineRefresh))
	}
	return o.raw()
}

// adaptStatusLine makes the existing statusLine value lit's: a status line
// that is already lit's is rewritten for this binary (keeping what the user
// tuned and the wrapped original); a foreign command is wrapped. ok is false
// for a value lit cannot wrap.
func adaptStatusLine(cur json.RawMessage, binary string, prev *statusLineRecord) (value json.RawMessage, rec *statusLineRecord, state string, ok bool) {
	o, err := parseObject(cur)
	if err != nil {
		return nil, nil, "", false
	}
	if raw, has := o.get("type"); has && string(compactJSON(raw)) != `"command"` {
		return nil, nil, "", false
	}
	command, has := o.get("command")
	var cmd string
	if json.Unmarshal(command, &cmd) != nil || !has || strings.TrimSpace(cmd) == "" {
		return nil, nil, "", false
	}
	original, wraps, isLit := parseLitStatusLine(cmd)
	if !isLit {
		next := newObject()
		next.set("type", marshalRaw("command"))
		next.set("command", marshalRaw(statusLineCommand(binary, cmd)))
		if p, has := o.get("padding"); has {
			next.set("padding", p)
		}
		value = statusLineValue(binary, cmd, next)
		return value, &statusLineRecord{Installed: compactJSON(value), Original: compactJSON(cur)}, "wrapped", true
	}
	value = statusLineValue(binary, original, o)
	rec = &statusLineRecord{Installed: compactJSON(value)}
	state = "set"
	if wraps {
		state = "wrapped"
		rec.Original = recordedOriginal(prev, original)
	}
	if equalJSON(value, cur) {
		state = "unchanged"
	}
	return value, rec, state, true
}

// recordedOriginal is the original status line lit wrapped. The earlier record
// has it exactly; a wrapper found without a record (written by hand) gets a
// command-only original.
func recordedOriginal(prev *statusLineRecord, command string) json.RawMessage {
	if prev != nil && prev.Original != nil {
		var o struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(prev.Original, &o) == nil && o.Command == command {
			return prev.Original
		}
	}
	synthesized := newObject()
	synthesized.set("type", marshalRaw("command"))
	synthesized.set("command", marshalRaw(command))
	return synthesized.raw()
}
