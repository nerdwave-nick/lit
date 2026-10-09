package keys

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func press(code rune, mod tea.KeyMod, text string) tea.Key {
	return tea.Key{Code: code, Mod: mod, Text: text}
}

var (
	keyUp    = press(tea.KeyUp, 0, "")
	keyDown  = press(tea.KeyDown, 0, "")
	keyLeft  = press(tea.KeyLeft, 0, "")
	keyRight = press(tea.KeyRight, 0, "")
	keyEnter = press(tea.KeyEnter, 0, "")
	keyEsc   = press(tea.KeyEscape, 0, "")
	keySpace = press(tea.KeySpace, 0, " ")
	keyCtrlC = press('c', tea.ModCtrl, "")
)

func char(r rune) tea.Key { return press(r, 0, string(r)) }

func mustNew(t *testing.T, o map[string]map[string][]string) Keymap {
	t.Helper()
	k, err := New(o)
	if err != nil {
		t.Fatalf("New(%v): %v", o, err)
	}
	return k
}

func TestNewWithoutOverridesIsTheDefaultKeymap(t *testing.T) {
	k := mustNew(t, nil)
	for _, ctx := range Contexts {
		if got, want := k.Bindings(ctx), Default().Bindings(ctx); !slices.EqualFunc(got, want, func(a, b Binding) bool {
			return a.Context == b.Context && a.Action == b.Action && slices.Equal(a.Keys, b.Keys)
		}) {
			t.Errorf("%s: New(nil) %v, Default %v", ctx, got, want)
		}
	}
}

// Arrows, Enter and Escape come first, with vim-style alternates.
func TestDefaultKeymapPutsArrowsEnterAndEscapeFirst(t *testing.T) {
	k := Default()
	for _, tc := range []struct {
		ctx    Context
		action Action
		keys   []string
	}{
		{Global, Quit, []string{"q"}},
		{Global, Help, []string{"?"}},
		{Tree, Up, []string{"up", "k"}},
		{Tree, Down, []string{"down", "j"}},
		{Tree, Collapse, []string{"left", "h"}},
		{Tree, Expand, []string{"right", "l"}},
		{Tree, Open, []string{"enter"}},
		{Tree, Top, []string{"home", "g"}},
		{Tree, Bottom, []string{"end", "G"}},
		{Detail, Back, []string{"esc"}},
		{Detail, Left, []string{"left", "h"}},
		{Detail, Right, []string{"right", "l"}},
		{Detail, PageDown, []string{"pgdown", "ctrl+d"}},
		{Menu, Cancel, []string{"esc"}},
		{Menu, Open, []string{"enter"}},
	} {
		if got := k.Keys(tc.ctx, tc.action); !slices.Equal(got, tc.keys) {
			t.Errorf("%s.%s: %v, want %v", tc.ctx, tc.action, got, tc.keys)
		}
	}
}

func TestLookupResolvesTheContextThenGlobal(t *testing.T) {
	k := Default()
	for _, tc := range []struct {
		name   string
		ctx    Context
		key    tea.Key
		action Action
		ok     bool
	}{
		{"tree down arrow", Tree, keyDown, Down, true},
		{"tree j", Tree, char('j'), Down, true},
		{"tree up arrow", Tree, keyUp, Up, true},
		{"tree left collapses", Tree, keyLeft, Collapse, true},
		{"tree right expands", Tree, keyRight, Expand, true},
		{"tree enter opens", Tree, keyEnter, Open, true},
		{"tree inherits quit", Tree, char('q'), Quit, true},
		{"tree shifted G by text", Tree, press('g', tea.ModShift, "G"), Bottom, true},
		{"tree shifted G without text", Tree, press('g', tea.ModShift, ""), Bottom, true},
		{"tree ctrl+d", Tree, press('d', tea.ModCtrl, ""), PageDown, true},
		{"tree esc unbound", Tree, keyEsc, "", false},
		{"detail esc goes back", Detail, keyEsc, Back, true},
		{"detail left scrolls", Detail, keyLeft, Left, true},
		{"menu esc cancels", Menu, keyEsc, Cancel, true},
		{"menu inherits help", Menu, char('?'), Help, true},
		{"global has no tree keys", Global, char('j'), "", false},
		{"unbound x", Tree, char('x'), "", false},
		{"space unbound", Tree, keySpace, "", false},
	} {
		action, ok := k.Lookup(tc.ctx, tc.key)
		if action != tc.action || ok != tc.ok {
			t.Errorf("%s: %q %v, want %q %v", tc.name, action, ok, tc.action, tc.ok)
		}
	}
}

