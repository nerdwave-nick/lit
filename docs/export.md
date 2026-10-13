# Markdown export

`lit-server export --to DIRECTORY` writes the tracker as a tree of Markdown
files for people to browse. The export is one-way: lit never reads it back,
edits are overwritten by the next export, and it has no git integration. It is
not a backup: claims, clients and transaction evidence are omitted (backups are
database snapshots).

```sh
lit-server export --to ./lit-export
lit-server export --data-dir /var/lib/lit-server --to /srv/lit-export
```

## Running beside lit-server

The export opens the database in the data directory read-only and never takes
the owner lock (`lit.db.lock`). It therefore runs the same whether lit-server is
stopped or running: a running lit-server keeps ownership and goes on serving
requests, and the export reads one SQLite WAL snapshot, so the tree shows one
exact point in history even while agents keep writing. The export never
creates, migrates or writes the database; it refuses a data directory without
a database, a legacy file store, and a schema version other than its own (start
the matching lit-server once to migrate first).

## Interval exports

lit-server can also keep an export up to date by itself. Set `export_dir`
(`LIT_EXPORT_DIR`) and `export_interval` (`LIT_EXPORT_INTERVAL`, a Go duration
such as `"10m"`); the interval defaults to `0`, which turns interval exports
off, and a positive interval needs a directory. lit-server then exports once
right after it starts listening and again every interval after the previous
run ended. A run is skipped, leaving the tree as it is, when the newest
committed transaction equals the `last_transaction` in the existing export's
`manifest.json` (and that export is of the same tracker); otherwise it writes
and switches in a new tree as described below.

Each run works exactly like `lit-server export --to`: it opens the database
read-only and reads one WAL snapshot of its own. It does not use one of the
server's read slots (`read_concurrency`), so a long export never makes requests
wait, and it never takes the write lock. A failed run, such as a target that is
not an export or a file system error, is logged
(`lit-server: interval export failed: ...; serving continues`) and retried at
the next interval; it never affects request serving and never counts as a
database failure. Degraded mode runs no exports. Stopping lit-server cancels a
run in progress, which leaves the previous tree. An on-demand export into the
same directory while a run is in progress fails with a busy error, and the
other way round.

## Replacing the tree

The tree is written to a staging directory next to `DIRECTORY` and switched
into place with an atomic directory exchange (`renameat2` with
`RENAME_EXCHANGE`), so readers see either the previous tree or the new one; the
previous tree is then removed. `DIRECTORY` must be missing, empty or a previous
export (its `manifest.json` names the generator `lit-server export`); anything
else is refused. Its parent directory must be writable: it holds the staging
directory and the lock file `.<name>.export-lock`, which makes a second export
into the same directory fail instead of interleaving. Because the directory
itself is exchanged, `DIRECTORY` cannot be a mount point; mount its parent.

## Layout

```text
README.md                          generated notice, service ID, export time, projects
manifest.json                      counts per type, export time, last_transaction
feat/lit-tui/                      a project title; its slashes become directories
  project.md                       front matter, description, generated issue index
  project.history.md
  issues/
    9f01ee0b-plan-the-manual-lit-tui.md
    9f01ee0b-plan-the-manual-lit-tui.history.md
  milestones/
    tui-1-foundation-and-browse.md
    tui-1-foundation-and-browse.history.md
```

- Issue files are `<first 8 ID characters>-<slug>.md`, flat in `issues/`, so
  reparenting or closing an issue never moves its file. The slug is ASCII,
  lowercase and hyphen-separated, at most 40 characters, cut at a word boundary
  when one is in its second half. Milestones are `milestones/<slug>.md`. When
  two milestones share a slug, the older keeps it and the newer gets its ID
  prefix appended.
- A project title segment `issues` or `milestones` below the second level
  becomes `_issues` or `_milestones`, so it never mixes with a parent
  project's records.
- `project.md` lists the project's issues, open then closed (each by creation),
  and its milestones.

## Record files

Front matter is YAML with a fixed key order, every string double quoted and
sequences as block lists; empty and null fields are omitted, and claims never
appear. Issue keys: `id`, `title`, `state`, `project`, `labels`, `assignee`,
`parent`, `children`, `blocks`, `blocked_by`, `related`, `milestones`,
`created_at`, `updated_at`, `revision`. Relationships are relative paths.

Under the issue's `# Title`, a **Links** list makes the project, the parent,
the children, the blockers (blocked by, and blocks) and the history file
clickable. In bodies, Markdown links to `lit:issue:UUID` become relative paths
(outside code fences); links the author wrote stay as written. Comments follow
under `## Comments`, oldest first, each headed by author and time with its
comment ID; headings inside comment bodies are demoted by two levels (at most
to level 6), outside code fences.

## History files

Each record file has a sibling `.history.md` with its revisions oldest first;
an issue's history file also holds its comments' histories, each under its
comment. A revision shows its number, time, actor and request hash, then its
field changes: scalars as `before → after`, lists as added and removed
elements, and relationship IDs as links. The creation entry lists its fields
and the body's line count; later body changes are fenced `diff` blocks
(unified, three lines of context). Old entries that changed only the computed
membership lists (`issue_ids` of projects, `child_ids` and `comment_ids` of
issues) are omitted.

## Determinism

An unchanged tracker gives byte-identical files on every export, except the
export time in `README.md` and `manifest.json`. `manifest.json` names the
newest committed request in `last_transaction` (`request_hash` and
`timestamp`, or `null` for an empty tracker), so a later run can tell whether
anything has committed since.
