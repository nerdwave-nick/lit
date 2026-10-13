# lit

lit combines a local tracker, a terminal UI, and customized skills for coordinating
coding agents and human decisions from planning through verified implementation.

## Project scope

- Keep implementation work traceable through shared specifications, decision
  records, research, prototypes, test evidence, and handoffs.
- Coordinate work across repositories and agents with projects, issues, comments,
  explicit blockers, optional milestones, and expiring work claims.
- Give humans an interactive terminal UI and agents a structured CLI, both using
  the same tracker service.
- Support discovery and recovery with literal/regex and ranked search, mutation
  history, uncertain-request reconciliation, database backups, and Markdown exports.

lit focuses on agent coordination and the details of implementation. Broader
product and feature planning can stay in a tracker such as Jira; lit carries the
decisions and execution context needed for the coding workflow.

## Recommended workflow

Use the agent skills to move from an idea to verified implementation:

```text
/planner -> /to-spec -> /to-tickets -> /orchestrate
```

1. **[/planner](internal/workflow-skills/skills/planner/SKILL.md) — settle the decisions.**
   Establish the intended outcome and constraints, then build a persistent planning
   map in lit. Track precise questions and their dependencies, gather evidence
   through research or prototypes, and resolve one human decision per working
   session. Accepted answers stay linked from the map so later work can build on
   them without reopening settled choices.
2. **[/to-spec](internal/workflow-skills/skills/to-spec/SKILL.md) — write the shared specification.**
   Synthesize the agreed decisions into one specification in lit: the problem,
   intended behavior, meaningful edge cases, accepted contracts, testing boundaries,
   and affected repositories. Keep exclusions and unresolved choices explicit.
   This specification owns the requirements shared by all implementation tickets.
3. **[/to-tickets](internal/workflow-skills/skills/to-tickets/SKILL.md) — make the work executable.**
   Break the specification into small, coherent implementation tickets with
   testable acceptance criteria and clear scope. Present the breakdown for human
   review, then publish the approved tickets and real blocker relationships in lit.
   Read back the dependency graph to identify which tickets can start; optional
   milestones group useful delivery boundaries.
4. **[/orchestrate](internal/workflow-skills/skills/orchestrate/SKILL.md) — implement and verify the authorized work.**
   Coordinate native agent workers in isolated branches and worktrees, running
   independent tickets in parallel as their dependencies allow. Review each
   handoff, integrate changes one at a time, and run the required checks before
   closing tickets. Keep durable progress and verification evidence so an
   interrupted run can resume, and verify the full authorized scope at completion.

Start at the stage that matches the work: an existing specification can go straight
to `/to-tickets`, and approved implementation tickets can go to `/orchestrate`.
Planning, specification, and ticket publication do not start implementation;
authorize the execution scope before orchestration begins.

## Other skills

The embedded suite contains 16 skills for Codex and Claude. The four stages above
can use the supporting skills below, which can also be invoked independently.
The `/skill` notation is used here for readability; Claude uses `/skill`, while
Codex uses `$skill`.

| Skill | Purpose |
| --- | --- |
| [/lit](internal/workflow-skills/skills/lit/SKILL.md) | Connect to the tracker, select project context, discover work, manage claims, and recover interrupted operations. |
| [/clarify](internal/workflow-skills/skills/clarify/SKILL.md) | Settle open questions in small discussion rounds and record accepted answers. |
| [/research](internal/workflow-skills/skills/research/SKILL.md) | Investigate a question using primary sources and preserve evidence, findings, and limitations. |
| [/prototype](internal/workflow-skills/skills/prototype/SKILL.md) | Build a small disposable artifact to answer a design question and capture the result and human verdict. |
| [/challenge](internal/workflow-skills/skills/challenge/SKILL.md) | Scrutinize proposals and consequential decisions against concrete evidence and failure scenarios. |
| [/consult](internal/workflow-skills/skills/consult/SKILL.md) | Request an independent, read-only Claude or Codex CLI consultation and reuse its explicit session for follow-ups. Requires consultation authorization and a separately installed vendor CLI. |
| [/modeling](internal/workflow-skills/skills/modeling/SKILL.md) | Refine domain terms and relationships, recording settled vocabulary and warranted architectural decisions in the owning repository. |
| [/codebase](internal/workflow-skills/skills/codebase/SKILL.md) | Design modules with small, useful interfaces, clear seams, and behavior that can be tested through those interfaces. |
| [/triage](internal/workflow-skills/skills/triage/SKILL.md) | Evaluate incoming issues, reproduce reported behavior, and record an agreed disposition with evidence. |
| [/impl](internal/workflow-skills/skills/impl/SKILL.md) | Implement one authorized ticket through appropriate checks, verified local commits, and tracker evidence; also used by orchestration workers. |
| [/what](internal/workflow-skills/skills/what/SKILL.md) | Re-explain an unclear response in plain language with the missing context. |
| [/i-have-adhd](internal/workflow-skills/skills/i-have-adhd/SKILL.md) | Apply a requested concise, action-oriented communication style for the session. |

