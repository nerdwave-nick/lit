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
| Claim idle period | `--claim-idle DURATION` | `LIT_TUI_CLAIM_IDLE` | `claim_idle` | `30m` |

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
  },
  "claim_idle": "45m"
}
```

- **`theme`**: `auto` detects the terminal background. Use `light` or `dark` if
  the terminal does not answer the background query, or answers it wrongly. No
  other styles exist.
- **`mouse`**: off by default, so the terminal's own text selection keeps working.
  `true` turns on mouse reporting for click-to-select and wheel scrolling. While
  it is on, many terminals select text only with Shift held down.
- **`keymap`**: rebinds actions per context. See below.
- **`claim_idle`**: how long claims are renewed without input or a draft change
  before renewal pauses and `lit-tui` asks whether to keep holding them (see
  [claims](#claims)). A Go duration such as `30m`, `1h` or `90s`, at least `1s`.

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

## Browsing projects

`lit-tui` starts with the project switcher, a list of every project read 50 at
a time (more load as the selection reaches the end). Enter chooses a project;
if there is only one, it opens at once. `switch_project` (`p`) opens the
switcher again, with the current project marked; `r` reloads the list. Each
project keeps its own selection and expanded branches for the session, so
switching back returns to where you were.

The tree shows the project's issues by hierarchy, at any depth. Roots are shown
first, each collapsed; `right` expands an issue and then moves to its first
child, and `left` collapses it and then moves to its parent. Siblings are in
creation order (issues created together by title). Very deep rows stop
indenting at half the pane and show their depth instead (`12›`). Titles are
cut to fit, with `…`; the state of a closed issue and the owner of a claim
(`⚑ Claude`) stay visible. The line under the header is the breadcrumb: the
project, then the selected issue's ancestors and the issue itself. When it is
too long, its start is cut, so the nearest ancestors stay visible. The
selection follows the issue, not the row: after a refresh it stays on the same
issue, even if that issue moved. If the issue is gone, the selection moves to
the row in its place and a note says so.

At 100 columns and more, the detail is shown beside the tree. Enter moves the
focus to the detail, so the detail keys scroll it, and Escape (`back`) returns
to the tree. On narrower terminals the tree and the detail are separate views:
Enter opens the detail full screen, and Escape returns to the tree with the
same selection. The detail shows the issue's title, state, labels, assignee,
claim (owner name and kind, and lease end), parent, children, links and last
update, then its rendered body and its comments, oldest first, each headed by
its author and time (and `edited` with the time of the last edit). Comments
are read-only.

The body and comments load when the detail is shown: as soon as an issue is
selected on a wide terminal, and when the detail opens on a narrow one, so
moving through the tree on a narrow terminal reads nothing. Until they arrive,
the detail says `Loading the body and comments…`. Choosing another issue
cancels a load that is still running, and a late answer for an issue that is
no longer selected is ignored. Every refresh also reloads the body and
comments of the shown issue, keeping the scroll position. Comments come in
one bounded read when they fit, with their count (`Comments (3)`); otherwise
they are read page by page, the count is left out, and a note says they may
have changed between pages. If loading fails, the detail names the error and
keeps whatever was read; `r` tries again.

Bodies and comments are sanitized before rendering, like every title: escape
sequences, other control characters and bidi overrides show as visible markers
(`␛[2J`, `␇`, `⟨U+202E⟩`) and never reach the terminal.

`action_menu` (`m`) lists the commands of the current view with their keys
(open or back, refresh, switch project, uncertain requests, search, filter,
clear filters, claim or release, help, quit and history); Enter runs one, Escape
closes the menu. With the `mouse` preference on, a click selects a row, a click
on the selected row expands or collapses it, a click on the detail pane focuses
it, and the wheel moves the selection or scrolls the detail.

### Search and filters

`search` (`/`) searches the project's current content with the semantics of
`lit grep`: a literal substring, ignoring case, matched by lit-server in issue
titles and bodies and in comments. A comment match shows its issue. The search
input owns every typed key, including `q` and `/`: Enter (`menu.open`)
searches, Escape (`menu.cancel`) cancels, Backspace deletes and Ctrl-U clears
the text. Searching for an empty text removes the search. While the search
runs, the footer says `searching "…"…`. If it fails, the footer names the
error and the previous search stays in effect. Each load of the project (`r`
or the background refresh) runs the search again, so its matches follow the
current content. One search reads up to 10,000 matching records, in pages of
up to 1,000; beyond that the footer says the search stopped.

`filter` (`f`) opens the filter panel: one row per filter, changed with Enter
and applied at once; Escape closes it. The filters are:

- **State:** any, open or closed (Enter cycles);
- **Claimed:** any, claimed or unclaimed;
- **Label:** any of the checked labels (every label of the loaded issues is
  offered);
- **Assignee:** any of the checked assignees, `unassigned` included;
- **Search:** Enter edits the search text;
- **Clear all filters.**

Different kinds combine: an issue must match each kind that is set. Within one
kind, any checked value matches.

The tree then shows every matching issue and, as context, the ancestors of
each match, expanded so that every match is visible. Matches are marked with
`•` (`*` without colours); context ancestors are dimmed. Other issues are
hidden, including non-matching children of a match. The footer line names the
filters and how many loaded issues match (`2 of 120 issues match`; after a
partial load it gives no total). When nothing matches, the tree says
`No issues match the filters.` `clear_filters` (`F`), or the action menu's
entry, removes every filter and the search at once.

The selection follows the issue: it stays while the issue is shown. When a
filter hides it, the first match is selected. When a refresh hides it because
it no longer matches, the row in its place is selected and a note says so.
Clearing the filters keeps the selection and shows it in the whole tree; the
branches you had expanded before filtering are restored. Filters belong to the
project: switching projects clears them.

### Loading and refresh

A project loads without issue bodies, through one bounded read of all its
issue summaries. The header then shows the number of issues and the time of
the load (`1000 issues · as of 10:31:02`). When the project is too large for
one read (the service answers `limit_exceeded`), `lit-tui` reads it page by
page instead. Those pages are not one consistent snapshot, so the header says
`partial: may have changed between pages` and shows no count. If a page
fails, the issues read so far stay shown and the header names the error.

`refresh` (`r`) reloads the current project. In the background, the project is
reloaded 30 s after the previous load ended, or after ten times as long as
that load took, whichever is longer, so a busy service sees fewer reads.
Refreshes never overlap: pressing `r` while a load runs asks for one more load
after it. While `lit-tui` is applying a change to the tracker, refresh waits
(the header says `refresh waits for a running change`) and runs once the change
is finished. Only the current project is ever reloaded.

The first release targets projects of up to about 1,000 issues, with bodies of
up to 64 KB. Larger projects still load, page by page, but every request
currently makes lit-server read its whole store, so they are slow.

### History

`history` (`H`, also in the action menu) shows the immutable mutation history
of the selected issue, read-only. In the tree it opens the issue's history at
once. In the detail it first offers the issue and each of its loaded comments
(author, time and the start of the comment); Enter shows the chosen record's
history. Escape steps back to that choice and then to the same selection and
scroll position; `H` closes the view at once.

Entries come oldest first, as `lit issues history` lists them. Each says:

- its number, the revisions (`rev 1 → 2`, or `created` for the first), the
  time in UTC, who made it (actor name and kind, for example
  `Claude (agent)`) and the client;
- the operations of its transaction (for example `issue update`, or
  `comment create 28dfb5ca` on the issue a comment was added to), the request
  hash, and the other records the transaction changed;
- every changed field with its JSON value before and after
  (`title: "Old" → "New"`; at creation only the values that were set). The
  revision and timestamps are left out, since the heading gives them;
- the body as a line diff: `-` before, `+` after, two unchanged lines of
  context around each change, and `…` for longer unchanged runs. Removed and
  added lines are coloured unless styling is off.

Every title, name, value and body line is sanitized like the detail's, so
control characters show as visible markers. Long lines wrap.

The history is read in pages of 10 entries (fewer if the service's maximum page
is smaller), following the service's cursors. A page is read only when the
view's end comes within one screen of the last entry read, so scrolling down
(`end` included) reads more; the header says `N entries so far · more below`
until the last page, and `N entries` once it is complete. If a page fails, the
entries read so far stay, the header names the error, and `refresh` (`r`)
reads the history again from the start. The view never writes: each page is
one read through the same gate as every other request.

## Claims

`claim` (`c`) claims the selected issue for this session, in the tree or in the
detail, and `release` (`u`) releases it; the action menu offers whichever
applies. Before claiming, `lit-tui` rereads the issue and its claim. If another
client holds the claim, nothing is written and the note names the owner, for
example `Cannot claim “Fix the parser”: claimed by Claude (agent) until …`.
`lit-tui` never forces a claim. After a claim or release the tree reloads, so the
row shows `⚑` and your name.

While `lit-tui` holds a claim, it renews it in the background. Each claim is
taken for 15 minutes and renewed after 10 (two thirds of the lease), one request
at a time. Renewal does not depend on the screen: it goes on while the screen is
busy or suspended for an editor. The footer counts the claims, for example
`Claims: 2 held · 1 lost`.

**Idle.** After the claim idle period (`claim_idle`, 30 minutes by default) with
no key press, no mouse input and no draft change, renewal stops and `lit-tui`
asks:

```text
Claim renewal paused: no input or draft change for 30m0s.
Keep holding these claims?

  “Fix the parser”: lapses about 14:05:12

Without an answer they lapse when their leases end and are shown as lost.

enter keep holding · esc let them lapse
```

Enter renews every listed claim at once and resumes renewal. The note says
`Holding N claim(s) again.` only for claims that were really renewed. Escape, or
no answer, lets them lapse when their leases end; until then the tree marks
them `paused`.

**Lost claims.** A claim is **lost** when a renewal fails (for example with
`claim_conflict`, because another client took it over) or when its lease passes.
The tree marks it `lost`, and the footer names it with the reason, for example
`Claim lost: “Fix the parser”: renewal failed: claim_conflict: …`. `lit-tui` never
retries a lost claim and never reacquires it on its own. Keeping a claim whose
lease has already passed reports `Could not keep holding …`, never success. To
take it back, use `claim` again; the action menu calls it reacquire. `lit-tui`
rereads the issue first, refuses if another client holds it, and otherwise
claims it at the issue's current revision, noting when the issue changed since
the claim was taken. `release` forgets a lost claim.

Quitting releases every claim this session holds (see [leaving](#leaving)).
After a crash, a claim lasts until its lease ends: at most 15 minutes for a claim
`lit-tui` took.

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
| global | `uncertain_requests` | `U` | show uncertain requests |
| tree | `up` | `up`, `k` | select the previous issue |
| tree | `down` | `down`, `j` | select the next issue |
| tree | `collapse` | `left`, `h` | collapse |
| tree | `expand` | `right`, `l` | expand |
| tree | `open` | `enter` | open the detail |
| tree | `page_up` | `pgup`, `ctrl+u` | page up |
| tree | `page_down` | `pgdown`, `ctrl+d` | page down |
| tree | `top` | `home`, `g` | select the first issue |
| tree | `bottom` | `end`, `G` | select the last issue |
| tree | `search` | `/` | search issue content |
| tree | `filter` | `f` | filter the tree |
| tree | `clear_filters` | `F` | clear the filters and the search |
| tree | `claim` | `c` | claim the issue, or reacquire a lost claim |
| tree | `release` | `u` | release the issue's claim |
| tree | `history` | `H` | show the issue's history |
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
| detail | `claim` | `c` | claim the issue, or reacquire a lost claim |
| detail | `release` | `u` | release the issue's claim |
| detail | `history` | `H` | show the history of the issue or a comment |
| menu | `up` | `up`, `k` | previous item |
| menu | `down` | `down`, `j` | next item |
| menu | `open` | `enter` | choose the item |
| menu | `cancel` | `esc` | close |
| menu | `page_up` | `pgup`, `ctrl+u` | page up |
| menu | `page_down` | `pgdown`, `ctrl+d` | page down |
| menu | `top` | `home`, `g` | first item |
| menu | `bottom` | `end`, `G` | last item |
| (all) | | `ctrl+c` | orderly shutdown and quit (reserved) |

## Sessions, exit and recovery

Every launch of `lit-tui` registers a fresh client, with actor kind `human`
and runtime vendor `lit-tui`, for a new logical session of its own. It never
reuses or adopts your named CLI sessions. Each running instance records an
entry in a registry inside the client state directory (`LIT_STATE_DIR`, or
the platform default), separate from the CLI's `sessions/`:

```text
<state>/tui/instances/<instance-id>.json           the session mapping (client, service, endpoint)
<state>/tui/instances/<instance-id>.json.lock      the liveness lock, held while the instance runs
<state>/tui/instances/<instance-id>.instance.json  pid, host, start time and endpoint
```

Pending request evidence stays in `<state>/pending/`, where
`lit transactions status` sees it. `lit-tui` never removes, moves or replays
it (see [recovery](recovery.md)).

`lit-tui` connects before it takes over the terminal. If the service cannot be
reached, it prints the error, removes its entry and exits with status 1. If the
registration's answer is lost, its outcome is uncertain: the request ID and
evidence path are printed, the entry is kept and the exit status is 4.

### Leaving

`q` (or your `quit` key), Ctrl-C, SIGINT, SIGTERM and SIGHUP (for example when
the terminal closes) all start the same orderly shutdown. The status line shows
`Disconnecting…`, or `Releasing N claim(s), disconnecting…` when claims are held.
Shutdown then:

1. refuses new requests;
2. waits for a request that is already running, up to its timeout (10 s),
   without cancelling or replaying it;
3. stops the claim renewer;
4. checks this session's retained requests read-only (5 s), and, when none is
   uncertain, disconnects once (5 s). The disconnect releases every claim the
   client holds;
5. removes this instance's own registry entry and lock, and nothing else, then
   restores the terminal.

If the program ends first, for example because the terminal went away, `lit-tui`
still finishes shutdown before it exits, waiting at most about 30 s.

When something is uncertain (a retained request, or a disconnect whose answer
never arrived) or cannot be verified, nothing more is disconnected: doing so could
make an uncertain outcome permanently unprovable. The screen lists the request
IDs and evidence paths, the registry entry is kept for the next launch, and
leaving takes a second keypress (after SIGHUP no terminal is left, so it exits
without one). The same lines are printed to stderr after the terminal is
restored, and `lit-tui` exits with status 4. Claims that were not
released stay until their leases expire (at most one hour).

A second Ctrl-C or signal during shutdown exits at once, with status 128 plus the
signal number (130 for Ctrl-C). It prints the retained request IDs and the claims
left to lease expiry, and keeps the registry entry.

### The next launch

At startup `lit-tui` scans the whole registry. An entry whose lock is held
belongs to a running instance and is never touched. A one-line note says how many
others are running. For an orphan, left behind by a crash or `kill -9`, it checks
the orphan's retained requests first:

- **Nothing uncertain:** it disconnects the orphan's client, which releases its
  leftover claims, removes the orphan's entry, and shows a one-line note.
- **Something uncertain:** it never disconnects. A panel shows the orphan, its
  request IDs and evidence paths. Inspect them with `lit transactions status`
  or the [uncertain-requests view](#uncertain-requests) (`U`) and decide;
  Escape closes the panel. The entry stays, so later launches
  report it again until the outcome is resolved.
- **Cannot verify** (the service is unreachable, or a disconnect is not
  confirmed): it reports the orphan as unknown, never as clean, and keeps the
  entry.

Advisory locks cannot be trusted on network and user-space file systems (NFS,
SMB/CIFS, AFS, 9P, Ceph, GFS2, OCFS2 and FUSE file systems such as sshfs; on
macOS also smbfs, afpfs, webdav and macFUSE; on Windows UNC shares). The same is
true for an entry recorded on another host. In those cases `lit-tui` cleans up
nothing on its own. It asks about each orphan instead: Enter disconnects it,
Escape keeps it. Confirm only if that instance is really gone.

The registry is local to the state directory. An instance that crashed on
another machine with a different state directory is not seen; its claims
lapse at lease expiry.

### Uncertain requests

`uncertain_requests` (`U`, also in the action menu) opens a read-only view of
retained requests whose outcome may not be established: this session's own
(checked again now with a read-only transaction status), and those of the
orphans found at launch (as recovery checked them). The panel about an orphan
with uncertain requests and the exit report that lists uncertain requests
name the key; in the exit report it opens the view instead of leaving, and
Escape returns to the report. After shutdown the view shows this session's
requests as shutdown checked them, because no more requests are sent.

For each request it shows the request ID, its outcome (status, outcome and
proof, for example `uncertain (acquisition_token_not_received)`), the
operation (method and path, and the intent or claim operation such as
`issue.update` or `claims.acquire`), the service and client IDs, the endpoint,
when it was recorded and the evidence path. Uncertain requests come first.
`refresh` (`r`) checks again; Escape or `U` closes the view.

The view only reads. It never replays, retires, moves or deletes evidence,
and repeats the guidance of [recovery](recovery.md): checking again cannot
always settle an outcome, the evidence must not be deleted, moved or worked
around with a new client or a replay, and a persistent uncertain outcome goes
to the human owner with the evidence and the request, service and client IDs.

### After `kill -9`, a crash or a lost connection

Nothing can run after SIGKILL. The client stays connected until the next launch
cleans it up, and its claims last until their leases expire. The terminal may be
left in the alternate screen with input settings changed, and an editor that
`lit-tui` started may keep running. Run `reset` (or close the terminal) to
restore the terminal, and end an orphaned editor yourself, after saving what you
need.
