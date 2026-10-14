---
name: research
description: Investigate a question using primary sources and preserve findings with source references, optionally as evidence for a lit planning decision.
---

# Research

Workflow stage: when the user invokes this skill directly, record it with
`lit session set --stage research` before starting. When another workflow skill
invokes it, leave the stage unchanged. On completion or handoff, run
`lit session unset --stage`. The Workflow stage section of the
[lit skill](../lit/SKILL.md) covers the session and older lit versions.

State the question and scope. Prefer the source that owns a claim: official docs,
specifications, source code, first-party APIs, or direct observations. Distinguish
verified facts, inference, conflicting sources, and unanswered questions. Include
versions, dates, repository commits, and exact paths when they affect the result.
Use available research tools; do not invent access or treat summaries as primary
sources. Local code can be the primary source without a web search.

Delegate once when a focused background investigation would help and delegation
is available and authorized. Give it the question, constraints, raw references,
artifact destination, and explicit ownership. An already delegated researcher
investigates directly; recursive delegation is not a requirement. If delegation
is unavailable or adds little, research directly. Independent workers need their
own sessions/worktrees as applicable; do not let parent and child share a claim
or race to publish the same resolution.

Write a durable Markdown findings artifact using the owning repository's existing
conventions. Cite the source for each material claim and preserve limitations.
Record where the artifact lives and how to recover it (repository/path and commit
when committed). Do not create empty commits or push automatically.

For tracker-backed research load [lit](../lit/SKILL.md): explicitly select the
session/project, claim and reread the ticket, search relevant current tracker
content and fetch full records, and renew during sustained work. Record findings
and a named artifact link on the decision. A factual research ticket can close
when its question is answered; a human choice exposed by research remains open.
If working under [planner](../planner/SKILL.md), the decision comment owns the full
answer and the map only indexes it. Record newly precise questions and blockers.
Research is evidence, not permission to implement or invoke paid consultants.
