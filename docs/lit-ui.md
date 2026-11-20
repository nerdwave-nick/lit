# lit-ui: lit in Claude Code

`lit-ui` is a Claude Code mod (Claude Code 2.1.287 or later) that shows a
conversation's lit state in one band above the prompt and in the lit pane, launches
the workflows by selection, toasts when a claim is about to lapse, and writes a
status feed a user-defined status line can read. All tracker behavior stays in lit
commands: the mod reads, presents and calls them. Its source is
`internal/workflow-skills/mods/lit-ui/`; `lit setup-workflows` installs it next to
`lit-hooks`.

## The band

| State | Band |
| --- | --- |
| ready, status `mod` | `🔥 <project> · ◷ <claim> <time> · ⚑ N waiting · <stage>` `1:<flow> 2:lit` |
| ready, status `both` | `⚑ N waiting · next /<flow>` `1:<flow> 2:lit` (project and claim are the status line's) |
| a claim under 5 minutes | `◷ <claim> 3m left` in yellow, in both modes; `3:renew` replaces `1` |
| noproject | `○ lit · no project selected` `1:choose project` |
| nosession | `○ lit · not connected in this conversation` `1:connect` |
| pending | `○ lit · connection pending` `1:reconcile` |
| offline | `⚫ lit · tracker not answering · <host:port>` `1:retry` |
| degraded | `⚫ lit · tracker needs recovery (docs/recovery.md)` `1:retry` |

The states take precedence in this order: offline, degraded,
nosession, pending, noproject, ready. Zero counts and an empty stage are left out,
and `1` is absent when nothing is recommended. While a turn runs the buttons hide.
In an empty prompt a digit presses the button:

- `1` puts the recommended command in the prompt (`/planner P`, `/planner P
  decision: T`, `/to-spec P`, `/to-tickets P`, `/impl P:T`, `/orchestrate P
  [milestone M]`); Enter runs it. In the other states it connects
  (`workflow-session fresh`), reconciles (`workflow-session reconcile`) or retries;
  without a project it opens the pane on Project.
- `2` opens the pane.
- `3` renews this conversation's expiring claim for 30 minutes.

The recommendation is the first that matches: no issues, planner; a claim of this
conversation, impl on that ticket; open decisions, planner on the first; settled
decisions and no specification (a heading starting with "Shared specification" in
the project description), to-spec; a specification and no open `ready-for-agent`
issues, to-tickets; two or more ready tickets, orchestrate (the milestone holding
most of them); one ready ticket, impl on it. Ready tickets are open
`ready-for-agent` issues without an open blocker or a claim. "Waiting" counts open
`decision` issues and open issues labelled `needs-info` or `ready-for-human`.

Toasts: one when a claim of this conversation falls under 5 minutes
(`lit: claim on "T" expires in 3m`) and one when it has expired. Timers, refreshes
and hooks never acquire, renew, reacquire, force or release a claim; only `3` renews.

## The pane

`2` on the band, `/lit-ui` and `1:choose project` open the pane, focused; Esc
closes it. Its header reads `lit · <project>` with the views Home, Launch, Board
and Project, switched with `h`, `l`, `b` and `p`; without a selected project it
shows Project and the other views are dim. Below 60 columns the right-hand meta
drops and the views go under the title. Every view ends with a dim line of key
hints. Choices are numbered rows pressed by their digit (or Tab and Enter); after a
choice the focus moves to the action Enter should take. A failed action shows once
as a toast and as a red `⚠` row at the top of the view until the next successful
refresh. Outside `ready` and `noproject` the pane shows the band's line and its
one action.

- **Home:** NEXT STEP shows `❯ <command>` and why; Enter puts it in the prompt,
  `r` runs it now. MY CLAIMS lists this conversation's claims; selecting one
  offers `r renew` (30 minutes) and `x release`, which asks for a second `x`.
  WAITING FOR YOU lists open decisions and `needs-info` and `ready-for-human`
  issues; a digit puts that item's command in the prompt. Empty sections are left
  out.
- **Launch:** planner, to-spec, to-tickets, impl and orchestrate, in that order,
  the recommended one marked `★ recommended`; to-tickets without a specification,
  impl without ready tickets (or a claim of this conversation) and orchestrate
  with fewer than two ready tickets stay dim with that reason. Choosing one opens
  its target step: planner the open decisions and the whole planning map,
  to-tickets and orchestrate the milestones and the whole project, impl this
  conversation's and the ready tickets (blocked tickets and other agents' claims
  are shown dim and cannot be chosen); to-spec has none. The step shows
  `→ <command>`; Enter puts it in the prompt, `r` runs it now, `back` or Esc
  returns to the list, and Esc there closes the pane.
- **Board** (read-only): a summary of the non-zero counts (in progress, ready,
  blocked, claimed by others, done), or "No implementation tickets yet · N other
  open issues"; then one row per open ticket: this conversation's `◷` with the
  time left, ready `●`, blocked `◌` with `← <blockers>`, another agent's `◐` with
  its name and time left (a blocked ticket counts as blocked only), and the last
  three closed `✓`, most recently changed first. LONGEST CHAIN shows the longest
  run of open issues each blocking the next, when it is longer than one; each
  milestone has a progress bar with `n of m done`.