## Components

- **`lit-server` — local issue tracker service:** one service per user on a machine,
  serving shared tracker records from an embedded SQLite database (`lit.db`).
- **`lit-tui` — recommended interface for humans:** browse and edit projects,
  issues, comments, and milestones in the terminal.
- **`lit`:** the command-line client for `lit-server`, with terminal, Markdown and JSON
  output for agents and scripts. `lit tui`, or bare `lit` on a terminal, starts
  the TUI.
- **`internal/workflow-skills/`:** the canonical skill sources, embedded in `lit`
  and installed into Codex and Claude by `lit setup-skills`.

Projects represent features or other efforts: each can span multiple repositories,
and each repository can participate in multiple projects. Milestones group explicit
issue sets within a project; parentage and milestone membership do not create
blocker relationships. Assignment records responsibility, while an expiring work
claim coordinates ownership. Agents renew claims explicitly; the TUI renews its
claims in the background while active.

SQLite is the authoritative store for records and transaction history. Markdown
bodies remain supported, and `lit-server export` produces a one-way Markdown tree
for browsing or external search. Editing that export does not update the tracker;
database snapshots provide backups. See [storage](docs/storage-format.md),
[exports](docs/export.md), and [recovery](docs/recovery.md).

## Getting started for humans

Use the TUI for everyday tracker work. On Linux, install the service and both
clients from this checkout with the Go version specified in `go.mod`:

```sh
go install ./cmd/lit-server ./cmd/lit ./cmd/lit-tui
```

Start the service in one terminal:

```sh
lit-server
```

Then open the TUI in another:

```sh
lit
# Or explicitly:
lit tui
```

Install `lit-tui` next to `lit`: `lit` looks for it in its own directory first, then
on `PATH`. The TUI creates its own human client session and opens the project
switcher; no manual `lit connect` is needed. It uses `http://127.0.0.1:7411` by
default; choose another endpoint with `lit tui --endpoint URL` or `LIT_ENDPOINT`.
Clients also build for macOS and Windows; the service runs on Linux. See
[installation](docs/installation.md) for supported targets, remote access, manual
updates, Docker Compose, and optional systemd user service or socket activation.

The TUI supports:

- Browsing issue trees with rendered Markdown, comments, literal content search,
  and filters for state, labels, assignee, and claims, with blocker details in
  the issue view.
- Creating projects and issues, editing shared specifications and repository
  references, and managing milestone descriptions and membership.
- Updating titles, state, labels, assignees, parents, and blocker or related links.
- Editing bodies and comments in your editor, previewing changes before applying
  them, and preserving drafts when revisions conflict or an operation is uncertain.
- Viewing mutation history and diffs, claiming work, and renewing held claims in
  the background, with an idle prompt before continued renewal.

Start with `m` for the action menu and `?` for help. Use `/` to search, `f` to
filter, `e` to edit a body, `a` to add a comment, `H` for history, `M` for milestones,
and `D` for saved drafts. The editor is `$VISUAL`, then `$EDITOR`, then `vi`.
See the [TUI guide](docs/tui.md) for the full keymap, themes, mouse settings,
editing, claim handling, and recovery.

## Agent setup and CLI use

Install the embedded skills into the current repository:

```sh
lit setup-skills --scope local --agent both
```

Setup works offline without a checkout or tracker connection. Choose `--scope user`
for your home or `--scope custom --path /path/to/parent` for a custom parent of
`.codex` and `.claude`. Rerun setup after a CLI upgrade to update managed skills;
unrelated files and local modifications are protected.

Agents should load the `lit` skill and use its
[runtime session guidance](internal/workflow-skills/skills/lit/references/hosts.md)
to establish distinct clients and explicit project context. Scripts can connect
directly with a named session:

```sh
lit connect --session automation --actor-name Automation --actor-kind agent --format json
lit projects list --session automation --format json
```

Without `--session` or `LIT_SESSION`, `connect` creates and displays a random
throwaway session name. Reuse that name explicitly for subsequent tracker calls.
For tracker commands, endpoint precedence is `--endpoint`, `LIT_ENDPOINT`, the
session's remembered endpoint, then `http://127.0.0.1:7411`. Commands do not start
the service automatically. Outside a terminal, bare `lit` exits 2 with
`invalid_arguments`; scripts and agents should use explicit subcommands.

Use executable `--help` for the installed version's command syntax. See
[CLI help and shell completion](docs/cli-help.md), [output formats](docs/cli-output.md),
and [development checks](docs/development/testing.md) for further details.

## Upgrading from pre-SQLite versions

`lit-migrate` is a one-time upgrade tool for importing a legacy file store into
a new SQLite data directory. It is only needed when upgrading from a pre-SQLite
version. See the [setup import guide](docs/installation.md#setup-import-from-a-legacy-file-store).
