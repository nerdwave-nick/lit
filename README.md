# lit

The home for my coding agent workflows, local tooling, and customized skills.

## Goals

- Simple and clear workflow for implementation tasks, supported by tooling for research, prototyping, testing, traceability
- NOT a replacement for a fully featured issue tracker like Jira, this is only for the agent synchronization and details of implementation, not to replace feature planning in general

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

## Components

- **`lit-server` — local issue tracker service:** one service per user on a machine,
  coordinating shared projects and issues stored as Markdown files.
- **`lit`:** the command-line client for `lit-server`, with terminal, Markdown and JSON
  output for people and coding agents.
- **`lit-tui`:** the terminal UI for people. `lit tui`, or bare `lit` on a terminal,
  starts it.
- **`internal/workflow-skills/`:** skills collected from various sources and adjusted to fit my
  coding workflows and preferences.

Projects represent features or other efforts: each can span multiple repositories,
and each repository can participate in multiple projects. Explicit work claims
and lease renewal coordinate concurrent coding agents; renewal is user/agent driven.

The initial implementation includes the Go service, command-line client, and
sixteen agent skills for Codex and Claude. Use the installed executable help for
command syntax. See [build, installation, and manual update instructions](docs/installation.md)
for supported binaries, per-user installation, foreground operation, and the
optional systemd user unit. Build or install with the standard Go toolchain:

```sh
go install ./cmd/lit ./cmd/lit-tui
lit setup-skills --scope local --agent both
```

Install `lit-tui` next to `lit`: `lit` looks for it in its own directory first, then
on `PATH`. Outside a terminal, bare `lit` still exits 2 with `invalid_arguments`, so
scripts and agents are unaffected.

The CLI embeds the skill suite; setup works offline without a checkout. Choose
`--scope user` for your home or `--scope custom --path /path/to/parent` for a
custom parent of `.codex` and `.claude`. Remote `go install` instructions and
publication caveats are in the installation guide.

`consult` is an installed skill, not a `lit` command. It guides agents to
invoke Claude or Codex directly, reuse explicit vendor sessions, and record
findings in discussion notes. Install the chosen vendor CLI separately; external
consultations still require authorization.

On Linux or macOS, install the daemon with `go install ./cmd/lit-server`. Start it with
`lit-server`, then connect locally with
`lit connect --session poc-human --actor-name Alex --actor-kind human`.
Without `--session` or `LIT_SESSION`, `connect` creates and displays a random
throwaway session name. Reuse that name explicitly for subsequent tracker calls.
The client defaults to `http://127.0.0.1:7411`; `--endpoint`, `LIT_ENDPOINT`, and
a session's remembered endpoint take precedence, in that order. Commands do not
start the service automatically.
