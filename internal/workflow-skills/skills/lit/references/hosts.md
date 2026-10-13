# Host discovery and explicit sessions

The CLI embeds the complete suite from `internal/workflow-skills/skills/`.
Install offline with `lit setup-workflows --scope local --agent codex` (or
`--agent claude` / `--agent both`). Local scope uses the current directory;
`--scope user` uses the home directory. Neither searches repository parents or
reads vendor configuration-root overrides (`CLAUDE_CONFIG_DIR`, `CODEX_HOME`). For
another agent configuration, such as `~/.claude-work`, name its directory:
`lit setup-workflows --agent claude --config-dir ~/.claude-work`. That takes
exactly one `--agent` (`codex` or `claude`), installs into that one directory,
which must already exist and is the configuration itself, not a parent of
`.claude`, and cannot be combined with `--scope`. Two configurations are two
runs and two independent installs. `--agent` and either `--scope` or
`--config-dir` are required.

The installer copies all sixteen skills into each host's `skills` directory,
adds a bounded managed block to `AGENTS.md` or `CLAUDE.md`, and records version 1
ownership in `.lit-workflows.json`. An install made by the removed `lit
setup-skills` command (ownership file `.lit-skills.json`) is adopted in the same
transaction; the receipt marks it `adopted`. Both destinations are preflighted
before writes. Each host has its own transaction; an unexpected filesystem
failure may leave the first host complete. The error identifies completed hosts;
resolve the error and rerun. Updates stage backups and a version-2 recovery
journal (version-1 journals left by `setup-skills` still recover). Interrupted
updates recover on the next run after any stale installer lock is resolved.

In the following recovery guidance, PATH means the host root (.codex or .claude).

A hard-killed installer leaves `PATH/.lit-install.lock`: check that no installer
is active before explicitly removing that empty lock directory, then rerun.
Preserve `.lit-install-transaction` for automatic recovery; damaged or incomplete
backups fail closed. Recovery also checks exact before/after host-file contents
and managed-skill fingerprints; unrelated edits made after interruption are
preserved, and recovery stops for explicit resolution rather than overwriting
them. It preserves unrelated configuration
and refuses local edits/collisions. Resolve those explicitly; never delete unrelated
skills to make installation succeed. Retired names are not installed; only entries
already owned by this installation record may be retired. No shell profiles, binary
installation, hooks, or service startup are changed.

Codex uses `SKILL.md` with `agents/openai.yaml` invocation policy and `$lit`;
Claude uses `SKILL.md` frontmatter and `/lit`. The canonical source preserves both
hosts' explicit-only metadata for what and i-have-adhd. Planner, to-spec,
to-tickets, and triage stay model-invocable but open with a gate: run only when
the user requested them or confirmed a proposal. Installation copies both forms unchanged. If discovery is unavailable,
read the installed `lit/SKILL.md` directly. Do not assume a generic Skill tool. These adapters select tracker identity;
they do not invoke either model CLI or assume vendor environment variables.

Use the installed CLI directly; workflow-session runs the same executable for tracker calls by default:

```sh
lit workflow-session --host codex --runtime-id THREAD_ID fresh --endpoint http://127.0.0.1:7411 --discussion-id TICKET_OR_DISCUSSION_ID
lit workflow-session --host codex --runtime-id THREAD_ID run -- projects list
lit workflow-session --host codex --runtime-id THREAD_ID run -- session set --project feat/tracker
lit workflow-session --host codex --runtime-id THREAD_ID resume
lit workflow-session --host codex --runtime-id CHILD_ID subagent --parent-runtime-id THREAD_ID --endpoint http://127.0.0.1:7411 --project feat/tracker
lit workflow-session --host codex --runtime-id THREAD_ID checkout --repository https://example.org/team/api --path /checkout/api
```

For Claude use `--host claude` and the explicit Claude session ID. Supply a known
runtime ID from the calling host or a deliberately assigned unique identifier;
never use “latest”. A new child needs a different ID and independent registration,
even when the parent selected the same project. A project's propagation to a child
is an explicit `--project`, not inheritance. Every `run` passes stored session and
endpoint flags, sets `--format json`, and ignores inherited LIT_SESSION/LIT_ENDPOINT.
Omit `--session`, `--endpoint`, and `--format` from the command after `run --`;
the adapter supplies them. Lifecycle commands return a JSON association record;
`run` forwards tracker output unchanged with `--format json`; help and version
retain their own text output. To read or export a record as
Markdown, use the direct CLI with the association's `local_session` and `endpoint`
and explicit `--format markdown`. A single complete resource `get` exports a
YAML-frontmatter/Markdown snapshot, not an import file: `--content-file` would
treat the entire export, including its metadata, as body content. The direct CLI
recipes use their own explicit flags.
Resume in another
checkout retains the same logical client and server project. Hooks may call these
commands but are optional; no child export into a parent shell is required.

`LIT_WORKFLOW_STATE_DIR` overrides the workflow directory. Defaults are Linux
`${XDG_STATE_HOME:-$HOME/.local/state}/lit/local` (ignore relative XDG values), macOS
`~/Library/Application Support/lit/local`, Windows `%LOCALAPPDATA%\lit\local`.
`vendor-sessions/` contains version-1 host/runtime associations with service,
client, project and optional discussion IDs. `checkouts/` contains version-1
service/project/repository/path mappings and optional discussion IDs. They are
separate from consultation records, CLI private `LIT_STATE_DIR`, and per-cwd caches.
Checkout mappings locate repositories; they never choose CLI project context.

Fresh setup persists a pending association before calling the CLI. On failure,
keep that association and all CLI diagnostics/private pending state. If the CLI
successfully connected but the adapter was interrupted, `resume` of a pending association (or explicit `reconcile`) performs only
`session get` and adopts its matching host/runtime and existing client/service.
It never creates a client or replays a mutation. If no confirmed connection exists,
inspect and resolve the retained CLI outcome explicitly; report the local session
name from the workflow record. Do not create another runtime to bypass uncertainty.
A stale `.lock` after a hard crash requires checking that no adapter is still active
before explicitly removing it. Unsupported or corrupt records fail without rewriting.
