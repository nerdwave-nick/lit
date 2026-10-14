# Remembered project for this checkout

A user can remember one project for a checkout: the Git worktree root that
contains the current directory (every subdirectory shares it; linked worktrees and
nested repositories are separate checkouts), or the exact directory outside Git.
It is the user's explicit choice, kept on this machine together with the service
ID. It is not a guess from a directory name, branch, timestamp or recent agent,
and it holds no client, claim, issue or handoff. Installed command help owns the
syntax:

```sh
lit context set --project feat/tracker
lit context show
lit context show --validate
lit context clear
```

- `set` remembers a project only when the user asks you to remember it. Reading a
  project, `session set --project`, or `--project` on another command never does.
  It replaces an earlier preference: tell the user what it replaced.
- `show` is offline and marks the answer not validated. `show --validate` also
  checks the service identity and the project with one read-only request and
  exits nonzero when the service is another one, does not answer, or no longer
  has the project. Neither form changes the preference. Use them when the user
  asks what is remembered or whether it still holds.
- `clear` forgets this checkout's preference only. It works while the server is
  down and never deletes a project, ends a session, releases a claim or removes
  recovery evidence.

## Starting a new conversation

A new conversation is a new runtime with a client of its own; the preference
never contains one. Tracker commands such as `session set` go through the adapter,
`lit workflow-session --host HOST --runtime-id ID run -- ...`, as described in
[host setup](hosts.md). Decide the project in this order.

1. An explicit project from the user for this conversation wins. Without a
   session yet, start it with `fresh --project REF`: the remembered project is then
   neither read nor changed, even when it is stale. When a hook already created the
   session, switch it with `session set --project REF`. Both leave the remembered
   project as it was.
2. Otherwise a new session adopts the remembered project only after validating it
   (the check `show --validate` makes). Do not add `--endpoint` yourself: the
   order is `--endpoint`, `LIT_ENDPOINT`, the endpoint saved with the preference,
   then the default. A different address that reaches the same service is
   accepted; another service is not.
3. With nothing remembered the session has no project: choose one with the user.

How the session was created depends on the setup; read the facts from that
setup instead of asking again.

- With hooks installed (`setup-workflows --scope user` or `--config-dir`), the
  session-start hook has already run `fresh` for this runtime. Read the context it
  injected: `session:`, `endpoint:`, `runtime ID:`, `project:` and a
  `remembered project:` line saying that the project was remembered for this
  checkout and adopted, or that none is remembered. Use that session and do not run
  `fresh` again: a second `fresh` for the same runtime is refused. If the context
  says instead that lit is unavailable or that setup did not finish, follow the
  exact command it gives (`fresh` once the server answers, or `reconcile`).
- With skills only (`--scope local`), or when no hook context is present (hooks
  not installed, Codex hooks not yet approved in `/hooks`, or the hook said lit is
  unavailable), run `lit workflow-session --host HOST --runtime-id ID fresh` yourself
  from the repository, without `--project` unless the user named one, and read
  `project_selection` in the JSON it prints.

`project_selection.source` is `remembered` (the validated preference was adopted:
`project_title` is its current title, `saved_title` appears only when the project
was renamed since it was remembered, `checkout` names the checkout), `explicit`
(the user's `--project`; the preference was not read), or `none` (no project
selected; `checkout` names the checkout consulted when nothing is remembered).

Announce a remembered adoption briefly before using the project: which project was
selected, that it was remembered for this checkout (and renamed since, when
`saved_title` says so), and that the user can name another one for this
conversation. Say nothing more for `explicit`; for `none`, go on to choose with
the user.

## Nothing remembered: choose with the user

Discover candidates with the repository's conventions
(`docs/agents/issue-tracker.md`) and current tracker queries such as
`lit projects list`, following every page. Show readable choices (title and what
each covers) and ask which one. Never choose from directory or branch names,
timestamps, the most recent agent, or a single search result unless the user
explicitly authorizes that rule. Select the answer with `session set --project REF`.
Run `lit context set --project REF` only if the user asks to remember it.

## A remembered project that cannot be used

When the preference is stale, damaged or unsupported, belongs to another service
than the one answering, cannot be reached, or names a project that is gone
(archived projects will be refused the same way once lit can report them),
`fresh` exits 1 before any client, association or pending request exists. The
hook likewise creates no session: its context begins `lit session not set up`
and gives the same two ways out. Tell the user why in plain words and let them
choose:

- forget it: run `lit context clear` from inside the checkout, then run `fresh`
  again (the session has no project; choose one as above), or
- use another project for this conversation only: `fresh --project REF`, which
  leaves the remembered project as it is.

Never substitute another project, rewrite the binding, restore an archived project,
create a project, or fall back to another endpoint or service. An explicit
`--project` does not require repairing or erasing the preference first. Run no
tracker commands until a session exists.

## What stays as it was

- Resuming the same runtime keeps its association. A later `set` or `clear` never
  redirects an active conversation; change its project with `session set --project`.
- Subagents never adopt. Their project scope is the explicit scope their
  coordinator gives them (`workflow-session subagent --parent-runtime-id PARENT
  --project REF`; the subagent-start hook passes the parent's selected project),
  and a worker checkout's own preference never overrides it.
- A remembered project is not a remembered issue. The project does not identify an
  issue: read the issue or handoff the user names, otherwise use the
  [work selection](work.md) workflow, and check the live records, current state,
  ownership and checkout evidence before implementing. Never pick the latest task.
- A new session owns nothing and inherits no claims. Adoption does not take a
  claim, renews nothing and takes nothing over; another client's work needs its
  handoff or the recovery rules, never force. Requests left uncertain by interrupted
  work still need `workflow-session reconcile` and the supported status checks
  before anything is replayed.