func TestCtrlCIsReservedAndCannotBeRebound(t *testing.T) {
	if !IsShutdown(keyCtrlC) {
		t.Error("ctrl+c is not the shutdown key")
	}
	if IsShutdown(char('c')) || IsShutdown(char('q')) {
		t.Error("c or q taken for the shutdown key")
	}
	for _, ctx := range Contexts {
		if action, ok := Default().Lookup(ctx, keyCtrlC); ok {
			t.Errorf("%s: ctrl+c looked up as %q", ctx, action)
		}
	}
	for _, o := range []map[string]map[string][]string{
		{"global": {"refresh": {"ctrl+c"}}},
		{"tree": {"expand": {"CTRL+c"}}},
		{"menu": {"cancel": {"esc", "ctrl+c"}}},
	} {
		_, err := New(o)
		if err == nil || !strings.Contains(err.Error(), `"ctrl+c" is reserved for the orderly shutdown and cannot be bound`) {
			t.Errorf("%v: %v", o, err)
		}
	}
}

func TestNewReplacesTheDefaultKeysOfListedActions(t *testing.T) {
	k := mustNew(t, map[string]map[string][]string{
		"tree":   {"expand": {"space", "L"}, "collapse": {}},
		"global": {"quit": {"x"}},
	})
	for _, tc := range []struct {
		name   string
		ctx    Context
		key    tea.Key
		action Action
		ok     bool
	}{
		{"space expands", Tree, keySpace, Expand, true},
		{"L expands", Tree, press('l', tea.ModShift, "L"), Expand, true},
		{"right no longer expands", Tree, keyRight, "", false},
		{"collapse unbound", Tree, keyLeft, "", false},
		{"x quits", Tree, char('x'), Quit, true},
		{"q no longer quits", Tree, char('q'), "", false},
		{"unlisted actions keep their keys", Tree, char('j'), Down, true},
		{"other contexts keep their keys", Detail, keyRight, Right, true},
	} {
		action, ok := k.Lookup(tc.ctx, tc.key)
		if action != tc.action || ok != tc.ok {
			t.Errorf("%s: %q %v, want %q %v", tc.name, action, ok, tc.action, tc.ok)
		}
	}
	if got := k.Keys(Tree, Collapse); len(got) != 0 {
		t.Errorf("collapse keys %v", got)
	}
}

