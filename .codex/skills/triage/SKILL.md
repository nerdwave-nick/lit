---
name: triage
description: Evaluate incoming lit issues, reproduce reported behavior, and record the requested or agreed disposition with evidence and related decisions.
---

# Triage

Run this skill only when the user requested it. Otherwise propose it and run it
only after the user confirms.

Workflow stage: when the user invokes this skill directly, record it with
`lit session set --stage triage` before starting. When another workflow skill
invokes it, leave the stage unchanged. On completion or handoff, run
`lit session unset --stage`. The Workflow stage section of the
[lit skill](../lit/SKILL.md) covers the session and older lit versions.

Use [lit guidance](../lit/SKILL.md) for tracker/session context, ownership, recovery,
and installed CLI syntax. Load repository tracker and triage-label conventions,
CONTEXT.md, and relevant ADRs for every affected repository. Triage is independently
invokable; neither planner nor a prior specification stage is required.

Default category roles are bug and enhancement. Default state roles are
needs-triage, needs-info, ready-for-agent, ready-for-human, and wontfix. Respect
repository overrides. Preserve unrelated labels while applying one category and
one state where classification is known. Open/closed status is separate from a
triage label: follow explicit status directions, and do not implicitly reopen an
issue merely because its label changes.

## Explicit direction

Execute a user's explicit state change within its stated scope, including resolving
conflicting old state labels. Do not demand another approval, clarification interview,
or mandatory brief. Record the requested disposition and report the actual changes.
If the user asks only for a label change, do only that change. If they direct a
rejection, record the supplied reason and references and close unless they explicitly
say to keep it open. A missing material rejection reason can be requested without
reopening settled outcome selection. Explicit direction does not license invented
verification or overriding the shared specification.

## Evaluate an issue

Read its full body, comments, labels, timestamps, related issues, blockers, and shared
specification. Use structured discovery and remote grep for similar reports and
prior decisions; follow continuation cursors and fetch full matching records.
Read existing .out-of-scope files as historical context only; see
[rejection records](OUT-OF-SCOPE.md). Search code by domain concepts for existing
behavior. Do not reject a report merely because a similarly named feature exists.

Verify the claim before recommending a final disposition: reproduce a bug using
reported steps and suitable existing checks when safe and authorized. Record the
command/environment and observed outcome, distinguishing confirmed behavior, failed
reproduction, and insufficient detail. For enhancements, inspect current behavior
and the requested difference. Missing access or unsafe reproduction is a limitation,
not evidence that the issue is false. Do not implement a fix during triage.

Recommend category/state with evidence. Ask where outcome selection needs human
judgment or conflicting state labels lack explicit resolution. Use clarify or
modeling by loading their discovered skill instructions when an unresolved decision
actually needs them; do not assume a host-specific Skill tool exists. Do not repeat
questions already resolved in comments.

Apply the agreed disposition:

- needs-triage: preserve evaluation notes and remaining work.
- needs-info: record established facts and specific unanswered questions.
- ready-for-agent: make the ticket's slice and acceptance criteria executable;
  add a [brief](AGENT-BRIEF.md) only when useful, preserving shared-spec authority.
- ready-for-human: record the same useful context and why human work is needed.
- wontfix: record the substantive reason and related tracker references before
  closure. Distinguish already implemented, duplicate, and rejected scope. Do not
  create or maintain a separate rejection database.

When showing attention queues, use repository conventions: untriaged issues,
needs-triage, and needs-info with new reporter activity since the last notes, oldest
first. Fetch comments where activity cannot be established from list metadata.
Do not silently truncate pages or infer new answers from an issue timestamp alone.

Identify triage-generated notes as such. Verify the stored labels, status, and
comments after mutation. Report references and limits; preserve partial success
and reconcile uncertainty before retrying. Parent completion and unrelated issues
remain separate decisions.
