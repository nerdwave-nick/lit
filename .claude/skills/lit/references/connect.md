# Connect and select a project

Use the host adapter described in [host setup](hosts.md), or pass an explicit
`--session NAME` on **every** direct tracker call. These examples match the current
executable help; check your installed version before using additional commands.
Offline help, skill setup, and shell completion need no session. For retained
requests, `transactions status` uses their saved bindings; only an explicit
`--session NAME` filters them, and `LIT_SESSION` does not supply that filter.

```sh
lit connect --session my-explicit-runtime --endpoint http://127.0.0.1:7411 --actor-name Codex --actor-kind agent --runtime-vendor codex --runtime-session-id my-explicit-runtime --format json
lit projects list --session my-explicit-runtime --format json
lit projects create --session my-explicit-runtime --project-title feat/tracker --content 'Shared requirements and repository roles' --repository https://example.org/team/api --format json
lit session set --session my-explicit-runtime --project feat/tracker --format json
lit session get --session my-explicit-runtime --format json
```

Direct session selection is `--session NAME` > `LIT_SESSION`. Without either,
`connect` creates a fresh `throwaway-<UUID>` logical session and returns its full
name in `items[0].logical_session` (also visible in CLI/Markdown). Preserve and
reuse that name; subsequent tracker commands require a selected session. There
is no implicit `default` mapping, and unnamed connects do not resume one another.
An explicit `--client-id UUID` can deliberately resume an existing client under
a new logical name. Agent workflows should continue using explicit runtime-bound
names through the host adapter, rather than losing the throwaway name.

Use `--format json` for structured agent reads. `--format cli` is the default
terminal view; `--format markdown` exports document-ready text. An invocation
override does not alter the saved session preference except on `connect`, which
saves `--format` unless `--output-format` supplies the preference explicitly.
Piping never changes format.

Project titles use branch-like syntax, for example `feat/tracker`, not free-form
prose. Default prefixes are `feat`, `fix`, `refactor`, `docs`, `test`, `chore`,
`decision`, `research`, and `spec`, followed by `/` and nonempty slash-separated
ASCII letter/digit/hyphen/underscore segments starting with a letter or digit
(maximum 128 characters overall). A service can configure its accepted prefixes;
its `/v1/meta` response advertises `title_prefixes`. Issue titles can use ordinary prose.

Creation requires task authorization. If an existing project is intended, select
it rather than creating another. With no clear project, list candidates and ask.
Handle pagination explicitly; one response need not contain all results.
Session `project_id` is the confirmed identity after title resolution. Renames
preserve that ID. Never infer a project from checkout name, branch, or `.lit` cache.

The service must already be running. Connections never start it. Preserve the
returned service/client identity and reject a service change at the same endpoint.
Resume deliberately; fresh independent work uses a new runtime ID and client.

To comment on an existing issue, name its owner with `--issue`:

```sh
lit comments create --session my-explicit-runtime --issue ISSUE_ID --content 'Checkpoint and verification evidence' --format json
```

Repeat `--issue ISSUE_REF` before each comment's content/author to create an
atomic batch, including multiple comments on the same issue. Updating an existing
comment uses its own ID with `comments update --comment COMMENT_ID`; the JSON
creation input continues to use the `issue` property.
