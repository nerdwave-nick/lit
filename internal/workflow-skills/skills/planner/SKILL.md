---
name: planner
description: Chart and resolve a persistent map of planning decisions in lit, using decision tickets, evidence, and human discussion before implementation.
---

# Planner

Run this skill only when the user requested it. Otherwise propose it and run it
only after the user confirms.

Workflow stage: when the user invokes this skill directly, record it with
`lit session set --stage planner` before starting. When another workflow skill
invokes it, leave the stage unchanged. On completion or handoff, run
`lit session unset --stage`. The Workflow stage section of the
[lit skill](../lit/SKILL.md) covers the session and older lit versions.

Load the shared [lit skill](../lit/SKILL.md) first for session, project, claim,
recovery, and cross-repository policy. Read skills by their installed discovery
mechanism or open their SKILL.md; do not assume a host-specific Skill tool.
Executable help owns command syntax. New maps are ordinary lit issues, not local
Markdown substitutes. Historical bootstrap maps remain historical records.

Planning produces decisions. Moving to implementation needs the user's instruction;
existing execution authorization still applies within its stated scope. Stages can
be invoked independently. Stop at the destination rather than manufacturing a map
when the route is already clear.

Milestones are optional named project worksets for useful delivery boundaries.
Use them when a plan has distinct objectives worth selecting later; do not create
one for every map or make one mandatory for a small task. A milestone groups the
explicit issues selected for that boundary. Parentage does not add members, and
membership does not create blocker edges or authorize implementation. Selecting a
milestone narrows the planning candidates to its members; it does not authorize
every member for execution. Read the shared [milestone guidance](../lit/references/milestones.md)
and installed command help before using `lit milestones`.

## Chart

Use [clarify](../clarify/SKILL.md), including its modeling/documentation behavior,
to establish the destination and known constraints. A map is an issue labeled
`planner:map`, containing:

- **Destination:** what the planning must settle.
- **Notes:** constraints, relevant skills, shared specification and repositories.
- **Decisions so far:** one named link and one-line gist per resolved decision.
- **Not yet specified:** in-scope questions too unclear to phrase precisely yet.
- **Out of scope:** excluded work and why; link any corresponding closed ticket.

The map is an index. Full answers live in decision resolution comments, never as
competing copies on the map. Human-facing references use titles as link text with
stable identities in the targets; retain service/project context where needed.
Do not invent a browser URL when the tracker supplies none: a named Markdown link
to `lit:issue:<uuid>` is an identity reference, resolved with lit issues get,
not a promise that a browser supports that scheme.

Create precise questions as child issues labeled `planner:research`,
`planner:prototype`, `planner:clarify`, or `planner:task`. Create records before
wiring native blocking links. Parentage alone does not block. Research gathers
facts; prototype and clarify require the human's decision; task removes a specific
planning prerequisite within existing authorization. Challenge and consult are
techniques inside decisions, not extra mandatory ticket types.

When a useful delivery boundary is agreed, describe its objective and candidate
issue scope in the map and identify the corresponding milestone by title. Keep
unresolved human choices visible and continue resolving at most one per logical
session, even when several belong to the same milestone. Do not add a planning
issue to an implementation milestone unless the user explicitly wants it included.

Do not mechanically ticket vague areas. Graduate them when their question becomes
precise, even if currently blocked. Charting ends with the map and its frontier;
it does not silently decide a human ticket. Research can proceed independently
when useful and authorized, following [research](../research/SKILL.md).

## Work one decision

Resolve at most one human decision per logical working session. Research is exempt;
a new invocation, cwd, or repository does not reset that limit. Never supply the
human's side of a HITL exchange. An already explicit human answer counts as evidence
for that decision; don't ask for redundant approval. If the answer is missing,
record facts and the open question, then wait or hand off without closing it.

1. Establish the explicit session/project and load the map. If the user names a
   ticket, check its context and blockers. Otherwise query the open, unblocked,
   unclaimed children oldest first and select one within the confirmed map. State
   the selected title. Ambiguous project or map context needs clarification.
2. Acquire the selected ticket's claim atomically, then reread its current body,
   comments, blockers and relevant shared decisions. Assignment is not ownership.
   A lost race means re-query, never force. Renew explicitly before expiry during
   sustained work; after expiry reacquire and reread before proceeding.
3. Gather only needed context: grep remote current content, fetch matching records
   and comments, follow relevant relationships. Follow pagination; a search excerpt
   is not the full record or current permission to mutate. Read each affected
   repository's instructions and domain docs. Keep project repository identities
   and roles durable; machine checkout paths remain workflow-local.
4. Use clarify, research, or [prototype](../prototype/SKILL.md) as appropriate.
   Before consequential choices, use [challenge](../challenge/SKILL.md) locally.
   Record material objections, evidence, and disposition; distinguish facts from
   risks. Do not reopen settled requirements silently. External consultation needs
   user request/approval through [consult](../consult/SKILL.md); local challenge is
   not approval. Link consultation records and put relevant findings on the decision.
5. Record the answer as a resolution comment with evidence/artifact references,
   human acceptance where required, challenge disposition, and remaining limits.
   Close the resolved ticket (ending its claim), then update the map's named gist.
   Shared map edits may conflict: reread and preserve other sessions' entries.
6. Create newly exposed questions and wire dependencies; remove only the graduated
   portions of Not yet specified. Re-query the frontier. Report the next question,
   not a second human resolution. Scope exclusions get a reason comment and named
   Out of scope entry rather than deletion or archival.

Git artifacts and tracker writes are separate successes. If closing, indexing,
linking, or saving evidence fails, record the outstanding step and reconcile before
retrying an uncertain mutation. Do not claim the map is updated when only the ticket
closed. On interruption, leave a durable comment with evidence, context, and next
step; release ownership on an orderly stop using lit's recovery recipe. No tracker
outage fallback creates a second local decision authority.
