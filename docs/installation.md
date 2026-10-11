# Building and installing lit

The service supports Linux amd64/arm64 with local filesystem storage. The clients,
`lit` and the terminal UI `lit-tui`, support Linux amd64/arm64, macOS amd64/arm64,
and Windows amd64. Linux behavior
is tested; cross-compilation does not demonstrate native macOS/Windows behavior.
Python is not required to build, install, or run the binaries and workflow skills.

## Standard Go build and install

Install Go matching `go.mod` (1.27 or later). From the repository root:

```sh
go build -o ./bin/ ./cmd/lit-server ./cmd/lit ./cmd/lit-tui  # Linux: create all three executables
go install ./cmd/lit-server ./cmd/lit ./cmd/lit-tui         # Linux: install checkout versions
```

On macOS or Windows, build/install only `./cmd/lit` and `./cmd/lit-tui`. Install
`lit-tui` into the same directory as `lit` (as `go install` does); see
[Terminal UI](#terminal-ui). Separate commands such as `go build ./cmd/lit` also
work and leave the executable in the current directory.
Installation uses `GOBIN`, or `GOPATH/bin` when unset (normally `$HOME/go/bin`).
Check `go env GOBIN GOPATH` and add the appropriate directory to PATH yourself;
installation does not edit your shell profile.

Once this module has been published and is accessible, installation without a
checkout uses an explicit version query:

```sh
go install github.com/nerdwave-nick/nerdwave-workflows/cmd/lit@latest
go install github.com/nerdwave-nick/nerdwave-workflows/cmd/lit-tui@latest
# Linux service, if needed:
go install github.com/nerdwave-nick/nerdwave-workflows/cmd/lit-server@latest
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
go install github.com/nerdwave-nick/nerdwave-workflows/cmd/lit-tui@latest
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
lit setup-skills --scope local --agent both
lit setup-skills --scope user --agent codex
lit setup-skills --scope custom --path /path/to/project --agent claude
```

Both `--scope` and `--agent` are required. Agent values are `codex`, `claude`, or
`both`. Local scope uses the current working directory, including a worktree;
user scope uses the user's home directory. Custom scope requires `--path` pointing
to the **parent containing `.codex` and/or `.claude`**, not either host directory
itself. `--path` is rejected for local/user scope. Setup does not walk to a Git
root or redirect using vendor configuration environment variables.

Setup manages files under the selected host directories, preserving unrelated
content. Rerun it after a CLI upgrade to update owned skills. Unmanaged collisions
and local edits are errors, not permission to overwrite. Both-agent setup preflights
both destinations before writing; an unexpected filesystem failure can leave one
host updated, since the two hosts are separate transactions. Correct the reported
problem and rerun for safe recovery. Setup does not start the service, establish
tracker identity, or enable paid consultations. Setup writes a JSON installation
receipt; it does not accept `--format`. If writing that receipt fails, it exits
nonzero and reports that skills were installed. The completed installation stays
in place; the output error does not roll it back. See the installed lit skill's host
reference for explicit session setup.

## Release archives

CI cross-builds twelve binaries across five targets: Linux amd64/arm64 archives
contain `lit-server`, `lit` and `lit-tui`; macOS amd64/arm64 and Windows amd64
archives contain `lit` and `lit-tui` (`lit.exe` and `lit-tui.exe` on Windows). All archives contain `INSTALL.md`; Linux
archives also contain the optional `lit.service.in` template and `lit.socket` unit. Skills are embedded
in the client, so no separate installer or Python program is included.

Pull requests run Go race tests, vet, and cross-builds. Version tags
(`vMAJOR.MINOR.PATCH`, optionally `-PRERELEASE`) additionally produce five archives
and `SHA256SUMS` as GitHub Actions artifacts. CI uses ordinary Go commands and
shell archive utilities; it does not create GitHub Releases. Publishing remains
an explicit separate action. CI embeds the release identifier in `lit-server --version`,
`lit version` and `lit-tui --version`; source builds without that override report `dev`.

Verify checksums before extracting: `sha256sum -c SHA256SUMS` on Linux,
`shasum -a 256 -c SHA256SUMS` on macOS, or compare PowerShell
`Get-FileHash .\lit-v0.1.0-windows-amd64.zip -Algorithm SHA256` with its checksum
entry. Download all listed archives for a full checksum check, or select the
entry matching your archive. Copy extracted binaries together to a directory on PATH.
No custom build or binary installation tool is necessary.

## Start and stop explicitly

On Linux:

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
Default data: `${XDG_DATA_HOME:-$HOME/.local/share}/lit-server`. Relative XDG values are
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

`read_concurrency` caps how many read requests run at once (default 8, a
positive integer); further reads wait until one finishes or their request ends.
Writes are always serialized and do not count against it.

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
of `LIT_STATE_DIR`. Neither location is the service data directory.

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
`LIT_ENDPOINT`, then the same loopback default. `resume`, `reconcile`, and `run`
keep the endpoint and identity saved in their runtime association, even when the
shell's `LIT_ENDPOINT` changes.

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

## Manual updates

Stop the foreground service or user service before replacing its executable.
With socket activation, stop `lit.socket` as well as `lit.service`, so that a
client connection cannot start the old executable during replacement.
Run `go install` again for the chosen source/version, or verify and extract the
chosen archive and copy its binaries. Rerun `lit setup-skills` with the same
scope/agent/path selection to update the installed skill bundle. Review any
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
lit-server first starts. It is Linux-only, like lit-server, is not part of the
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
   the foreground. Its startup check runs first. An old binary started on `$NEW`
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
`backup_keep`, `export_dir` and `export_interval` (it refuses unknown fields;
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
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o ./bin/darwin-arm64/ ./cmd/lit ./cmd/lit-tui
```

The workflow in `.github/workflows/lit.yml` contains the full accepted matrix
and archive commands. To verify its generated artifacts locally:

```sh
LIT_ARCHIVE_DIR=/absolute/path/to/dist LIT_ARCHIVE_VERSION=v0.1.0 go test -count=1 -v ./packaging
```

The archive check verifies all five checksums, exact archive contents, executable
permissions, and (on Linux amd64) versions from the actual archived executables.
It does not start or enable systemd or claim native non-Linux runtime validation.

## Consultation skill

`consult` is included in the embedded skills. Agents use it to invoke a separately
installed Claude or Codex CLI directly; there is no `lit consult` command.
The skill guides exact model/effort selection, explicit session reuse, read-only
permissions and agent-managed discussion notes. Coordination is followed by the
agent rather than enforced by a helper. Existing consultation JSON records,
locks and vendor transcripts are preserved as evidence; no migration or deletion
is performed. Rerun `setup-skills` after updating the CLI to install this revision.

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
