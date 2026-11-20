package workflowskills

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
)

// pluginName is the lit-owned Claude plugin directory below skills/ that
// carries the Claude hooks.
const pluginName = "lit-hooks"

// hookDef is one hook lit installs: the host's event name, the `lit hook`
// event serving it and the host's backstop timeout in seconds. lit enforces its
// own, shorter budgets (about 3 s for session-start, 1 s for the others); the
// host's value only bounds a lit that hangs.
type hookDef struct {
	event   string
	command string
	timeout int
}

// hookDefs are the events both hosts get hooks for. The definitions change only
// when the hooks themselves do, because the commands call the installed lit and
// Codex asks for approval again whenever a hook's definition changes.
var hookDefs = []hookDef{
	{"SessionStart", "session-start", 10},
	{"SubagentStart", "subagent-start", 5},
	{"Stop", "stop", 5},
}

func (o Options) binary() string {
	if o.Binary == "" {
		return "lit"
	}
	return o.Binary
}

// hookPlan is what the hook installation changes in one agent configuration,
// computed before anything is written. The zero value (a local scope) installs
// nothing and keeps whatever an earlier run installed.
type hookPlan struct {
	active bool
	// plugin holds the files of the Claude plugin directory, by relative path;
	// mod those of the lit-ui mod directory (nil: no mod after the run), and
	// modRemoved that the run takes away the mod an earlier one installed.
	plugin     map[string][]byte
	mod        map[string][]byte
	modRemoved bool
	// status is the Claude status surface (line, mod or both).
	status string
	// file is the host file to write (settings.json or hooks.json); nil when the
	// installation leaves it as it is.
	file *fileChange
	// hooks and statusLine are the record sections after the run.
	hooks      []hookEntry
	statusLine *statusLineRecord
	created    []string
	// state reports the status line outcome, see Result.StatusLine.
	state    string
	warnings []string
	notes    []string
}

// planHooks decides the hook installation of one target. Local scopes install
// no hooks: they are repositories, and a hook in a repository would run next to
// the user-level one.
func planHooks(root string, t Target, o Options, prev owned) (hookPlan, error) {
	if t.Scope == ScopeLocal {
		return hookPlan{}, nil
	}
	if t.Host == "claude" {
		return planClaude(root, o, prev)
	}
	return planCodex(root, o, prev)
}

// readHostFile reads a file of the host configuration that may not exist; it
// returns the content and the hash the plan is based on (nil when absent).
func readHostFile(path string) (content []byte, before *string, err error) {
	if err = safe(path); err != nil {
		return nil, nil, err
	}
	content, err = os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return content, ptr(hash(content)), nil
}

// hostFileChange is the change of a host file the installation edits. Both the
// user and the host write these files, so the change is guarded: it is applied
// only to the content the plan was computed from.
func hostFileChange(name string, old, content []byte, before *string) *fileChange {
	if bytes.Equal(old, content) && before != nil {
		return nil
	}
	return &fileChange{name: name, content: content, before: before, guard: true}
}

func compactJSON(raw json.RawMessage) json.RawMessage {
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return raw
	}
	return b.Bytes()
}

// equalJSON compares two JSON values by meaning.
func equalJSON(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

func marshalRaw(v any) json.RawMessage {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	_ = e.Encode(v)
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}

func pluginDir(root string) string { return filepath.Join(root, "skills", pluginName) }

// createdFiles returns the host files setup created: name when it did not exist
// before this run or was created by an earlier one.
func createdFiles(prev []string, name string, before *string) []string {
	for _, p := range prev {
		if p == name {
			return []string{name}
		}
	}
	if before == nil {
		return []string{name}
	}
	return nil
}
