# Milestones

A milestone is an optional, named workset of issue IDs in one project. It provides
a durable scope that a user can select for planning or orchestration. Milestones
can overlap, but membership is explicit: parent issues do not bring their children
in automatically, and grouping does not create blocker relationships. A milestone
has no close or archive lifecycle. Progress is a read-only view of its members;
an empty milestone is an empty scope, not a completed effort.

Use the host adapter's `run --` or include the explicit logical session on each
direct call. Read the confirmed project's milestones with
`lit milestones list --session NAME --project P`
and inspect one with `lit milestones get --session NAME --project P M1`.
For current flags and output forms, read `lit milestones --help` and the relevant
subcommand help.
Milestone titles resolve within the selected project; verify the returned UUID and
use that full UUID for later reads or writes. Include `--project P` on each
title-based command unless the logical session's project selection is confirmed.
Membership references resolve in that project's scope, and members must belong to
it. Issues may still depend on blockers in other projects; those blockers remain
external dependencies and cannot be milestone members.

Create a named workset with
`lit milestones create --session NAME --project P --milestone M1`
and add members with
`lit milestones update --session NAME --project P MILESTONE_UUID --add-issue ISSUE_ID`.
Use `--remove-issue` or `--clear issues` only for the explicitly approved membership
change. Milestone mutations use the normal revision-guarded transaction path. For
a multi-record publication, preserve existing members and unrelated records,
verify each mutation's returned record type and stable ID, then read back the
milestone membership and issue graph. If a response leaves a mutation outcome
uncertain, reconcile the retained request before retrying it.
