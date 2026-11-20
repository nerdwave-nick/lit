# lit-ui: lit in Claude Code

`lit-ui` is a Claude Code mod (Claude Code 2.1.287 or later) that shows a
conversation's lit state in one band above the prompt, toasts when a claim is
about to lapse, and writes a status feed a user-defined status line can read. All
tracker behavior stays in lit commands: the mod reads, presents and calls them.
Its source is `internal/workflow-skills/mods/lit-ui/`; `lit setup-workflows`
installs it next to `lit-hooks`. The pane the band's `2:lit` will open is not part
of this version: `2`, `1:choose project` and `/lit-ui` say so in a toast.

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
  (`workflow-session fresh`), reconciles (`workflow-session reconcile`) or retries.
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

## Data and refreshes

Every refresh reads `$.session.id()` (it changes on `/clear`) and the association
with `lit workflow-session --host claude --runtime-id <id> show`. Tracker reads use
the association's `--session` and `--endpoint` with `--format json`: `/v1/meta`
(reachability, degraded mode, title prefixes), `session get`, `projects list`,
`claims list`, and the selected project's `issues list` and `milestones list`.
Without an association it probes `LIT_ENDPOINT` or `http://127.0.0.1:7411`. lit
runs in `<workflow state>/lit-ui/`, outside any checkout, because it writes a
cache into its working directory. Claim tokens are dropped when lit's output is
read and are never shown or written.

The mod refreshes every 5 seconds, right after the agent runs a Bash command that
invokes lit, after every button press, and on `/lit-refresh`. A refresh requested
while one runs is served by one more run after it.

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

- `/lit-refresh` refreshes now and answers with the state.
- `/lit-ui debug` prints the endpoint, the status mode and its source, the lit
  session, the state, the stage, the last refresh's time and duration, and any read
  errors.
