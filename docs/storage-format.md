# Durable record storage

lit-server keeps its authority in one embedded SQLite database, owned by the
storage module (`internal/storage`): the schema, its migrations and every
query live there, and the service (`internal/service`) keeps the business
rules. The driver is `modernc.org/sqlite`, which is pure Go, so lit-server
builds with `CGO_ENABLED=0`.

## Data directory

The data directory holds:

- `lit.db`, the database, with its WAL files `lit.db-wal` and `lit.db-shm`
  while it is open;
- `lit.db.lock`, the owner lock. lit-server takes an exclusive `flock` on it,
  so only one lit-server serves a database. A second one fails with
  `storage is already owned`;
- `lit.db.clean`, the clean-shutdown marker, present only while no owner has
  the database open after a clean shutdown (or a `lit-server verify` that
  passed). Every owner removes it when it opens the database; it names the
  database files it was written for, so it does not apply to a database that
  changed since. See [clean and unclean shutdowns](recovery.md#clean-and-unclean-shutdowns);
- `backup/`, the default backup directory (`backup_dir`, `LIT_BACKUP_DIR`):
  the scheduled `VACUUM INTO` snapshots `lit-<UTC time>-scheduled.db`, of which
  the newest `backup_keep` are kept, and the snapshots
  `lit-<UTC time>-pre-migration-v<version>.db` taken before a schema migration.
  After a setup import it also holds lit-migrate's body SHA-256 manifest
  `lit-migrate-<UTC time>-bodies.json`, which retention never removes.
  A scheduled snapshot runs on a connection of its own, beside requests,
  without the write lock or a read slot. See [backups](recovery.md#backups).

An empty or missing directory gets a fresh database with a new service
identity. A directory that still holds a legacy file store (it contains
`service.json`, the legacy store's marker) is refused before anything is
written: nothing of that store is read or converted. Convert it once with the
setup import (`lit-migrate`) into a new, empty data directory instead. A
database whose schema is newer than the binary is refused; an older one is
snapshotted into the backup directory and migrated.

Offline readers such as `lit-server export`, and lit-server's own interval
export, open the database read-only (`storage.OpenReadOnly`: `mode=ro`,
`query_only`) without taking the owner lock, so they work whether or not
lit-server runs. Each reads one WAL snapshot,
which SQLite isolates from the owner's later commits. A reader never creates,
migrates or writes the database; it refuses a missing database, a legacy file
store and a schema version other than its own.
`lit-server verify` is different: it runs the full integrity scan, which may rebuild
a search index, so it takes the owner lock (`storage.OpenExisting`) and needs
lit-server stopped. It never creates or migrates a database either.

Every connection enables WAL, foreign keys and a five-second busy timeout.
The writer connection sets `synchronous=NORMAL`. In WAL mode a commit then
appends to the WAL without an fsync; the WAL is fsynced only when it is
checkpointed into the database file. The trade-off:

- An application crash (lit-server killed, panics, or exits) loses nothing: a
  commit the process acknowledged is in the WAL in the operating system's
  cache and is recovered on the next open.
- Power loss or an operating-system crash can lose the most recent
  acknowledged commits, those since the last checkpoint. The database stays
  consistent: it reopens at an earlier committed state, never with a partial
  transaction, and passes the full integrity scan, which the next start runs
  because the crash left no clean-shutdown marker.

The owner chose this to shorten the time each write holds the write lock.
Restoring `synchronous=FULL` in `internal/storage/storage.go` makes every
acknowledged commit durable again at the cost of one fsync per commit.

The writer connection checkpoints the WAL itself, inside the commit that
reaches SQLite's default of 1,000 pages. An earlier build moved checkpoints to
a background connection; in the benchmark that made writes and reads slower,
so it was reverted (ticket 2aaa11f1).

## Requests and transactions

One static route table (`internal/service/routes.go`) classifies every route
as a read or a write; the HTTP method cannot, because several reads use `POST`
(`/v1/search`, `/v1/snapshots`, `/v1/claim-snapshots`, `/v1/issue-summaries`,
`/v1/transaction-previews`). A route missing from the table is a write.

- **Reads** run in parallel, each in one WAL read snapshot for the whole
  handler, without the write lock. At most `read_concurrency` snapshots are open
  at once (default 8); further reads wait until one closes or their request
  ends. A read handler cannot write: it has no write transaction.
- **Writes** hold the process write lock and run in one write transaction
  (`BEGIN IMMEDIATE`) that reads, validates and writes, re-reading the client
  inside it. Connect, client updates, disconnect, every mutation and
  `transactions status` with the `/requests/{hash}` lookups are writes, so a
  reconciliation never reports a request that is still waiting for or holding
  the write lock as uncommitted. The response is materialized and bounded
  before the commit; whatever fails, including an oversized response after the
  writes, rolls the whole transaction back.

Previews (`/v1/transaction-previews`) and `transactions status` never commit.
Request times come from one monotonic process clock (`max(last issued, wall
clock)`): a read samples it before its snapshot begins, a write after taking
the write lock. A claim snapshot's `server_time` is therefore never later than
the clock of a claim write that follows it, so renewing for one hour from it
never exceeds the one-hour cap. A read may briefly see a claim as expired while
its renewal commits, for at most one write's duration; writes always re-check,
so a competing acquire still gets `claim_conflict`.

Lock contention from outside lit-server, and a request that ends while waiting
for the write lock or a read snapshot, are reported as `503 storage_busy`,
never as `storage_error`. Searches stop when their request is cancelled.

### Search limits

Search routes (`POST /v1/search` today) are a third class in the route table:
reads whose work is bounded by the search deadline (`search_timeout`,
`LIT_SEARCH_TIMEOUT`, default 10 s). The deadline starts when the search's read
snapshot opens; the wait for a snapshot over the read cap is bounded by the
request, as for every read. A search that reaches its deadline stops at its
next record, or SQLite interrupts its running statement, and its read slot is
freed; so searches can no longer keep other reads waiting indefinitely.

| Condition | Status | Error code |
| --- | --- | --- |
| a pattern or query longer than 1,000 bytes (UTF-8) | `422` | `validation_failed` |
| the search reached its deadline | `504` | `search_timeout` |
| the request ended first (client deadline or disconnect) | `503` | `storage_busy` |

The `search_timeout` message names the deadline and says to narrow the query
or its scope (one project instead of all projects). It is `504`, not `503`:
the service could not finish in time, and repeating the same search is likely
to time out again, while `503 storage_busy` means the same request can simply
be retried. The CLI exits 1 for it, and 2 for `validation_failed`.

## Schema

All tables are `STRICT`. The schema is version 3:
[`internal/storage/schema_v1.sql`](../internal/storage/schema_v1.sql), then
the migration to version 2
([`schema_v2.sql`](../internal/storage/schema_v2.sql), the search index of
`lit grep`) and to version 3
([`schema_v3.sql`](../internal/storage/schema_v3.sql), the word index of
`lit search`). The version is recorded in the `service` table with the
service identity. Each migration of an existing database is preceded by a
`VACUUM INTO` snapshot in the backup directory and fills its new tables from
the records.

| Table | Holds |
| --- | --- |
| `service` | service ID and schema version |
| `projects`, `project_repositories` | project fields and description; repository references |
| `issues`, `issue_labels` | issue fields and body, `parent_id`, the title key; labels |
| `issue_links` | `blocks(a, b)` once in its direction, `related(a, b)` once with `a < b` |
| `comments` | comment fields and body, the owning issue |
| `milestones`, `milestone_members` | milestone fields and body; members of the same project |
| `clients` | client records |
| `claims` | one live or expired claim per issue; release deletes the row |
| `transactions` | one row per committed durable request: request hash, canonical intent, actor, timestamp, results |
| `history` | one row per changed record per request: changed fields and the body diff |
| `grep_text` | one row per record (`owner_type`, `owner_id`): its searchable fields folded for `lit grep`, `title` and `body` |
| `grep_index` | FTS5 `trigram case_sensitive 1` table over `grep_text` as external content |
| `rank_text` | one row per record (`owner_type`, `owner_id`): its original text for `lit search` in `title`, `body` (a project's description) and `comment` (a comment's body), with its `project_id` and `issue_id` (an issue's own, a comment's issue) |
| `rank_index` | FTS5 `unicode61 remove_diacritics 2` table over `rank_text` as external content |

Records keep their exact text: titles, bodies (CR bytes, Unicode and a missing
final newline included) and RFC 3339 timestamps. Record and transaction stamps
are UTC with six fractional digits (microseconds) and strictly increasing:
each request is stamped after the newest committed transaction. Claim times
are stored as Unix nanoseconds and read back as the RFC 3339 text they were
written with.

Folded text is computed in Go and stored beside the original text, in two
folds:

- title keys and the `folded_*` columns of bodies and project descriptions:
  NFC, full case fold, NFC (`textfold.Fold`). They serve title lookup and
  uniqueness, and the discovery text filter (`q`);
- `grep_text`: an issue's and a milestone's title and body, a comment's body
  and a project's description, each as the full case fold of each scalar on
  its own, without normalization (`textfold.Grep`), which is what `lit grep`
  matches against, with NUL written as U+FFFD. Unlike NFC, this fold keeps
  substrings: a substring of a text folds to a substring of its fold.

## Search

`lit grep` reads only candidate records. The service folds the query as it
matches it (its full case fold, or for `--case-sensitive` its scalar-by-scalar
fold, which every case-sensitive match also contains) and asks the storage
module for the records whose `grep_text` may contain it:

- from three folded characters on, through `grep_index` with `GLOB`,
  with `*`, `?` and `[` written as bracket expressions (`LIKE ... ESCAPE`
  cannot use the trigram index);
- below that, by scanning `grep_text`.

The storage module writes a record's `grep_text` row in the same transaction
as the record, and triggers keep `grep_index` in step with `grep_text`. A
writer that skips `grep_text` (or a changed fold) is repaired by the next
full integrity scan, which runs at a start after an unclean shutdown and in
`lit-server verify`. The final match and the excerpts are computed by the service
on the original text, exactly as before the index, so results, excerpts and
pages are unchanged; the scan stops once a page and the start of the next one
are found.

`lit grep -E` (`"regex": true`) reads its candidates the same way. The service
extracts, with `regexp/syntax`, literals one of which every match of the
pattern contains, folds them as `grep_text` is folded (a case-insensitive rune
as every member of its RE2 case-fold orbit, since lit's full fold does not
always map an orbit to one string), and the storage module reads the records
whose `grep_text` may contain any of them (`SearchScanAny`, one index `GLOB`
per literal). The best set is used: literals of three or more folded
characters, at most 32 of them, with the longest shortest literal. A pattern
without one reads every record of the scope. The final match is RE2 on the
original text, line by line, so the prefilter only ever drops records that
cannot match.

### Ranked search

`lit search` (`POST /v1/ranked-search`) reads `rank_index`, a word index that
ignores case and diacritics (`cafe` finds `Café`; `ß` stays `ß`). The storage
module parses the search box query into an FTS5 query whose terms are all
quoted strings, so user text is never FTS5 syntax: words are ANDed,
`"phrases"`, `-word` and `-"phrase"` exclusions (one `NOT` of all of them),
`OR` between two terms (binding tighter than AND) and `word*` prefixes are the
only operators. Terms without a letter or digit are dropped; a query without
anything to search for has no results.

Every matching `rank_text` row is scored with `bm25` weighted title 10, body
3, comment 1, and grouped into its result: a comment's row counts for its
issue, so one issue is one result, and projects and milestones are results of
their own. A result's score is its best row's; results come in score order,
then by type and ID, which is also the key that a page's cursor continues
after. A `state` filter returns issues only. Each result carries the matching
fields of its own row and its three best matching comments, with the matched
words found by FTS5 `highlight()` (between Unicode noncharacter markers;
a text containing a marker is reported without offsets). The service cuts
plain-text snippets of at most 160 characters around the first match.

The storage module writes a record's `rank_text` row in the same transaction
as the record, and triggers keep `rank_index` in step with it.

FTS5 merges the segments of both indexes inside the commits that cross its
work units (`automerge`, its default of 4). An earlier build turned that off
and merged in a background merger instead; writes then queued behind its
steps and searches slowed down on less merged indexes, so opening the
database restores the default where it is off (`internal/storage/merge.go`).

## Computed membership lists

A project's `issue_ids` and an issue's `child_ids` and `comment_ids` are
computed when a record is read, from `project_id`, `parent_id` and a comment's
`issue_id`. They are not stored and not revisioned: a record's revision
changes only when its own fields change. Creating an issue does not change its
project, adding a comment does not change its issue, and reparenting does not
change either parent; those records are structural guards of the request
instead of targets. Claim policy still asks for the claim of an existing
parent that gains or loses a child.

## History

Each committed durable request stores its canonical intent and its results
once, in `transactions`, keyed by the request hash. Each record it changed gets
a `history` row with its new revision:

- the changed non-body fields, each with its JSON value before and after
  (computed membership lists are never among them);
- the body change, if any, as a forward unified diff from the previous body
  (`internal/textdiff`: `@@ -a,b +c,d @@` hunks, three lines of context,
  `\ No newline at end of file`, lines split after `\n`). The write fails
  unless the diff applied to the old body reproduces the new body exactly.

The history API rebuilds each entry's `body_hunks` (the whole body before and
after) by replaying the diffs from the empty body of the record's creation.
It writes each entry's intent, results and field differences as they are
stored, without decoding them; the bytes equal encoding the decoded values. A
stored intent must hash to its request hash, or the read fails.
History pages follow revisions; their cursors name the previous page's last
entry by timestamp and request hash, as before. `transactions status` proves a
commit by looking up the request hash in `transactions`, with the proof
`immutable_history` and the results exactly as they were returned.

A database created by the setup import (`lit-migrate`, see
[installation](installation.md#setup-import-from-a-legacy-file-store)) holds the
legacy history as it was accepted: one `transactions` row per legacy request
hash with its stored intent, timestamp and results, and one `history` row per
legacy history entry with its field differences verbatim. Legacy entries may
therefore change `issue_ids`, `child_ids` or `comment_ids`, even only those;
`lit-server verify` skips those keys. Legacy whole-body hunks are stored as
diffs like any other, each checked to reproduce its body.

## Invariants

The service validates every rule first, with the protocol's error codes and
messages, reading only the records a request involves. The schema is the
backstop: unique project titles (case-insensitive) and unique issue and
milestone title keys per project, foreign keys (including same-project keys
for parents and milestone members), link edges stored once with
`CHECK(a <> b)`, and recursive cycle checks over changed parents and blocking
edges. A violation the service missed fails the whole request; it never
corrupts data.

Use the service API or `lit` to change records. lit-server owns the database
while it runs; do not write to it from outside.

## Integrity

A start after an unclean shutdown, and `lit-server verify`, run the full
integrity scan; a start after a clean shutdown (`lit.db.clean`, see
[clean and unclean shutdowns](recovery.md#clean-and-unclean-shutdowns)) skips
it. The scan runs the FTS5 `integrity-check` of each search table (rebuilding
one that fails), recomputes `grep_text` and resynchronizes `rank_text` from the
records (rewriting missing, stale and orphaned rows, with a log line per record type),
`PRAGMA integrity_check` and `PRAGMA foreign_key_check`. The checks that only
read (both pragmas and the comparison of `grep_text` and `rank_text` with the
records) run concurrently on read snapshots while the FTS5 checks, which are
write statements, run on the writer; when anything was repaired, the text and
pragma checks run again in order on the repaired database. A
damaged database puts lit-server in degraded mode (`503 recovery_required` for
every request); damage met while serving makes it exit for its supervisor to
restart it. `lit-server verify` replays all history against current state on
demand. See [damaged tracker database](recovery.md#damaged-tracker-database).
