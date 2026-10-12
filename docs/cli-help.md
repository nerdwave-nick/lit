# Command help and completion

Use focused help at any command level:

```sh
lit help
lit projects help
lit projects list --help
lit milestones create --help
lit projects list -h
lit projects list help
lit help projects list
```

Family help lists its commands. Leaf help explains that operation's flags,
argument grammar and examples. Each application command has a Requirements section
and Valid forms showing alternatives: brackets mean optional inputs, `|` separates
choices, and `...` means repeatable. Flag descriptions distinguish required,
conditional and optional inputs; conditional inputs depend on the chosen form or
a saved selection, so they are not unconditional requirements.

`connect` needs no flags. It uses `--session NAME`, then `LIT_SESSION`, or creates
a fresh random throwaway name when neither is supplied. The result exposes
`items[0].logical_session` (Logical session in CLI/Markdown output); reuse it
explicitly for subsequent commands. Generated sessions are saved, not automatically
expired or deleted. Other tracker commands require `--session`
or `LIT_SESSION`; `version` needs neither, and `transactions status` only uses an
explicit `--session` as a filter. Actor defaults, saved-client resume, input-file
alternatives, project selection and flag conflicts are explained in the relevant
command help.

Help, completion-script generation, and command/flag-name completion are offline.
Existing-record completion queries the service's metadata-only completion endpoints;
it never creates or reconnects a session or installs skills.

## Shell completion

Generate completion with the same installed `lit` that you run. Completion is
available for bash (4.4 or later), zsh (5.8 or later) and fish (3.4 or later).
These commands print scripts; they never modify your shell configuration
automatically.

For the current Bash shell (no bash-completion package is required):

```bash
source <(lit completion bash)
```

For the current Fish shell:

```fish
lit completion fish | source
```

For the current Zsh shell (initialize completion first if needed):

```zsh
autoload -Uz compinit
compinit
source <(lit completion zsh)
```

`lit completion SHELL --help` explains persistent installation for your shell.
Other shells have no completion script.
Command names, flag names and fixed values such as `--format cli|markdown|json`,
`--state open|closed`, `--scope local|user|custom`, and `--agent codex|claude|both`
complete without contacting the service. Existing project, issue, milestone and
comment references are fetched on demand, including reference flags such as
`--project`, `--issue`, `--parent`, `--from` and `--to`. Flags for naming new
records do not fetch existing records.

Tab on a command that takes no operands, such as `lit projects list`, offers
that command's flags with descriptions; flags already given are omitted unless
they are repeatable. `--session` offers saved logical session names (described
by their endpoint), `--endpoint` offers the default and saved sessions' endpoints,
and `--for` offers common lease lengths, all from local state without contacting
the service. Only path-valued flags (`--file`, `--content-file`, `--cli`, and
directory-valued `--path`) complete file names. Free-form values such as titles,
labels, limits, timestamps and search patterns offer no suggestions.

Record completion works without a session. The endpoint follows `--endpoint`,
`LIT_ENDPOINT`, an optional session's remembered endpoint, then the localhost
default. An explicit project or issue project qualifier takes precedence over
an optional connected session's saved project. Unknown or disconnected clients
provide no defaults. Invalid explicit project selectors do not fall back to all
projects. Ordinary commands still require their usual session.

With no selected project, issue candidates are project-qualified, for example
`feat/api:Fix timeout`. Press Tab on an empty argument to discover them, or type
a project prefix such as `feat/api:` to narrow them. Milestones use UUIDs with
title/project descriptions when no project is selected; comments always use IDs.
Within a selected project, issue and milestone titles complete directly. `id:`
and `title:` selectors are supported, and titles that would be interpreted as
IDs, options, help or qualified references are offered with `title:`. The shell
handles quoting spaces and punctuation.

