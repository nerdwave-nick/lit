# Uncertain tracker outcomes

An interrupted request may have committed even when the CLI did not receive its
response. The client preserves pending request evidence under its private
`LIT_STATE_DIR` (or the platform default). This is separate from service data.
`lit transactions status --format json` inspects retained requests and never
replays them. Only an explicit `--session NAME` filters the result; neither
`LIT_SESSION` nor the working directory supplies that filter. `--file PATH`
selects one existing retained request envelope, not a mutation input file.

An `uncertain` result retains evidence and exits 4. Repeating status cannot
always establish an outcome:

- If the response to a first connection is lost before the client identity is
  received, the pending record has no client ID. Status reports
  `registration_identity_not_received` without querying the service for that
  request. There is no initial-scope registration lookup/repair operation.
- A durable request can be proven committed from immutable history. When that
  history evidence is absent and original preconditions no longer hold, such as
  after another edit, the service reports `original_preconditions_no_longer_valid`.
  It cannot conclude that replay is safe merely from current content.
- Disconnected clients and operational effects without durable history can also
  leave uncertainty. Reconnecting or obtaining another claim does not supply
  missing proof about an earlier request.

These are supported conservative outcomes, not promises that retrying status
will eventually clear the record. There is currently no supported command to
retire unresolved evidence, repair a registration identity, or override an
uncertain result. Do not delete/move pending files, create a replacement client,
or replay the operation as a workaround.

For a persistent uncertain outcome, stop dependent mutations and escalate to the
human owner. Preserve the pending file, any returned proof/error, request and
service/client identifiers, repository changes, and an explicit handoff describing
what is confirmed and what remains unknown. Record the intended operation and
whether claims or ticket updates might have partially succeeded. Keep unrelated
work separate from the unresolved operation; do not claim completion or resume
claimed work by assuming it failed.

A human should inspect the preserved evidence and accepted history before
choosing a next step. If those do not prove an outcome, retain the unresolved
state and seek an explicit repair design or maintainer assistance. Human
escalation alone does not authorize evidence deletion, bypass, or replay; the
initial delivery provides no verified manual retirement procedure. The workflow
adapter's `reconcile` only adopts a confirmed existing binding, and does not
repair these cases.

# Damaged tracker database

lit-server's database fails closed. Three mechanisms cover damage; the
reference is the storage module (`internal/storage`: `CheckIntegrity`,
`Verify`, `ErrFatal`). Backup snapshots, which lit-server takes itself, bring
a tracker back.

## Startup checks

Every start runs, before the first request is served:

1. the FTS5 `integrity-check` of every FTS5 (search) table, compared with its
   content table. Search tables are derived from rows of the same database, so
   one that fails is rebuilt automatically, with a log line on stderr such as
   `lit-server: search index NAME failed its integrity check (...); rebuilt it`.
   Only a rebuild that fails, or a table that still fails afterwards, is a
   startup failure;
2. the folded text that the search index of `lit grep` indexes (`grep_text`).
   It is recomputed from every record; missing, stale and orphaned rows are
   rewritten, and the search index follows, with a log line such as
   `lit-server: folded text of N issues was stale; rewrote it`;
3. `PRAGMA integrity_check` and `PRAGMA foreign_key_check` of the whole
   database.

The full check reads the whole database, so a start takes longer as the
database grows; with socket activation it delays the first request after a
start.

## Degraded mode

When the database cannot be opened because it is damaged (`SQLITE_CORRUPT`,
`SQLITE_NOTADB` or an I/O error) or a startup check fails, lit-server does not
exit. It stays up in degraded mode, logs the reason, and answers every request,
`/v1/meta` and every read included, with `503 recovery_required`:

```json
{"error": {"code": "recovery_required",
           "message": "lit-server is in degraded mode: REASON. An operator must restore a backup or repair the database, then restart lit-server; see docs/recovery.md",
           "details": {"mode": "degraded", "reason": "REASON"}},
 "server_time": "..."}
```

No route stays readable: data from a damaged database cannot be trusted, and a
read that met the damage would make lit-server exit and restart into the same
state. Every write is refused with this error before anything is changed.
Clients report the error; `recovery_required` always means a persistent state
that needs an operator, never a condition to retry.

lit-server stays in degraded mode until an operator restores a backup or
repairs the database and restarts it. When the database could be opened,
degraded lit-server keeps the data directory's owner lock, so stop it before
repairing.

## Damage while serving

When a request meets `SQLITE_CORRUPT`, `SQLITE_NOTADB` or an I/O error,
lit-server logs `lit-server: database failure while serving; exiting ...` and
exits with status 1. The supervisor restarts it (systemd `Restart=on-failure`;
a container restart policy such as `restart: unless-stopped`), and the startup
checks run again: a rebuilt search index lets it serve normally, damage to the
records puts it in degraded mode. Requests in flight lose their connection;
mutations among them are uncertain outcomes that clients reconcile with
`lit transactions status`, as after any crash.

