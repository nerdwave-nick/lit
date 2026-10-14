# Verification

Run the active Go checks from the repository root:

```sh
go test -count=1 -race ./...
go vet ./...
go test -count=1 -v ./integration -run '^TestGoDistributionWalkthrough$'
```

The integration harness uses temporary service data, private client state, and
working directories. The distribution walkthrough installs the public binaries
(`lit`, `lit-server`, `lit-tui`) with `go install`, checks that bare `lit` outside a
terminal keeps its usage error while `lit tui` hands over to `lit-tui`, extracts
embedded skills offline, and exercises sessions,
claims, checkpoints, cross-checkout resume, and closure against a temporary service.
It does not invoke paid models or start a persistent service.

The end-to-end benchmark in `integration/bench` is gated and never runs in CI;
plain `go test ./...` runs only its unit tests (percentiles, the pass/fail
checks, the report and the corpus generator). Before a milestone merges, run it
by hand on an otherwise idle machine and record the report on the milestone's
final ticket:

```sh
LIT_BENCH=1 LIT_BENCH_DIR=~/.cache/lit-bench go test -run TestBenchmark -timeout 3h -v ./integration/bench/
```

It builds `lit-server` without the race detector, starts it on a temporary data
directory and free port, seeds 100 projects of about 15 issues through the HTTP
API in transactions of 1-5 operations, then measures p50/p95/p99 per operation,
the database size and the startup time after a graceful stop (gated; the
clean-shutdown marker skips the full integrity scan) and after kill -9
(`startup.unclean`, informational; the full scan runs); it doubles the history with updates,
runs 8 writing agents and 2 readers (grep and ranked search), grows to 1,000 projects, and fails
on any missed target. `LIT_BENCH_DIR` should be on the filesystem lit-server
really uses (tmpfs makes fsync free). The package documentation lists the other
variables, and the report lists how each operation is measured.

Installer and workflow-session regression tests live in
`internal/workflow-skills` and `internal/workflowsession`. Protocol fixtures in
`internal/protocol/testdata` pin canonical request bytes and hashes.

`internal/hook` tests feed recorded host hook JSON to `hook.Run` and to the built
`lit hook` binary against a real service in a temporary directory.
`testdata/fixtures.json` lists every input with its provenance: the Claude
`startup` input was captured from Claude Code 2.1.296, the other Claude sources
are derived from the captured shape and the documented fields, and the Codex
inputs are built from the SessionStart schema embedded in codex-cli 0.162.1 (no
Codex capture was made; `testdata/codex/*.schema.json` hold that schema, and
`fixtures_test.go` checks every Codex input and the hook's own output against
it). The tests cover each source for both hosts, a server that does not answer, a
connect that outlives the budget (a stalled `/v1/connect` leaves the pending
association), duplicate and eight concurrent runs (exactly one `POST
/v1/connect`), a lock that is released during the wait and one that never is,
malformed input, an unreadable association, the log, its rotation and
`RecentFailures`, and that the agent's working directory stays untouched.
`internal/workflowsession/adapter_test.go` covers the adapter library API that
`lit hook` shares with `workflow-session`.

`internal/hook/compact_test.go` covers the `compact` source for both hosts against
a real service: claims present (each with the excerpt of its latest comment), none,
a lease that really ran out (a one-second lease, polled until the tracker drops it),
a claim the tracker still lists with an ended lease (a wrapper rewrites the listing),
long checkpoints and many claims (the context stays under 10000 UTF-16 units), an
unreachable server (no remembered claim may appear), claims or comments that cannot
be read, no association and a pending one. A request wrapper counts every request
that could change the tracker and the tests require none after the compaction.

