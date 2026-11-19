---
status: proposed
---

# SQLite as lit-server's storage authority, with a one-way Markdown export

lit-server kept its authority as one directory per record: `content.md` with YAML
front matter, `relationships.json`, and immutable JSON history files, written
through a write-ahead journal. Request cost grew with the whole store. Every
request re-read and SHA-256-hashed every file to detect edits made outside lit,
and most request paths also decoded every record of a type: lists, mutations,
search, claims, and resolving a title. Per-record history embedded the whole
transaction intent, so one batch was copied into the history of every record it
touched. At 7,500 issues every request took 16-25 s, and history made up 6.68 GB
of a 6.7 GB store. Editing those files behind the service was never supported
(`docs/storage-format.md`), so the per-request scan guarded a format nobody was
meant to edit.

We replace the file store with an embedded SQLite database (`modernc.org/sqlite`,
pure Go, FTS5), owned by a storage module (`internal/storage`) that holds the
schema, schema migrations and every query. The database is the only authority:
records change only through the service API, and the external-edit fence, the
journal and the per-request scan go away. Markdown remains as a one-way,
human-oriented export that lit never reads back.

## Decision

- **Transactions.** Writes, connect/disconnect and transaction-status
  reconciliation are serialized. Each takes a Go write lock and runs in one
  `BEGIN IMMEDIATE` transaction that reads, validates and writes; dry runs roll
  back. Reads run in parallel, each in one WAL snapshot. One monotonic process
  clock replaces the per-request clock that the global mutex used to protect.
  An exclusive lock file keeps one lit-server per database.
- **History.** Each request's intent is stored once, in a transactions table
  keyed by request hash. Per-record history rows hold field differences and the
  body change as a forward unified diff (three lines of context, applied
  strictly, checked at write time). The history API still returns full
  before/after bodies, rebuilt by the server. A request-hash lookup proves a
  commit.
- **Membership lists.** A project's issue IDs and an issue's child and comment
  IDs are computed when read, no longer stored and revisioned. A record's
  revision changes only when its own fields change.
- **Integrity.** Rules are validated in Go first, with SQL constraints
  (unique title keys, composite same-project foreign keys, edges stored once)
  as a backstop. Every startup runs `PRAGMA integrity_check`, a foreign-key
  check and an FTS check; derived search indexes rebuild automatically.
  `lit-server verify` replays all history on demand. Corruption at runtime makes
  the process exit so the supervisor restarts it; a failing startup check leaves
  it in a degraded `503 recovery_required` mode instead of a restart loop.
- **Search.** `lit grep` keeps its literal, fully case-folded semantics through a
  Go-folded column with a trigram index, and gains `-E` for RE2 regex. A new
  `lit search` ranks with `bm25`, ignores diacritics, and never passes raw FTS5
  syntax.
- **Export.** A one-way Markdown tree for people, with history, on demand or on
  an interval. It has no git integration and is not a backup; backups are
  `VACUUM INTO` snapshots.
- **Migration.** A separate one-time setup binary imports a stopped copy of the
  file store into an empty database. lit-server never links legacy code and
  refuses to start on a directory that still holds a legacy store. The old
  directory stays untouched as the rollback.
- **Deployment.** A docker compose stack runs one lit-server service with host
  networking, so the loopback-only listen rule is unchanged. systemd socket
  activation remains the non-container path.

## Considered options

- **Keep the files and fix the scan** (stat fingerprints, a watcher, or a
  rebuildable SQLite index beside the files). This removes the hashing but not
  the whole-type reads, and a file store with an index needs reconciliation
  that a single authority does not. External editing was never a feature.
- **PostgreSQL 18.** Sound and it suits a compose stack, with native regex
  acceleration (`pg_trgm`) and many writers. It adds a second service,
  credentials, manual major-version upgrades and a database server for every
  test. For one user with many agents, measured at 10× the volume target, SQLite
  was as fast or faster on every query lit makes; ranked top-20 over a broad
  query took 11 ms with `bm25` against 375-518 ms with `ts_rank_cd`. PostgreSQL
  remains the path if lit-server must serve several users or run as several
  instances.
- **MySQL.** Not operated here; its default full-text parser drops short words
  and splits on punctuation, which suits technical text badly.
- **An external search engine** (Bleve, Meilisearch, Typesense). A second
  consistency domain to keep in step, for no gain at this size. Bleve's regex
  matches whole terms and cannot provide `grep`.
- **A storage abstraction over several engines.** Not built speculatively;
  keeping SQL portable and storage behind one module keeps a later move a
  migration rather than a rewrite.

## Consequences

- Request cost no longer grows with total history. A gated end-to-end
  benchmark (100 projects of about 15 issues, and 10× that) checks latency
  targets before each milestone merges.
- The protocol changes in one place: membership lists stop bumping their
  owner's revision, and creating an issue no longer targets its project.
  Saved plans prepared before the migration must be prepared again.
- Requests retained by clients that never committed before the migration end up
  `uncertain`, as `docs/recovery.md` allows.
- The journal fault-injection tests retire with the file store; atomicity rests
  on SQLite transactions and is tested by triggering real failures.
- Regex mode uses RE2's simple case folding, so `ss` does not match `ß` there,
  unlike literal `lit grep`.
- Remote access and authentication remain future decisions; lit-server stays
  loopback-only.

Evidence: `docs/research/lit-storage-and-search.md`. Decisions: tracker project
`feat/lit-sqlite-storage`, map "Plan the SQLite storage authority for
lit-server".