One metadata request has a one-second deadline. Unavailable services, invalid
state or a mismatched saved service identity produce no record suggestions and
do not fall back to filenames. Up to 100 matching candidates are displayed
(or the service's lower page maximum); type a longer prefix to narrow the result.
There is no persistent completion cache or local state write.

See the [completion API](completion-api.md) for the metadata projection and limits.

## Ranked search

`lit grep` finds literal substrings; `lit search QUERY` finds words and ranks
the results by relevance, most relevant first:

```sh
lit search --session demo 'connection timeout'
lit search --session demo --all-projects --state open '"claim renewal" -flaky'
lit search --session demo -- '-flaky timeout'
```

Words match whole words, ignoring case and diacritics (`cafe` finds `Café`),
and every word must match. `"a phrase"` matches its words in order (an
unclosed quote runs to the end), `-word` or `-"a phrase"` excludes, `a OR b`
matches either (`a OR b c` means `(a OR b) c`), and `word*` matches words
beginning with `word`. Everything else is plain text, so `C++`, `net/http`,
`NOT` or `title:x` never cause errors. Titles count most, then bodies and
project descriptions, then comments.

Each result is an issue, a project or a milestone. An issue's result carries
its matching title and body and its best three matching comments (with their
IDs), and `comment_matches` counts them all. Matches are plain-text snippets
of at most 160 characters of the field around its first match, with
`highlights` as byte offsets into the snippet; on a terminal the CLI shows the
matched words in bold (unless `NO_COLOR` is set). `--state open|closed`
returns only issues in that state. Pages hold 20 results by default (at most
100, `--limit`); `--cursor` continues the same query in rank order. Like
grep, the search runs under the server's search deadline and accepts queries
of at most 1,000 bytes.

## Batch and literal arguments

Arguments are parsed in order against lit's command grammar, so flag order is
meaningful. An item flag such as `--project-title`, `--issue` or `--from` begins
another atomic item, and owns that item's fields: each field may be given once per
item (accumulating fields such as `--label` or `--to` any number of times); a
field repeated within one item fails. For example:

```sh
lit projects create --session demo \
  --project-title test/one --content 'First' \
  --project-title test/two --content 'Second'
lit issues link --session demo \
  --from A --to B --to C --relation blocks \
  --from B --to C --relation related
lit grep --session demo -- --help
```

The `grep` example searches for literal `--help`. Use `issues get -- help` to fetch an issue titled `help` (bare `issues get help` opens help). Arguments after `--` remain literal operands. Likewise `grep help` searches for
`help`, and `--content help` supplies content. Workflow passthrough uses an explicit
separator, for example `workflow-session --host codex --runtime-id ID run -- issues list`.

Comment creation uses `comments create --issue ISSUE_REF --content TEXT`; repeat
`--issue` to begin another comment, even for the same owner. Comment updates use
`comments update --comment COMMENT_REF --content TEXT`. JSON creation items
retain their `issue` property.

Milestones are project-scoped named worksets. Create a milestone with
`milestones create --project REF --milestone TITLE`; repeat `--milestone` to
start another item and repeat `--issue REF` for its members. Members resolve
inside the selected project and are stored as stable issue IDs. Update a
milestone with `milestones update REF` and `--add-issue` or `--remove-issue`;
`--clear issues` removes all membership. Flag inputs and typed `--file` JSON are
separate atomic modes. `issues list --project REF --milestone M1` filters to
members of that milestone and cannot span projects. Milestone list tables show
member counts and current progress; progress is a read-only projection. A single
milestone `get --format markdown` exports durable YAML metadata and the exact
body, without transient progress. Mutations use the same prepare/transaction
boundary as other records.

CLI argument errors retain the JSON error contract with `--format json` and exit
code 2. Service and reconciliation outcomes retain their existing exit codes.

## Existing record references

A reference selects an existing record; it is not the syntax for naming a new
project or issue. Title matching is case-insensitive. A UUID prefix must be
unique; use the full UUID when a prefix is ambiguous.

| Record | Accepted reference forms |
| --- | --- |
| Project | Exact title, UUID, unique hexadecimal UUID prefix, `title:TITLE`, or `id:UUID_PREFIX` |
| Issue | Title in the selected project, UUID, unique UUID prefix, `title:TITLE`, `id:UUID_PREFIX`, or `PROJECT_TITLE:ISSUE_REF` |
| Comment | UUID or unique UUID prefix, optionally preceded by `id:`; no title or project-qualified form |

`title:` forces literal title matching, and `id:` forces ID matching. These
resolve title-versus-ID ambiguity without renaming a record. For issues, a
project-qualified reference such as `feat/api:title:Review API` uses that
project's title and scopes the following title or ID prefix to it. An unqualified
issue UUID or ID prefix remains global even when a session project is selected.
A plain issue title or `title:TITLE` needs `--project` or a saved project selection.

The issue resolver treats a colon after text containing `/` as project
qualification. To address a title that itself contains slashes and colons, force
literal title matching and quote the complete argument:

```sh
lit issues get --session my-session --project test/poc 'title:Refactor a/b: cleanup'
lit issues get --session my-session 'feat/api:title:Review API'
lit comments get --session my-session id:01234567
```

The final example needs a matching unique comment ID prefix. Flags explicitly
named `--id`, `--issue-id` or `--owner-client-id` are query UUID fields, not this
reference grammar. Use each command's References section for its applicable
record types. See [recovery limitations](recovery.md) for uncertain outcomes;
selector disambiguation does not resolve mutation uncertainty.

`lit session get` shows both the selected logical session name (`logical_session`)
and its client UUID (`client_id`). The `--session-id` property selector retains its
client UUID meaning; property-filtered results contain only the selected fields.
