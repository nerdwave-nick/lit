// Package keys is lit-tui's keymap: the action vocabulary, the default
// bindings, validation of rebindings from the preferences file, key lookup per
// context, and help drawn from the effective bindings.
//
// Ctrl-C is reserved: it always starts the orderly shutdown, is never returned
// by Lookup and cannot be bound.
package keys

// Context is where a binding applies. Tree, detail and menu inherit the global
// bindings; a key bound in global cannot also be bound in another context.
type Context string

const (
	Global Context = "global" // everywhere
	Tree   Context = "tree"   // the issue hierarchy
	Detail Context = "detail" // the issue detail
	Menu   Context = "menu"   // menus, pickers, prompts and the help overlay
)

// Contexts lists every context in display order.
var Contexts = []Context{Global, Tree, Detail, Menu}

// Action is a named user command. Later screens may add actions.
type Action string

const (
	Quit          Action = "quit"
	Help          Action = "help"
	Cancel        Action = "cancel"
	Refresh       Action = "refresh"
	SwitchProject Action = "switch_project"
	ActionMenu    Action = "action_menu"
	Up            Action = "up"
	Down          Action = "down"
	Left          Action = "left"
	Right         Action = "right"
	Open          Action = "open"
	Back          Action = "back"
	Expand        Action = "expand"
	Collapse      Action = "collapse"
	PageUp        Action = "page_up"
	PageDown      Action = "page_down"
	Top           Action = "top"
	Bottom        Action = "bottom"
	NextBlock     Action = "next_block"
	PrevBlock     Action = "prev_block"
	Search        Action = "search"
	Filter        Action = "filter"
	ClearFilters  Action = "clear_filters"
	Claim         Action = "claim"
	Release       Action = "release"
	// History opens the read-only history of the selected issue or of one
	// of its comments.
	History Action = "history"
	// UncertainRequests opens the read-only view of retained requests whose
	// outcome is not established.
	UncertainRequests Action = "uncertain_requests"
	// Edit edits the selected issue's body in the editor (suspending the
	// TUI); EditDetached shows the draft's path for any editor and watches it.
	Edit         Action = "edit"
	EditDetached Action = "edit_detached"
)

// Required actions must keep at least one key, so the way out, help and
// dismissing a prompt are always reachable.
var required = []struct {
	Context Context
	Action  Action
}{{Global, Quit}, {Global, Help}, {Menu, Cancel}}

// Shutdown is the reserved key that starts the orderly shutdown.
const Shutdown = "ctrl+c"

type entry struct {
	action      Action
	description string
	keys        []string
}

// vocabulary is the default keymap, in display order per context. Arrows,
// Enter and Escape come first, with vim-style alternates.
var vocabulary = map[Context][]entry{
	Global: {
		{Quit, "quit", []string{"q"}},
		{Help, "show or hide help", []string{"?"}},
		{Refresh, "refresh", []string{"r"}},
		{SwitchProject, "switch project", []string{"p"}},
		{ActionMenu, "open the action menu", []string{"m"}},
		{UncertainRequests, "show uncertain requests", []string{"U"}},
	},
	Tree: {
		{Up, "select the previous issue", []string{"up", "k"}},
		{Down, "select the next issue", []string{"down", "j"}},
		{Collapse, "collapse", []string{"left", "h"}},
		{Expand, "expand", []string{"right", "l"}},
		{Open, "open the detail", []string{"enter"}},
		{PageUp, "page up", []string{"pgup", "ctrl+u"}},
		{PageDown, "page down", []string{"pgdown", "ctrl+d"}},
		{Top, "select the first issue", []string{"home", "g"}},
		{Bottom, "select the last issue", []string{"end", "G"}},
		{Search, "search issue content", []string{"/"}},
		{Filter, "filter the tree", []string{"f"}},
		{ClearFilters, "clear the filters and the search", []string{"F"}},
		{Claim, "claim the issue, or reacquire a lost claim", []string{"c"}},
		{Release, "release the issue's claim", []string{"u"}},
		{History, "show the issue's history", []string{"H"}},
		{Edit, "edit the body, or resume, apply or discard its draft", []string{"e"}},
		{EditDetached, "edit the body in any editor (shows the draft path)", []string{"E"}},
	},
	Detail: {
		{Up, "scroll up", []string{"up", "k"}},
		{Down, "scroll down", []string{"down", "j"}},
		{Left, "scroll a wide block left", []string{"left", "h"}},
		{Right, "scroll a wide block right", []string{"right", "l"}},
		{NextBlock, "focus the next wide block", []string{"tab", "]"}},
		{PrevBlock, "focus the previous wide block", []string{"shift+tab", "["}},
		{Back, "back to the tree", []string{"esc"}},
		{PageUp, "page up", []string{"pgup", "ctrl+u"}},
		{PageDown, "page down", []string{"pgdown", "ctrl+d"}},
		{Top, "go to the top", []string{"home", "g"}},
		{Bottom, "go to the bottom", []string{"end", "G"}},
		{Claim, "claim the issue, or reacquire a lost claim", []string{"c"}},
		{Release, "release the issue's claim", []string{"u"}},
		{History, "show the history of the issue or a comment", []string{"H"}},
		{Edit, "edit the body, or resume, apply or discard its draft", []string{"e"}},
		{EditDetached, "edit the body in any editor (shows the draft path)", []string{"E"}},
	},
	Menu: {
		{Up, "previous item", []string{"up", "k"}},
		{Down, "next item", []string{"down", "j"}},
		{Open, "choose the item", []string{"enter"}},
		{Cancel, "close", []string{"esc"}},
		{PageUp, "page up", []string{"pgup", "ctrl+u"}},
		{PageDown, "page down", []string{"pgdown", "ctrl+d"}},
		{Top, "first item", []string{"home", "g"}},
		{Bottom, "last item", []string{"end", "G"}},
	},
}
