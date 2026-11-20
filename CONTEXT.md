# Coding agent workflows

The vocabulary for my coding agent workflows and their supporting tools.

## Language

**lit-server**:
The issue tracker service shared by one user's projects and coding agents,
initially hosted locally and designed to support a remote host later.

**lit**:
The command-line client through which people and coding agents interact with lit-server.
_Avoid_: issue-tracker (when referring to this client)

**Client ID**:
The identity issued by lit-server for a logical working session, reused across its CLI
invocations and associated with its claims, editing windows, and retained state.
Vendor session identifiers describe the runtime rather than tracker ownership.

**Client draft**:
Unapplied work a client stashes with lit-server for later use. Keeping a draft does not
reserve records or grant permission to apply its changes.

**Service ID**:
The persistent identity of a tracker, independent of the network address used
to reach it. Saved plans identify both the service and their logical client.

**Project**:
A feature or other effort, represented by a dedicated container of related
issues. Projects can span multiple repositories, and a repository can participate
in multiple projects; a project is not itself an issue.
_Avoid_: repository, checkout, workspace (as synonyms for project)

**Project title**:
The human-readable, branch-like display title used to refer to a project,
unique without regard to case across active and archived projects. There is no
separate project lookup name or slug. Titles begin with an allowed prefix and
`/`, have at most 128 characters, and consist of slash-separated segments of
ASCII letters, digits, hyphens, and underscores, each beginning with a letter
or digit.

**Issue title**:
The human-readable display title used to refer to an issue, unique without
regard to case within its project, including archived issues. Normal prose
titles are allowed; project-prefix and branch-like syntax restrictions do not
apply. There is no separate slug.

Issue titles are nonempty single-line text of at most 128 characters, allowing
Unicode, spaces, and punctuation but no leading/trailing whitespace or control
characters.

**Issue**:
A unit of tracked work or discussion owned by exactly one project. Issues may
have one parent within that project and may link to or block issues in other
projects.

**Milestone**:
An optional named workset of issues within one project, used to identify an
explicit delivery or orchestration scope. Membership is explicit and may overlap
with other milestones; it does not follow parentage or imply a blocker. A milestone
has no lifecycle state, and its progress is a read-only projection of its members.

**Shared specification**:
The agreed behavior that applies across an effort's implementation tickets.
Individual tickets describe their slices without silently overriding it.

**Implementation ticket**:
An issue defining a verifiable slice of implementation work and its acceptance
criteria, potentially spanning several repositories.

**Work handoff**:
A record of an unfinished ticket's context, repository states, verification,
and remaining work that lets another session continue it.

**Parent issue**:
An issue that groups child issues within the same project. Parentage alone
implies neither a blocking relationship nor shared completion.

**Blocker**:
An issue whose open state prevents a dependent issue from being available through
the workflow frontier. Closing it satisfies that dependency; reopening it restores
the block.

**Related issues**:
Issues connected for context through a symmetric link, without implying parentage
or a blocking dependency.

**Object revision**:
A version of an object's durable content and properties at a particular point
in its history, numbered monotonically from one. Restoring earlier content
produces a new revision rather than rewinding that number.

**Mutation log**:
The chronological history of changes to an object, retaining differences between
its previous and resulting revisions.

**History entry**:
An immutable record of one mutation of its owning object, identifying when and
by whom the change was made and the differences between its pre/post states.

**Request hash**:
The identity of an intended atomic durable change, derived from its operation,
targets, expected revisions, and requested changes. The same identity appears in
each affected object's history, allowing clients to establish whether it committed.

**Transaction**:
A set of compatible requested changes applied together, or rejected together
when any required condition fails. Repeated targets can contribute compatible
changes without making argument order significant.

**Degraded mode**:
lit-server running but answering every request with `503 recovery_required`
because its database could not be opened or failed the full integrity scan.
Only an operator leaves it, by restoring a backup or repairing the database.

**Clean shutdown**:
A graceful lit-server stop after which the database closed without a failure,
recorded in the data directory's clean-shutdown marker. The next start skips
the full integrity scan, which otherwise runs after every unclean shutdown
(crash, kill, database failure, degraded mode) and in `lit-server verify`.

**Backup snapshot**:
A complete copy of lit-server's database that lit-server writes itself with
`VACUUM INTO`: scheduled ones at an interval, of which the newest few are kept,
and one before each schema migration. Restoring one loses everything committed
after it was taken.

