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
- `backup/`, the snapshots taken with `VACUUM INTO` before a schema migration.

An empty or missing directory gets a fresh database with a new service
identity. A directory that still holds a legacy file store (it contains
`service.json`, the legacy store's marker) is refused before anything is
written: nothing of that store is read or converted. Convert it once with the
setup import (`lit-migrate`) into a new, empty data directory instead. A
database whose schema is newer than the binary is refused; an older one is
snapshotted into `backup/` and migrated.

Every connection enables WAL, foreign keys and a five-second busy timeout.
`synchronous` stays at SQLite's default (`FULL`), so an acknowledged commit is
durable.

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

## Schema

All tables are `STRICT`. The current schema is
[`internal/storage/schema_v1.sql`](../internal/storage/schema_v1.sql); its
version is recorded in the `service` table with the service identity.

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

Records keep their exact text: titles, bodies (CR bytes, Unicode and a missing
final newline included) and RFC 3339 timestamps. Record and transaction stamps
are UTC with six fractional digits (microseconds) and strictly increasing:
each request is stamped after the newest committed transaction. Claim times
are stored as Unix nanoseconds and read back as the RFC 3339 text they were
written with.

Title keys (NFC, full case fold, NFC) and folded copies of bodies and project
descriptions are computed in Go and stored beside the original text.

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
History pages follow revisions; their cursors name the previous page's last
entry by timestamp and request hash, as before. `transactions status` proves a
commit by looking up the request hash in `transactions`, with the proof
`immutable_history` and the results exactly as they were returned.

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
