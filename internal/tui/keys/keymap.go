package keys

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Binding is one action's effective keys in one context.
type Binding struct {
	Context     Context
	Action      Action
	Description string
	Keys        []string // canonical key names; empty when unbound
}

// Keymap is a validated set of bindings. Build one with Default or New; it is
// immutable afterwards.
type Keymap struct {
	keys  map[Context]map[Action][]string
	index map[Context]map[string]Action
}

// Default returns the documented default keymap.
func Default() Keymap {
	k := Keymap{keys: map[Context]map[Action][]string{}}
	for ctx, entries := range vocabulary {
		k.keys[ctx] = map[Action][]string{}
		for _, e := range entries {
			k.keys[ctx][e.action] = slices.Clone(e.keys)
		}
	}
	k.reindex()
	return k
}

// New applies overrides, context -> action -> keys, to the default keymap and
// validates the result. A listed action's keys replace its default keys; an
// empty list unbinds it. Every problem found is reported in one error.
func New(overrides map[string]map[string][]string) (Keymap, error) {
	k := Default()
	var errs []error
	for _, cname := range slices.Sorted(maps.Keys(overrides)) {
		ctx := Context(cname)
		entries, ok := vocabulary[ctx]
		if !ok {
			errs = append(errs, fmt.Errorf("keymap: unknown context %q (contexts: %s)", cname, join(Contexts)))
			continue
		}
		for _, aname := range slices.Sorted(maps.Keys(overrides[cname])) {
			if !slices.ContainsFunc(entries, func(e entry) bool { return e.action == Action(aname) }) {
				errs = append(errs, fmt.Errorf("keymap.%s: unknown action %q (%s actions: %s)", cname, aname, cname, join(actions(entries))))
				continue
			}
			keys, err := parseList("keymap."+cname+"."+aname, overrides[cname][aname])
			if err != nil {
				errs = append(errs, err)
				continue
			}
			k.keys[ctx][Action(aname)] = keys
		}
	}
	for _, r := range required {
		if len(k.keys[r.Context][r.Action]) == 0 {
			errs = append(errs, fmt.Errorf("keymap.%s.%s: %s must keep at least one key", r.Context, r.Action, r.Action))
		}
	}
	errs = append(errs, k.conflicts()...)
	if len(errs) > 0 {
		return Keymap{}, errors.Join(errs...)
	}
	k.reindex()
	return k, nil
}

// parseList validates one action's key list from the preferences file.
func parseList(where string, list []string) ([]string, error) {
	if list == nil {
		return nil, fmt.Errorf("%s: must be a list of keys", where)
	}
	var errs []error
	keys := []string{}
	for _, s := range list {
		c, err := ParseKey(s)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("%s: cannot parse key %q: %w", where, s, err))
		case c == Shutdown:
			errs = append(errs, fmt.Errorf("%s: %q is reserved for the orderly shutdown and cannot be bound", where, Shutdown))
		case slices.Contains(keys, c):
			errs = append(errs, fmt.Errorf("%s: key %q is listed twice", where, c))
		default:
			keys = append(keys, c)
		}
	}
	return keys, errors.Join(errs...)
}

// conflicts reports keys bound to two actions in one context, counting the
// global bindings every other context inherits.
func (k Keymap) conflicts() []error {
	var errs []error
	seen := map[string]bool{}
	for _, ctx := range Contexts[1:] {
		owner := map[string]string{}
		for _, c := range []Context{Global, ctx} {
			for _, e := range vocabulary[c] {
				name := string(c) + "." + string(e.action)
				for _, key := range k.keys[c][e.action] {
					prev, taken := owner[key]
					if !taken {
						owner[key] = name
						continue
					}
					if msg := fmt.Sprintf("keymap: key %q is bound to both %s and %s", key, prev, name); !seen[msg] {
						seen[msg] = true
						errs = append(errs, errors.New(msg))
					}
				}
			}
		}
	}
	return errs
}

func (k *Keymap) reindex() {
	k.index = map[Context]map[string]Action{}
	for ctx, actions := range k.keys {
		k.index[ctx] = map[string]Action{}
		for a, keys := range actions {
			for _, key := range keys {
				k.index[ctx][key] = a
			}
		}
	}
}

// chain lists the contexts a lookup in ctx consults: ctx, then global.
func chain(ctx Context) []Context {
	if ctx == Global {
		return []Context{Global}
	}
	return []Context{ctx, Global}
}

// Lookup returns the action key is bound to in ctx, including the inherited
// global bindings. Ctrl-C is never bound: check IsShutdown first.
func (k Keymap) Lookup(ctx Context, key tea.Key) (Action, bool) {
	if IsShutdown(key) {
		return "", false
	}
	for _, name := range canonical(key) {
		for _, c := range chain(ctx) {
			if a, ok := k.index[c][name]; ok {
				return a, true
			}
		}
	}
	return "", false
}

// Keys returns the effective keys of action in ctx itself.
func (k Keymap) Keys(ctx Context, a Action) []string {
	return append([]string{}, k.keys[ctx][a]...)
}

// Bindings returns ctx's own bindings, then the inherited global ones, in
// vocabulary order, including unbound actions. The action menu lists these.
func (k Keymap) Bindings(ctx Context) []Binding {
	var out []Binding
	for _, c := range chain(ctx) {
		for _, e := range vocabulary[c] {
			out = append(out, Binding{Context: c, Action: e.action, Description: e.description, Keys: k.Keys(c, e.action)})
		}
	}
	return out
}

// Help returns help lines for ctx from the effective bindings: a section for
// ctx, then one for the global bindings including the reserved Ctrl-C.
// Unbound actions are left out.
func (k Keymap) Help(ctx Context) []string {
	type row struct{ keys, description string }
	var sections [][]row
	width := 0
	for _, c := range chain(ctx) {
		rows := []row{{keys: strings.ToUpper(string(c[:1])) + string(c[1:])}}
		for _, b := range k.Bindings(c)[:len(vocabulary[c])] {
			if len(b.Keys) > 0 {
				rows = append(rows, row{strings.Join(b.Keys, ", "), b.Description})
			}
		}
		if c == Global {
			rows = append(rows, row{Shutdown, "orderly shutdown and quit (reserved)"})
		}
		for _, r := range rows[1:] {
			width = max(width, len(r.keys))
		}
		sections = append(sections, rows)
	}
	var lines []string
	for i, rows := range sections {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, rows[0].keys)
		for _, r := range rows[1:] {
			lines = append(lines, fmt.Sprintf("  %-*s  %s", width, r.keys, r.description))
		}
	}
	return lines
}

// Hint returns a one-line hint such as "q quit · ? help" naming the first
// effective key of each bound action; unbound actions are left out.
func (k Keymap) Hint(ctx Context, actions ...Action) string {
	var parts []string
	for _, a := range actions {
		for _, c := range chain(ctx) {
			if keys := k.keys[c][a]; len(keys) > 0 {
				parts = append(parts, keys[0]+" "+strings.ReplaceAll(string(a), "_", " "))
				break
			}
		}
	}
	return strings.Join(parts, " · ")
}

func actions(entries []entry) []Action {
	out := make([]Action, len(entries))
	for i, e := range entries {
		out[i] = e.action
	}
	return out
}

func join[T ~string](xs []T) string {
	s := make([]string, len(xs))
	for i, x := range xs {
		s[i] = string(x)
	}
	return strings.Join(s, ", ")
}
