---
name: lit
description: Coordinate agent work in lit through explicit session and project context, issue discovery, milestones, claims, and recovery.
---

# lit

Use explicit `lit` CLI subcommands for agent tracker work.
The client executable is `lit`; the server executable is `lit-server`.
Connections do not start the server.

Use `lit --help` for command syntax. Read
repository conventions in `docs/agents/issue-tracker.md`, resolving paths against
that repository. Read and mutate tracker records through `lit`; local files do
not establish tracker state or ownership.

Establish an explicit logical session using [host setup](references/hosts.md).
Independent agents and subagents need distinct sessions. Resume only the same
logical working session. Pass session selection on every call; a child process
cannot export settings into its parent. Changing cwd never selects a project.
Select a project explicitly or retain the confirmed server session selection.
Ask when project context is ambiguous. Repository evidence can support a proposal,
not silently select a project.

Use the relevant short recipe:

- [Connect and select a project](references/connect.md).
- [Find, claim, and read work](references/work.md).
- [Checkpoint, recover, and finish](references/recovery.md).

Assignment records responsibility; a work claim grants temporary ownership.
Sustained work requires an explicit claim. Claims default to thirty minutes
and are capped at one hour. Agents renew explicitly; ordinary edits do not renew.
After expiry, reacquire and reread current context before continuing. Agents never
force takeover. Claims do not protect Git files: coordinate shared checkouts or
use separate worktrees.

Projects own shared requirements and can span repositories. Put repository
identities and roles in project content; use local checkout mappings for machine
paths. Read each affected repository's instructions and domain docs. Tickets own
their slice and acceptance criteria; they cannot override the shared specification.
Workflow stages are independently invokable, and planning does not authorize
implementation, external consultation, push, publication, or deployment.

Milestones are optional named worksets of issues within one project. Membership is
explicit, may overlap across milestones, and does not follow parentage or create
blockers. Read [milestone guidance](references/milestones.md) when planning,
specifying, publishing, or selecting a milestone scope; installed command help
remains authoritative for syntax.

## Recommended workflow

When guiding a human through an effort, explain the recommended sequence:

1. [planner](../planner/SKILL.md): settle open decisions and record accepted
   answers in a planning map.
2. [to-spec](../to-spec/SKILL.md): synthesize agreed behavior, boundaries, and
   decisions into the shared specification.
3. [to-tickets](../to-tickets/SKILL.md): draft verifiable implementation slices,
   review them with the human, and publish the approved tickets and blockers.
4. [orchestrate](../orchestrate/SKILL.md): coordinate authorized implementation,
   review handoffs, integrate changes, and verify the completed scope.

Humans invoke these as `/planner -> /to-spec -> /to-tickets -> /orchestrate` in
Claude, or `$planner -> $to-spec -> $to-tickets -> $orchestrate` in Codex.
Recommend the stage matching the existing work; earlier stages are optional when
their outcome is already settled. Explaining the sequence does not invoke the
skills or authorize execution; follow the human's requested stage and scope.