**Setup import**:
The one-time conversion of a stopped legacy file store into a new, empty data
directory by the separate `lit-migrate` binary, before the SQLite-backed
lit-server first starts. lit-server itself never reads or converts a legacy
store; it refuses a data directory that still holds one.

**Saved plan**:
A prepared durable transaction retained for explicit later application, bound
to its tracker and logical client and to the original changes and preconditions.
Preparing a plan reserves no work.

**Pending request**:
A locally retained outgoing mutation whose outcome may need reconciliation
after interruption. Keeping it neither reserves work nor authorizes replay.

**Archived project**:
A project retained for reference and excluded from default discovery, whose
records require restoration of the project before further editing.

**Archived issue**:
An issue retained with its subtree and comments that requires restoration before
editing. Archival preserves open/closed state and does not satisfy a blocking
dependency.

**Archived comment**:
A comment retained through reversible archival instead of deletion, requiring
restoration before editing.

**Lease helper**:
A local companion to coding agents that observes registered sessions and renews
their eligible work claims through lit-server.

**lit hook**:
The shared command, `lit hook --host claude|codex EVENT`, that agent hosts call
at session events. It reads the host's hook JSON, acts on the runtime's tracker
session, and hands the agent its details only through the hook output. Once an
event is being served it exits successfully whatever happens and writes failures
to the hook log. Distinct from the lease helper, which renews claims.

**Stop guard**:
The `stop` event of lit hook. When an agent tries to end its turn while its
session holds live claims, it blocks the stop once with a reminder to record a
checkpoint or handoff and release, or to continue; the host's `stop_hook_active`
makes the next attempt pass. It only reads: it never releases, renews or closes
a claim, and any doubt allows the stop.

**Agent configuration**:
A host's configuration directory (for example `~/.claude`, `~/.claude-work` or a
Codex home) into which lit installs skills, instructions, hooks and the status
line. Two configurations are two independent installs.
_Avoid_: profile (settings are a file inside a configuration)

**Status line**:
Claude Code's one-line bar, served by `lit hook --host claude statusline`: an
original status line (a built-in default, or the user's own command wrapped with
`--original`) followed by lit's part: project, claim count and workflow stage, or
`not lit` when the server does not answer. It reads the server's state from a
short-lived cache so that it answers within about 300 ms.
_Avoid_: status bar, statusline (the event and flag keep that spelling)

**Assignment**:
The durable record of responsibility for an issue, independent of permission to
work under an active claim.

**Work claim**:
An exclusive, temporary reservation of one issue owned by a logical client session.
An issue has at most one active claim; a run may hold claims on multiple issues.

**Lease**:
The renewable, time-limited validity of a work claim. Expiry ends ownership
without removing durable assignment.

**Editing window**:
A human's reservation of selected records for coordinated edits to temporary
copies. Accepted records remain readable while competing changes are excluded;
finishing the window validates and applies the edits together.

**workflow skills**:
The collection of skills gathered from other sources and adapted to my coding
workflows and preferences.

**Workflow stage**:
The outer workflow skill (planner, clarify, research, prototype, to-spec,
to-tickets, triage, impl or orchestrate) that a logical client session is
currently running, stored with the client by lit-server so the status line, the
TUI and other agents can see what the session is doing. It is a display label
set and cleared with `lit session set|unset --stage`; changing it is a session
update, never issue history. Values are checked for shape only, not against the
standard names, and a session starts without one.
_Avoid_: phase, mode

**Checkout**:
The place on one machine a remembered project belongs to: the Git worktree root
containing the current directory (a linked worktree or nested repository is its
own checkout), or, outside Git, the exact current directory. Symlink aliases
resolve to the same physical checkout, and a moved checkout is a different one.

**Remembered project**:
A user's explicit project choice for one checkout, kept as a machine-local
preference holding the project ID and display title, the service ID and the
endpoint. It carries project context only, never a client ID, claim token or
session name, and is not validated until a command or new session checks it
against the service. A fresh agent session adopts a valid one unless a project is
named explicitly, and records which it did (its project selection); subagents
never adopt it. The session-start hook asks `fresh` to adopt it and tells the agent
whether it did; a preference that cannot be adopted creates no session, and the hook
explains it (a warning in the hook log, not a failure). The installed lit skill teaches
agents the same workflow with or without hooks: announce a validated adoption, ask the
user to choose when nothing is remembered, and never infer a project from a directory,
branch or checkout name.
_Avoid_: default project, active project
