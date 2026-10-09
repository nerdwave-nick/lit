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
| Claim lease | `--claim-lease DURATION` | `LIT_TUI_CLAIM_LEASE` | `claim_lease` | `15m` |

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
  "claim_idle": "45m",
  "claim_lease": "15m"
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
- **`claim_lease`**: how long each claim `lit-tui` takes or renews lasts; it is
  renewed after two thirds of it. A Go duration from `1s` to `1h` (the service's
  maximum). A shorter lease ends a crashed instance's claims sooner.

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
switching back returns to where you were. `new_project` (`n`) in the switcher
creates a project (see [Projects](#projects)).

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
are added and edited through drafts (see [Comments](#comments)).

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
clear filters, claim or release, the [quick actions](#quick-actions), help,
quit, history, edit and edit detached, new comment and edit comment, edit
the specification and the repositories, new issue and new child); Enter runs
one, Escape
closes the menu; a menu taller than the screen scrolls with its selection. With the `mouse` preference on, a click selects a row, a click
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
taken for 15 minutes (`claim_lease`) and renewed after 10 (two thirds of the
lease), one request
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

## Quick actions

Quick actions change one thing about the selected issue, in the tree or in the
detail, without a draft. Each is one transaction:

| Action | Key | Change |
| --- | --- | --- |
| `edit_title` | `t` | an inline title field, filled with the current title |
| `set_state` | `s` | open or closed (closing ends the issue's claim) |
| `set_labels` | `#` | toggle labels; typing a new one offers to add it |
| `set_assignee` | `A` | an assignee, unassigned, or a typed new name |
| `set_parent` | `P` | another issue of the project, or no parent |
| `link` | `b` | is blocked by, blocks, or is related to another issue |
| `unlink` | `B` | remove one of the issue's links |

The pickers offer the labels and assignees the loaded project already uses, and
its issues (an issue's own descendants cannot become its parent). Typing filters
them, so letters such as `j`, `k` and `q` are text there; the arrows, page keys
and Home/End move, Enter chooses (for labels it toggles, and **Preview the
change** at the top previews), and Escape cancels. In the title field Backspace
deletes and Ctrl-U clears.

Choosing previews the change first, through lit-server, and nothing is written
yet. The preview lists every record the transaction would write, at the
revision it is fenced at, and those it only reads: for a parent or a link these
include the other issues whose relationship fields change. Each issue's claim
is shown. A claim this session holds is passed on with the change
(`claimed by you: your claim is passed on`). Enter applies exactly that
preview, once; Escape cancels it. After an apply the footer says
`Applied: …` and the tree reloads.

A refusal is shown with its error code, and nothing is written:

- **`claim_conflict`:** another client holds a claim on an affected issue. The
  preview names it and its owner, for example
  `Refused (claim_conflict): “Fix the parser” is claimed by Claude (agent) until 14:05:12. Nothing was written.`
  The same is shown when the claim was taken after the preview. `lit-tui` never
  forces.
- **`revision_conflict`:** an affected record changed since it was shown (in
  the tree, or after the preview). The tree reloads; try again.
- Any other refusal, such as `validation_failed` for an invalid title, names
  its code and message. An uncertain outcome (`outcome_uncertain`) is kept for
  recovery and never replayed; the [uncertain-requests view](#uncertain-requests)
  lists it.

## Editing issue bodies

`edit` (`e`) edits the selected issue's body in your editor, in the tree or the
detail. `lit-tui` claims the issue first (claim-on-edit). If another client holds
the claim, the edit does not start and the note names the owner, for example
`Cannot edit “Fix the parser”: claimed by Claude (agent) until …`. Otherwise it
writes a **draft**: a file holding exactly the body, with nothing added, and
suspends into the editor. The editor is `$VISUAL`, then `$EDITOR`, then `vi`
(`notepad` on Windows). Its value is split into arguments like a shell would
split words, with `'…'` and `"…"` quoting and `\` escapes, but no shell runs
it: variables, pipes and `;` are not interpreted. Quote an editor path that
contains spaces, for example `EDITOR='"/opt/My Editor/bin/nvim" --clean'`; a
value that names an existing file as a whole also works unquoted. The draft's
path is passed as one more argument.

`edit_detached` (`E`) starts the same edit without suspending: the footer shows
the draft's path (`Draft open: “Fix the parser” · e applies it`) and `lit-tui`
keeps running. Edit the file in any editor. `lit-tui` checks it twice a second
and shows when it last changed. A change counts as activity for the claim's
idle period, in both kinds of edit.

**Applying.** When the editor exits, or when you choose to apply a detached
draft, `lit-tui` reads the draft and previews the change. The panel shows the
claim (held; or, when it was lost or released, a warning that the apply is
optimistic), the affected records with the revisions they are fenced at (the
issue, changed, and its project, read), and the diff against the revision the
draft was started from. `Apply this change` commits exactly that preview, at
that revision. On success the draft is removed and the edit claim is released.
A claim you had taken yourself before the edit (with `claim`) is kept. A
preview taken while the claim was held is shown again, with the warning, if the
claim was lost since.

Nothing is ever overwritten silently. The draft is kept, and the panel says why,
when:

- the issue changed since the draft was started (`changed since the draft was
  started: revision 3 → 4`): `Diff against the current body` shows what applying
  would replace, and `Reload` records the current revision while keeping your
  text, so the next preview applies it over the new revision;
- another client holds a claim on the issue: nothing is written and the owner
  is named;
- the outcome is uncertain: the request ID and evidence are shown, nothing is
  replayed, and the claim stays (see [uncertain requests](#uncertain-requests)).

After a claim is lost, for example when renewal paused for idleness and the
lease ended, the draft stays and applying is optimistic: fenced by the draft's
revision only, and refused by a revision change or another client's claim.

**Existing drafts.** `e` on an issue that already has a draft never starts a new
one. When the issue's revision changed since the draft was started, the draft
opens as a conflict (diff, reload, resume, keep or discard). Otherwise it
offers the draft: resume it in the editor or detached (claiming
the issue again if this session does not hold the claim), preview and apply it,
diff it against the current body, or discard it. `Keep the draft for later`
(or Escape) closes the panel; an edit claim stays held while its draft is open.
Discarding asks first, deletes the draft and releases the edit claim. Drafts
are removed only by a successful apply or a discard.

Drafts live in the client state directory, one directory per project:

```text
<state>/tui/drafts/<project-id>/<title-slug>--<id8>.md         exactly the body
<state>/tui/drafts/<project-id>/.lit/issue_body-<issue-id>.json  the sidecar
```

The sidecar records the issue, its title, the revision the draft applies at and
the body at that revision, and whether the claim belongs to the edit. Only
sidecars identify drafts, so editor swap and backup files in the directory
(`.name.md.swp`, `name.md~`, …) are ignored; discarding or applying a draft
removes its own. The title part of the name is cosmetic: a renamed issue keeps
its draft. Drafts are TUI drafts only: lit-server knows nothing about them.

### Drafts across exits

Drafts outlive the session. Quitting, a signal, a crash or the next launch's
recovery never removes one; only a successful apply or a discard does.

**At launch** a footer line counts this service's unapplied drafts in every
project, for example `1 unapplied draft · D opens the Drafts view`. `drafts`
(`D`, also in the action menu) opens the **Drafts view** at any time; opening
it ends the banner. It lists every draft, the current project's first, with
its kind (`issue body`, `new comment`, `comment`, `project spec` or `new
issue`; later kinds are listed the same way),
title, project, recorded revision and start time; the selected draft's file
is shown below. Enter opens the selected draft: it is compared with its own
record first (an issue body with its issue, a comment draft with the comment),
and opens as a conflict when that record's revision changed
(`changed: revision 3 → 4, opens as a conflict` in the list); otherwise it is
offered as with `e`: resume it in the editor or detached, preview and apply,
diff against the current body, or discard. A kind this version cannot resume
can still be kept or discarded. Escape or `D` closes the view. Drafts of
another lit service sharing the state directory are not listed.

In the tree, an issue with a draft is marked `draft`, or `draft: changed` when
the issue changed since; other kinds are marked by kind on their issue, for
example `comment draft` or `new comment draft`. Drafts without an owning issue
are not marked in the tree.

**Quitting with open drafts.** Drafts started or resumed in this session that
are still unapplied make `quit` ask once:

```text
1 open draft (“Fix the parser”). Drafts are kept; claims will be released.

[Apply now] [Quit] [Cancel]
```

`Apply now` cancels quitting and previews applying the first open draft.
`Quit` (or `q` again) quits: the drafts stay, and the disconnect releases the
claims, edit claims included. `Cancel` (Escape) keeps running. Ctrl-C and
signals never ask.

## Comments

`new_comment` (`a`) writes a new comment on the selected issue, in the tree or
the detail. It suspends into the editor on an empty draft
(`comment-<issue-slug>--<id8>.md`); when the editor exits, the panel previews
the comment (`Apply the new comment on “Fix the parser”?`) with its text and
the affected records, and `Apply this change` posts exactly that preview. A
new comment takes **no claim**: lit-server lets anyone append a comment, so
another client's claim on the issue never blocks it (the panel says `New
comments take no claim`). An empty draft has nothing to post. `a` on an issue
that already has an unposted new comment offers that draft (resume, preview
and apply, discard) instead of starting another.

The draft's ID becomes the comment's ID, so a draft is posted at most once.
If an apply's outcome is uncertain, the draft is kept; a later preview finds
the comment if the apply went through (`was posted already`) and only offers
to discard the draft.

`edit_comment` (`o`) loads the selected issue's comments and lists them
(`comment by Claude · <time> · <first line>`); Enter edits the chosen one. Like
a body edit, this claims the issue first (claim-on-edit): another client's
claim refuses it and is named (`Cannot edit the comment on “Fix the parser”:
claimed by Claude (agent) until …`). The draft holds exactly the comment's
text, and applying commits the previewed update fenced at the comment's
revision, then releases the edit claim; a claim you held explicitly is kept.
A body draft and comment drafts of one issue share its edit claim, which is
released when the last of them is applied or discarded. A revision change of
the comment or another client's claim keeps the draft, with `Diff against the
current comment` and `Reload`, exactly as for bodies.

Comment drafts sit beside body drafts, with sidecars
`.lit/comment_new-<draft-id>.json` (owned by the issue; the draft ID is the
future comment's) and `.lit/comment_body-<comment-id>.json`. They are listed
in the Drafts view (`new comment`, `comment`) and open there as from `a` and
`o`: a new comment is offered, or shown as posted already; a comment draft
whose comment changed opens as a conflict. A comment draft started or
resumed in this session makes quit ask, and `Apply now` previews it.

## Projects

Projects have no claims, so every project change is optimistic and
revision-fenced: it applies exactly what was previewed, or is refused when
the project changed meanwhile.

`new_project` (`n`) in the project switcher asks for the new project's
title. The title is checked before anything is previewed, as lit-server
checks it: an allowed prefix from the service's `title_prefixes` (shown above
the field, from `/v1/meta`), then one or more parts separated by slashes, each
of ASCII letters, digits, `-` and `_` and starting with a letter or digit, at
most 128 characters. An invalid title is refused under the field (`“nope” is
not an allowed prefix; the service allows feat, fix, …`) and nothing is
sent. A valid title previews the creation (`Create the project “feat/x”`),
which still refuses a title that exists already; Enter creates the project,
with an empty specification and no repositories, and browses it. Escape
returns to the switcher.

`edit_spec` (`S`, in the tree or the detail) edits the current project's
shared specification, its description, in the editor. The draft
(`spec-<project-slug>--<id8>.md`, sidecar `.lit/project_spec-<project-id>.json`)
holds exactly the description at the project's current revision and takes no
claim. When the editor exits, the panel shows the diff against that revision
and the project as the affected record; `Apply this change` commits exactly
that preview at the recorded revision. If the project changed after the draft
was started (its description, title, repositories or issues), the apply is
refused and the draft is kept: `S` then opens it as a conflict (`The
specification of “feat/x” changed since the draft was started: revision 3 →
4`), with `Diff against the current specification` and `Reload` (record the
current revision, keep the draft's text, preview again). `S` with an existing
draft offers it (resume, diff, apply, discard) instead of starting another.
Specification drafts are listed in the Drafts view (`project spec`) and open
there as from `S`.

`edit_repositories` (`R`, in the tree or the detail) reads the current
project and lists its repositories, checked. Enter toggles one; typing filters,
or offers the typed text as a new repository (`add the repository “…”`).
`Preview the change` lists the additions and removals (`Change the
repositories of “feat/x”: +https://… −https://…`) and the project fenced at
the revision read when the list opened; Enter applies exactly that, and a
change of the project meanwhile refuses it with `revision_conflict`, writing
nothing.

## Creating issues

`new_issue` (`N`) creates a top-level issue in the current project, and
`new_child` (`C`) a child of the selected issue, in the tree or the detail.
Both first ask for the title on one line (`New issue in “test/one”`, or `New
child of “Fix the parser”`). The field owns typed keys, `q` and `j` included;
Backspace deletes, Ctrl-U clears, and a paste joins its lines. Enter refuses,
with a note, a title lit-server would refuse: an empty one, one longer than 128
characters, or one an issue the tree has loaded uses (titles are unique in a
project, ignoring case). Escape cancels; nothing is kept. When another client
claims the parent, the prompt says so: a new child changes its parent, so its
creation is refused while that claim lasts.

Enter then starts a **draft** of the new issue's body, empty
(`<title-slug>--<id8>.md`), and suspends into the editor. A new issue takes no
claim. When the editor exits, the panel previews the creation (`Create the new
issue “Plan”?`): the new issue, a child's parent (its children change; it is
fenced at its revision, and a claim this session holds on it is passed on) and
the project, which is only read; then the body. A blank body is allowed.
`Apply this change` commits exactly that preview, once: the title, the body
and the parent in one transaction. The draft is removed, the tree reloads
with the new issue selected (its parent expanded), and the footer says
`Created “Plan” under “Fix the parser”.` Set the labels, the assignee, the
state and the links afterwards with the [quick actions](#quick-actions), each
its own previewed transaction.

Nothing is written, and the draft is kept, when:

- another client claims the parent: `Cannot apply the new issue “Plan”` names
  the parent and its owner, for example `“Fix the parser” is claimed by Claude
  (agent) until …`. lit-server refuses the preview itself, so no change is sent;
- the title was taken meanwhile, or the parent is gone: the panel names the
  error. `Change the title` asks for another title (the draft keeps its body)
  and previews again;
- the outcome is uncertain: the request ID and evidence are shown and nothing
  is replayed. The draft's ID is the new issue's ID, so a draft creates at most
  one issue: a later preview finds the issue if the apply went through (`was
  created already`) and only offers to discard the draft.

A kept new issue has no issue to resume it from, so it is resumed from the
Drafts view (`D`), where it is listed as `new issue` and offers resume, preview
and create, change the title, and discard; `N` and `C` always start a new
draft. Creating an issue from the Drafts view closes the view, so the tree
shows the new issue. The sidecar is `.lit/issue_new-<draft-id>.json`; it
records the title and, for a child, the parent (`parent_id`). A new-issue draft
started or resumed in this session makes quit ask, like other drafts.

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
| global | `drafts` | `D` | show the unapplied drafts |
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
| tree | `edit_title` | `t` | edit the title |
| tree | `set_state` | `s` | close or reopen the issue |
| tree | `set_labels` | `#` | change the labels |
| tree | `set_assignee` | `A` | change the assignee |
| tree | `set_parent` | `P` | change the parent |
| tree | `link` | `b` | add a link: blocker, blocked or related issue |
| tree | `unlink` | `B` | remove a link |
| tree | `edit` | `e` | edit the body, or resume, apply or discard its draft |
| tree | `edit_detached` | `E` | edit the body in any editor (shows the draft path) |
| tree | `new_comment` | `a` | add a comment, or resume its draft |
| tree | `edit_comment` | `o` | edit one of the issue's comments |
| tree | `edit_spec` | `S` | edit the project's specification, or resume its draft |
| tree | `edit_repositories` | `R` | change the project's repositories |
| tree | `new_issue` | `N` | create a top-level issue |
| tree | `new_child` | `C` | create a child of the issue |
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
| detail | `edit_title` | `t` | edit the title |
| detail | `set_state` | `s` | close or reopen the issue |
| detail | `set_labels` | `#` | change the labels |
| detail | `set_assignee` | `A` | change the assignee |
| detail | `set_parent` | `P` | change the parent |
| detail | `link` | `b` | add a link: blocker, blocked or related issue |
| detail | `unlink` | `B` | remove a link |
| detail | `edit` | `e` | edit the body, or resume, apply or discard its draft |
| detail | `edit_detached` | `E` | edit the body in any editor (shows the draft path) |
| detail | `new_comment` | `a` | add a comment, or resume its draft |
| detail | `edit_comment` | `o` | edit one of the issue's comments |
| detail | `edit_spec` | `S` | edit the project's specification, or resume its draft |
| detail | `edit_repositories` | `R` | change the project's repositories |
| detail | `new_issue` | `N` | create a top-level issue |
| detail | `new_child` | `C` | create a child of the issue |
| menu | `up` | `up`, `k` | previous item |
| menu | `down` | `down`, `j` | next item |
| menu | `open` | `enter` | choose the item |
| menu | `cancel` | `esc` | close |
| menu | `page_up` | `pgup`, `ctrl+u` | page up |
| menu | `page_down` | `pgdown`, `ctrl+d` | page down |
| menu | `top` | `home`, `g` | first item |
| menu | `bottom` | `end`, `G` | last item |
| menu | `new_project` | `n` | create a project (in the project switcher) |
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

**An open editor is never killed.** When shutdown starts while `lit-tui` is
suspended in the editor (SIGTERM, or SIGHUP when the terminal closes), steps 1
to 4 still run at once, so the claims are released and the client disconnects.
`lit-tui` then waits for the editor to exit before it restores the terminal,
and afterwards prints `shutdown finished while the editor was open and then
waited for it to exit; the draft is kept: …` to stderr. Whatever the editor
saved stays in the draft.

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
left to lease expiry, and keeps the registry entry. If the editor is still open,
it is left running and the terminal is left to it: stderr names the editor's
process ID and the draft file it edits.

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