- **Project:** CHOOSE A PROJECT lists the selected project and this repository's
  projects (the session directory's git origin among the projects' repository
  refs, compared in their https form and ignoring case), numbered, then `more…`
  for all the others. A project is selected only when chosen (`lit session set
  --project <ID>`), and the pane goes to Home; Esc folds the full list back first.
  NEW PROJECT: `n` moves to the title field. Once something is typed, a title lit
  would refuse shows why in red: a prefix outside the tracker's `title_prefixes`,
  a part that is not letters, digits, `-` and `_` starting with a letter or digit,
  over 128 characters, or a title that exists in any case. Enter creates it with
  `lit projects create --project-title <title> --repository <https ref>` (the
  origin's https form: `git@github.com:o/r.git` becomes `https://github.com/o/r`;
  without a git origin, no repository), selects it with `lit session set
  --project title:<title>`, puts `/planner <title>` in the prompt and closes the
  pane. A refused create or select shows as the failure row; the reason stays
  under the field (Claude Code empties the field on Enter).

Putting a command in the prompt and running it now both close the pane.
Orchestrate is never run from the pane: it always goes through the prompt. Run
now runs the slash command from a timer after the press, as the person typing it
would.

## Next-stage suggestion

When the session's stage (`lit session set|unset --stage`) goes from `planner`,
`to-spec` or `to-tickets` to none between two refreshes, the mod proposes the
following stage's command as the prompt's ghost text (Tab takes it): `/to-spec P`,
`/to-tickets P` or `/orchestrate P` for the selected project P. It does so once per
cleared stage, never at every turn end, and only when the same lit session keeps the
same project selected. Other changes (a stage replaced by another, `impl` or
`orchestrate` cleared, no project) suggest nothing, and a refresh that could not read
the stage counts as no reading. A stage cleared while a turn runs, or while the
prompt holds text, is proposed at the turn's end or the next refresh after that,
until the prompt has shown it or a new stage, project or session replaces it.

## Data and refreshes

Every refresh reads `$.session.id()` (it changes on `/clear`) and the association
with `lit workflow-session --host claude --runtime-id <id> show`. Tracker reads use
the association's `--session` and `--endpoint` with `--format json`: `/v1/meta`
(reachability, degraded mode, title prefixes), `session get`, `projects list`,
`claims list`, and the selected project's `issues list` and `milestones list`.
Without an association it probes `LIT_ENDPOINT` or `http://127.0.0.1:7411`. lit
runs in `<workflow state>/lit-ui/`, outside any checkout, because it writes a
cache into its working directory. Claim tokens are dropped when lit's output is
read and are never shown or written. `git remote get-url origin` runs once per
session directory (`$.session.cwd()`), in that directory, for the Project view.

The mod refreshes every 5 seconds, right after the agent runs a Bash command that
invokes lit, after every press in the band or the pane that fills the prompt or runs
lit, and on `/lit-refresh`. A refresh requested while one runs is served by one more
run after it.

## Configuration

`lit setup-workflows --scope user --agent claude` (or `--config-dir`) installs the
mod as `<config>/skills/lit-ui/` with `--status mod` or `both` (the default); see
[Status surface](installation.md#status-surface-the-status-line-and-the-lit-ui-mod).
Claude Code loads it once the workspace is trusted.

Setup writes `config.json` in the mod's directory, beside `.claude-plugin/`:

```json
{"schema_version": 1, "status": "mod", "lit": "/home/alice/go/bin/lit"}
```

`status` is `mod` or `both` (the `--status` of `lit setup-workflows`; `line`
installs no mod) and `lit` the executable the hooks call. Without the file the mod
runs `lit` from PATH in mode `both`: the development default, for example

```sh
claude --plugin-dir internal/workflow-skills/mods/lit-ui
```

A file that cannot be used (not JSON, another `schema_version`, an unknown
`status`, no `lit`) also gives the default, and `/lit-ui debug` names the problem.

## Status feed

Each refresh writes `<workflow state>/ui-feed/<Claude session ID>.json`, where
`<workflow state>` is `LIT_WORKFLOW_STATE_DIR` or the platform default
(`${XDG_STATE_HOME:-$HOME/.local/state}/lit/local` on Linux, `~/Library/Application
Support/lit/local` on macOS, `%LOCALAPPDATA%\lit\local` on Windows):

```json
{
  "updatedAt": "2026-11-20T00:00:00.000Z",
  "state": "ready",
  "project": "feat/demo",
  "claims": [{"title": "Add cart model", "remainingMs": 1440000}],
  "stage": "impl",
  "ready": 1,
  "waiting": 3,
  "recommend": {"flow": "impl", "reason": "You hold the claim on this ticket."},
  "errors": []
}
```

The Claude session ID is the `session_id` a status line command receives on stdin.

## Commands

- `/lit-ui` opens the pane.
- `/lit-refresh` refreshes now and answers with the state.
- `/lit-ui debug` prints the endpoint, the status mode and its source, the lit
  session, the state, the stage, the last refresh's time and duration, and any read
  errors.