func TestNewRejectsInvalidKeymaps(t *testing.T) {
	for _, tc := range []struct {
		name string
		o    map[string]map[string][]string
		want string
	}{
		{"unknown context", map[string]map[string][]string{"sidebar": {"up": {"k"}}},
			`keymap: unknown context "sidebar" (contexts: global, tree, detail, menu)`},
		{"unknown action", map[string]map[string][]string{"tree": {"jump": {"J"}}},
			`keymap.tree: unknown action "jump" (tree actions: up, down, collapse, expand, open, page_up, page_down, top, bottom)`},
		{"action of another context", map[string]map[string][]string{"global": {"up": {"k"}}},
			`keymap.global: unknown action "up"`},
		{"empty key", map[string]map[string][]string{"tree": {"up": {""}}},
			`keymap.tree.up: cannot parse key "": empty key`},
		{"dangling modifier", map[string]map[string][]string{"tree": {"up": {"ctrl+"}}},
			`keymap.tree.up: cannot parse key "ctrl+"`},
		{"unknown modifier", map[string]map[string][]string{"tree": {"up": {"hyperx+a"}}},
			`keymap.tree.up: cannot parse key "hyperx+a": unknown modifier "hyperx"`},
		{"unknown key name", map[string]map[string][]string{"tree": {"up": {"pageup"}}},
			`keymap.tree.up: cannot parse key "pageup": unknown key name "pageup"`},
		{"repeated modifier", map[string]map[string][]string{"tree": {"up": {"ctrl+ctrl+a"}}},
			`keymap.tree.up: cannot parse key "ctrl+ctrl+a": repeated modifier "ctrl"`},
		{"literal space", map[string]map[string][]string{"tree": {"up": {" "}}},
			`keymap.tree.up: cannot parse key " ": write "space"`},
		{"missing key list", map[string]map[string][]string{"tree": {"up": nil}},
			`keymap.tree.up: must be a list of keys`},
		{"key listed twice", map[string]map[string][]string{"tree": {"up": {"k", "K", "k"}}},
			`keymap.tree.up: key "k" is listed twice`},
		{"conflict within a context", map[string]map[string][]string{"tree": {"expand": {"k"}}},
			`keymap: key "k" is bound to both tree.up and tree.expand`},
		{"spelling variants conflict", map[string]map[string][]string{"tree": {"expand": {"shift+g"}}},
			`keymap: key "G" is bound to both tree.expand and tree.bottom`},
		{"conflict with an inherited global key", map[string]map[string][]string{"detail": {"back": {"q"}}},
			`keymap: key "q" is bound to both global.quit and detail.back`},
		{"global key shadowing a context key", map[string]map[string][]string{"global": {"refresh": {"j"}}},
			`keymap: key "j" is bound to both global.refresh and tree.down`},
		{"quit unbound", map[string]map[string][]string{"global": {"quit": {}}},
			`keymap.global.quit: quit must keep at least one key`},
		{"help unbound", map[string]map[string][]string{"global": {"help": {}}},
			`keymap.global.help: help must keep at least one key`},
		{"cancel unbound", map[string]map[string][]string{"menu": {"cancel": {}}},
			`keymap.menu.cancel: cancel must keep at least one key`},
	} {
		_, err := New(tc.o)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %v, want %q", tc.name, err, tc.want)
		}
	}
}

