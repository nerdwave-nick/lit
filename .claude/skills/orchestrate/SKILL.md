---
name: orchestrate
description: Coordinate authorized implementation tickets with isolated native workers, durable handoffs, serial integration, and coordinator-owned closure.
---

# Orchestrate

Coordinate a user-authorized set of implementation tickets in one confirmed
`lit` project, whether the user names ticket IDs, the project, or a milestone.
You are the coordinator: check scope and dependencies, assign one
native worker to each isolated ticket, review each handoff, integrate serially,
and close tickets only after integrated verification. This skill does not itself
authorize implementation. Before creating workers or mutating tracker or Git
state, confirm that the user authorized the exact project or ticket set and the
delegation and integration scope. If that boundary is unclear, ask once and wait.

Workflow stage: when the user invokes this skill directly, record it with
`lit session set --stage orchestrate` before starting; workers record `impl` in
their own sessions, so do not set it for them. When another workflow skill
invokes it, leave the stage unchanged. On completion or handoff, run
`lit session unset --stage`. The Workflow stage section of the
[lit skill](../lit/SKILL.md) covers the session and older lit versions.

Load [lit](../lit/SKILL.md) for session, claim, and recovery rules and
[impl](../impl/SKILL.md) for a worker's one-ticket implementation. Read
[the coordination protocol](references/protocol.md) for the durable ledger,
frontier, profiles, ownership transfers, and integration. Use installed command
help for tracker syntax. Use native host agents only; never launch external
model CLIs as workers.

Create a native goal only when the host exposes goal tracking, and reflect only
the goal states and controls that host actually supports. Do not invent token
budgets or pause/block controls. The durable run ledger and tracker evidence are
the recovery record when native goal tracking is unavailable.
