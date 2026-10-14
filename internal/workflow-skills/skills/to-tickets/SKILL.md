---
name: to-tickets
description: Turn requirements or an agreed specification into reviewed, verifiable implementation tickets and real blocking links in lit.
---

# To tickets

Run this skill only when the user requested it. Otherwise propose it and run it
only after the user confirms.

Workflow stage: when the user invokes this skill directly, record it with
`lit session set --stage to-tickets` before starting. When another workflow
skill invokes it, leave the stage unchanged. On completion or handoff, run
`lit session unset --stage`. The Workflow stage section of the
[lit skill](../lit/SKILL.md) covers the session and older lit versions.

Accept a plan, specification, existing discussion, or issue directly. No planner
prerequisite is needed. Read [lit guidance](../lit/SKILL.md), repository tracker and
triage conventions, and each affected repository's instructions, CONTEXT.md, and
relevant ADRs. Resolve repository-relative references against their owning repo.
Use tracker context and grep to discover earlier decisions and overlapping work,
then read the current full records and comments, including continuation pages.

Identify the project's shared specification and accepted decisions. It owns common
behavior; each ticket owns its slice and acceptance criteria. A standalone ticket
may include its complete specification. Flag contradictions instead of silently
changing shared requirements. Record repository identities and roles on the project;
keep checkout paths in client-local mappings. Missing access blocks affected slices,
not independent drafting.

Draft narrow, complete, verifiable slices. A ticket may span repositories when that
is the smallest coherent behavior change; do not split mechanically by repository.
Each ticket should fit a fresh working context and be demonstrable on its own.
Prefactor only when needed. For mechanical changes that cannot land independently,
use expand–migrate–contract or name a shared integration branch and explicit final
verification gate; explain where intermediate green checks are possible.

Milestones are optional project worksets, useful when the breakdown has named
delivery boundaries that users may select later. Read the shared [milestone
guidance](../lit/references/milestones.md) and installed `lit milestones` help.
Reuse a suitable existing milestone when its objective matches; otherwise propose
a new one. For each proposed milestone, keep its body to a short objective and a
reference to the shared specification. Do not make a milestone for every small
task, treat membership as a blocker, or infer membership from parentage. An issue
may belong to multiple milestones; reuse its one issue record rather than duplicate
the ticket. Milestone members must belong to the milestone's project. Keep blockers
outside that project as real external issue dependencies, not milestone members.

Present a new breakdown for human review before publication. Show each title,
delivery, acceptance criteria, affected repositories, and true prerequisite tickets.
When milestones apply, also show each milestone's title and objective, whether it
will be created or reused, and the exact issue membership being proposed. Call out
existing members that will remain, any issue shared by multiple milestones, and
every external blocker with its owning project. Parentage and related context do
not imply a blocker. Review granularity and edges; await approval where the
breakdown or membership is new. Explicit direction to publish an already agreed
breakdown is sufficient: do not ask again. Carry prior authorization forward
within scope, and return changed material choices for review.

Publish the approved breakdown through installed lit operations:

1. Create or reuse the approved milestones and record their stable UUIDs. Create
   or reuse one issue for each approved slice, recording actual returned issue
   identities. Before reusing an issue, fetch its current project, body, state,
   relationships, and membership context; only reuse it if it represents the
   reviewed slice, and do not duplicate it. Use the designated shared-spec
   reference, repository roles, expected
   outcome, testable acceptance criteria, scope boundaries, and relevant accepted
   contracts. Apply ready-for-agent unless the user's direction or repository
   conventions differ. Mutation output can include a changed project or milestone
   before a new issue: inspect each returned record's type and UUID, then fetch the
   issue before using its ID. Never assume the first result item is the new ticket.
2. After all issue IDs are known, fill or add the approved memberships using those
   stable IDs. Preserve unrelated existing members. Use each same-project issue
   once per milestone, allowing the same issue in multiple milestones. Then wire
   native blocker relationships, including cross-project blockers, using their real
   issue IDs and executable help. Do not include an external blocker as a member.
   Body references alone do not establish the frontier; parentage stays within one
   project and does not imply a dependency.
3. Read back the milestones, issues, and relationships. Verify exact membership,
   preserved unrelated members, ticket criteria, and the real dependency graph.
   Report stable references, available frontier, external blockers, and any partial
   publication. If a mutation response is interrupted or uncertain, reconcile its
   outcome before retrying; do not blindly duplicate milestones, tickets, or links.

Preserve the parent's existing status and content. Creating or finishing children
never completes the parent implicitly. Publication does not start implementation;
hand off to impl only when the user has authorized execution.
