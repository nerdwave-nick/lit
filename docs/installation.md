# Building and installing lit

The service supports Linux amd64/arm64 and macOS amd64/arm64 with local filesystem
storage. The clients, `lit` and the terminal UI `lit-tui`, support Linux amd64/arm64,
macOS amd64/arm64, and Windows amd64. Linux and macOS behavior
is tested; cross-compilation does not demonstrate native Windows behavior.
Python is not required to build, install, or run the binaries and workflow skills.

## Standard Go build and install

Install Go matching `go.mod` (1.27 or later). From the repository root:

```sh
go build -o ./bin/ ./cmd/lit-server ./cmd/lit ./cmd/lit-tui  # Linux, macOS: create all three executables
go install ./cmd/lit-server ./cmd/lit ./cmd/lit-tui         # Linux, macOS: install checkout versions
```

On Windows, build/install only `./cmd/lit` and `./cmd/lit-tui`. Install
`lit-tui` into the same directory as `lit` (as `go install` does); see
[Terminal UI](#terminal-ui). Separate commands such as `go build ./cmd/lit` also
work and leave the executable in the current directory.
Installation uses `GOBIN`, or `GOPATH/bin` when unset (normally `$HOME/go/bin`).
Check `go env GOBIN GOPATH` and add the appropriate directory to PATH yourself;
installation does not edit your shell profile.

Once this module has been published and is accessible, installation without a
checkout uses an explicit version query:

```sh
go install github.com/nerdwave-nick/lit/cmd/lit@latest
go install github.com/nerdwave-nick/lit/cmd/lit-tui@latest
# Linux or macOS service, if needed:
go install github.com/nerdwave-nick/lit/cmd/lit-server@latest
```

Replace `latest` with a published tag to pin a version. Remote publication and
installation availability have not been verified; these commands describe the
supported Go interface, not an already published release.

## Terminal UI

`lit-tui` is the terminal UI for people. It is a separate executable so that only
it links the Charm terminal and Markdown libraries; `lit` and `lit-server` stay
small and quick to start. Start it with `lit tui`, or with bare `lit` when both
stdin and stdout are terminals. `lit` looks for `lit-tui` in the directory of its
own executable first, then on `PATH`, and passes `--endpoint` through. On Unix,
`lit` is replaced by `lit-tui` (process replacement), so signals reach the TUI
directly; on Windows, `lit` runs it as a child process on the same console and
waits for it. `lit-tui` can also be run directly; its endpoint is `--endpoint`,
then `LIT_ENDPOINT`, then `http://127.0.0.1:7411`.

Theme, mouse and keymap preferences live in
`${XDG_CONFIG_HOME:-$HOME/.config}/lit/tui.json`, with `LIT_TUI_*` environment
variables and `lit-tui` flags taking precedence. `lit tui` passes only
`--endpoint` through. See [docs/tui.md](tui.md) for the file format
and the default keymap.

Outside a terminal, bare `lit` is unchanged: it exits with status 2 and the
`invalid_arguments` error `a subcommand is required for lit`, so scripts and
agents are unaffected. `lit tui` itself always hands over to `lit-tui`, which
refuses to start without a terminal (status 2). If `lit-tui` is not installed,
`lit tui` fails with the error code `tui_not_found`, naming where it looked and
the install command:

```sh
go install github.com/nerdwave-nick/lit/cmd/lit-tui@latest
```

## Shell completion

`lit` prints completion scripts for bash (4.4 or later), zsh (5.8 or later) and
fish (3.4 or later); bash needs no bash-completion package. Load one into the
current shell with `source <(lit completion bash)`, `source <(lit completion zsh)`
after `compinit`, or `lit completion fish | source`. `lit completion SHELL --help`
explains loading it in every new shell. Other shells, including the Windows
client's, have no completion script. See [CLI help](cli-help.md#shell-completion)
for what completes.

## Install embedded workflow skills

The installed CLI contains the sixteen workflow skills and their instruction
template. Setup is offline and needs no source checkout or tracker connection:

```sh
lit setup-workflows --scope local --agent both
lit setup-workflows --scope user --agent codex
lit setup-workflows --agent claude --config-dir ~/.claude-work
```

`--agent` is required, with either `--scope` or `--config-dir`. Agent values are
`codex`, `claude`, or `both` (only `codex` or `claude` with `--config-dir`). Local
scope uses the current working directory, including a worktree; user scope uses
the user's home directory. `--config-dir` names one agent configuration directory
itself, for example `~/.claude-work` or an alternative Codex home: it takes exactly
one `--agent`, installs into that single directory and cannot be combined with
`--scope`. The directory must already exist; a missing path is refused instead of
created, and so is a directory that contains `.claude` or `.codex` (the
configuration is the directory itself, not its parent). Two configurations are two
runs and two independent installs, each with its own ownership record. Setup does
not walk to a Git root, and `CLAUDE_CONFIG_DIR` and `CODEX_HOME` are never read.
`lit setup-skills` and `--scope custom --path` no longer exist.

Setup records what it owns in `.lit-workflows.json` in each configuration
directory. A configuration installed by the removed `lit setup-skills` command
(ownership file `.lit-skills.json`) is adopted in the same transaction: the next
`setup-workflows` run takes over its skills and managed block, writes the new
record and removes the old file, or changes nothing if any step fails. The
receipt marks adopted targets.

Setup manages files under the selected host directories, preserving unrelated
content. Rerun it after a CLI upgrade to update owned skills. Unmanaged collisions
and local edits are errors, not permission to overwrite. Both-agent setup preflights
both destinations before writing; an unexpected filesystem failure can leave one
host updated, since the two hosts are separate transactions. Correct the reported
problem and rerun for safe recovery. Setup does not start the service, establish
tracker identity, or enable paid consultations. Setup writes a JSON installation
receipt listing each target's `agent`, `scope` (`local`, `user` or `config-dir`),
`directory`, whether it was `adopted`, whether it installed `hooks`, the Claude
`status_line` outcome, the Claude `status` surface and what became of the lit-ui
`mod` (see [Status surface](#status-surface-the-status-line-and-the-lit-ui-mod)),
and its `warnings` and `notes`; it does not accept
`--format`. The warnings and notes are also printed on stderr, so stdout stays
the receipt. If writing that receipt fails, it exits nonzero and reports that
workflows were installed. The completed installation stays
in place; the output error does not roll it back. `--remove` undoes an
installation, see [Removing the installation](#removing-the-installation). See the
installed lit skill's host reference for explicit session setup.

### Starting a new conversation

The installed `lit` skill (`references/context.md`) teaches the same new-conversation
workflow in both setups, so a user with hooks and a user with skills only get the
same behavior from their agents. It explains the four `lit context` commands
(`set` only when the user asks, `show` offline, `show --validate`, `clear`) and
separates the project a user explicitly remembered for a checkout from the
inference that stays forbidden: a directory name, checkout name, branch,
timestamp, the most recent agent or a single search result never selects a
project.

- An explicit project the user names for the conversation wins and leaves the
  remembered one alone (`fresh --project REF`, or `session set --project REF` on a
  session a hook already created).
- Otherwise the new session adopts the remembered project after validating it. With
  hooks installed, the session-start hook has already run `fresh`, and the agent reads
  the hook's `remembered project:` line; with skills only (`--scope local`), or when
  no hook context is present (for example Codex has not been told to trust the
  hooks), the agent runs `workflow-session fresh` itself and reads
  `project_selection`. Either way it announces briefly which project was
  selected because it was remembered for the checkout.
- With nothing remembered the agent lists candidates from the repository's
  conventions and current tracker queries and asks, rather than choosing.
- A stale, damaged, other-service or unreachable preference creates no session. The
  agent explains why and offers `lit context clear` or `--project REF` for this
  conversation; it never substitutes a project, rewrites the binding or creates one.
- The remembered project is not a remembered issue, takes no claim, renews nothing
  and takes nothing over. Subagents never adopt: their coordinator gives them an
  explicit project, and a worker checkout's own preference never overrides it.

Archived projects are not reported yet because lit has no archive state; the
guidance already treats them like any other preference that cannot be used.

### Hooks and the status line

`--scope user` and `--config-dir` also install the lit hooks (`SessionStart`,
`SubagentStart` and `Stop`, see [Session hooks](#session-hooks)) and, for Claude,
the [status line](#claude-status-line) into the agent configuration. `--scope local`
installs the skills and the managed block only: a hook in a repository would run
next to the user-level one, so setup never writes hooks into a repository's
`.claude` or `.codex`.

| Host | Hooks | Status line |
| --- | --- | --- |
| Claude | The lit-owned plugin directory `skills/lit-hooks/` (`.claude-plugin/plugin.json`, `hooks/hooks.json`), which Claude loads as a skills-directory plugin; `settings.json` is not touched for hooks. | The `statusLine` member of `settings.json`: set when it is unset or already lit's; a foreign command is wrapped as `lit hook --host claude statusline --original 'COMMAND'` and recorded; a `refreshInterval` of 2 seconds is configured. A status line that is not a command (or has none) is left alone, with a note. |
| Codex | lit's groups appended at the end of the `SessionStart`, `SubagentStart` and `Stop` lists of `hooks.json`; no other entry is touched. | none (Codex has only built-in status items) |

#### Status surface: the status line and the lit-ui mod

For Claude, `--status line|mod|both` chooses how lit's state is shown; the default
is `both`.

| `--status` | Status line | [lit-ui mod](lit-ui.md) |
| --- | --- | --- |
| `line` | installed as described above | not installed; one setup installed earlier is removed |
| `mod` | not written; a status line setup installed is restored to the original it wrapped, or removed when lit had set its own (through the same path as `--remove`); a status line setup does not own is left untouched | installed |
| `both` (default) | installed | installed |

The mod (Claude Code 2.1.287 or later; older versions do not load it, and the
status line keeps working there) is the lit-owned plugin directory
`skills/lit-ui/` next to `skills/lit-hooks/`: its manifest
`.claude-plugin/plugin.json` (plugin name `lit`), the hooks module `hooks/` and its type contract
`types/`, all embedded in `lit`, plus a generated `config.json` from which the mod
reads its mode and the lit executable it runs:

```json
{"schema_version": 1, "status": "both", "lit": "/home/alice/go/bin/lit"}
```

`lit` is the absolute path of the lit on PATH when that is the executable running
setup (a link there survives upgrades), otherwise of the running executable. Claude
Code loads the mod as a skills-directory plugin, like the hooks plugin, once the
workspace is trusted. The receipt's `status` is the chosen surface and `mod` is
`installed` when the mod is in place after the run or `removed` when this run took
it away (`line` after `mod` or `both`); with `mod`, `status_line` is `restored`,
`removed` or `left` (lit's status line was changed after setup wrote it, so it
stays, with a warning), and absent when there was no status line of lit's.
Rerunning with another `--status` converges: `line` → `mod` restores the status
line and installs the mod, `mod` → `line` removes the mod and sets the status line,
`mod` ↔ `both` rewrites `config.json`. A `settings.json` setup created is deleted
when restoring leaves it empty.

`--status` does not apply to Codex, which has neither: it is ignored for Codex
targets with a note in that target's receipt, also with `--agent both`. The local
scope installs neither mod nor status line, and an explicit `--status` there is
noted the same way. `--remove` does not take `--status`.

The hook commands are `lit hook --host claude|codex session-start|subagent-start|stop`
with a backstop timeout of 10, 5 and 5 seconds (lit enforces its own, shorter
budgets). They name `lit` when the lit on PATH is the executable that ran setup,
so the commands survive upgrades, and otherwise the running executable's quoted
path. The definitions change only when the hooks themselves do, because Codex asks
for approval again whenever one changes.

**Endpoint.** The hook commands carry no endpoint: `lit hook` has no `--endpoint`
flag. A new runtime's `session-start` uses `LIT_ENDPOINT` from the environment of
the agent that runs the hook, else `http://127.0.0.1:7411`; a known runtime (and
the stop and subagent-start hooks, which only serve known runtimes) keep the
endpoint saved in their association. To use another server, export `LIT_ENDPOINT`
where you start the agent (for Claude Code also the `env` member of `settings.json`).
Setup prints a note when it runs with `LIT_ENDPOINT` set; it does not copy the value,
because the endpoint belongs to the environment (the `lit` CLI reads it everywhere)
and changing a hook command would make Codex ask for approval again.

**Codex trust.** Codex skips a new or changed hook until the user approves it, and
it keys the approval by position and a hash of the handler. Setup never writes
trust state and never pre-trusts: it prints the three hooks to approve in `/hooks`.
Setup warns when `config.toml` of the same configuration defines `[hooks]` (Codex
warns about a layer that has both) or disables hooks (`[features] hooks = false`);
it never edits `config.toml`. Entries the user adds after lit's shift when lit's are
replaced, so those need approving once more.

**Warnings.** Hooks in several layers all run, so setup reports lit hooks it finds
elsewhere and never edits them: in the target's own `settings.json` and
`settings.local.json`, in the other Claude configurations in the home directory
(`~/.claude`, `~/.claude-work` and any `~/.claude*` directory), in the current
directory's `.claude/settings*.json`, and for Codex in the other Codex home and the
current directory's `.codex`. A command counts as lit's when it calls `lit hook` or
`lit workflow-session`, is a script named `lit-*`, or is a script whose text calls
lit; that finds the hand-written stage hook and status line of the interim setup. A
wrapped status line that already shows lit's state is reported too.

**Safety.** `settings.json` and `hooks.json` are written by their hosts as well. Setup
keeps their key order and member values (only the indentation is normalized when a
member changes), keeps their permissions, and writes a file only when its content is
still what the plan was read from; recovery refuses rather than overwrites an edit
made after an interruption. A file that is not a valid JSON object, a `hooks.json`
whose `hooks` or event entries have another shape, a symlinked file, a modified
`skills/lit-hooks` or `skills/lit-ui` and an unowned `skills/lit-hooks` are refused
with nothing changed; so is an unowned `skills/lit-ui` unless `--status line`, which
leaves it alone. Hooks, status line and mod sit in the same transaction as the
skills: the journal is version 4 and covers `settings.json` or `hooks.json`, the
plugin directory and the mod directory (version-1 to version-3 journals still
recover; only version 4 may touch the mod). [Removal](#removing-the-installation)
writes the same journal.

**Ownership record.** `.lit-workflows.json` lists everything setup owns, so a later
run (and removal) acts on exactly that. Schema version 1, strict (an unknown member
means a newer lit wrote it, and the file is refused):

```json
{
  "schema_version": 1,
  "host": "claude",
  "skills": {"lit": "<sha256 of the skill tree>"},
  "plugin": {"name": "lit-hooks", "fingerprint": "<sha256 of skills/lit-hooks>"},
  "mod": {"name": "lit-ui", "fingerprint": "<sha256 of skills/lit-ui, config.json included>"},
  "status_line": {
    "installed": {"type": "command", "command": "lit hook --host claude statusline", "padding": 0, "refreshInterval": 2},
    "original": {"type": "command", "command": "~/.claude/statusline.sh"}
  },
  "created_files": ["settings.json"]
}
```

A Codex record has `"hooks": [{"event": "Stop", "group": {...}}, ...]` instead of
`plugin`, `mod` and `status_line`: each group exactly as appended, to be found again by
value. `original` is present only when lit wrapped a foreign status line, and is
that status line as it was. `created_files` names the host file (`settings.json` or
`hooks.json`) when setup created it, so removal can delete it again once only lit's
content is left; it is absent when the file was the user's. `mod` is present
while the lit-ui mod is installed (`--status mod` or `both`) and `status_line` while
lit's status line is (`line` or `both`). A skills-only run (the
local scope in a directory that also holds an agent configuration) leaves these
sections as they are.

### Removing the installation

```sh
lit setup-workflows --remove --scope user --agent both
lit setup-workflows --remove --agent claude --config-dir ~/.claude-work
```

`--remove` takes the same target flags as installing (`--agent`, and `--scope` or
`--config-dir`) and removes exactly what the target's ownership record lists:

- the skills the record names, each checked against its recorded fingerprint first,
  the lit-owned `skills/lit-hooks` plugin directory and the `skills/lit-ui` mod
  directory (both checked the same way);
- the managed block of `CLAUDE.md` or `AGENTS.md`, with the line break that ends
  it; text around the block stays, and an instruction file that held nothing else
  is deleted;
- Claude's `statusLine`: when the current value is still what setup wrote (equal
  by meaning to the record's `installed`), the wrapped original is put back exactly
  as recorded, or the member is deleted when there was none. A status line that
  changed since is yours now: it is left as it is and the warning names the
  original that setup had wrapped;
- Codex's hook groups: for each recorded `{event, group}` the first group equal to
  it by meaning is taken out of `hooks.json`, so entries you added or edited stay
  (an edited group no longer matches and is reported). Event lists that become
  empty stay as empty lists, since the file does not say whether you wrote them;
- `settings.json` or `hooks.json` itself, only when `created_files` says setup
  created it and nothing but an empty `{}` or empty event lists is left;
- the ownership record, last.

Nothing else is touched: skills and files the record does not list, the rest of
`settings.json` and `hooks.json` (key order and member texts are kept, only the
indentation of an edited member is normalized) and `config.toml`, which setup
never edits, so Codex's remembered approval of the removed hooks stays there. The
`skills/` directory and the configuration directory remain even when empty.
`--scope local` in a directory that also holds an agent configuration (the home
directory) removes what that directory's record lists, hooks included: the record
decides, not the scope.

Removal uses the same lock, backups, journal and recovery as installing (journal
version 4; an interrupted removal is rolled back by the next `setup-workflows` run
with or without `--remove`, which refuses rather than overwrites an edit made
meanwhile) and the same guards for the files hosts also write. Both agents are
preflighted first: a skill or plugin directory you modified, a record that cannot
be read, a `settings.json` or `hooks.json` that is not a JSON object or is a
symlink all refuse the run with nothing changed (exit 1). A skill that is already
gone is skipped with a note. A target without an installation is not an error: it
reports `found: false` and a note, creates nothing, and a second `--remove` is a
no-op. A managed block without a record is not setup's and stays, with a warning.

An install made by the removed `setup-skills` command (only `.lit-skills.json`) is
removed through that file, which lists the same skills and owns the block the same
way, and is deleted in the same transaction; it is not adopted first. A directory
holding both record files is refused as for installing.

The receipt lists each target as `{"removed": [{"agent", "scope", "directory",
"found", "skills", "hooks", "mod", "status_line", "warnings", "notes"}]}`. `skills`
counts the skill directories removed, `hooks` says the lit hooks were removed, `mod`
(present only when true) that the lit-ui mod was removed and
`status_line` is `restored`, `removed` or `left`. Warnings and notes are also
printed on stderr.

## Release archives

CI cross-builds fourteen binaries across five targets: Linux and macOS amd64/arm64
archives contain `lit-server`, `lit` and `lit-tui`; the Windows amd64 archive
contains `lit.exe` and `lit-tui.exe`. All archives contain `INSTALL.md`; Linux
archives also contain the optional `lit.service.in` template and `lit.socket` unit. Skills are embedded
in the client, so no separate installer or Python program is included.

Pull requests run Go race tests and vet on Linux and macOS, and cross-builds. Version tags
(`vMAJOR.MINOR.PATCH`, optionally `-PRERELEASE`) additionally produce five archives
and `SHA256SUMS` as GitHub Actions artifacts. CI uses ordinary Go commands and
shell archive utilities; it does not create GitHub Releases. Publishing remains
an explicit separate action. CI embeds the release identifier in `lit-server --version`,
`lit version` and `lit-tui --version`. Without that override, the binaries report
the module version Go recorded: the tag for `go install …@v0.7.1`, a
pseudo-version or VCS-derived version (for example `v0.7.1+dirty`) for a build
from a git checkout, and `dev` when Go recorded none.

Verify checksums before extracting: `sha256sum -c SHA256SUMS` on Linux,
`shasum -a 256 -c SHA256SUMS` on macOS, or compare PowerShell
`Get-FileHash .\lit-v0.1.0-windows-amd64.zip -Algorithm SHA256` with its checksum
entry. Download all listed archives for a full checksum check, or select the
entry matching your archive. Copy extracted binaries together to a directory on PATH.
No custom build or binary installation tool is necessary.

## Start and stop explicitly

On Linux or macOS:

```sh
lit-server
```

The initial service accepts only a loopback IP listener, such as
`127.0.0.1:7411` or `[::1]:7411`. Wildcard/public IP addresses and hostnames
(including `localhost`) are rejected. Remote hosting is a future design goal,
not a supported listener mode in this delivery.

The process runs in the foreground. Stop it with Ctrl-C or SIGTERM. Shutdown
allows active work up to thirty seconds. Logs go to stderr; record contents are
not dumped. An occupied port fails without selecting another port. CLI commands
do not start the service. Use `lit --help` for the installed command surface.

Default configuration: `${XDG_CONFIG_HOME:-$HOME/.config}/lit-server/config.json`.
Default data: `${XDG_DATA_HOME:-$HOME/.local/share}/lit-server`. macOS uses the
same XDG paths, not `~/Library/Application Support`. Relative XDG values are
ignored in favor of HOME defaults. Missing default configuration uses defaults;
explicitly selecting a missing file is an error. An optional JSON file is:

```json
{"schema_version": 1, "data_dir": "/home/alice/.local/share/lit-server", "listen": "127.0.0.1:7411"}
```

Use absolute paths in configuration. Precedence is command flags, environment,
JSON, defaults:

| Flag | Environment | JSON field |
| --- | --- | --- |
| `--config` | `LIT_CONFIG_FILE` | selects the file |
| `--data-dir` | `LIT_DATA_DIR` | `data_dir` |
| `--listen` | `LIT_LISTEN` | `listen` |
| none | `LIT_READ_CONCURRENCY` | `read_concurrency` |
| none | `LIT_BACKUP_DIR` | `backup_dir` |
| none | `LIT_BACKUP_INTERVAL` | `backup_interval` |
| none | `LIT_BACKUP_KEEP` | `backup_keep` |
| none | `LIT_EXPORT_DIR` | `export_dir` |
| none | `LIT_EXPORT_INTERVAL` | `export_interval` |
| none | `LIT_SEARCH_TIMEOUT` | `search_timeout` |

`read_concurrency` caps how many read requests run at once (default 8, a
positive integer); further reads wait until one finishes or their request ends.
Writes are always serialized and do not count against it.

`search_timeout` is the deadline of every search request (a positive Go
duration such as `"10s"`, the default). It counts from the moment the search's
read snapshot opens, so waiting for a read slot does not use it up. A search
that reaches it fails with `504 search_timeout`; narrow the pattern or search
one project instead of all projects. Patterns are at most 1,000 bytes.

lit-server takes `VACUUM INTO` backup snapshots of its database into
`backup_dir` (default `<data dir>/backup`) every `backup_interval` (a Go
duration such as `"24h"`, the default; `"0"` turns scheduled snapshots off),
keeping the newest `backup_keep` (default 14, a positive integer). See
[backups](recovery.md#backups) and [restoring a snapshot](recovery.md#restore-a-snapshot).

With `export_interval` set (a Go duration such as `"10m"`; the default `0`
turns it off), lit-server writes the [Markdown export](export.md) into
`export_dir` at that interval, skipping runs when nothing has committed since
the last export. `export_dir` must then be set; it cannot be a mount point
(mount its parent instead). See [interval exports](export.md#interval-exports).

Changes require an explicit restart. Invalid/unknown configuration fields and
unsupported format versions fail clearly. First explicit startup initializes an
empty store under exclusive ownership; populated damaged stores do not gain a
fabricated identity. Keep storage together and preserve it when replacing binaries.
The client uses separate private state. `LIT_STATE_DIR` overrides these defaults:

| Platform | Client state directory |
| --- | --- |
| Linux | `${XDG_STATE_HOME:-$HOME/.local/state}/lit` |
| macOS | `~/Library/Application Support/lit` |
| Windows | `%LOCALAPPDATA%\lit` |

Workflow records default to the `local` subdirectory of each platform's client
state directory above. `LIT_WORKFLOW_STATE_DIR` overrides that location independently
of `LIT_STATE_DIR`. Neither location is the service data directory. The project
remembered for a checkout (`lit context`) is kept there too, in `checkout-context/`;
see [command help](cli-help.md#remembered-project-per-checkout).

## Connect a client

With `lit-server` running on its default listener:

```sh
lit connect --session poc-human --actor-name Alex --actor-kind human
lit projects list --session poc-human
```

To capture the logical session name in Fish without replacing the current
selection if connection fails:

```fish
set -l lit_session (lit connect --session-id-only)
and set -gx LIT_SESSION "$lit_session"
```

For Bash or Zsh:

```sh
lit_session=$(lit connect --session-id-only) && export LIT_SESSION="$lit_session"
```

`--session-id-only` prints only the logical name and a newline on success, even
with `--format json` or a saved Markdown preference. Errors and warnings stay on
stderr. The flag does not itself change saved output preferences. Session selection
still honors `--session` and `LIT_SESSION`, so an existing exported session is reused.

Logical session selection is `--session NAME` > `LIT_SESSION`. An explicitly
supplied empty `--session` is invalid; omit the flag to use the fallback. With neither,
`lit connect` creates a fresh isolated client under a random
`throwaway-<UUID>` name. The response shows **Logical session** in CLI/Markdown
and `items[0].logical_session` in JSON. Reuse that complete name with `--session`
on later calls (or set `LIT_SESSION`); other tracker commands require a session.
No session is automatically selected for the shell, and “throwaway” does not mean
auto-deleted: the mapping remains available for deliberate reuse. Explicit
`--session default` still resumes an existing legacy mapping. `--client-id UUID`
can explicitly attach a new logical name to an existing client instead of
registering a fresh client. `version`, help/completion, and local
`transactions status` do not require a selected logical session.

If registration succeeds but saving the local mapping fails, the
`local_state_error` includes `logical_session`, `client_id`, `service_id`, and
`endpoint` in its JSON `error.details`, and prints a recovery command. Repair
the reported local filesystem problem, then reconnect using that session name,
client ID, and endpoint to retain the registered client instead of creating another.

Client endpoint precedence is `--endpoint` > `LIT_ENDPOINT` > the selected
session's remembered endpoint > `http://127.0.0.1:7411`. A first local connection
therefore needs no endpoint flag. Remote connections can use
`--endpoint https://tracker.example.com`; saved remote sessions keep that endpoint
unless explicitly overridden. Invalid selected endpoints are rejected without
falling back, and a changed service identity is still rejected as `wrong_service`.
The client never starts the service or falls back after a connection failure.

`workflow-session fresh` and `workflow-session subagent` use `--endpoint`, then
`LIT_ENDPOINT`, then the same loopback default; a `fresh` that adopts the project
remembered for the checkout (see [CLI help](cli-help.md#remembered-project-per-checkout))
inserts the endpoint saved with that project between `LIT_ENDPOINT` and the default.
`resume`, `reconcile`, and `run` keep the endpoint and identity saved in their
runtime association, even when the shell's `LIT_ENDPOINT` changes.

`lit workflow-session --host claude|codex --runtime-id ID show` prints the
runtime's stored association in the JSON the lifecycle commands print, and exits
0. It is read-only: it takes no adapter lock, runs no tracker command, never
connects or contacts lit-server and writes no state, so a status display can call
it while the agent's own `run` holds the runtime's lock. A runtime without an
association exits 3 and prints
`{"error":{"code":"association_not_found","message":"...","details":{"host":"claude","runtime_id":"ID"}}}`
on stdout, with nothing on stderr. A record that cannot be read or validated exits
1 with the reason on stderr and is left untouched.

A session can also carry a workflow stage (`lit session set --stage impl`, cleared
with `lit session unset --stage`). lit-server stores it with the client, so it is
shared with other sessions and the status line, survives `workflow-session resume`
and creates no issue history. A fresh session starts without one. A server that
migrates an older database to storage schema 4 adds the column after the usual
pre-migration snapshot; an older `lit-server` refuses the migrated database, and
an older `lit` ignores the new `stage` field.

## Session hooks

`lit hook --host claude|codex session-start` is the command an agent host calls
when a conversation starts, resumes or is cleared. The host writes its hook JSON
to stdin; lit uses the `session_id` as the workflow-session runtime ID, sets up
the lit session for it and prints host output whose `additionalContext` tells the
agent its session name, endpoint, runtime ID, selected project and current claims
(with lease expiry), so the agent runs no setup commands. Nothing is passed through
environment variables. `lit setup-workflows` installs the hooks into an agent
configuration (see [Hooks and the status line](#hooks-and-the-status-line));
this section describes what they do once installed.

| Source | Behavior |
| --- | --- |
| `startup`, `clear`, `fork` | A new runtime: `workflow-session fresh` with the `session_id`. |
| `resume` | A known runtime reconnects (`resume`); one that was never set up gets `fresh`. |
| `compact` | Restores tracker context after compaction (see below). |

Before connecting, the hook probes the endpoint (`GET /v1/meta`, one second); the
whole event has a budget of about three seconds. When the endpoint does not answer
nothing is created, and the context says lit is unavailable and gives the exact
`lit workflow-session ... fresh --endpoint URL` command to set the session up
later; a known runtime keeps its association and is told to `resume`. When the
service answers but connecting does not finish in time, the pending association
is kept and the agent is told to run `workflow-session reconcile` before any
tracker work, as the recovery rules require; it never runs `fresh` again.

A hook never breaks the host session. Once an event is being served, `lit hook`
exits 0 whatever happens; argument errors still exit 2. Errors become a one-line
note in the context and a line in the hook log,
`<workflow state>/hooks/hook.log` (JSON lines, rotated at 256 KiB to `hook.log.1`;
`LIT_WORKFLOW_STATE_DIR` relocates it). Error lines are the failures the status
line reports; warnings, such as a server that is down or a preference that cannot be
adopted, are not. The new runtime's endpoint is `LIT_ENDPOINT`, the endpoint saved
with the remembered project it adopts, or `http://127.0.0.1:7411`.

Hooks are idempotent per runtime. When another hook run already created the
association, or holds the adapter lock while doing so, the hook waits briefly (up
to 1.5 seconds), reads the existing association and prints the usual context
without logging an error; it never registers a second client. A lock that
outlasts the wait is a failure: the hook leaves it alone, names its directory in
the note and logs the error. The hook runs lit in a directory of its own
(`<workflow state>/hooks/work`), so the CLI's output cache stays out of your
project.

### Remembered project

A new runtime's session adopts the project remembered for the checkout of the
hook input's `cwd` (`lit context set --project REF`; see [CLI
help](cli-help.md#remembered-project-per-checkout)): the hook asks
`workflow-session fresh` to adopt it, as the command does, and announces the result
in the context with a `remembered project:` line.

- **Adopted.** The project is selected in the new session, and the line says that it
  was remembered for this checkout and adopted when the session started (and, when
  the project was renamed since, the title it was saved under). A resumed session
  and a repeated start print the same line from the association, as long as the
  session still has that project; they never read the preference again.
- **Nothing remembered.** The session is created with no project selected and the
  line says that no project is remembered for this checkout. A project remembered
  for another checkout does not apply.
- **A preference that cannot be adopted** (damaged or unsupported record, another
  service at the address, project gone, service refusing the check) creates no
  session, no pending association and no client. The context says so, names the
  checkout, the problem and its code, and gives the two ways out with their exact
  commands: `lit context clear` from inside the checkout and then
  `workflow-session fresh`, or `workflow-session fresh --project REF` to start this
  conversation with another project while the preference stays as it is. The agent
  is told to ask the user and not to run tracker commands until a session exists.
  This is a warning in the hook log, not a failure: the status line shows no
  `[hooks degraded]` for it, and the hook still exits 0. The runtime ID stays free,
  so either command (or the same hook run again) sets the session up later.

The reachability probe asks the endpoint the adoption will use: `LIT_ENDPOINT`, then
the endpoint saved with the preference, then `http://127.0.0.1:7411`. A damaged record
is reported without probing, since it is refused before any server is contacted.
Only a new runtime adopts; `resume` of a known runtime and subagent sessions never
read the preference.

### After compaction

When a conversation compacts, the host fires `session-start` with source
`compact` (Codex only in root sessions). The hook reminds the agent what it was
doing: the session name, endpoint, runtime ID and project, the issues this
session has claimed with their lease expiry, and a bounded excerpt of each
claimed issue's latest comment with its issue ID and the commands to reread the
full records. lit comments carry no type, so the latest comment of a claimed issue
stands for its latest checkpoint; its author and time are shown. Excerpts keep the
start and the end of a long comment (350 and 150 characters) and say how many
characters they omit; only the five most recently acquired claims carry an excerpt,
at most twenty are named, and the whole context stays under the 10000 characters
hosts accept. The excerpts are quoted and flagged as data, not instructions.

The hook only reads. It creates no session or client, and it never acquires,
renews, releases or reacquires a claim; nothing is written to the tracker. The
tracker lists only live claims, so a lease that ran out disappears from the list
without notice: the context tells the agent that an issue it was working on and
that is not listed as held has expired or been released, and that it must recheck
the issue and reacquire only deliberately. A claim that the tracker still lists
with a lease that had already ended at its reading time is reported as expired.
When the server does not answer, the context carries the session name, endpoint
and the project ID from the local association and says that the claims could not
be read and must be rechecked (`claims list`); it never names remembered claims. A
conversation without an association gets a note with the `fresh` command, and a
pending one the `reconcile` command.

### Stop guard

`lit hook --host claude|codex stop` is the command a host calls when the agent
finishes responding. If the agent's lit session still holds claims, it blocks the
stop once: both hosts get `{"decision":"block","reason":"..."}`, and the reason
names each claim by issue ID and lease expiry and asks the agent to record a
checkpoint or handoff comment and release the claim, or to continue, with the
commands to do so (it never prints a claim token). It also says that the reminder
is shown once, so an agent that keeps its claims on purpose, for example while it
waits for your answer, says so and stops again. The host sets `stop_hook_active`
on the stop that follows a block, and lit always allows that one; Claude
additionally caps consecutive continuations at eight, Codex relies on
`stop_hook_active` alone. Only live claims count: lit-server does not list expired
leases.

The guard only reads. It never releases, renews or closes anything, and it never
sets a session up. Whatever it cannot be sure about allows the stop and prints
nothing: no lit session for the runtime, a session that is still pending, a
server that does not answer (probed for 400 ms, logged as a warning), a failed or
slow claims read (the event has a budget of one second), and input without a
boolean `stop_hook_active`, which could otherwise block every stop. Errors go to
the hook log as failures; a server that is down is not one.

A hook entry installed by a newer lit can name an event this lit does not know. An
exit status of 2 would block a Stop hook, so `lit hook --host claude|codex EVENT`
with an unknown event exits 0 and prints nothing, and the hook log records a
failure that says to update lit or reinstall the hooks. An invalid host, no
event, or an unknown flag of a known event still exit 2.

### Subagent sessions

`lit hook --host claude|codex subagent-start` gives each subagent a lit session of
its own, so that a subagent never works on its parent's session or claims. The
hook input's `agent_id` is the subagent's runtime ID and its `session_id` (on both
hosts the root conversation's) is the parent. The hook runs
`workflow-session subagent --parent-runtime-id <session_id> --runtime-id <agent_id>`
against the parent's endpoint and, when the parent has selected one, with the
parent's project passed as an explicit `--project`; a remembered project of the
checkout is never adopted for a subagent. The child gets its own client and starts
without claims, and the hook prints host output whose `additionalContext` names the
subagent's session, endpoint, runtime ID and project and tells it not to use another
agent's session. The hook does not change anything the parent owns.

The hook is idempotent per `agent_id`. Claude runs it again when a subagent is
resumed, and an in-process teammate runs it with every message; a repeat registers
no client and prints exactly the same text, from the local association alone (no
server needed), because Claude injects hook output into a subagent only when it does
not already hold that text.

The whole event has a budget of about one second (the reachability probe gets 0.4 s
of it). A conversation without a ready lit session of its own gets no subagent
session and the hook prints nothing; that is a log warning, not a failure. If the
server does not answer, nothing is created and the subagent is told that lit is
unavailable and which `workflow-session subagent` command sets its session up
later. If connecting does not finish in time the pending association is kept and the
subagent is told to run `workflow-session reconcile`. Anything else (a lock that
persists, a service that is not the parent's, malformed input) is logged as a hook
failure and prints nothing. As for every hook, the command exits 0. Codex does not
run its start hooks again when a subagent compacts, so nothing restores the session
text in a compacted Codex subagent.

## Claude status line

`lit hook --host claude statusline [--original COMMAND]` is the command Claude
Code runs for its status line (Claude passes the status line JSON on stdin and
sets `COLUMNS`). It prints one line, an original status line followed by lit's
part, joined with ` | `. Installing it into a Claude configuration is a separate
step.

Without `--original` the original is a built-in line,
`<model> <effort> | <directory> | <git> | context: N%`: the directory is shortened
the way fish's `prompt_pwd` does it, git shows the branch with `+` staged, `*`
modified and `?` untracked changes and `↑n`/`↓n` commits ahead and behind, and
fields the host did not send are left out. With `--original COMMAND` it is that
command's output: it runs through `sh -c` with the same stdin, at most one
second and, in `COLUMNS`, the width left once lit's smallest part, the separator
and two spare columns are taken from the terminal's. A command that fails, times
out (it is killed with everything it started) or prints nothing leaves lit's part
alone; a multi-line original gets lit's part after its last line.

lit's part is `🔥 lit | <project or "no project selected"> | claims:N | <stage>`
(no stage, no last part), `⚫ not lit` when the server does not answer, `lit:
pending` while the session's association is pending and `🔥 lit | no session`
when the server answers but this runtime has none. `[hooks degraded]` follows
it when a hook of the same host logged an error in the last ten minutes
(warnings, such as a server that is down, do not count; this is unrelated to
lit-server's Degraded mode).

In a narrow terminal lit's part shortens first. With `--original` the line uses
the fullest form of lit's part that fits next to the original, which is never
cut. The built-in line collapses one step at a time: the ahead/behind counts,
`no project selected` to `no project`, the effort, the `context:` label, lit's
wording to `🔥 <project> | c:N` (and `⚫ not lit` to `⚫`), the directory to its
last component, git, the model, the directory and finally the project; the stage
and the failure indicator (`[hooks!]` when short) stay. Two columns stay spare
at the right, where Claude shows notifications.

The status line runs every couple of seconds and answers within about 300 ms
because it reads what it shows from a cache. There is one entry per host
session, `<workflow state>/hooks/statusline/<hash>.json` (never in the project;
`LIT_WORKFLOW_STATE_DIR` relocates it). Reachability, the claim count and the
stage are trusted for five seconds, the project's title for five minutes; an old
entry is refreshed with at most 250 ms of requests as the session (three small
reads, no writes), and a refresh that fails or times out is cached as `⚫ not
lit` for the same five seconds. Entries nothing has refreshed for a day are
removed. `git status` has 250 ms. Only unexpected errors reach the hook log.

## Optional systemd user service

The checked-in `packaging/lit.service.in` (also in Linux archives) is a template,
not an installed unit. To opt into service operation, copy it to
`${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/lit.service` and edit it manually:

- Replace `@EXEC_START@` with the absolute path to your installed `lit-server`,
  for example `/home/alice/go/bin/lit-server`. Quote executable paths
  containing spaces with double quotes using systemd unit syntax.
- Remove `@ENVIRONMENT@`, or replace it with explicit `Environment=` lines for
  the configuration/data settings you want the service to use.
- Review configuration and permissions. A user manager may have different
  environment variables than your shell, so make custom paths explicit. An
  explicitly selected configuration file must already exist before startup.

The unit allows thirty seconds of graceful shutdown plus five seconds for exit.
Once reviewed, choose service operation explicitly:

```sh
systemctl --user daemon-reload
systemctl --user start lit.service
systemctl --user status lit.service
journalctl --user -u lit.service
systemctl --user stop lit.service
```

Login-time enablement is a separate `systemctl --user enable lit.service`
decision. No persistent service, lingering, or machine-wide configuration is
installed or enabled automatically.

### Optional socket activation

Instead of running `lit-server` continuously, systemd can hold the listening
socket and start the service on the first connection. Install the service unit
as above, then copy `packaging/lit.socket` (also in Linux archives) beside it as
`${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/lit.socket`. Its `ListenStream=`
is the default `127.0.0.1:7411`; edit it to change the address.

While socket-activated, the socket's address replaces the configured listener:
`--listen`, `LIT_LISTEN`, and the JSON `listen` field are still validated but not
bound. The inherited socket must be exactly one loopback TCP listener. Anything else,
such as a wildcard or public address, a Unix socket path, several `Listen*` lines,
or `Accept=yes`, is unsupported; the service exits with an error rather than
serving it. The log reports
`lit-server listening ADDRESS (socket-activated)`.

Enable the socket, not the service:

```sh
systemctl --user daemon-reload
systemctl --user enable --now lit.socket
systemctl --user status lit.socket lit.service
```

The `lit` client is unchanged and still never starts the service itself; its
first connection to the socket causes systemd to start `lit-server`. The service
does not exit when idle; it keeps running until stopped. Stopping only
`lit.service` leaves the socket listening, so the next connection starts it
again. To turn activation off, run `systemctl --user disable --now lit.socket`
and stop `lit.service`.

On macOS there is no packaged service unit. Run `lit-server` in the foreground,
or under a launchd user agent you write yourself with the same explicit
configuration. A launchd agent does not read your shell profile, so it does not
see `XDG_CONFIG_HOME` or `XDG_DATA_HOME` set there and falls back to
`~/.config/lit-server` and `~/.local/share/lit-server`. If you moved either
directory, pass `--config` and `--data-dir` in the agent's `ProgramArguments`
or set the variables in its `EnvironmentVariables`.

## Docker Compose

The repository's `compose.yaml` runs lit-server as a single container on Linux,
next to the systemd units above, which remain the path without containers. The
image is built locally from the repository's `Dockerfile`: a static binary
(`CGO_ENABLED=0`) on `gcr.io/distroless/static`, running as a non-root user.
Nothing is pulled from or published to a registry except the public Go and
distroless base images. Docker with Compose v2 is required; Docker Desktop on
macOS or Windows is not a supported server platform.

From the repository root:

```sh
mkdir -p backup export
printf 'LIT_UID=%s\nLIT_GID=%s\n' "$(id -u)" "$(id -g)" > .env   # optional, see below
docker compose up -d --build
docker compose ps            # STATUS shows (healthy) once lit-server answers
docker compose logs -f lit-server
```

The stack:

- **Network:** `network_mode: host`, and lit-server binds the host's
  `127.0.0.1:7411` itself. The loopback-only listen rule is unchanged, so
  nothing beyond the host can connect, and `lit` connects to the default
  endpoint as usual. No ports are published. Stop a lit-server already running
  on the host first, or the container cannot bind the address.
- **Data:** the database, its WAL files and the lock file live in the named
  volume `lit_lit-data` at `/data`. Keep SQLite on this volume, never on a
  network filesystem or a bind mount.
- **Backups and exports:** the host directories `./backup` (scheduled
  `VACUUM INTO` snapshots and pre-migration snapshots, `LIT_BACKUP_DIR=/backup`)
  and `./export` (the [interval Markdown export](export.md#interval-exports) in
  `./export/tree`; the export directory cannot be a mount point itself). Create
  both before the first start: the bind mounts use `create_host_path: false`,
  so a missing directory fails with `bind source path does not exist` instead
  of Docker creating it owned by root.
- **User:** lit-server runs as `LIT_UID:LIT_GID`, by default distroless's
  `nonroot` (65532:65532). It must be able to write `./backup` and `./export`:
  either set `LIT_UID` and `LIT_GID` to your own IDs (in `.env`, as above, then
  rebuild), so that you own the snapshots and exports, or give the directories
  to 65532 (`sudo chown 65532:65532 backup export`). The IDs are build
  arguments; the named volume takes the owner of the image's `/data` when it is
  first created, so changing them later also needs a new volume or a `chown`
  inside it.
- **Restarts:** `restart: unless-stopped`. When lit-server exits, for example
  after [damage met while serving](recovery.md#damage-while-serving) or a
  crash, Docker starts it again; `docker compose stop` stops it until the next
  `docker compose start` or `up`. SIGTERM gets the 30-second drain
  (`stop_grace_period: 35s`); a stop that drains in time is a
  [clean shutdown](recovery.md#clean-and-unclean-shutdowns), so the next start
  skips the full integrity scan, while a restart after a crash runs it.
- **Health:** an exec-form healthcheck runs `lit-server healthcheck`, which
  calls `GET /v1/meta` on the configured listen address. It reports unhealthy
  in [degraded mode](recovery.md#degraded-mode) (503 `recovery_required`), on
  any other answer and when nothing answers. Docker does not restart unhealthy
  containers; degraded mode needs an operator.
- **Hardening:** a read-only root filesystem with a tmpfs `/tmp`, no Linux
  capabilities and `no-new-privileges`.

The settings are environment variables in `compose.yaml` (see the
configuration table above): `LIT_LISTEN=127.0.0.1:7411`, `LIT_DATA_DIR=/data`,
`LIT_BACKUP_DIR=/backup`, `LIT_BACKUP_INTERVAL=24h`, `LIT_BACKUP_KEEP=14`,
`LIT_EXPORT_DIR=/export/tree` and `LIT_EXPORT_INTERVAL=15m` (runs are skipped
while nothing changes; `0` turns interval exports off). Put local changes in a
`compose.override.yaml` next to `compose.yaml`, which Compose merges in, for
example:

```yaml
services:
  lit-server:
    environment:
      LIT_LISTEN: 127.0.0.1:7412
      LIT_EXPORT_INTERVAL: "0"
```

The listen address must stay a loopback IP; lit-server refuses anything else.
`.env`, `compose.override.yaml`, `backup/` and `export/` are ignored by Git.

Other lit-server commands run in the same container setup:

```sh
docker compose exec lit-server /lit-server healthcheck
docker compose exec lit-server /lit-server export --to /export/manual
docker compose stop lit-server
docker compose run --rm --no-deps lit-server verify   # needs the stopped server
docker compose start lit-server
```

To restore a snapshot ([restore a snapshot](recovery.md#restore-a-snapshot)),
stop the service, move `lit.db`, `lit.db-wal` and `lit.db-shm` aside inside the
volume and copy the snapshot from `./backup` in as `lit.db`, using any image
with a shell as lit-server's user, then verify and start:

```sh
docker compose stop lit-server
docker run --rm --user "$(id -u):$(id -g)" -v lit_lit-data:/data -v "$PWD/backup:/backup:ro" \
  busybox sh -c 'set -e; old=/data/replaced-$(date -u +%Y%m%dT%H%M%SZ); mkdir "$old"
    for f in lit.db lit.db-wal lit.db-shm; do [ ! -e "/data/$f" ] || mv "/data/$f" "$old/"; done
    cp /backup/SNAPSHOT.db /data/lit.db'
docker compose run --rm --no-deps lit-server verify
docker compose start lit-server
```

Replace `SNAPSHOT.db` with the snapshot's name, and `--user` with the image's
user when you did not set `LIT_UID`/`LIT_GID`. To upgrade, check out the new
version and run `docker compose up -d --build`; lit-server takes a
pre-migration snapshot into `./backup` before migrating the database.

## Manual updates

Stop the foreground service or user service before replacing its executable.
With socket activation, stop `lit.socket` as well as `lit.service`, so that a
client connection cannot start the old executable during replacement.
Run `go install` again for the chosen source/version, or verify and extract the
chosen archive and copy its binaries. Rerun `lit setup-workflows` with the same
scope/agent or agent/config-dir selection to update the installed skill bundle. Review any
reported local edits before proceeding. Explicitly restart the service afterward
(with socket activation, start `lit.socket` again).
Service identity and records remain in the existing data directory. Unsupported
persisted formats are refused without conversion; there is no automatic updater.
The one conversion is the explicit, one-time setup import of a legacy file store
below.

## Setup import from a legacy file store

lit-server keeps the tracker in one SQLite database, `lit.db`, in its data
directory. Releases before it kept a file store (one directory per record, with
a `service.json` identity file). lit-server never reads, detects or converts
that store at runtime: on a data directory that still holds `service.json` it
refuses to start and names this setup step. The conversion is a separate binary,
`lit-migrate`, run once while the old lit-server is stopped and before the new
lit-server first starts. It runs on Linux and macOS, like lit-server, is not part of the
release archives, and is removed in a later release. Build it from the same
checkout as the new lit-server:

```sh
go build -o ./bin/ ./cmd/lit-server ./cmd/lit-migrate
```

`lit-migrate --from COPY --to NEW [--config FILE] [--dry-run]`:

- reads only a copy of the stopped store. It refuses a store that a running
  lit-server still owns, never writes to `--from`, and runs the frozen legacy
  reader on its own private copy of it, because that reader writes to the store
  it opens (lock file, temporary files, journal replay, title index);
- loads the legacy server's JSON config (`--config`, otherwise `LIT_CONFIG_FILE`
  or the default config file), because legacy verification checks the
  configured title prefixes. Its `data_dir` and `listen` are not used;
- writes only into `NEW`, which must be missing or empty, and builds the
  database as a temporary file in one transaction. It runs validation checks
  1-9 (legacy verification of the copy, every legacy file imported or
  allowlisted, counts, record, client and claim equality, body SHA-256
  manifests, diff round trips and body chains, history read-back, the newest
  stamp, and `lit-server verify`), and moves the database into `NEW` only after
  all of them passed. A failure leaves no database, so the import can simply be
  run again on the same copy; it is deterministic for a given copy;
- keeps the service ID, every history entry (deduplicated by request hash into
  transaction records, with whole-body hunks converted to verified diffs and
  entries that changed only membership lists kept verbatim), every client, and
  every non-null claim with its original times, expired ones included;
- keeps the body SHA-256 manifest (type, ID, revision, body hash) as
  `NEW/backup/lit-migrate-TIME-bodies.json` for audits: in the default backup
  directory of a lit-server on `NEW`, whose retention removes only its own
  scheduled snapshots, never this file. lit-migrate writes nothing outside
  `NEW`, so it ignores a configured `backup_dir`; if you set one, move the
  manifest there, or next to the frozen old directory;
- with `--dry-run`, runs every step and check in a temporary directory
  (`TMPDIR`), never touches `NEW`, and reports how long the import took.

Exit status 0 means every check passed, 1 a failed import (nothing was
written), 2 invalid arguments. Progress and the result of each check go to
stderr; the summary with the duration goes to stdout. Ctrl-C stops an import
and cleans up like any failure. Only a hard kill (`SIGKILL`, power loss) can
leave a `.lit-migrate-*` staging directory and `lit.db.lock` in `NEW`, never a
`lit.db`; delete `NEW` before running the import again.

### Setup checklist

The commands assume the default paths and the systemd user units; adapt them to
your configuration.

```sh
OLD=${XDG_DATA_HOME:-$HOME/.local/share}/lit-server         # legacy store; stays untouched
COPY=$HOME/lit-migrate/lit-server.copy                     # scratch copy for the import
NEW=${XDG_DATA_HOME:-$HOME/.local/share}/lit-server-sqlite  # new, empty data directory
CONFIG=${XDG_CONFIG_HOME:-$HOME/.config}/lit-server/config.json
```

1. **Freeze.** Announce a write freeze. Have agents finish or release their work
   and claims. Resolve retained requests with `lit transactions status` in every
   known client state directory (`LIT_STATE_DIR`), and keep its output. Capture
   API baselines from the running old server for later comparison: full lists
   (`lit projects list`, `lit issues list`, `lit milestones list` and
   `lit comments list` with `--all`), the history of a few owners, and `lit grep`
   on a fixed query set (at least `net/http`, `C++`, `Go`, `UI`, `strasse`,
   `Straße`, `cafe`, `foo.bar`, `a_b`, `100%`, `APP-123`, plus multi-page and
   `-C` context queries). Keep a copy of the old `lit-server` binary for
   rollback.
2. **Stop and disable the old server.** With systemd:
   `systemctl --user disable --now lit.socket lit.service`, so socket activation
   cannot start it again. Confirm the process exited:
   `systemctl --user is-active lit.service` reports `inactive`, and
   `pgrep -x lit-server` prints nothing. Stop a foreground server with Ctrl-C.
3. **Copy.** `mkdir -p "$(dirname "$COPY")" && cp -a "$OLD" "$COPY"`. The original
   `$OLD` is the frozen backup; never modify it. Optionally record its checksums:
   `(cd "$OLD" && find . -type f -print0 | sort -z | xargs -0 sha256sum) > "$HOME/lit-migrate/old.sha256"`.
4. **Dry run, then import.** First
   `lit-migrate --from "$COPY" --to "$NEW" --config "$CONFIG" --dry-run`; it
   reports `dry run passed all 9 checks in DURATION`. Then the real import:
   `lit-migrate --from "$COPY" --to "$NEW" --config "$CONFIG"`.
5. **Start the new lit-server on `$NEW`.** Install the new `lit-server`. Point it
   at the new directory: set `data_dir` in `$CONFIG`, or `Environment=LIT_DATA_DIR=`
   in the unit, or pass `--data-dir "$NEW"`. Then
   `systemctl --user enable --now lit.socket` (or `lit.service`), or start it in
   the foreground. Its full integrity scan runs first: an imported database
   has no clean-shutdown marker. An old binary started on `$NEW`
   refuses it, and the new binary refuses `$OLD`.
6. **Smoke test.** `curl -s http://127.0.0.1:7411/v1/meta` shows the same
   `service_id` as before; connect with an existing session (`lit connect`);
   read an issue; run a `lit grep`; run `lit transactions status`; make one
   write on a scratch issue in a test project. Then compare the API baselines
   from step 1: the grep results byte for byte, the full lists and per-owner
   history (excluding time-dependent claim projections), and the
   `transactions status` output: requests that were `committed` stay
   `committed` with equal results.
7. **Lift the freeze.**

### Accepted consequences

- Retained requests that never committed before the import become a permanent
  `uncertain` with `original_preconditions_no_longer_valid`, as
  [recovery](recovery.md) allows. Resolve them in step 1 instead.
- Saved plans prepared before the import must be prepared again.
- Claims keep their original times. Leases keep running during the downtime and
  may lapse; `claims renew` still tells `claim_expired` from a conflict.
- Old history entries that changed only `issue_ids`, `child_ids` or
  `comment_ids`, now computed lists, are kept verbatim; `lit-server verify`
  skips those keys.
- A claim time that the legacy server did not write itself (not canonical UTC
  RFC3339Nano) cannot be copied exactly: the import stops and names the claim.
  Release that claim on the old server before step 2, then repeat from step 3.
- The estimated downtime is 15-30 minutes in total; the dry run measures the
  import itself.

### Rollback

Record `lit transactions status` output in every client state directory first.
Then stop and disable the new server (`systemctl --user disable --now lit.socket lit.service`),
point the configuration back at `$OLD` and remove the fields the old binary
does not know: `read_concurrency`, `backup_dir`, `backup_interval`,
`backup_keep`, `export_dir`, `export_interval` and `search_timeout` (it refuses unknown fields;
it ignores their `LIT_*` environment variables). Reinstall the old `lit-server`
binary and start it: it verifies the untouched old directory as before (about
30 seconds). Writes, clients and claims created after the import are lost,
which is accepted. The old server writes no exports: an interval export tree
keeps showing the new server's last state until you remove it or it is
exported again after a later import. Keep `$NEW` for inspection, or delete it
once you are done.

## Local checks

```sh
go test -count=1 -race ./...
go vet ./...
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o ./bin/linux-arm64/ ./cmd/lit-server ./cmd/lit ./cmd/lit-tui
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o ./bin/darwin-arm64/ ./cmd/lit-server ./cmd/lit ./cmd/lit-tui
```

The workflow in `.github/workflows/lit.yml` contains the full accepted matrix
and archive commands. To verify its generated artifacts locally:

```sh
LIT_ARCHIVE_DIR=/absolute/path/to/dist LIT_ARCHIVE_VERSION=v0.1.0 go test -count=1 -v ./packaging
```

The archive check verifies all five checksums, exact archive contents, executable
permissions, and (on Linux amd64) versions from the actual archived executables.
It does not start or enable systemd; native macOS behavior is covered by the
macOS test job, not by the archive check.

## Consultation skill

`consult` is included in the embedded skills. Agents use it to invoke a separately
installed Claude or Codex CLI directly; there is no `lit consult` command.
The skill guides exact model/effort selection, explicit session reuse, read-only
permissions and agent-managed discussion notes. Coordination is followed by the
agent rather than enforced by a helper. Existing consultation JSON records,
locks and vendor transcripts are preserved as evidence; no migration or deletion
is performed. Rerun `setup-workflows` after updating the CLI to install this revision.

## Output formats

Tracker commands accept `--format cli|markdown|json`. The default `cli` format
uses terminal tables and labeled details. `markdown` produces document-ready
Markdown, including readable tables. `json` retains the machine-readable result
and error envelopes. Piping does not change the selected format.

```sh
lit projects list --session poc-human
lit projects list --session poc-human --format markdown > projects.md
lit projects list --session poc-human --format json
lit session set --session poc-human --output-format cli
```

`--format` affects one invocation on ordinary commands. `connect --output-format`
and `session set --output-format` save the session preference. For compatibility,
`connect --format` also saves that format when `--output-format` is absent; with
both flags, `--output-format` controls persistence and `--format` controls display. A session without a
saved preference uses `cli`. Existing saved `human` preferences and checkout
caches render as `cli` without changing client IDs, state revisions or storage
versions. The service continues accepting legacy `human` values for old API
clients, while new CLI flags and completions offer only the three public names.
JSON responses retain the stored preference verbatim, including legacy `human`.

Errors before contacting the service use an explicit `--format`, otherwise the
matching checkout cache. Without either, offline/bootstrap errors retain the
JSON fallback so scripts can still inspect the error code. A cache for another
service/client or unsupported cache version is ignored.
