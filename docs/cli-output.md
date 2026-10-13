# Reading lit output

Tracker commands support `--format cli`, `--format markdown`, and `--format json`.
CLI is the default unless the session has selected another format. Piping does
not implicitly switch formats. Existing saved `human` preferences read as `cli`;
new commands should use the three public names above.

## CLI: terminal output

- `projects list` shows titles, issue counts, repository references and full IDs.
- `issues list` shows titles, state, assignees, labels and full IDs.
- `comments list` shows authors, comment text, owning issue IDs and full IDs.
- `claims list` shows issue IDs, owning clients and expiry times.
- Details, session commands, history and mutation results use labeled fields.
  Multiline bodies are indented; history differences show before/after values.
  `session get` includes the session's `Stage`, or `none` when it has none.

IDs are never shortened. List tables show line breaks as `\n` and shorten free
text beyond 60 display columns, with an ellipsis and a `get` hint. Fetch the
record for its full content. Wide East Asian characters count as two columns;
combining marks count as zero. Complex grapheme clusters may occupy less space
than this conservative estimate.

Actual terminal width is detected from the output file descriptor using
`golang.org/x/term`. If a table would exceed it, output switches to complete
labeled records. Long detail lines may wrap naturally in the terminal; full IDs
are retained even in a narrow terminal. Redirected output has no terminal width
limit and uses the same bounded table previews. No ANSI styling is emitted.
Control and direction-formatting characters are displayed as visible escapes.
The small custom renderer keeps the document structure and ID preservation
explicit; a styling framework is unnecessary for these simple tables.

Mutation receipts identify changed objects and revisions, not full records.
Fetch a changed object with its resource's `get ID` command for full content.
Empty queries say there are no results. Paginated results show the next cursor;
retain the original query and filters when requesting the next page.

## Markdown: documents

```sh
lit projects list --session poc-human --format markdown > projects.md
lit issues history ISSUE_ID --session poc-human --format markdown > history.md
```

A single complete `projects get`, `issues get`, or `comments get` exports a
Markdown document: block YAML frontmatter followed immediately by the original
Markdown description/body. The body preserves its exact whitespace, line endings,
HTML and Markdown, including whether it ends with a newline. No heading, transport
footer, escaping or wrapping is added.

```sh
lit projects get test/poc --session poc-human --format markdown > project.md
lit issues get ISSUE_ID --session poc-human --format markdown > issue.md
lit comments get COMMENT_ID --session poc-human --format markdown > comment.md
```

Frontmatter includes all metadata returned with the record, including schema
version, revision, relationship IDs, nulls and any live claim fields. The project
`description` or issue/comment `body` is excluded from frontmatter and becomes the
document body. This is an API read snapshot, not a byte-for-byte clone of a stored
record: project membership remains owned by `relationships.json`, and claim data
is a point-in-time observation that grants no ownership.

Exports are not an edit/import format: `--content-file` reads the **entire file**
as body content and does not extract YAML metadata. JSON remains the machine
interface for complete response envelopes and transport metadata.

Lists retain Markdown tables with headers and separator rows. Multi-record get,
incomplete projections, mutation receipts, search excerpts, history and session
results retain structured field/value tables. Export records separately when you
need individual Markdown documents. Structured tables escape embedded Markdown,
HTML and cell separators, represent line breaks as `<br>`, and show outcome,
request hash, pagination and service time when present. Markdown values are never
shortened based on terminal width.

## JSON: scripts and exact contents

Use `--format json` for automation and lossless content. Its existing JSON schema,
field names, values and pagination envelope are unchanged; strings retain their
original control characters through normal JSON escaping. CLI and structured Markdown tables omit internal schema versions and use
readable labels; single-record Markdown documents retain the complete metadata.

The separate `setup-skills` and `workflow-session` helpers retain their own
machine-readable result formats.