The Stop guard tests (`stop_test.go`, `stopfixtures_test.go`, `skew_test.go`) use
the same service rig. The Stop inputs are not captured: the Claude ones are built
from the Stop section of the Claude Code hooks reference and the captured
startup input, the Codex ones from the `stop.command.input` schema embedded in
codex-cli 0.162.1 (`testdata/codex/stop.*.schema.json`, which also checks the
guard's block decision), with the provenance of each in `fixtures.json`. They
cover both hosts for a block while claims are held (every claim named, no token,
claims and issues unchanged afterwards), an allowed stop for `stop_hook_active`
(no tracker call), no claims, a released or expired claim, no session, a pending
or unreadable association, a server that is down, one that fails or stalls the
claims read past the budget, and input the guard cannot trust, with the failures
each one logs; and an event this lit does not know, through `hook.Run`, the CLI
and the built binary.

The `subagent-start` tests set up the root conversation's session and feed the
recorded SubagentStart input of each host. The Claude input is only partly
recorded: `session_id`, `agent_id` and `agent_type` were observed in the planning
prototype (Claude Code 2.1.296; its log kept those three fields), the rest follows
the documented example. codex-cli 0.162.1 does have a SubagentStart event; its
input is built from the embedded schema (`testdata/codex/subagent-start.*.schema.json`,
extracted verbatim) and, like the hook's output, validated against it. The tests
cover the child's own client without the parent's claims, explicit project
propagation (a later parent change does not move the child, a parent without a
project gives none), byte-identical repeats that need no server, eight concurrent
runs (one child client), a held lock, a parent without a ready session, a server that
is down, a stalled `/v1/connect` under the real one-second budget (the pending child
association is kept), a service that is not the parent's, and malformed input.

The `statusline` event (`internal/hook/statusline*_test.go`) is tested the same
way. Its two recorded inputs are in `fixtures.json` as well: `statusline-early`
was captured from Claude Code 2.1.296 (the interactive TUI in a pty with a
temporary `CLAUDE_CONFIG_DIR` whose `statusLine` command dumped stdin, a dummy
API key and no model call, so it is the input of a session that has not had a
response yet), `statusline-session` is the mid-session shape written from the
documented schema. Pure tests cover fish-style directory shortening, display
width (emoji, wide characters, escape sequences), `git status` parsing, the
built-in line at each of its eleven collapse levels and the choice of the
fullest level that fits. Service tests start a real `lit-server` and count the
requests that reach it, so they pin the cache: a fresh entry makes none, a stale
one reads the server again, the project title is read once per five minutes, a
stalled server costs one bounded attempt and its failure is cached. Further
cases cover a down server, a reachable server without a session, a pending
association, recent and old hook errors of both hosts, missing input fields,
real Git repositories (outside Git, dirty, ahead and behind, detached; skipped
without `git`) and `--original` as shell scripts: stdin passed through exactly,
`COLUMNS` handed down, a non-zero exit, no output, a missing command and a
timeout (the process group is killed too) leaving lit's part alone. A test runs
the built binary and logs how long a cold and a cached status line took.

`internal/checkoutcontext` tests run against temporary directories and real Git
repositories created with the `git` executable and no inherited configuration
(skipped where `git` is missing): every subdirectory of a repository resolving
to its worktree root, linked worktrees, symlink aliases, a nested repository, a
submodule-style `.git` file, `.git` lookalikes that must not count (an empty
directory, one without `refs`, junk or dangling `gitdir:` files), the exact
directory outside Git, and a moved checkout inheriting nothing. The store tests
cover the round trip, the exact set of stored fields (no client IDs or tokens),
rejected preferences writing nothing, every damaged and unsupported record shape
(each is reported by `Load`, refused by `Save`, byte-for-byte preserved, and
removed only by `Clear`), reads and clears of nothing creating no directory,
neighbouring workflow state left untouched, a failed write in a read-only
directory leaving the old record complete, and the state location following
`LIT_WORKFLOW_STATE_DIR` and the platform default. The concurrency tests run
writers and lock-free readers as goroutines and as re-executed test-binary
processes (no reader ever sees a partial record), hold the lock in another
process to see `ErrLocked` after the timeout and a waiting `Save` win after the
release, and kill the holder to see that the lock dies with its process.
`internal/cli` `TestContext*` tests drive `lit context show` and `clear` through
`Run` in all three formats from subdirectories, linked worktrees and symlink
aliases, with an ambient `LIT_SESSION` and `LIT_ENDPOINT` pointing at a service
that fails the test on any request, hashed copies of the private client state
and neighbouring workflow state proving they are untouched, the error mapping,
argument rejection, help and completion in real bash, zsh and fish.

`internal/storage` tests run against real SQLite databases in temporary
directories, without mocks: the owner lock, connection settings, `BEGIN
IMMEDIATE` and typed busy errors (a second connection holds the write lock),
the write-lock and read-snapshot API, schema migrations (refusing a newer
version, upgrading after a `VACUUM INTO` snapshot), schema constraints and
the monotonic clock. `TestModuleSelectsTheDriversLibc` fails when `go.mod`
selects a `modernc.org/libc` other than the one `modernc.org/sqlite` requires.
The record tests write through `WriteTx.WriteRequest` and cover every typed
read (records with computed membership lists, selectors, discovery-filter
lists and keyset pages, neighbours, milestone progress inputs, the structural
guard set and cycle queries), each rejected write and the late failure of a
multi-record request, and history rebuilt from stored diffs.
`TestHistoryMatchesTheLegacyHistoryAPI` replays requests recorded from the
legacy file store in `internal/storage/testdata/history_compat.json` and
compares the history output. The recording is fixed: the file store and the
generator that wrote it are gone. Opening a directory that holds a legacy file
store (its `service.json`) is refused before anything is written.
The integrity tests damage real databases on purpose: an invalid b-tree page
and a foreign-key violation fail the startup checks; an FTS5 table created in
the test, stale against its content table or with damaged index blocks, is
rebuilt, and one over a missing content table fails; a garbage file and an
unparsable schema (`writable_schema`, then a schema cookie bump so open
connections reload it) are `ErrFatal`. The `Verify` tests change rows and
history behind the history (fields, bodies, sets, links, members, revisions,
diffs, "before" values) and insert legacy membership-list history rows that
must be skipped. The clean-shutdown tests (`clean_test.go`) require
`CloseClean` to leave the marker that the next open consumes (`Open` and
`OpenExisting` alike), a plain `Close` and a fresh database to leave none, a
fatal error met while open to prevent it, and the marker to go stale when the
database is written from outside, replaced by a snapshot (renamed or copied
over in place), or the marker is deleted or damaged.

The grep index tests cover the migration to schema version 2 (a version 1
database with rows gets a filled `grep_text` and `grep_index` after its
pre-migration snapshot), writes keeping both in step, the
candidate scan (index `GLOB` from three folded characters, with literal `*`,
`?` and `[`; a column scan below; no match across fields; NUL), its query plan,
and the startup check rebuilding a stale `grep_index` and rewriting missing,
stale and orphaned `grep_text` rows. In the service, `TestGrepThroughTheIndexMatchesTheScan` runs the
search acceptance cases (`net/http`, `C++`, `Go`, `UI`, `strasse`/`Straße`,
`cafe`, `foo.bar`, `a_b`, `100%`, `APP-123`, and folding hazards such as
decomposed accents, `İ`, the Kelvin sign, NUL and GLOB's wildcards) folded and
case-sensitive, with context lines, several page sizes and both scopes, and
requires pages identical to a scan of every record with `searchExcerpt`;
`TestGrepThroughTheIndexMatchesTheScanForRandomText` does the same for random
text and queries over an alphabet of those hazards. `internal/textfold` checks
`Scalar` against `cases.Fold` for every scalar and that `Grep` keeps
substrings.

Regex grep (`internal/service/regex_test.go`) has a table test of anchors,
alternation, case, RE2's simple folding (`ss` is not `ß`, `ß` is `ẞ`), patterns
without literals, matches that would cross a newline, excerpts, paging and
invalid patterns (`validation_failed`), and a table of the literals extracted
per pattern. `TestRegexGrepPrefilterMatchesTheScan` and its random-pattern
twin require pages identical to a scan of every record without the
prefilter, over the literal grep world plus case-folding hazards: Cherokee and
the newest Latin letters, whose RE2 fold orbits lit's full fold maps to more
than one string, the Kelvin sign and the long s. `internal/storage` tests
`SearchScanAny` and its query plan (one index `GLOB` per literal);
`integration/regex_test.go` drives `lit grep -E` end to end.

The backup tests snapshot a tracker with history (`Backup`), restore the
snapshot into a new data directory and require `OpenExisting` plus `Verify`
and the startup checks to pass; a snapshot taken while a write transaction is
open holds only committed data; a cancelled backup, or one into a read-only
directory, leaves no file. Retention tests surround scheduled snapshots with
pre-migration snapshots, near-miss names, a directory and a symlink, which
must all survive. `cmd/lit-server` tests run the periodic job (failures and
panics are logged and the job carries on; stop cancels a run and waits), the
scheduled backups of a started service with retention and a restore through
`lit-server verify`, and their absence with interval 0 and in degraded mode.
`integration/backup_test.go` drives the binary with `LIT_BACKUP_*`, restores
the newest snapshot as `docs/recovery.md` describes and reads the tracker back.

`internal/export` tests write a small fixture tracker through the storage
module with fixed IDs and times, then export it. `TestExportMatchesTheGoldenTree`
compares the whole tree with `internal/export/testdata/golden` (regenerate with
`go test ./internal/export -run TestExportMatchesTheGoldenTree -update` and
review the diff). The other tests check that two exports give identical bytes
apart from the export time, that every link and front-matter path resolves
(goldmark parses each file; only the fixture's own author-written relative link
may point outside the tree), that the front-matter writer round-trips through a
YAML parser (a table of awkward strings plus a `testing/quick` property), that
a write committed during an export is not exported (one snapshot), that a
target that is not a previous export is refused, that a failed export leaves
the previous tree, and that a second export into the same directory is refused.
`storage.OpenReadOnly` is tested beside a running owner and without one.
`cmd/lit-server` tests run the interval export: a run exports after a change
and leaves the tree (manifest and directory inode) untouched when the newest
transaction is the manifest's `last_transaction`, an export of another tracker
is replaced, a started service exports at once and follows a later write,
failed runs are logged while the service keeps serving without a database
failure, and nothing is exported by default, with only a directory set, or in
degraded mode.

`cmd/lit-migrate` tests build legacy fixture stores with the frozen legacy
writer (`cmd/lit-migrate/internal/legacy`: the legacy preparer and
`recordTransaction`, claim operations with a controlled clock, client saves and
disconnect): multi-owner transactions, entries that change only membership
lists, links, a milestone, CRLF, Unicode and unterminated bodies, live, expired
and released claims, and connected and disconnected clients. The importer tests
run the full import with all nine checks, then compare claims, clients, records
and history with the legacy reader; they cover dry runs (source and target
untouched), a non-empty target, a store still owned by a legacy server,
failures that must leave no database (an unlisted file, a record tampered
behind its history, a claim time that is not canonical), a rerun after a
failure, and determinism. `cmd/lit-server`'s `TestLitServerDoesNotLinkTheLegacyStore`
keeps the frozen copy and `internal/store` out of lit-server.

`internal/service` tests run the service against real SQLite databases in
temporary directories. All-or-nothing behaviour is tested with real late
failures: a request whose response exceeds the configured limit fails after all
its writes, and the database, also after reopening, must be unchanged (multi-
record batches, links, disconnect with live and expired claims, closing an
issue that releases its claim, and every other mutation family); the same
request then succeeds. `TestShutdownDrainsAdmittedTransaction` holds a commit
open without a test hook: a second connection holds SQLite's write lock, so the
admitted request waits inside its `BEGIN IMMEDIATE` (the test waits until a
goroutine runs `storage.(*Writer).Begin`), `Stop` refuses new requests, and
releasing the lock lets the admitted request commit. Lock contention outlasting
the busy timeout answers `503 storage_busy`. `cmd/lit-server` tests start the service on
damaged and healthy data directories (degraded mode, search index rebuild),
check the startup log line of each path (a clean marker skips the scan, so a
foreign-key violation behind it is served; a missing one runs it) and run
`lit-server verify`, which leaves the marker only when the database verified;
`integration/integrity_test.go` drives the binary in degraded mode, its exit
on corruption met while serving (leaving no marker), and `verify` on a
tracker written through the CLI. `integration/clean_shutdown_test.go` starts
the binary, stops it gracefully and restarts it with the scan skipped, kills it
with kill -9 and restarts it with the scan run, and restores an identical copy
of the database, which is scanned.
`cmd/lit-server` healthcheck tests serve a started service and degraded mode
over loopback HTTP and run `lit-server healthcheck` against them, a closed port
and foreign answers; the listen address comes from flags, environment or config,
and the data directory is never touched. `packaging/compose_test.go` keeps
`compose.yaml` and the `Dockerfile` within the deployment rules (host network,
the default loopback listener, the restart policy, the named volume and bind
mounts, an export directory below its mount, the exec-form healthcheck, a static
non-root distroless image). Building and running the image is a manual check
with Docker, recorded on the compose ticket; the suite needs no Docker.

The session-free project context read has three layers.
`internal/service/project_context_test.go` sends requests with no client, an
unknown client and a connected client, and covers reference resolution against the
resolver the project commands use, missing and ambiguous targets, stored-binding
validation across a rename and a foreign service, malformed requests, an exact
key set (no project content leaks), unchanged durable state, and that every other
route still answers `client_required`. `internal/litclient/project_context_test.go`
counts the requests a fresh caller sends (one `GET /v1/project-context` each, no
connect, claim or transaction, no pending record) and covers a replacement
service, local argument rejection, service refusals, malformed and incompatible
answers, timeouts, an unreachable service and a cancelled context.
`integration/project_context_test.go` repeats the resolve, rename and
replacement-service cases against the real binaries and compares table row counts
and the client state directory before and after. There is no archived-project
case: the tracker has no archive state yet.

`internal/service/routes_test.go` checks the route table's classification of
every route. `internal/service/concurrency_test.go` and `readcap_test.go` run
reads concurrently with writes under the race detector. A request is held
inside its transaction without a production hook: the tests' `claimClock`
replacement is consulted once the request's snapshot or write transaction is
open, so parking it parks the request (a slow read does not delay writes; a
claim renewal racing a read stays correct for writers), and the second-
connection lock holds a write inside `BEGIN IMMEDIATE` while reconciliation
must wait for it. Holding read snapshots through `DB.Read` exhausts the read
cap.

`internal/service/search_limits_test.go` covers the search limits. A search
parked through `claimClock` until its deadline has passed answers `504
search_timeout` and frees its snapshot; a search waiting over the read cap past
its deadline still succeeds, because the deadline starts when its snapshot
opens; queries of 1,000 bytes pass and 1,001 bytes (also counted in multi-byte
characters) are `validation_failed`. `integration/search_test.go` runs the same
limits through the binaries with `LIT_SEARCH_TIMEOUT=1ns`.

Ranked search (`lit search`) is covered at each layer. `internal/storage`
`rank_index_test.go` checks schema version 3 (`rank_text` and `rank_index`),
writes keeping both in step, the migration of a version 2 database with rows,
the setup import (`Create` + `ImportRecords`) producing a complete index, and
the startup check rebuilding a stale `rank_index` and rewriting missing, stale
and orphaned `rank_text` rows. `rank_query_test.go` pins the search box parser
(phrases, exclusions, `OR`, prefixes, unbalanced quotes, `C++`, `net/http`,
FTS5 keywords and queries without words); `rank_search_test.go` checks
ranking order on a fixture (title over body over comments), grouping of
comments under their issue, projects and milestones as results, scope and
state filters, rank-order paging, diacritics (`cafe` finds `Café`), byte
offsets, text containing the highlight markers, and 300 random queries
without an FTS5 error. `internal/service/ranked_search_test.go` covers
snippets, the route's settled JSON shape, cursors, validation and the search
deadline; `integration/ranked_search_test.go` runs `lit search` through the
binaries.

`integration/tui_dispatch_test.go` covers how `lit` finds and starts `lit-tui`
(sibling first, then PATH, process replacement, endpoint pass-through, the missing
binary error, and bare `lit` on a pseudo-terminal via util-linux `script`).
`internal/tui/tmux_test.go` drives the detail viewport in a private tmux server:
the `internal/tui` test binary re-executes itself as a demo program showing a
Markdown file (no service is contacted). It covers resizing mid-scroll, per-block
horizontal scroll and the plain markers under `TERM=dumb` and `NO_COLOR`. These
tests skip when tmux is not installed or with `-short`.

`integration/tui_lifecycle_test.go` builds `lit-tui` and drives it in a private
tmux server against a temporary `lit-server`, with its own HOME, XDG directories,
`LIT_STATE_DIR` and `LIT_ENDPOINT`: quit, Ctrl-C, SIGHUP (the tmux session is
killed), SIGTERM, `kill -9` then relaunch (the orphan is disconnected), an orphan
with fabricated uncertain evidence (not disconnected), a second live instance
(never disconnected) and an uncertain exit (entry kept, second keypress). The
SIGHUP and SIGTERM runs delay the disconnect behind a proxy, so the process must
wait for it. `integration/tui_browse_test.go` drives browsing the same way: about 1,000
synthetic issues (40 transactions of 25, seeded by the non-race helper
`integration/testdata/seed`) load in one consistent bodiless read, and the test
logs the seeding and load times; a server configured with small read limits
shows the partial indicator; a deep branch shows its breadcrumb, and a narrow
terminal opens the detail and returns to the same selection; switching back to
a project keeps its selection. `integration/tui_detail_test.go` runs bare `lit`
in the tmux pane, expands a deep branch and opens an issue whose body and
comment try to clear the screen, retitle the window, write the clipboard (with
tmux `set-clipboard on`), leave the alternate screen and reorder text; it
checks the rendered body and comment, the visible markers, and with
`capture-pane -e` that the terminal is unchanged. `integration/tui_filter_test.go` searches and
filters the same way: a deep match keeps its ancestors, typed letters belong to
the search input, no match shows the empty state, filters combine with the
search, and one key clears them. `integration/tui_uncertain_test.go` opens the
uncertain-requests view with fabricated uncertain evidence: from an orphan's
launch panel and the action menu, and from its key and the exit report of the
instance itself, checking that the evidence stays byte-identical.
`integration/tui_claims_test.go` claims an issue, sees an agent's claim attempt
refused and an agent-held issue refused with its owner named, waits for the idle
question after a short `claim_idle`, keeps the claim (renewed at once) and checks
that quitting releases it. `integration/tui_edit_test.go` edits a body with
`nvim --clean` and `vim -u NONE -N` as `EDITOR` (subtests skip without them):
the claim is taken before the editor opens and renews while it is open (a
short `claim_lease`), an agent's claim attempt is refused, and the apply
commits at the recorded revision and releases the claim. With a short
`claim_idle` the claim lapses unanswered, and the later optimistic apply
conflicts on an agent's revision, reloads, and is refused by the agent's claim
with the draft kept. A detached edit is applied after another program saves
the file. `integration/tui_drafts_test.go` keeps drafts across exits with
`nvim --clean`: quitting with a draft shows the one prompt, releases the claim
and keeps the draft, and the next launch shows `1 unapplied draft`, marks the
issue an agent changed meanwhile and opens its draft from the Drafts view as a
conflict (story 8). Killing the tmux session while suspended in nvim still
disconnects cleanly (story 9). SIGTERM with nvim open releases the claim and
disconnects while nvim keeps running, then `lit-tui` exits cleanly after nvim
does; a second SIGTERM exits at once with status 143, reports the editor on
stderr and leaves nvim running (story 10). `internal/tui/drafts_view_test.go`
covers the banner, the Drafts view, marks, conflicts, the quit prompt and
drafts surviving a crash in the model, and `internal/tui/draftsview` tests the
listing, marks and prompt. `internal/tui/editing_test.go` covers the same flows in the model,
with the editor simulated, and `internal/tui/edit` and `internal/tui/drafts`
test the claim-on-edit flow and the draft store against a temporary service.
`integration/tui_comments_test.go` adds and edits comments with the same
editors: a new comment (`a`) is posted while an agent holds the issue's claim,
which stays the agent's; editing a comment (`o`, then the chooser) claims the
issue before the editor opens, refuses the agent's claim attempt, applies at
the comment's revision and releases the claim; and after the claim is released
and the agent edits the comment, the preview is a revision conflict that keeps
the draft byte-exact and diffs it against the current comment.
`internal/tui/comments_ui_test.go` covers these flows in the model, and
`internal/tui/comments` tests the comment kinds (claim-exempt appends created
at most once under the draft's ID; claim-on-edit at the comment's revision;
shared edit claims) against a temporary service. `internal/tui/drafts`
also tests the draft kinds (new records under generated draft IDs, kinds
without an owning issue), and `internal/tui/edit` a registered kind without
an owner (a project specification) through the generic editor.
`integration/tui_projects_test.go` administers projects: in the switcher an
invalid title is refused under the field with the service's prefixes before
anything is previewed, a valid one previews and creates the project, which
lit-tui then browses and lists; the specification is edited with nvim and
vim through a `project_spec` draft (no claim, applied at the recorded
revision); the repository list is changed through an exact preview, and a
change of the project after the preview refuses it; and an agent's change
after the specification's preview refuses the apply, keeps the draft
byte-exact and opens it as a conflict with a diff.
`internal/tui/project_admin_test.go` covers these flows in the model, and
`internal/tui/projectadmin` checks that the local title check accepts
exactly the titles lit-server's preview accepts (listed and random titles),
the forms' exact previews and revision fences, and the specification kind.
`integration/tui_create_test.go` creates issues with `nvim --clean` and
`vim -u NONE -N`: `N` asks for the title inline (`q` is text), opens the
editor on an empty draft, previews the creation and applies it, and the new
issue is selected; `C` then creates a child of it, whose preview lists the
parent. A child of an issue an agent claims is refused before anything is
written, naming the owner; quitting keeps its draft, and the next launch
lists it in the Drafts view, where it creates the child once the claim is
gone. `internal/tui/creating_test.go` covers these flows in the model
(including a title taken meanwhile and changed from the panel), and
`internal/tui/create` tests the issue_new kind against a temporary service
(the draft's ID as the issue's, created at most once; the parent fenced and
its foreign claim named; own claims passed on; blank bodies) and the title
prompt.
`integration/tui_history_test.go` opens the history
view of an issue edited a few times (title, a body with an escape sequence, a
label, the state) and of a comment edited by another client, from the tree and
from the detail's chooser: it checks the actors, field changes, the body diff
and its visible markers, that Escape returns to the same selection, and that
nothing was written.
`integration/tui_milestones_test.go` manages milestones with `nvim --clean`:
it creates a milestone in the Milestones view, adds the selected issue with
`W` and removes it again (its child never follows, no blocker appears),
edits the description in nvim (no claim, applied at the milestone's
revision), and keeps the draft byte-exact when the agent changed the
description meanwhile (a revision conflict, diffed against the current
description). `internal/tui/milestones_ui_test.go` covers these flows in the
model, `internal/tui/milestones` tests the description kind, the load with
progress and the view, `internal/tui/quick` the membership, creation and
rename actions (fenced at the milestone's revision), and `internal/litclient`
the progress read.
`integration/tui_quick_test.go` is user story 7:
adding a blocker previews the other issue, an agent's claim on it refuses the
action with `claim_conflict` naming the owner while nothing is written (no
commit, no pending evidence, no revision change), and after the claim is
released the same action applies its preview. These
scenarios skip without tmux or with `-short`. The lifecycle,
registry, recovery, claims and quick-action packages are tested in-process
against a temporary service (`internal/tui/tuitest`); `internal/tui/quick`
covers every quick action's preview and exact commit, the affected-issue list,
claim refusals at preview and at commit, revision fences and own claim tokens
on the wire. The claims tests use leases of about a second;
one renews a claim across several leases while the program's event loop is
blocked inside Update.

`internal/textdiff` tests the line diff shared by lit-tui and lit-server:
byte-exact diffs and strict `Apply` for CRLF, lone CR, a missing final newline,
empty bodies, Unicode and invalid UTF-8; refusals of another base and of
malformed diffs; the property `Apply(Diff(a, b), a) == b` (`testing/quick` and a
fuzz target whose seeds `go test` runs); and the LCS cell bound against 64 KB
bodies. `go test -run '^$' -bench Diff64KB ./internal/textdiff` measures it.

`cmd/lit/deps_test.go` uses `go list -deps` to prove that only `lit-tui` links the
Charm, Glamour and chroma packages.

Keep generated logs, agent transcripts, and session snapshots out of version
control. These checks do not establish native agent behavior or macOS/Windows
runtime compatibility.
