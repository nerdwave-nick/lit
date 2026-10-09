# Terminal UI preferences

`lit-tui` reads its preferences once, at startup. Changes apply on the next
launch. The `lit` CLI keeps its own session-based preferences; this file is for
the terminal UI only. See [installation](installation.md#terminal-ui) for how
to install and start `lit-tui`.

## Precedence and sources

Precedence is flags, then environment, then the JSON config file, then
defaults, the same order `lit-server` uses.

| Preference | Flag | Environment | Config field | Default |
| --- | --- | --- | --- | --- |
| Config file | `--config FILE` | `LIT_TUI_CONFIG` | | `${XDG_CONFIG_HOME:-$HOME/.config}/lit/tui.json` |
| Theme | `--theme auto\|light\|dark` | `LIT_TUI_THEME` | `theme` | `auto` |
| Mouse | `--mouse true\|false` | `LIT_TUI_MOUSE` | `mouse` | `false` |
| Keymap | | | `keymap` | the default keymap below |

An empty environment variable counts as unset. A relative `XDG_CONFIG_HOME` is
ignored. The default config file may be missing. A file selected explicitly with
`--config` or `LIT_TUI_CONFIG` must exist.

`lit tui` and bare `lit` pass only `--endpoint` through to `lit-tui`. To set
preferences when you start it that way, use the environment variables or the
config file, or run `lit-tui` directly with its flags.

## Config file

The file is one JSON object with `schema_version: 1`. Unknown fields, duplicate
keys and trailing data are rejected:

```json
{
  "schema_version": 1,
  "theme": "dark",
  "mouse": true,
  "keymap": {
    "global": {"quit": ["q", "Q"]},
    "tree": {"expand": ["right", "l", "space"], "collapse": ["left", "h"]}
  }
}
```

- **`theme`**: `auto` detects the terminal background. Use `light` or `dark` if
  the terminal does not answer the background query, or answers it wrongly. No
  other styles exist.
- **`mouse`**: off by default, so the terminal's own text selection keeps working.
  `true` turns on mouse reporting for click-to-select and wheel scrolling. While
  it is on, many terminals select text only with Shift held down.
- **`keymap`**: rebinds actions per context. See below.

Invalid preferences stop `lit-tui` before it takes over the terminal. It prints
one `lit-tui:` line per problem, naming the file or variable, and exits with
status 2.

## Theme and the detail view

With `theme` set to `auto`, `lit-tui` asks the terminal for its background
colour and waits up to 300 ms for the answer before it falls back to dark. A
late answer still switches the style. A forced `light` or `dark` theme skips
the question. With `NO_COLOR` set (to anything but the empty string), or on a
terminal without colours such as `TERM=dumb`, bodies use a plain ASCII style.

In the detail view, prose wraps to the pane and never scrolls sideways.
Top-level code blocks and tables keep their lines unwrapped. The focused wide
block is marked in the left margin (`▌`, or `>` without colours), and
`left`/`right` scroll it on its own; every block keeps its own offset. A
clipped edge shows `‹` or `›` (`$` without colours). The focus follows the
first wide block on screen, and `next_block`/`prev_block` move it. The scroll
position follows the text across resizes and re-renders. A body that takes
longer than about 100 ms to render shows `rendering…` meanwhile.

## Keymap

Every binding belongs to a **context**:

- `global` bindings work everywhere.
- `tree` is the issue hierarchy.
- `detail` is the issue detail.
- `menu` covers menus, pickers, prompts and the help overlay.

`tree`, `detail` and `menu` inherit the `global` bindings.

Under `keymap`, each context maps action names to lists of keys. A listed
action's keys **replace** its default keys, and an empty list unbinds it.
Actions you do not list keep their defaults. Help (`?`) and the action menu are
drawn from the effective bindings, so they always show your keys.

Keys are written as in Bubble Tea: modifiers (`ctrl`, `alt`, `shift`, `meta`,
`hyper`, `super`) joined by `+`, then one character or a key name:

- key names: `enter`, `esc`, `tab`, `backspace`, `space`, `up`, `down`, `left`,
  `right`, `home`, `end`, `pgup`, `pgdown`, `insert`, `delete`, `f1` to `f20`;
- examples: `ctrl+k`, `alt+enter`, `shift+tab`, `+`.

Modifier and key names ignore case. An upper-case letter means Shift: `G` and
`shift+g` are the same key.

At startup the keymap is rejected when it has any of these:

- an unknown context or action, or an action used in a context it does not
  belong to;
- a key that cannot be parsed, or a key listed twice for one action;
- a conflict: one key bound to two actions in one context. Inherited `global`
  bindings count, so a `tree` key cannot also be a `global` key;
- no key left for `global.quit`, `global.help` or `menu.cancel`;
- any binding of `ctrl+c`.

**Ctrl-C is reserved.** It always starts the orderly shutdown and quits, whatever
the keymap says, and it cannot be bound to an action.

### Default keymap

Arrows, Enter and Escape come first, with vim-style alternates.

| Context | Action | Default keys | Description |
| --- | --- | --- | --- |
| global | `quit` | `q` | quit |
| global | `help` | `?` | show or hide help |
| global | `refresh` | `r` | refresh |
| global | `switch_project` | `p` | switch project |
| global | `action_menu` | `m` | open the action menu |
| tree | `up` | `up`, `k` | select the previous issue |
| tree | `down` | `down`, `j` | select the next issue |
| tree | `collapse` | `left`, `h` | collapse |
| tree | `expand` | `right`, `l` | expand |
| tree | `open` | `enter` | open the detail |
| tree | `page_up` | `pgup`, `ctrl+u` | page up |
| tree | `page_down` | `pgdown`, `ctrl+d` | page down |
| tree | `top` | `home`, `g` | select the first issue |
| tree | `bottom` | `end`, `G` | select the last issue |
| detail | `up` | `up`, `k` | scroll up |
| detail | `down` | `down`, `j` | scroll down |
| detail | `left` | `left`, `h` | scroll a wide block left |
| detail | `right` | `right`, `l` | scroll a wide block right |
| detail | `next_block` | `tab`, `]` | focus the next wide block |
| detail | `prev_block` | `shift+tab`, `[` | focus the previous wide block |
| detail | `back` | `esc` | back to the tree |
| detail | `page_up` | `pgup`, `ctrl+u` | page up |
| detail | `page_down` | `pgdown`, `ctrl+d` | page down |
| detail | `top` | `home`, `g` | go to the top |
| detail | `bottom` | `end`, `G` | go to the bottom |
| menu | `up` | `up`, `k` | previous item |
| menu | `down` | `down`, `j` | next item |
| menu | `open` | `enter` | choose the item |
| menu | `cancel` | `esc` | close |
| menu | `page_up` | `pgup`, `ctrl+u` | page up |
| menu | `page_down` | `pgdown`, `ctrl+d` | page down |
| menu | `top` | `home`, `g` | first item |
| menu | `bottom` | `end`, `G` | last item |
| (all) | | `ctrl+c` | orderly shutdown and quit (reserved) |