func TestNewReportsEveryProblemAtOnce(t *testing.T) {
	_, err := New(map[string]map[string][]string{
		"global": {"quit": {}},
		"tree":   {"jump": {"J"}, "up": {"ctrl+"}},
	})
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{"quit must keep at least one key", `unknown action "jump"`, `cannot parse key "ctrl+"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

// Rebinding quit away from q to a key a context uses is fine once the context
// key moves too: conflicts are checked on the result, not step by step.
func TestNewChecksConflictsOnTheResult(t *testing.T) {
	k := mustNew(t, map[string]map[string][]string{
		"global": {"quit": {"j"}},
		"tree":   {"down": {"down", "n"}},
		"detail": {"down": {"down"}},
		"menu":   {"down": {"down"}},
	})
	if action, _ := k.Lookup(Tree, char('j')); action != Quit {
		t.Errorf("j: %q", action)
	}
}

func TestParseKeyCanonicalises(t *testing.T) {
	for in, want := range map[string]string{
		"q":            "q",
		"?":            "?",
		"G":            "G",
		"shift+g":      "G",
		"Shift+G":      "G",
		"ctrl+k":       "ctrl+k",
		"Ctrl+K":       "ctrl+shift+k",
		"ctrl+shift+k": "ctrl+shift+k",
		"shift+alt+x":  "alt+shift+x",
		"alt+ctrl+x":   "ctrl+alt+x",
		"PgDown":       "pgdown",
		"ESC":          "esc",
		"shift+tab":    "shift+tab",
		"f5":           "f5",
		"space":        "space",
		"+":            "+",
		"ctrl++":       "ctrl++",
		"é":            "é",
	} {
		got, err := ParseKey(in)
		if err != nil || got != want {
			t.Errorf("ParseKey(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "ctrl+", "+a", "ctrl", "foo", "\x1b", "\t", "ctrl+foo", "shift+"} {
		if got, err := ParseKey(in); err == nil {
			t.Errorf("ParseKey(%q) = %q, want an error", in, got)
		}
	}
}

func TestHelpComesFromTheEffectiveBindings(t *testing.T) {
	help := strings.Join(Default().Help(Tree), "\n")
	for _, want := range []string{"Tree", "up, k", "select the previous issue", "Global", "q", "quit", "ctrl+c", "orderly shutdown"} {
		if !strings.Contains(help, want) {
			t.Errorf("default tree help lacks %q:\n%s", want, help)
		}
	}
	if strings.Index(help, "Tree") > strings.Index(help, "Global") {
		t.Errorf("context section must come before global:\n%s", help)
	}
	k := mustNew(t, map[string]map[string][]string{
		"global": {"quit": {"x"}, "refresh": {}},
		"tree":   {"up": {"w"}},
	})
	lines := k.Help(Tree)
	if !lineWith(lines, "x", "quit") || lineWith(lines, "q", "quit") {
		t.Errorf("rebound quit not shown as x:\n%s", strings.Join(lines, "\n"))
	}
	if !lineWith(lines, "w", "select the previous issue") || lineWith(lines, "up, k", "select the previous issue") {
		t.Errorf("rebound up not shown as w:\n%s", strings.Join(lines, "\n"))
	}
	if lineWith(lines, "", "refresh") {
		t.Errorf("unbound refresh shown:\n%s", strings.Join(lines, "\n"))
	}
	if g := k.Help(Global); lineWith(g, "", "select the previous issue") || !lineWith(g, "x", "quit") {
		t.Errorf("global help:\n%s", strings.Join(g, "\n"))
	}
}

// lineWith reports whether a help line lists exactly keys (when not empty)
// followed by description.
func lineWith(lines []string, keys, description string) bool {
	for _, l := range lines {
		f := strings.TrimSpace(l)
		if !strings.HasSuffix(f, description) {
			continue
		}
		if keys == "" || strings.TrimSpace(strings.TrimSuffix(f, description)) == keys {
			return true
		}
	}
	return false
}

func TestBindingsListTheContextThenGlobalForTheActionMenu(t *testing.T) {
	b := mustNew(t, map[string]map[string][]string{"global": {"refresh": {}}}).Bindings(Detail)
	var names []string
	for _, x := range b {
		names = append(names, string(x.Context)+"."+string(x.Action))
	}
	want := []string{"detail.up", "detail.down", "detail.left", "detail.right", "detail.back", "detail.page_up", "detail.page_down", "detail.top", "detail.bottom",
		"global.quit", "global.help", "global.refresh", "global.switch_project", "global.action_menu"}
	if !slices.Equal(names, want) {
		t.Errorf("bindings %v, want %v", names, want)
	}
	if b[11].Keys == nil || len(b[11].Keys) != 0 || b[11].Description != "refresh" {
		t.Errorf("unbound refresh: %+v", b[11])
	}
	if got := Default().Bindings(Global); len(got) != 5 {
		t.Errorf("global bindings %v", got)
	}
}

func TestHintNamesTheEffectiveKeys(t *testing.T) {
	if got := Default().Hint(Global, Quit, Help); got != "q quit · ? help" {
		t.Errorf("default hint %q", got)
	}
	k := mustNew(t, map[string]map[string][]string{"global": {"quit": {"x", "Q"}, "refresh": {}}})
	if got := k.Hint(Global, Quit, Refresh, Help); got != "x quit · ? help" {
		t.Errorf("rebound hint %q", got)
	}
}
