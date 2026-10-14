---
name: prototype
description: Build the smallest disposable artifact that answers a design question, capture evidence and a verdict, and leave production application to authorized implementation.
---

# Prototype

Workflow stage: when the user invokes this skill directly, record it with
`lit session set --stage prototype` before starting. When another workflow skill
invokes it, leave the stage unchanged. On completion or handoff, run
`lit session unset --stage`. The Workflow stage section of the
[lit skill](../lit/SKILL.md) covers the session and older lit versions.

State the question, constraints, and what observation would answer it. Choose the
smallest useful form: an outline, table, diagram, paper flow, script, code sketch,
or UI demo. A logic question does not automatically need HTML; a visual question
does not automatically need several variants or an app route.

Use [LOGIC.md](LOGIC.md) when interactive state exploration adds value, or
[UI.md](UI.md) when comparing visual alternatives does. These are optional forms.
Use the owning repository's vocabulary and instructions. Isolate artifacts in a
clearly marked scratch location or disposable branch/worktree. Keep production
files and real mutation endpoints untouched unless explicitly in execution scope.
Do not add dependencies, persistent services, or unrelated configuration merely
to make the prototype convenient.

Make it easy to inspect or run, show the relevant input/state/output, and perform
the smallest check that makes the evidence credible. Tests are useful when they
answer the question; a production-grade harness is not mandatory. Use synthetic
or isolated data. Record what the prototype cannot establish.

Capture the question, artifact, observed result, and verdict. For a human design
choice, present the artifact and wait for their answer; do not turn your preference
into acceptance. An unanswered verdict stays explicitly pending. Preserve the
artifact with a durable path and repository/branch/commit when appropriate, linking
it from the decision rather than pasting a competing specification.

For tracker-backed work load [lit](../lit/SKILL.md), claim/reread the ticket and
renew explicitly while working. Use [challenge](../challenge/SKILL.md) locally for
consequential choices; record material objections and their disposition. Follow
[planner](../planner/SKILL.md) for resolution comments, map indexing, and the
one-human-decision limit. A prototype's result never authorizes applying it to
production. Handoff the accepted decision and artifact to implementation only
within the user's execution scope; preserve the experiment as evidence.
