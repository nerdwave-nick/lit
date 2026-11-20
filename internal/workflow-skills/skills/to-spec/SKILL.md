---
name: to-spec
description: Synthesize settled discussion or requirements into a shared specification in lit, preserving agreed decisions and unresolved boundaries.
---

# To spec

Run this skill only when the user requested it. Otherwise propose it and run it
only after the user confirms.

Workflow stage: when the user invokes this skill directly, record it with
`lit session set --stage to-spec` before starting. When another workflow skill
invokes it, leave the stage unchanged. On completion or handoff, run
`lit session unset --stage`. The Workflow stage section of the
[lit skill](../lit/SKILL.md) covers the session and older lit versions.

Turn existing discussion, requirements, or a referenced issue into a specification.
No planner map or earlier skill invocation is required. Synthesize settled choices;
do not restart an interview or require a test-seam confirmation already answered.
Ask only about unresolved material choices. Keep uncertainty explicit rather than
inventing agreement.

Read the shared [lit guidance](../lit/SKILL.md) for session/project selection,
ownership, recovery, and installed CLI discovery. Read repository conventions,
CONTEXT.md, and relevant ADRs in each affected repository. Use structured tracker
queries and `grep` to discover relevant decisions; fetch current full records and
comments, following pagination and relationships. Search excerpts are not authority.
Do not assume tracker files are mounted locally or infer project selection from
directory, branch or checkout names; a project the session adopted because the user
remembered it for the checkout is announced by the lit guidance and may be used.

The project owns requirements shared across tickets. Read its current description
and linked specification before writing. Synthesize into the existing designated
shared specification, or establish one with an explicit reference from the project.
A standalone issue may carry its entire specification. Preserve unrelated project
content and repository identities/roles; machine-specific checkout paths stay local.
Do not create competing authoritative briefs or silently override accepted decisions.

If the effort uses milestones, read their current membership and objective using
the shared [milestone guidance](../lit/references/milestones.md) and installed
`lit milestones` help. Record the accepted delivery boundaries, member intent, and
cross-cutting acceptance in the shared specification, where they remain authoritative
for all tickets. Keep each milestone body to a short objective and a reference to
that shared specification; do not copy the full requirements into milestone bodies.
Milestones are optional, and their membership does not imply parentage or blockers.

Use the structure below when useful; scale detail to the actual requirements:

- Problem and intended user-visible solution.
- User stories covering agreed behavior and meaningful edge cases.
- Accepted implementation decisions and contract references, distinguishing facts
  from proposals. Include a concise prototype snippet only when it captures the
  decision more precisely than prose; identify its provenance.
- Testing decisions: observable behavior, existing suitable seams, relevant prior
  art, and agreed boundaries. Do not manufacture new frameworks or test interviews.
- Out of scope, unresolved material choices, and source decision references.
- Affected repository identities and roles when the work crosses repositories.

Publishing a requested specification is within this skill's task. Preserve existing
publication authorization; resolve ambiguous destination or material choices before
the dependent write. Apply repository triage conventions (default ready-for-agent
for a settled, executable specification); incomplete requirements remain visibly
unsettled. Verify the persisted record and report its stable reference and remaining
questions. This does not authorize ticket execution, push, deployment, or closing a
parent. A later to-tickets invocation owns reviewed slices and their acceptance
criteria, while this shared specification retains common behavior authority.