## `lit-server verify`

`lit-server verify [--data-dir DIR] [--config FILE]` verifies a database on
demand; it never runs at startup. It runs the startup checks (including the
automatic search index rebuild and folded text rewrite), then replays every record's field and body
history from its creation, in one read snapshot, and compares the result with
the record's current state: every revision from 1 must be present, each stored
"before" value must match the replay, each body diff must apply, and the replay
must equal the current fields and body. The computed membership lists (a
project's `issue_ids`, an issue's `child_ids` and `comment_ids`) are skipped,
because history kept verbatim from the legacy store may change them; a
milestone's members are its own field and are compared. History is not
replayed when the startup checks fail.

Each problem is one line on stdout, naming the record type and ID. Exit status
0 means the database verified, 1 that a check or the replay found a problem or
the database could not be opened, 2 that the arguments were invalid.

`verify` takes the data directory's owner lock, because its startup checks
may rebuild a search index, so lit-server must be stopped (unlike
`lit-server export`, which only reads): while it runs, `verify` fails with
`storage is already owned`. It never creates
or migrates a database; a database with an older schema is migrated by
starting lit-server on it first.

To bring a tracker back from degraded mode, restore a backup snapshot (see
[restore a snapshot](#restore-a-snapshot)). Run `verify` after any repair, too.

## Backups

lit-server takes its own backups: consistent `VACUUM INTO` snapshots of the
database, written while it serves.

| Setting | Environment | JSON field | Default |
| --- | --- | --- | --- |
| backup directory | `LIT_BACKUP_DIR` | `backup_dir` | `<data dir>/backup` |
| interval | `LIT_BACKUP_INTERVAL` | `backup_interval` | `24h` |
| retention | `LIT_BACKUP_KEEP` | `backup_keep` | `14` |

- **Scheduled snapshots** are named `lit-<UTC time>-scheduled.db`, for example
  `lit-20261011T031500.123456Z-scheduled.db`. The interval is a Go duration
  (`24h`, `90m`); `0` turns scheduled snapshots off. The first snapshot after a
  start follows the newest scheduled one in the directory, so restarts do not
  postpone it; without one, it is taken right after the start. A snapshot
  holds the committed state when it starts, and only lit-server's user can
  read it (mode 0600). It runs beside requests: it takes neither the write
  lock nor a read slot.
- **Retention** keeps the newest `backup_keep` scheduled snapshots and removes
  older ones after each successful snapshot. It removes only regular files with
  exactly that name; anything else in the directory is left alone, including
  pre-migration snapshots, which are never removed. Give each lit-server its own
  backup directory: retention cannot tell two trackers' snapshots apart.
- **Pre-migration snapshots** (`lit-<UTC time>-pre-migration-v<old version>.db`)
  are taken in the same directory before a schema upgrade.
- **Failures** are logged (`lit-server: scheduled backup failed: ...; serving
  continues`) and retried at the next interval; they never affect requests and
  never stop lit-server. A failed snapshot leaves no file (it is written under
  a temporary `.lit-...partial` name and renamed when complete), and retention
  only runs after a snapshot succeeded. Each success logs
  `lit-server: backup written to PATH`.
- **Degraded mode takes no snapshots**, so a damaged database never pushes the
  good snapshots out of retention.

Snapshots in the default directory share the data directory's disk. Copy them
elsewhere, or point `backup_dir` at another disk, to survive losing it.

## Restore a snapshot

A snapshot is a complete database file without WAL files. To restore one:

1. Stop lit-server (for example `systemctl --user stop lit.service`, and
   `lit.socket` too under socket activation, so a connection cannot start it
   again). `verify` and the restore need the data directory's owner lock.
2. Move the current `lit.db`, and `lit.db-wal` and `lit.db-shm` if present, out
   of the data directory. Keep them for analysis rather than deleting them.
3. Copy the snapshot into the data directory as `lit.db`, readable and writable
   by lit-server's user.
4. Run `lit-server verify` with the same data directory (`--data-dir`,
   `LIT_DATA_DIR` or the config file). It runs the startup checks and replays
   all history; continue only when it exits 0. If it reports problems, try the
   next older snapshot.
5. Start lit-server. Its startup checks run again before it serves.

This order verifies the snapshot before anything is served from it.

Everything committed after the snapshot was taken is lost. Clients reconcile
as after any interruption: `lit transactions status` reports a request
committed after the snapshot as not committed, so its change can be applied
again. A client that connected after the snapshot is unknown to the restored
tracker and connects again. Claims return to their state in the snapshot; most
will have expired.

A pre-migration snapshot has the older schema, which `verify` refuses. Verify
it with the older lit-server, or start the current lit-server on it once (it
migrates the restored database after another pre-migration snapshot), stop
it, and run `verify` before serving clients again.
