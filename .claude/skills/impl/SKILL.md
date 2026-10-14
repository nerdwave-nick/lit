---
name: impl
description: Implement one lit ticket through observed tests, verified local commits, tracker evidence, and an honest completion or handoff.
---

# Impl

Handle one implementation ticket per invocation. Load [lit](../lit/SKILL.md)
for session/project selection, ownership, discovery, and recovery. Executable
help owns command syntax. Use the current installed operations; do not substitute
local Markdown claims for tracker ownership. Implementation authorization does
not imply push, PR creation, publication, deployment, or paid consultation.

Workflow stage: when the user invokes this skill directly, or a coordinator
started you as a worker, record it with `lit session set --stage impl` before
starting; a worker does this in its own session, never the coordinator's. When
another workflow skill invokes it in your session, leave the stage unchanged. On
completion or handoff, run `lit session unset --stage`. The Workflow stage
section of the [lit skill](../lit/SKILL.md) covers the session and older lit
versions.

## Select and claim

For a named ticket, fetch its full body, project, comments, blockers, relationships,
shared specification, relevant decisions, and previous handoffs. Confirm the
requirements are sufficient and the work is available. A named ticket already
owned by another client is a conflict, not permission to replace it or force.

Without a named ticket, use clear, confirmed project context. Repository evidence
may support a project proposal; it never silently selects it. Ask for missing
context. Discover open, unblocked, unclaimed `ready-for-agent` implementation
work, respecting repository label overrides and following all relevant pages.
Propose the oldest eligible ticket with its scope and obtain selection confirmation
before acquiring it. If none matches, report that result; do not relax filters,
create work, or begin another ticket. Existing confirmation of a specific ticket
suffices; do not ask twice.

Acquire the selected ticket atomically, then reread its current requirements,
comments, blockers, and ownership before editing. If another client wins between
proposal and acquisition, report the conflict and propose a replacement for fresh
confirmation. Do not silently substitute even if the next candidate is obvious.
A claim is separate from assignment and from Git file isolation. Use a dedicated
worktree or explicit coordination for overlapping agents.

## Execute a behavior slice

Read each affected repository's applicable instructions, CONTEXT.md, relevant
ADRs, tracker conventions, and linked requirements. Resolve repository-relative
paths in their owning repository. Shared specifications own common behavior;
tickets own their slices. Ask about material missing context or contradictions;
do not invent acceptance criteria. Preserve existing unrelated changes and record
initial branches/commits. Plan nontrivial work before changing it.

Load only applicable language references: [Go](references/go.md),
[TypeScript](references/typescript.md), or [Vue](references/vue.md). Follow existing
tools, versions, test boundaries, and hooks; do not change frameworks or compiler
settings merely to fit this workflow.

For each changed behavior, write a meaningful test at an observable boundary,
run it and observe the intended failure, implement the smallest passing change,
then refactor while keeping it green. A compile/import/setup failure is not proof
of the intended red behavior. Record actual commands and results. Documentation,
configuration, or covered behavior-preserving refactors use appropriate validation
without manufactured failures; explain the exception and establish coverage or
add characterization tests where needed. Broaden checks according to risk and
repository requirements before completion.

Maintain ownership during sustained work. Explicitly renew before expiry; ordinary
mutations never renew. Use a bounded absolute renewal target from current service
time; never change a retained request to disguise an uncertain retry. On expiry,
stop, reacquire, and reread before continuing. If another client owns it, stop and
report. Agents never force takeover. Do not start new claimed work while lit is
unavailable; preserve local progress and use supported reconciliation before
resuming or retrying uncertain mutations.

## Checkpoint and finish

When a coordinator explicitly dispatches this ticket through `$orchestrate`,
use its confirmed assignment instead of proposing a standalone ticket. Still
reread the issue and acquire and maintain your own claim under your own client.
Read [coordinated worker protocol](../orchestrate/references/protocol.md):
verify the bounded scope, commit and report evidence, release your own claim,
and stop. Leave integration and ticket closure to the coordinator. Outside
that explicit dispatch, this skill remains the standalone one-ticket workflow
below.

For cross-repository work, interruption, or return from a handoff, read
[Interrupt and resume implementation](references/resume.md) before checkpointing
or resuming. It covers partial commits, failed hooks, pending tracker updates,
checkout/object recovery, and the versioned local handoff.

At each meaningful green milestone, review and commit only ticket-owned changes,
respect hooks, then append a ticket comment with repository/branch identities,
full commit hashes, commands/results, current TDD phase, remaining work, and next
action. Commit before commenting. Preserve reachable cited commits: do not amend,
rebase, or squash them during active work. A later authorized rewrite needs an
appended old-to-new hash mapping. Unchanged repositories reference their existing
commits; do not manufacture empty commits. Git and lit are separate systems:
record partial commit/comment success and outstanding updates honestly.

Required check or commit failures leave the ticket open. Do not bypass hooks or
weaken validation. On an orderly stop preserve an explicitly marked WIP checkpoint
when needed, list expected failures/uncommitted files and the next action in a
handoff, and release the claim when available. Record pending tracker updates
locally if the service is down; never claim those updates were stored. A hard
crash may lack a final handoff, so checkpoint throughout meaningful work.

Complete only after every acceptance criterion and required check passes, the
ticket-owned diff is reviewed, all changed repositories have verified local
commits, and a completion comment containing commit/check evidence is stored.
Then close the ticket and verify the closed state and ended claim. If evidence
or closure is interrupted, reconcile the retained request and reread before
retrying; do not assume the comment or close failed. Report completed work, exact
commit/check evidence, and remaining limitations. Stop after this one ticket.
