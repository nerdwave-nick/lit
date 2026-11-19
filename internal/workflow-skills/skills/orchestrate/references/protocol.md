# Coordination protocol

## Scope and run record

The authorized scope is an explicit issue set, a confirmed project, or a named
milestone in a confirmed project. Do not silently add tickets discovered through
links, labels, repository names, or a matching project. Read the project and
every candidate issue, its comments, blockers, and claim. Resolve missing
acceptance criteria before dispatch. For a project-wide request, snapshot all
authorized implementation tickets, including currently blocked tickets; exclude
planning, human-decision, and unrelated issues with a recorded reason. The
dispatch frontier is only the currently open, unblocked, unclaimed tickets in
that frozen set. Determine which issues are implementation work from the
project's explicit conventions; ask about ambiguous issues instead of guessing.
Recompute availability from the frozen set after each closure;
new project issues are not implicitly added. State the selected set and
exclusions to the user before starting if the request did not already name the
boundary. Never use force to resolve ownership conflicts.

For a milestone, first confirm that the installed `lit --help` exposes the
milestone commands. Resolve its stable UUID in the selected project with
`lit milestones get`, then freeze the service ID, project ID, milestone UUID,
revision, and sorted member issue UUIDs in the ledger. The requested milestone
and its objective scope only approved eligible implementation issues; do not
dispatch planning or human-decision issues automatically. An empty membership
is an empty run, not a successful goal. For members already closed, require
available verification evidence before counting them toward the outcome.
If the installed CLI lacks milestone support, report the version mismatch; do
not approximate milestones with labels or invented commands.

Bind the coordinator runtime to its own fresh `lit` client and explicitly select
the confirmed service and project. Give each native worker a unique runtime ID,
client, branch, and worktree. Do not share client identity, claim credentials,
or a worker's live session. Consult the current host's native-agent interface
for available model and effort choices; do not assume an API or identifier that
the host does not expose.

Persist a Markdown ledger with `schema_version: 1` front matter under the
workflow state directory (`LIT_WORKFLOW_STATE_DIR`, otherwise the host's
documented workflow default), for example
`orchestrations/<service-id>/<project-id>/<run-id>.md`. Record the authorizing
scope, service and project IDs, coordinator runtime/client IDs, integration
branch/worktree, and creation time. For each ticket record its current state,
canonical assigned worker runtime/client, branch/worktree, base and latest full
commit hashes, selected profile, claim observations, check results, and pending
handoff. Update it after each transition. Keep full claim tokens out of prose.
When a parent issue is in scope, add a concise tracker comment linking the run
record and scope; do not assume a local file is visible to other agents.

If native goal tracking exists, inspect current goals first. Reuse a matching
goal for this exact run; if an unrelated or ambiguous goal could conflict,
report it rather than overwriting or replacing it. If the host cannot establish
whether another goal conflicts, report that limitation instead of creating a
duplicate. Otherwise create one goal for the frozen authorized outcome and
mirror ticket progress using only supported goal features. Never invent a
token budget, pause state, blocked state, or lifecycle control. Mark the goal
complete only when the full integration gate below is met. The ledger and
tracker remain the recovery evidence.

## Assign and dispatch

Re-read current tracker state and recompute the open, unblocked frontier from
the issue relationships before each dispatch wave and after every closure. An
assignment is not a claim. Confirm no live competing claim, then assign one
ticket to one native worker with an isolated worktree based on the current
integration head. A worker may implement only its ticket and must use the
coordinated mode in `$impl`.

Resolve each worker's profile in this order: exact user choice; user-local
override; repository convention in `docs/agents/orchestrate.md`. The user-local file is
`${XDG_CONFIG_HOME:-$HOME/.config}/lit/orchestrate.yaml` (or
`%APPDATA%\lit\orchestrate.yaml` on Windows). Read it only; do not create or
edit it. A profile names exact model and, optionally, effort identifiers; for
example:

```yaml
profiles:
  worker:
    codex:
      model: "<exact supported model id>"
      effort: "<exact supported effort>"
    claude:
      model: "<exact supported model id>"
      effort: "<exact supported effort>"
```

Check each configured value against the host's supported native-agent choices
before dispatch. If no profile is configured for this run, or a requested value
is unavailable, present the actual supported choices and ask once for the run's
worker profile. If the host does not expose its choices, say that they could not
be verified and ask the user to provide a supported profile; do not invent a
list. Do not silently use the host default, substitute a model, lower effort,
or use a paid/external CLI as fallback.

