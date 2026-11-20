package hook

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Host is an agent host whose hooks lit serves. Both hosts share the JSON
// shapes lit needs for session start; the type is where they diverge for
// events that do not (a stop decision, a subagent's identity).
type Host struct{ Name string }

func lookupHost(name string) (Host, bool) {
	switch name {
	case "claude", "codex":
		return Host{name}, true
	}
	return Host{}, false
}

// Input is a host's hook input reduced to the fields lit uses.
type Input struct {
	// SessionID is the host's session ID, which lit uses as the runtime ID.
	SessionID string
	// Source is the SessionStart source: startup, resume, clear, compact or fork.
	Source string
	Cwd    string
	// Raw holds every field of the input for events that need more.
	Raw map[string]json.RawMessage
	// Body is the input exactly as the host wrote it.
	Body []byte
}

// Decode parses the hook JSON of event e. Unknown fields are ignored: hosts add
// them between versions.
func (h Host) Decode(e Event, raw []byte) (Input, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return Input{}, errors.New("the hook input is not a JSON object")
	}
	text := func(name string) string {
		var s string
		if json.Unmarshal(fields[name], &s) != nil {
			return ""
		}
		return s
	}
	in := Input{SessionID: text("session_id"), Source: text("source"), Cwd: text("cwd"), Raw: fields, Body: raw}
	if name := text("hook_event_name"); name != "" && e.HostName != "" && name != e.HostName {
		return in, fmt.Errorf("the hook input is for %s, not %s", name, e.HostName)
	}
	if in.SessionID == "" {
		return in, errors.New("the hook input has no session_id")
	}
	return in, nil
}

// Context renders host output that hands text to the agent as additional
// context of event e.
func (h Host) Context(e Event, text string) []byte {
	out, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{
		"hookEventName":     e.HostName,
		"additionalContext": text,
	}})
	return append(out, '\n')
}
