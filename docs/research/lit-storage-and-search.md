# lit storage and search: moving the authority into a database

Investigated 2026-10-11 at `main` b9beeb5. This is research evidence, not an
approved implementation plan. The owner's answers to the questions it raised
are recorded under [Owner decisions](#owner-decisions-2026-10-11).

## Question and scope

lit-server keeps its authority in Markdown files and rescans the whole store on
every request (tracker project `fix/lit-server-store-scan`, issue
`0c93d0d5`). The owner wants to take lit further: a database becomes the
authority, lit-server ships with a docker compose stack, full-text search and
efficiency become first-class, and Markdown remains available as a
**one-directional export** (on demand, optionally on a configurable interval).
Bidirectional sync is out of scope; periodic export is a nice-to-have.

The baseline was a preliminary handoff (`.scratch/lit-evolution/issue-tracker-storage-search-research-handoff.md`,
local, not committed). It was written without inspecting this repository. This
document checks it against the code, runs its proposed acceptance cases against
SQLite and PostgreSQL, and adds external sources. Claims are marked:
**verified** (code read or command run here), **source** (cited documentation),
**inference**, or **unverified**.

## Conclusion

1. **Make a database the authority; keep Markdown as an export.** The
   repository already says that editing files behind the service is not a
   supported path (`docs/storage-format.md`: "Use the service API or `lit` to
   mutate accepted records"). The per-request scan is therefore a safety fence
   around a format nobody is meant to edit, not a feature to preserve. This
   answers the handoff's main open question (whether external editing is
   required): it is not.
2. **Fixing `Store.Check()` alone would not fix request cost.** Almost every
   request path also reads every record of a type into memory (see
   [Whole-store reads](#whole-store-reads-beyond-the-scan)). Request cost grows
   with store size until reads become indexed queries. A database does this
   naturally; patching the file store would mean building an index beside it.
3. **Engine: embedded SQLite (FTS5) through a pure-Go driver, shipped as a
   single-service compose stack (owner decision, 2026-10-11).** For one user
   with many agents, measured at 10× the accepted volume target, SQLite was as
   fast or faster on every query lit makes. It keeps the single static binary
   and the in-process test suite, and its compose stack has one service instead
   of two or three. PostgreSQL 18 remains the alternative if lit-server must
   later run as several instances or needs regex-accelerated search. See
   [Engine comparison](#engine-comparison).
4. **Keep `lit grep`'s exact semantics with a folded shadow column.** Neither
   engine's tokenizer reproduces lit's current matching (full Unicode case
   folding, so `strasse` finds `Straße`). Indexing an application-folded copy
   of each text field does reproduce it. This was verified on SQLite; the
   PostgreSQL equivalent is inference. Add ranked word search as a separate
   capability, not as a replacement.
5. **Store each transaction's intent once.** A `transactions` table keyed by
   request hash removes the history amplification (6.68 GB of the 6.7 GB probe
   store). It also gives `transactions status` an exact proof: the row exists
   if and only if the transaction committed.

## Current implementation (verified)

| Concern | Today | Where |
| --- | --- | --- |
| Authority | One directory per record: `content.md` (YAML front matter + body), `relationships.json`, `history/*.json`; `clients/*.json`; claims at `issues/<id>/claim.json` | `internal/service/records.go:93-117`, `claims.go:20` |
| Atomicity | Write-ahead `journal/pending.json` + `commit.json` marker, replayed at startup | `internal/store/store.go:207-336` |
| External-edit fence | `scan()` reads and SHA-256-hashes every file; `Check()` compares with the fingerprints from the last commit; a mismatch returns `503 recovery_required` | `store.go:139-195`, called at `service.go:141` and `store.go:216` |
| Concurrency | One process-wide mutex around every request, reads included; `flock` on `.lock` | `service.go:118`, `store.go:58-63` |
| Startup | `InitRecords` replays every history entry and compares the reconstruction with current state | `record_history.go:113+` |
| History | Per-record immutable entries. Each embeds the full transaction intent, and its file name and canonical hash are checked on read | `record_history.go:76-101` |
| Body history | One hunk holding the whole body before and after | `record_history.go:62-70` |
| Uncertain outcomes | `transactions status` proves "committed" by finding the request hash in a target's history | `reconcile.go:51-107` |
| Search | `POST /v1/search`: literal substring, Unicode full case folding (`cases.Fold`), first match per field, line numbers, context lines, 400-rune excerpt cap, sorted by project/type/ID, unranked, cursor-paged | `search.go` |
| Deployment | Single Linux binary, loopback listener, optional systemd socket activation; no authentication | `packaging/`, `docs/installation.md` |
| Clients | `lit`, `lit-tui` and agents use only the HTTP API. Nothing outside `internal/service` reads the store | `grep` of `internal/` |

The last row matters for scope: the storage change stays behind the existing
API. The integration suite (`integration/`, 30 files) exercises lit through
that API and can serve as a black-box regression net for the swap.

### Whole-store reads beyond the scan

`Store.Check()` is not the only per-request cost proportional to the store. Each
of the following loads every record of one or more types from disk on every
call (verified by reading the call sites):

- `ReadRecordState()` (all projects, issues, comments, milestones): every
  mutation (`record_transaction.go:519`, `:787`), list/discovery
  (`record_http.go:347`), search (`search.go:139`), milestone projection
  (`claim_http.go:217`).
- `s.Issues()`: resolving any issue **by title** (`records.go:342`), claim
  listing (`claims.go:85`), claim policy (`claim_policy.go:93`), completion
  (`completion.go:97`).
- `s.Records(type)`: list queries (`record_http.go:340`), completion.

The triage issue names only the scan. A stat-based fingerprint or a watcher
would remove the hashing but leave these reads; at the probe's 7,500 issues they
would still decode thousands of files per request.

### Live baseline (verified 2026-10-11)

Against the running local tracker (13 MB, about 1,500 files, 83 issues on
2026-10-09): `GET /v1/meta`, which touches no records, took 50-64 ms (three
runs). An all-project `lit grep timeout` took 0.43 s and a full one-project
`issues list --all` took 0.29 s, both including CLI start-up. The isolated probe
recorded on the triage issue reached 16-25 s per request at 7,500 issues.

## Requirements

From the owner (this request):

- Database as authority; deployment through docker compose and still easy.
- Markdown export kept for compatibility, one-directional, optionally periodic
  with a configurable interval. Nice-to-have, not a core requirement.
- Full-text search, usability and efficiency are the core requirements.

Already accepted elsewhere:

- First-release TUI target: about 1,000 issues per project and bodies up to
  64 KB (decision `8a79bf42`, 2026-10-09). The bench below uses 10× that.
- Fail-closed safety, immutable auditable history, and a migration plus ADR for
  any storage-format change (project `fix/lit-server-store-scan`).
- lit-server serves one user's projects and agents, local first, remote later
  (`CONTEXT.md`).

## Search acceptance cases (verified)

The handoff's candidate cases were run on sqlite3 3.53.4 and on
`postgres:18-alpine` (PostgreSQL 18.6). Scripts and corpus are in the session
scratchpad. They are reproducible from the descriptions below but were not
committed.

"lit today" is `search.go` behaviour. "Trigram" is FTS5
`tokenize='trigram remove_diacritics 1'` with a quoted phrase `MATCH`; "words"
is FTS5 `unicode61 remove_diacritics 2`. PG ILIKE is plain `ILIKE`; PG FTS is a
`to_tsvector('simple', …)` column queried with `websearch_to_tsquery('simple', …)`.

| Query | lit today | SQLite trigram | SQLite words | PG ILIKE | PG FTS `simple` |
| --- | --- | --- | --- | --- | --- |
| `net/http` | match | match | syntax error unless quoted | match | match (`file` token) |
| `C++` | match | match | syntax error; quoted, it means token `c` | match | means `c` |
| `Go`, `UI` (2 chars) | substring match | **no match** (under 3 chars) | word match | substring match | word match |
| `strasse` vs `Straße` | **match** (full case fold) | no match | no match | no match | no match |
| `cafe` vs `Café` | no match | match (`remove_diacritics`) | match | no match | no match (needs `unaccent`) |
| `foo.bar` in `foo.bar.baz()` | match | match | — | match | **no match** (parsed as one `host` token) |
| `a_b`, `100%` | literal | literal | — | wildcards unless escaped | — |
| `APP-123` | match | match | — | match | phrase `app <-> -123` |
| ranked phrase `"connection timeout"` | not supported | — | phrase + `bm25` | — | `<->` + `ts_rank_cd` |

What follows:

- **Two capabilities, not one.** A grep-like literal search (today's contract,
  which agents rely on for paths, identifiers and error text) and a ranked word
  search for prose. No single tokenizer serves both.
- **Engine folding differs from lit's.** Both engines miss `strasse`→`Straße`,
  which lit matches today. A *folded shadow column* fixes this: store
  `NFC(casefold(NFC(text)))` computed in Go, index it with a case-sensitive
  trigram index (FTS5 `trigram case_sensitive 1` or `gin_trgm_ops`), fold the
  query the same way, and run it as a `GLOB` (SQLite) or `LIKE` (PostgreSQL)
  substring. With `case_sensitive 1`, FTS5 accelerates only `GLOB`, and `LIKE
  … ESCAPE` never uses the index [S3]. SQLite literal search should therefore
  use `GLOB` with `*`, `?` and `[` escaped as bracket expressions. Verified on the
  10k-issue corpus: folded `strasse` returned exactly the 34,224 rows of a
  reference `LIKE '%Straße%'` scan. The final match and excerpt still go through
  the existing Go code, so the result is the same as today's.
- **Escape user input.** Raw FTS5 query syntax fails on `C++` and `net/http`.
  LIKE needs `%`, `_` and the escape character escaped; GLOB needs `*`, `?` and
  `[` escaped. Ranked search should quote user terms by default and offer
  operators only explicitly.
- **Short queries (under 3 characters)** cannot use a trigram index in either
  engine. Today's behaviour (substring) can still be kept with a sequential scan
  of the folded column, which costs about 0.1 s at 10× target (below).

## Efficiency at 10× the target (verified)

Synthetic corpus: 20 projects, 10,000 issues (60-600 words, about 2.5 KB) and
50,000 comments (about 0.9 KB); 70 MB of text, roughly 80× today's live
tracker. Technical tokens (`net/http`, stack traces, `C++`, German words) were
mixed in. **Limitation:** the vocabulary is tiny (about 80 words), so word
queries match most rows (a worst case for ranking) and trigram indexes are
smaller than real text would produce. Same laptop; times are single warm runs
from `.timer on` and `EXPLAIN ANALYZE` (server-side, no client round trip).

| Operation | SQLite 3.53.4 | PostgreSQL 18.6 |
| --- | --- | --- |
| Bulk load 60k rows | 1.3 s | 3.0 s |
| Build search indexes | 7.2 s (trigram ×2, words) | 20.5 s (tsvector + 2 trigram GIN) |
| Rare literal, indexed (`UNIQUENEEDLE-4242`) | 0.26-0.29 ms (trigram `LIKE`) | 0.5-0.7 ms (trigram `ILIKE`) |
| Rare literal, **no index** (full scan of all bodies) | 131-149 ms | — |
| Common literal `net/http`, first 100 | 0.1 ms | 2.3 ms |
| Common literal `net/http`, count all 15,964 | 4-32 ms | 193 ms |
| Line-anchored regex | not native (see sources) | 114 ms (trigram-assisted `~`) |
| Ranked top 20, query matching 88% of issues | **11 ms** (`bm25`) | **375-518 ms** (`ts_rank_cd` must rank every match) |
| Ranked top 20, selective query | — | 0.1 ms |
| List one project's issues, no bodies | 0.15 ms | 1.6 ms |
| One issue's comments | 0.14 ms | 0.7 ms |
| Update one body + maintain trigram index | 0.7 ms | ~5 ms incl. `docker exec` round trip |

Index sizes for 44 MB of comment text: FTS5 trigram `detail=none` 18.6 MB,
`detail=full` 101 MB; PG `gin_trgm_ops` 23 MB. **Trap found:** with
`detail=none`, trigram phrase `MATCH` fails ("phrase queries are not supported
(detail!=full)"), but `LIKE`/`GLOB` remain index-accelerated. So `detail=none`
is enough for grep-like search, at about a fifth of the size.

Reading: at this scale both engines turn every current O(store) path into
sub-millisecond lookups. Engine speed does not decide the choice. The one
qualitative difference is broad ranked queries, where PostgreSQL has no top-k
shortcut and ranks every match.

## Engine comparison

MySQL is dropped: it is not operated here, and its default FULLTEXT parser
ignores short words and splits on punctuation (handoff, [S8]), which is worse
for technical text. A dedicated search engine is also dropped for now (see
[Prior art](#prior-art)).

| Criterion | SQLite (embedded) | PostgreSQL 18 (service) |
| --- | --- | --- |
| Go driver, cgo | `modernc.org/sqlite` v1.60.1: pure Go, FTS5 on by default, SQLite 3.53.4 [D1] | `jackc/pgx/v5` v5.11.0: pure Go [P1] |
| Static binary / cross-build | Kept (`CGO_ENABLED=0`) | Kept |
| Deployment | Inside lit-server; one compose service and a volume | Second service; credentials, healthcheck, startup order |
| Major upgrades | File format stable; library bump only | Not automatic in Docker; dump/restore or `pg_upgrade` [P5] |
| Tests | In-process temp files, as today's suite does | Need a running server (container) for every test package touching storage |
| Concurrency | WAL: readers parallel to one writer, same host only [S2]; writes `BEGIN IMMEDIATE` + `busy_timeout` [D3] | MVCC, many writers, multiple hosts |
| Literal / grep search | FTS5 trigram accelerates `LIKE`/`GLOB`; folded column + `GLOB` verified | `pg_trgm` accelerates `LIKE`/`ILIKE`/regex [P3] |
| Ranked search | `bm25`, `snippet`, `highlight` built in; English-only stemmer [S3] | `ts_rank_cd`, `ts_headline` (slow, use on top N) [P2]; Snowball `german`; no top-k (measured above) |
| Regex | None built in; register Go `regexp` as a function [D1] | Native, trigram-assisted |
| Backups | `VACUUM INTO` snapshot on a timer [D4]; Litestream v0.5.17 for continuous replication [D5] | `pg_dump` sidecar or cron |
| Integrity | `PRAGMA integrity_check`, FTS `integrity-check` | Data checksums on by default in `postgres:18-alpine` (verified) |
| Remote later | One lit-server per database; never on a network filesystem [S2] | Database can live elsewhere; several lit-server instances possible |

Driver notes [D1]:

- `modernc.org/sqlite` is the conservative choice. Its own benchmarks put it at
  1.3-2.0× the CPU time of C SQLite, irrelevant at these sizes. Its known
  footgun: the application's `go.mod` must pin the same `modernc.org/libc`
  version that the sqlite module pins.
- `ncruces/go-sqlite3` has the best extension API: Go `regexp`, and FTS5
  tokenizers written in Go, which could implement lit's folding inside the
  index. But it is still v0.x, and in March/April 2026 it moved from wazero to
  wasm2go, with a stability warning from its author.
- `mattn/go-sqlite3` needs cgo and the `sqlite_fts5` build tag.

**Recommendation (inference).** SQLite fits the workload: one user, many agents,
short serialized writes, a corpus measured in megabytes, and a test suite built
around fast in-process durability checks. It needs no second service, no
upgrade procedure and no credentials. In this workload it is as fast or faster
on everything lit does today.

PostgreSQL earns its cost if lit-server becomes a shared multi-user or
multi-instance service, or if regex search becomes a requirement. Choosing
SQLite now does not rule that out. Keep SQL portable (no SQLite-only types in
the domain), keep storage behind the service's existing record functions, and
the later move is a migration, not a rewrite. Do **not** build a two-engine
abstraction speculatively; beads added and then rolled back PostgreSQL and
MySQL adapters for exactly that complexity [A3].

The owner's compose picture works either way. With SQLite, the compose stack
is one service plus optional sidecars rather than a database pair.

## Proposed shape

### Ownership and transactions

- The database is the only authority. External edits are impossible by
  construction, so `Store.Check()`, fingerprints, the journal and
  `recovery_required` for tampering go away. Fail-closed behaviour moves to the
  database's own integrity: `PRAGMA integrity_check` or `quick_check` at
  startup and on demand for SQLite, and checksums for PostgreSQL.
- Keep the process-wide mutex for writes at first. That makes the port
  semantically identical (lit's prepare/execute, preconditions and claim rules
  assume serialization). Allow concurrent reads once the read paths are
  queries. SQLite has a single writer anyway; per-request write transactions
  here take milliseconds.
- Keep record `revision` and the existing precondition checks for optimistic
  concurrency. A database transaction does not replace them.

### Logical schema (sketch, not DDL)

| Table | Holds |
| --- | --- |
| `service` | service ID, schema version |
| `projects`, `issues`, `comments`, `milestones` | current state: columns for every front-matter field, `body` text, `revision`, timestamps; the issue title key (`IssueTitleKey`) unique per project |
| `issue_links` | `parent` (or a `parent_id` column), `blocks` (directed, stored once), `related` (stored once, normalized order) |
| `milestone_members`, `issue_labels`, `project_repositories` | set-valued fields |
| `clients`, `claims` | today's JSON records as rows; claim expiry indexed |
| `transactions` | `request_hash` (unique), canonical intent JSON **once**, actor/client, timestamp, results |
| `history` | one row per changed record per transaction: owner type/ID, revision, field differences, body change, `request_hash` reference |
| search projection | per searchable field: owner, field, folded text (trigram), word index |

`history` keeps the current semantics (immutable, ordered, replayable). The
canonical intent hash remains verifiable from `transactions`.
`reconcileDurable` becomes one lookup by `request_hash` instead of scanning each
target's history.

**Body history stores diffs (owner decision); the server rebuilds full bodies
for the unchanged history API (owner decision).** Today each edit stores the
whole body twice, before and after (`record_transaction.go:561`). Instead, store
a line diff from the previous revision, and verify at write time that applying
it reproduces the new body exactly. Before committing to this, plan for three
consequences:

- **API compatibility.** `GET …/history` returns `body_hunks` with the full
  before and after text, and `internal/litclient/history.go` and
  `internal/tui/history` consume it. Either the service rebuilds those bodies
  by replaying diffs, keeping the API unchanged, or the API changes to carry
  diffs and the clients render them. Rebuilding keeps the change inside the
  service.
- **Replay cost.** Rebuilding revision N replays N diffs from creation. The
  current body is always stored in full on the record row, so replaying
  backwards from it is an alternative. Periodic full snapshots (for example
  every 50 revisions) bound the cost if long histories appear.
- **Migration.** The importer converts each existing whole-body pair into a
  diff and checks that the diff reproduces the stored `after` text.

### Search

- `lit grep` (existing API): candidates from the folded trigram index (rows
  `>= 3` folded characters), else a scan of the folded column. Final match and
  excerpt use the existing `searchExcerpt`, so the output matches today's,
  including line numbers and context. Cost becomes index-bound instead of
  store-bound.
- `lit search` (new): ranked words over title (weighted) and body, comments
  as separate hits grouped by issue, snippets, quoted-by-default input.
  `unicode61 remove_diacritics 2` (SQLite) or `simple` plus `unaccent`
  (PostgreSQL). FTS5's only built-in stemmer is English (`porter`) [S3]. German
  stemming would need a custom tokenizer; `simple` without stemming is the safe
  default for mixed technical text. PostgreSQL's `unaccent()` is `STABLE`, so
  it needs an `IMMUTABLE` wrapper or a text-search configuration to be indexed
  [P2].
- Ranked search ignores diacritics (owner decision): `cafe` finds `Café`.
  `unicode61 remove_diacritics 2` does this out of the box (verified above).
- Regex (owner decision: wanted). SQLite has no built-in `REGEXP`. Register
  Go's `regexp` (RE2: linear time, no catastrophic backtracking) as a SQL
  function through the driver [D1], and apply it only to candidates:
  1. Extract the literal strings the pattern requires (`regexp/syntax`; for
     example `econn` from `(?i)econn(refused|reset)`), fold them, and select
     candidates through the folded trigram index with `GLOB`.
  2. If no literal of 3 or more characters is required, scan the bodies.
  3. Run the Go regex on the original text for the final match and excerpts.

  Measured cost of a full scan with Go `regexp` over the 10× corpus (65 MB of
  bodies, one run each, Go 1.27): 1.7-3.1 s for patterns without a leading
  literal, 57 ms for one starting with a literal. At the 1,000-issue target
  that is roughly 0.2-0.3 s (inference, linear scaling). A broad regex
  therefore needs three things: the literal prefilter, a timeout or
  cancellation with a clear error, and execution **outside the global write
  mutex**. Today every request holds that mutex (`service.go:118`), so a
  3-second search would block every agent. Reads under SQLite WAL can run
  alongside the single writer, so search should move to a read transaction
  that doesn't take the lock.

### Markdown export (one-directional)

The export is a tree organized for people to read (owner decision), not a
mirror of today's storage layout. Sketch:

```text
export/
  README.md                   generated notice, service ID, export time
  manifest.json               last transaction included, record counts
  feat/lit-tui/               project title; its slashes become directories
    project.md                front matter + description
    milestones/tui-1.md
    issues/
      9f01ee0b-plan-the-manual-lit-tui.md
```

- One file per issue, its comments appended in order under a `## Comments`
  heading with author and time. Parent, blockers, related issues, labels,
  state and claim appear in front matter, with links to the other files by
  relative path.
- File names are the issue ID prefix plus a slug of the title. The prefix keeps
  names unique; renames move files, which is harmless because the tree is
  regenerated and never read back.
- Keep it flat (parent in front matter, not nested directories), so that a
  reparent doesn't move a subtree.
- Reuse the strict front-matter writer (`frontmatter.go`, deterministic keys,
  quoted strings) so the files are stable across runs and diff cleanly if
  someone keeps them in git.
- Write to a staging directory, then switch atomically (rename a symlink or the
  directory) so readers never see a half-written export. Include a manifest
  with the service ID, export time and the last transaction included. Skip an
  interval run when nothing has committed since.
- Trigger on demand (`lit-server export --to DIR`, which works without the
  HTTP server) and optionally on an interval (`export.interval`, off by
  default). The export carries a "generated, edits are overwritten" notice. Its
  contents are never read back, so no conflict handling exists.
- History is included (owner decision). Each issue gets a sibling
  `<file>.history.md` listing revisions oldest first: time, actor, field
  changes, and body changes as fenced `diff` blocks. Projects, milestones and
  comments get the same treatment. Keeping history in separate files leaves the
  issue file readable.
- No git integration: the export never commits or pushes (owner decision).
- An export is not a backup: it omits claims, clients and transaction
  evidence. Backups use `VACUUM INTO` snapshots.

### Migration

- One-time importer that opens the old store with today's code (`store.Open`,
  then `InitRecords` verification) read-only and inserts everything in one
  database transaction. History entries are deduplicated by `request_hash` into
  `transactions`.
- Validate:
  - counts per type;
  - every record decoded by the old reader equal, field for field, to its
    database row, with body bytes compared by SHA-256;
  - every converted history diff reproduces the recorded `after` text;
  - equal `lit grep` results from the old and new server for a fixed query set;
  - `transactions status` proofs for a sample of retained requests.

  The human-oriented export cannot provide a byte-for-byte check against the
  old files, so record equality replaces it.
- Rollback: the old data directory stays untouched as a frozen backup.
  Returning to the old server loses writes made after the switch; the owner
  accepted this. There is no tool to write them back, and the
  one-way export is not read back.

### docker compose

Both sketches are illustrative and untested. Common to both:

- lit-server has **no authentication** and listens on loopback by default.
  Publish the port as `127.0.0.1:7411:7411`. Serving beyond the host needs an
  authentication decision first; that is out of scope here.
- Distroless/scratch images have no `curl`; a `lit-server healthcheck`
  subcommand in exec form serves the compose `healthcheck` [C1].
- The export directory is a bind mount; it is write-only from lit's side.
- systemd socket activation (`packaging/`) remains the non-container path.

SQLite (recommended shape):

```yaml
services:
  lit-server:
    image: lit-server:local          # built from this repository
    environment:
      LIT_DATA_DIR: /data
      LIT_LISTEN: 0.0.0.0:7411       # inside the container only
      LIT_EXPORT_DIR: /export
      LIT_EXPORT_INTERVAL: 15m       # 0 or unset disables periodic export
      LIT_BACKUP_DIR: /backup        # VACUUM INTO snapshots
    volumes:
      - lit-data:/data               # named volume, not a host bind mount
      - ./export:/export
      - ./backup:/backup
    ports: ["127.0.0.1:7411:7411"]
    healthcheck:
      test: ["CMD", "/lit-server", "healthcheck"]
volumes:
  lit-data: {}
```

Keep the SQLite database on a **named volume**. SQLite must never sit on a
network filesystem [S2] [D6]. Litestream warns that Docker Desktop's host bind
mounts on macOS and Windows cross a VM file-sharing layer that can break WAL
and corrupt the database [D5]. No Docker-official statement was found; the
named volume avoids the question. Litestream can be added later as a sidecar
sharing the volume [D5].

PostgreSQL alternative:

```yaml
services:
  db:
    image: postgres:18
    environment:
      POSTGRES_DB: lit
      POSTGRES_USER: lit
      POSTGRES_PASSWORD_FILE: /run/secrets/db_password
    secrets: [db_password]
    volumes:
      - pg-data:/var/lib/postgresql  # PG 18+: not /var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U lit -d lit"]
  lit-server:
    image: lit-server:local
    environment:
      LIT_DATABASE_URL_FILE: /run/secrets/lit_dsn
    secrets: [lit_dsn]
    depends_on:
      db: { condition: service_healthy, restart: true }
    ports: ["127.0.0.1:7411:7411"]
  backup:                            # pg_dump -Fc on a schedule
    image: postgres:18
    # entrypoint loop or cron; see sources for maintained images
secrets:
  db_password: { file: ./secrets/db_password }
  lit_dsn: { file: ./secrets/lit_dsn }
volumes:
  pg-data: {}
```

Verified on `postgres:18-alpine` (18.6): `PGDATA=/var/lib/postgresql/18/docker`,
image volume `/var/lib/postgresql`, `pg_trgm` and `unaccent` bundled, `pg_bigm`
absent. Mounting the old `/var/lib/postgresql/data` path on 18 silently uses an
anonymous volume [P5]. Major upgrades are manual; at lit's size, dump and
restore into a new major is the safe route [P5].

## Prior art

| Tracker | Authority | Search | Relevance |
| --- | --- | --- | --- |
| beads (agent issue tracker) | Embedded Dolt (formerly SQLite + JSONL in git) | Title/ID match, no FTS | `issues.jsonl` is now "an export for viewers and interchange, not the source of truth or a backup", the same move proposed here. PostgreSQL/MySQL adapters rolled back in 1.2.1 (2026-08) for complexity [A3] |
| Fossil tickets | Immutable change artifacts; SQL tables derived and rebuildable | SQLite FTS (porter/trigram/unicode61) | Closest analogue to lit's immutable history [A4] |
| Gitea / Forgejo | SQLite, PostgreSQL, MySQL (Gitea also MSSQL) | Bleve by default; `db`, Elasticsearch, Meilisearch optional [A1] [A2] | Engine choice left to the operator |
| Vikunja | SQLite (default), PostgreSQL, MySQL | Database only since v2.0.0 (2026-02) removed Typesense [A5] | Precedent against an external engine at small scale |
| Plane | PostgreSQL (+ Valkey, RabbitMQ, object store) | Django `icontains` (ILIKE) [A6] | DB-first products often use plain substring search |
| git-bug | Git objects | Local cache + Bleve [A7] | Index as a rebuildable cache over another authority |

External engines were considered and rejected at this size (inference from the
sources). Meilisearch indexes asynchronously, so read-after-write needs polling
[E2]. Typesense keeps its index in RAM and wants 2+ vCPUs [E3]. Bleve's regex
and wildcard queries match whole indexed terms, not raw text, so it cannot
provide `lit grep` [E1]. Each adds a second consistency domain to keep in step
with the database.

## Owner decisions (2026-10-11)

Answered in the research conversation:

1. **Engine: SQLite**, embedded, as a single-service compose stack.
2. **Search contract:** today's `lit grep` behaviour is the goal. Weakening it
   is acceptable if needed. The folded shadow column keeps it unchanged, so no
   weakening is currently needed. Substrings under 3 characters fall back to a
   scan of the folded text.
3. **Export layout:** a tree organized for people to read (see
   [Markdown export](#markdown-export-one-directional)).
4. **Body history:** diffs, not whole-body copies (see
   [Logical schema](#logical-schema-sketch-not-ddl)).
5. **Interim fix to the file store:** none. The migration replaces it.
6. **Remote access and authentication:** later. lit-server stays local for now.
   Remote use could initially go through an SSH tunnel (interpretation of the
   owner's "same ssh"); real authentication, possibly OIDC, is a later
   decision. Compose publishes the port on `127.0.0.1` only.
7. **Rollback after the switch:** the old data directory stays frozen as a
   backup; going back to the old server loses writes made after migration.
   Accepted.
8. **Ranked `lit search`:** ignores diacritics. **Regex search is wanted**
   (see [Search](#search) for the prefilter, timeout and locking it needs).
9. **History API:** the server rebuilds full before/after bodies from the
   stored diffs; the API and clients stay unchanged.
10. **Export history:** included. No git commits or other git integration.
11. **Driver:** `modernc.org/sqlite`.

No questions from this research remain open. Implementation still needs a
plan, an ADR for the storage change (required by `fix/lit-server-store-scan`),
and explicit authorization.

## Sources

Fetched 2026-10-11. Versions as observed on that date.

SQLite and drivers:

- [S2] SQLite: Write-Ahead Logging, https://sqlite.org/wal.html
- [S3] SQLite: FTS5 (tokenizers, trigram `LIKE`/`GLOB` acceleration and limits, `detail=`, external content), https://sqlite.org/fts5.html
- [S8] MySQL 8.4: Natural Language Full-Text Searches, https://dev.mysql.com/doc/refman/8.4/en/fulltext-natural-language.html
- [D1] Driver modules, inspected from the Go module proxy: `modernc.org/sqlite` v1.60.1 (https://gitlab.com/cznic/sqlite, `doc.go`, `CHANGELOG.md`); `github.com/ncruces/go-sqlite3` v0.35.6 (README, `ext/fts5`, `ext/regexp`, release notes v0.32.0/v0.33.x); `github.com/mattn/go-sqlite3` v1.14.52 (README, `sqlite3_opt_fts5.go`)
- [D3] SQLite: transactions, https://sqlite.org/lang_transaction.html; busy handler, https://sqlite.org/c3ref/busy_handler.html
- [D4] SQLite: VACUUM INTO, https://sqlite.org/lang_vacuum.html; backup API, https://sqlite.org/backup.html
- [D5] Litestream v0.5.17 (2026-08-31), https://github.com/benbjohnson/litestream; tips, https://litestream.io/tips/; Docker guide, https://litestream.io/guides/docker/
- [D6] SQLite over a network, https://sqlite.org/useovernet.html; appropriate uses, https://sqlite.org/whentouse.html

PostgreSQL:

- [P1] pgx v5.11.0 (2026-09-07), https://github.com/jackc/pgx
- [P2] PostgreSQL 18 text search: controls, https://www.postgresql.org/docs/18/textsearch-controls.html; limitations, https://www.postgresql.org/docs/18/textsearch-limitations.html; unaccent, https://www.postgresql.org/docs/18/unaccent.html; generated columns (virtual by default in 18), https://www.postgresql.org/docs/18/ddl-generated-columns.html
- [P3] pg_trgm, https://www.postgresql.org/docs/18/pgtrgm.html; pg_bigm v1.2-20250903, https://github.com/pgbigm/pg_bigm (not in the official image or PGDG apt)
- [P4] Versioning policy (18.6 current, EOL 2030-11-14), https://www.postgresql.org/support/versioning/
- [P5] Official image docs, https://github.com/docker-library/docs/blob/master/postgres/content.md; PG 18 volume change, docker-library/postgres#1259; `tianon/postgres-upgrade` (self-described proof of concept)

Compose:

- [C1] Docker: startup order, https://docs.docker.com/compose/how-tos/startup-order/; secrets, https://docs.docker.com/compose/how-tos/use-secrets/

Search engines:

- [E1] Bleve v2.6.1, https://github.com/blevesearch/bleve (`search/query/regexp.go`: whole terms only)
- [E2] Meilisearch asynchronous operations, https://www.meilisearch.com/docs/learn/async/asynchronous_operations
- [E3] Typesense system requirements, https://typesense.org/docs/guide/system-requirements.html

Prior art:

- [A1] Gitea v28.1.0, `custom/conf/app.example.ini` (`ISSUE_INDEXER_TYPE`), https://github.com/go-gitea/gitea
- [A2] Forgejo v16.0.5, `app.example.ini`, https://codeberg.org/forgejo/forgejo
- [A3] beads v1.3.1, README and CHANGELOG (Dolt migration from v0.55.0; 1.2.1 adapter rollback), https://github.com/steveyegge/beads
- [A4] Fossil tickets, https://fossil-scm.org/home/doc/trunk/www/tickets.wiki
- [A5] Vikunja v2.0.0 release and commit a5b1a90, https://github.com/go-vikunja/vikunja
- [A6] Plane v1.4.2, `apps/api/plane/app/views/search/base.py`, https://github.com/makeplane/plane
- [A7] git-bug v0.11.0, `doc/design/architecture.md`, https://github.com/git-bug/git-bug

Not verified: when beads removed its SQLite backend; PostgreSQL parser
handling of `::` and `->` in identifiers. Underscores split tokens; that was
verified with `ts_debug`.