Parallelize only independent tickets whose branches and worktrees are isolated.
Dispatch dependents only after their blockers are closed and the integration
head contains the prerequisite interfaces. Give each worker its ticket body,
relevant shared decisions, exact checkout/branch/base, profile, checks, and
handoff requirements. Record each dispatch in the ledger and, where supported,
the ticket's tracker discussion.

## Worker handoff and recovery

The worker owns its claim. On completion or an orderly stop, it posts exact
repository, branch, full commit hashes, check commands/results, remaining work,
and its handoff state; then it releases its own claim and stops. Coordinated
workers never close tickets, integrate, push, or begin another ticket. The
coordinator rereads the issue and claim after the worker stops, then acquires
the claim before integration. A competing claim is a conflict: report it and
wait for an authorized resolution, never force takeover.

If a worker exits without releasing, do not impersonate it or use its session.
An expired claim alone does not prove the worker stopped. Establish through
actual host status or an explicit handoff that the worker can no longer edit;
if that cannot be established, do not redispatch or take over its checkout.
After confirmed exit, wait for claim expiry, then reacquire and reread before
proceeding. If it may still be running, wait or use only a host-supported
interrupt and verify the resulting state. For uncertain tracker
mutations, inspect `transactions status` with the original session before any
new mutation. A tracker outage stops new dispatch and preserves worktrees and
the ledger for recovery.

On coordinator resume, reestablish the recorded runtime/client/service, inspect
uncertain requests read-only, reread every ticket and claim, verify worktree
identity and every cited commit, then recompute the frontier. Never redispatch
a ticket with a live claim. A ledger is evidence of prior observations, not
current ownership or permission.

## Review, integrate, and close

Review the worker's diff against its acceptance criteria and rerun required
checks in its worktree as needed. If code or checks need correction, first
record the failed integration result and release the coordinator's claim. Then
hand the finding back explicitly; the worker rereads and reacquires the claim
under its own client before editing. If its commits were already integrated,
continue with new fix-forward commits on the updated integration head so cited
commits remain unchanged. After the worker commits and releases, the coordinator
reacquires and rereads before continuing. Never overlap claims or drive the
worker's session. Continue until the work passes or a user decision, unavailable capability,
or external constraint prevents progress; report that constraint without an
arbitrary automatic fix-round cap.

Integrate only one reviewed worker branch at a time into the dedicated
integration branch. Preserve worker commits and every cited hash; do not amend,
rebase, squash, or rewrite them. Run the full required checks at the resulting
integration head. If integration checks fail, return the concrete failure to
the worker and repeat review and integration. After each successful integration,
record the integration commit and evidence, post a completion comment, and close
the ticket as coordinator while owning its claim. Verify closed state and ended
claim before marking it closed in the ledger. On any failure, leave it open.

After each closure, recompute the frontier from current tracker state and the
new integration head. For a milestone, query its current members and compare
the UUID set and revision with the frozen record before dispatch. The supported
frontier query is `lit issues list --session NAME --project P --milestone M1
--state open --blocked false --claimed false`; run it directly or through the
coordinator's host adapter without `--session`. Inspect claims and requirements
for each result, and intersect with the frozen member IDs and approved
implementation scope. Never dispatch a newly added member. A membership change pauses new
dispatch: report the current and frozen sets and ask the user how to proceed.
Do not silently expand or shrink scope or undo integrated work. Stop or hold
work on an affected member whose authorization changed; independently authorized
work can continue when safe. A title/body-only revision change is not membership
drift: reread the objective and affected requirements, then ask if the change
alters the authorized scope. External blockers remain blockers, but do not
prevent independent in-scope members from progressing; never implement those
external issues. A milestone has no close/state mutation: goal completion comes
from integrated ticket evidence.

When the authorized set is complete, verify every ticket is closed,
every worker branch is integrated, and final checks pass at the final head;
for a milestone, reread it once more at this final gate and compare its current
membership and objective revision with the frozen run record. Membership drift
requires pausing completion and getting the user's direction; never mark the
run or native goal complete against a changed member set. A title/body-only
revision change requires rereading the objective and deciding whether it alters
the authorized outcome before completion. Only after this final scope check,
store a concise summary in the parent discussion and ledger. Report remaining
limitations plainly.
