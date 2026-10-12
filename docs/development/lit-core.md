# lit core development

The executable paths are `cmd/lit-server` and `cmd/lit` in the module
`github.com/nerdwave-nick/nerdwave-workflows`. Run `go test -race ./...` for the public
CLI / real service suite and focused persistence, protocol, and HTTP tests.
See [verification](testing.md) for the full validation commands.
The integration harness builds race-instrumented executable binaries into a
removed temporary directory and uses temporary data, private client state, and
working directories. It never starts a persistent service or consults vendors.

`lit-server` reads strict version-1 JSON configuration. Flags override environment,
which overrides JSON, which overrides defaults. The listener defaults to
`127.0.0.1:7411`. An explicitly selected missing configuration is an error.
The service holds its Linux owner lock (`lit.db.lock`) from opening the database
and releases it only after HTTP shutdown has drained storage work. SIGINT and
SIGTERM allow up to thirty seconds; exceeding that bound terminates, and SQLite
discards the uncommitted transaction on the next start.

Ticket 01 provides connect, disconnect, and the available session properties.
Project references need ticket 02's project resolver; no project is invented by
setting a session field. Disconnect releases the client’s active claims in the same atomic storage
operation as the client transition.
Deferred archives, editing windows, drafts, and saved plans have no routes.

## Extension interfaces

- `internal/protocol` holds public types, strict request/persisted JSON decoding,
  stable errors, UUID generation, and HTTP envelopes. Response decoding is
  intentionally additive; unknown successful mutation outcomes retain pending
  evidence. Add canonical durable encoding here with the projects slice.
- `internal/storage` owns the SQLite database: the owner lock, the schema and
  its migrations, and every query (see [the storage format](../storage-format.md)).
  Reads are typed around the service's use cases (records by ID, selectors,
  filtered keyset lists, neighbours, milestone progress, claims, history, the
  client, the search scan, ranked search). Writes go through `WriteTx.WriteRequest` (records,
  history and the transaction record of one durable request), the claim and
  client writes. Only a `Writer`, which proves the process write lock is held,
  begins a write transaction. There is no fault-injection seam: atomicity rests
  on SQLite transactions and is tested by failing requests after their writes.
- `internal/service.New` takes the open storage module. The static route table
  in `routes.go` classifies every route as a read (one WAL read snapshot, no
  lock, at most `read_concurrency` at once) or a write (the write lock and one
  write transaction); add each new route there, or it is serialized as a write.
  A search route is classified `searchAccess`: a read bounded by the search
  deadline (`search_limits.go`), whose handler also checks each pattern or
  query with `checkSearchText` (at most 1,000 bytes).
  Each request runs on a request-scoped `Server` holding its transaction and
  request time. Request-body network reads happen first, response bytes are
  materialized in the transaction and transmitted after the snapshot closes or
  the lock is released. Resource handlers belong in separate files and are
  dispatched from `route`. Mutations apply their writes, bound the success output
  and only then commit; a limit rejection rolls everything back.
- `internal/litclient.Session` is the client safety core shared by every Go
  client: it holds the client/service binding, deadline, last observed service
  time and an injected warning writer. `Lock` holds a logical session's mapping
  lock until `Close` (`LockPath` does the same for a mapping outside the
  `sessions/` namespace, which lit-tui uses for its per-instance registry); `Open` resolves the endpoint, checks the service's API
  major and identity, and reads the client. `Call` persists exact outgoing
  operational requests before transmission. Failed local persistence prevents
  sending. Uncertain transport or response results retain pending evidence (the
  CLI exits 4) and are reconciled read-only, never blindly replayed. A `Session`
  is not safe for concurrent use.
- Typed operations on `Session` take a per-operation `context.Context` and
  return `*protocol.Error` values; `litclient.Code(err)` exposes the stable
  code, so callers never branch on HTTP status. `Get`/`List` read snapshot
  targets and typed `Query` pages (cursors, limits, `All`). `Prepare` returns
  an opaque `Prepared` and `Commit` sends exactly that preview with the claim
  tokens of the `RequiredClaims` the client owned; a `Prepared` is bound to
  its client and submitted at most once, so a commit refused because any
  affected record (including mirrors and guards) changed is never prepared
  again. Request builders cover issue, comment, project and milestone
  mutations. `AcquireClaims`/`RenewClaims`/`ReleaseClaims` follow the CLI
  (renewing an absent or expired claim is `claim_expired` and never
  reacquires). `TransactionStatus` reconciles retained requests read-only and
  never replays, moves or deletes evidence. `Connect` registers a fresh client
  for a locked, unmapped session and saves its mapping; `Disconnect` releases
  the client's claims. The CLI uses Prepare/Commit, the claim operations and
  Disconnect; its connect and `transactions status` stay in `internal/cli`.
- `internal/cli.App` embeds that session and adds arguments and output
  preference; it passes its stderr as the warning writer.
- Resource CLI verbs should live in separate files, expanding argument validation
  and dispatch. Session selections are property selectors, distinct from the
  global project context used by later resource commands. Grouped resource flags
  need their own item-aware parsing; do not treat them as singleton session flags.

The CLI encodes local names under `sessions/`, uses an OS lock for each name,
and keeps exact requests in `pending/`. Every envelope has `schema_version: 1`.
The separate cwd `.lit/<client-id>/state.json` caches only output preference and
binding. A corrupt/mismatched cache grants no authority; offline output falls
back to JSON. Server-held project/output settings remain authoritative online.

Testing limitations: atomicity tests fail requests after their writes and reopen
the database; they do not simulate a physical disk or power failure. Linux is the
service runtime boundary. Cross-compilation is not native macOS/Windows runtime
validation. The later build/install ticket owns release automation and installation.

## Durable projects and transaction extension points

Ticket 02 adds projects create/get/list/update/history, grouped arguments and JSON
files (including a single stdin consumer), exact selectors, session project
selection, and atomic semantic batches. The public CLI always prepares a mutation
before sending the exact returned typed intent/hash through its existing pending
writer. Preparation is an ordinary read; it creates no saved plan, reservation,
claim, or other durable state. Direct HTTP execution uses that exact returned
canonical descriptor rather than reconstructing fields. Optional outer transport
schema markers are accepted; persisted canonical intents and all application-owned
files carry version 1.

- `protocol.Intent`, `Operation`, `ObjectRef`, and `Canonical` define durable intent.
  Canonical fixtures in `internal/protocol/testdata` pin bytes and SHA-256 values,
  including Markdown escaping/CRLF, set/key order, and omitted versus clear fields.
  Extend the typed semantic fields and normalization together for issues/comments;
  never introduce arbitrary JSON mutation maps. Expand strict per-command presence
  validation when adding fields. Repeated compatible descriptors merge before
  canonical encoding, while duplicate members inside one input are invalid.
- `prepareProjects` resolves all selectors in the request's transaction, merges
  semantic groups, fixes IDs/revisions, and validates the final state.
  `recordTransaction` returns the request's writes plus changed-owner results
  without applying them. Preparation, execution and reconciliation use that
  engine.
- `commitResponse` applies the writes, constructs and checks the exact success
  bytes, and commits. Responses are transmitted after the write lock is
  released. A read request has no write transaction, so a write attempted from
  a route misclassified as a read fails instead of running unserialized. Durable HTTP results use `data.results`; CLI output uses `items`.
  List/snapshot/history pages use top-level HTTP `items` and `next_cursor`.
- History holds only changed field before/after values and the body change as a
  forward diff, which the history API turns back into whole-body hunks. Each
  request's intent and results are stored once. Time advances past the newest
  committed transaction even if the clock repeats or moves backward. Changed
  owners advance once; guards/no-ops do not.
- The resolvers (`resolveProject`, `resolveIssue`, ...), the record reads and
  the storage module's queries are the read seams for resource, discovery and
  reconciliation handlers. The exported `Server` methods (`ApplyClaims`,
  `PrepareRecords`, `Issue`, `RecordHistory`, ...) run as requests of their own
  for embedders and tests.
  UUID prefixes accept case-insensitive hex and standard-position hyphens. History
  detail/request outcome routes use UUID owners. Missing owner request lookup is
  `not_recorded_here`; it does not prove an entire batch failed.

Project tests exercise actual CLI binaries against a real temporary service,
including a proxy that drops a committed mutation's response. Pending evidence
retains its creation IDs/hash and the transaction record establishes the
committed outcome. Atomicity tests fail multi-record requests after their
writes and reopen the database. They do not simulate physical power failure. Complete
interactive transaction reconciliation remains ticket 06's scope.

Successor slices must preserve canonical history hashes. Add typed optional
fields with omission rules that leave canonical request bytes unchanged. The old
project-only file store fixture now verifies that a legacy store is refused
without modification; it is not a compatibility promise.

## Issues, comments, and shared record transactions

Ticket 03 adds `Issue` and `Comment` to `protocol`, with optional typed semantic
fields in the original `ProjectSet` / `ProjectMembers` / `ProjectInput` types.
Those retained names are source compatibility seams, not restrictions on resource
type. Canonical field omission preserves every project-only version-1 hash.
`Set.Assignee` and `Set.ParentID` are `**string`: nil outer pointer means omitted,
a nonnil outer pointer with nil value means explicit JSON null, and a string
value means assignment. Custom decoding preserves all three states. Input
`clear` is translated into those typed null fields; body clears are empty text.
All other null set values and inapplicable resource fields are rejected.

- `records.go` owns the record reads, the exact resolvers and the shared
  NFC/default-fold `IssueTitleKey`.
- `loadState` reads only what one request involves: its records, the records
  they name, and the structural closure (ancestors, transitively blocked issues
  and their projects) found with recursive queries. `finalState` applies typed
  operations and validates the changed records, their references, title
  uniqueness (in the request and in storage) and cycles. Membership lists are
  computed by storage and never rebuilt or compared. `requiredGuards` captures
  unchanged projects/owners/ancestors relied upon during resolution and
  structure validation. Explicit targets are revision preconditions; guards
  never get revisions or history.
  Each changed owner advances once. Future relationship operations should extend
  typed operations and this engine, without introducing a second commit path.
- `recordTransaction` checks actor/service/client binding, revisions and expanded
  limits, produces validated writes/results, and leaves commitment to the existing
  bounded `commitDurableResponse`. Claims can add policy checks at this shared
  execution boundary before writes are produced; this slice invents no claims.
- `record_history.go` pages history through the storage module.
- `record_http.go` extends the snapshot/query seam and resource routes. Basic
  issue lists require a project or explicit all-projects; comments require an
  owning issue. `ProjectQuery` retains its existing name and adds those scope
  fields, with strict field presence validation; storage applies the filters and
  keyset pages.
- `cli/records.go` owns grouped issue/comment arguments, JSON array/file/stdin
  inputs, mutation preparation and read commands. CLI mutation results remain
  `items`; HTTP mutation results remain `data.results`, and snapshot/list/history
  pages remain top-level `items` / `next_cursor`.

The literal `title:` selector consumes every remaining colon. A qualifying
project title scopes the following title or UUID prefix; unqualified UUID
prefixes remain global regardless of session project. Lookup, uniqueness and
index keys use the same Unicode normalization while preserving displayed titles
and all Markdown bytes. Parent updates stay in the owning project and retain
children; no move, archive, restore or deletion behavior was added.

## Claims and live execution policy

Ticket 05 keeps claims in the `claims` table, one row per issue; release
deletes the row. Expired claims never appear as live and never regain time on
restart. `liveClaim(id)` and `liveClaims(ownerID)` are the read seams. Each
request captures one request time for claim filtering, ownership fencing,
projections and success response timestamps; a later request evaluates expiry
anew.

`claimTransaction` validates and returns operational writes; HTTP materializes
and bounds the exact response before the commit. `POST /v1/operations`
accepts `protocol.ClaimOperation` for atomic acquire/renew/release batches;
`selection: all_owned` fences the complete live ID/token set. Singular issue
claim routes use that engine. `POST /v1/claim-snapshots` resolves explicit issue
selectors or an owner-wide set under one mutex, returning service time for the
CLI's once-computed absolute expiry. Public `GET /v1/claims?owner_client_id=...`
and `GET /v1/issues/<id>/claim` expose live authority; null is an explicit result.

`recordTransaction` adds live policy after structural/revision validation and
before writes. Its optional token arguments do not enter canonical intent or
history. `Prepared.RequiredClaims` reports current ownership or eligibility for
a transaction-only reservation; the CLI forwards same-client tokens through
`DurableRequest.Claims`. A formerly unclaimed issue may reuse a current same-client
claim, while supplied stale tokens never get replaced silently. Link mutations
must forward prepared tokens just like other durable mutations.

The policy covers explicit existing issues, existing parents that gain or lose a
child, and existing comment owners. Comment append changes no issue and needs no
claim. Structural guards remain independent of live ownership. No persisted
transient lease is needed because one transaction spans validation and commit.
Closing and explicit force release claims in the same transaction; disconnect
deletes all owned live and expired claims with the client's transition.
Claim changes alone do not increment durable revisions or create object history.

Excessive absolute renewal targets are rejected (user-confirmed), preserving the one-hour
grant cap and exact-request retry semantics. Acquisition separately caps excessive
absolute targets at one hour, retaining its 30-minute default. Duplicate/older targets on a live
matching claim remain no-ops, even when the requested target is now in the past.

Current issue reads project live `claim`/`claimed` outside the durable schema.
Every claim status a read exposes (issue reads and query pages, issue summaries,
claim snapshots, claim lists and `GET /v1/issues/<id>/claim`) adds `owner_actor`
with only the owner's actor `name` and `kind`; clients still cannot read other
clients' records. The member is projected on a copy and a durable claim envelope
carrying it is invalid. Claim operation results and `Prepared.RequiredClaims`
are unchanged.
Claim lists support owner/issue filters and query-bound cursors, acquisition-time
(`created_at`) ordering with issue UUID ties, direction and bounded all-results
reads. Owner-wide renewal still uses a complete atomic claim snapshot.

## Links and discovery

Ticket 04 extends typed member deltas with optional `blocks`, `blocked_by`, and
`related` UUID sets. Public link/unlink preparation normalizes each logical edge
into mirrored endpoint operations, so inverse spellings and duplicate identical
edges produce the same canonical hash. Ordinary issue update inputs use the
link/unlink commands for relationship changes. The shared final-state engine
validates mirrors, endpoint existence, self links, and blocking cycles before
producing any writes. Both endpoint revisions participate, and structural guards
include reachable blocking paths. Parentage and blocker state remain independent.

`logicalMutationCount` counts each normalized logical edge once and each ordinary
record operation once; endpoint expansion is separately capped by the expanded
record bound. This preserves the advertised explicit limit at direct execution
without counting the mirror as a second user mutation. Preparation additionally
bounds input groups. Existing version-1 history and canonical fixtures retain
identical bytes because the new members are omitted when absent.

All durable lists now use `recordPage`: scope, AND-combined structured filters,
Unicode-normalized casefolded substring matching, inclusive-after/exclusive-before
times, deterministic sort/UUID ordering, and cursors bound to normalized queries.
`all` remains one bounded snapshot, never a page loop. Nullable parent/assignee
JSON filters use null for absence; the literal string `none` remains a real name,
while CLI `--assignee none` and URL `assignee=none` select absence.

`POST /v1/issue-summaries` takes `{"schema_version":1,"query":{...}}` with an
issue query (`type` omitted or `issues`; the session project applies as for
snapshot queries) and returns `protocol.IssueSummary` pages: identity, revision,
timestamps, title, state, labels, assignee, project, parent/children, link sets
and live `claimed`/`claim`, never bodies or comments. It reuses `recordPage`, so
filters, `default_page`/`max_page`, query-bound cursors, `all` and the 413
`limit_exceeded` item and `snapshot_bytes` bounds behave exactly as for snapshots.

## Interrupted-request reconciliation

The CLI provides `lit transactions status [--file PATH] [--session NAME]`.
With no scope flag it examines all retained private requests; an explicit session
filters by the mapping's service/client identity. `--endpoint` can reach that
same service at a new address but cannot change the retained identity. A supplied
file is evidence only and is never deleted. Existing version-1 pending envelopes
remain readable unchanged; there is no saved-plan or repair format.

`POST /v1/transaction-status` is a read-only serialized observation of an exact
`protocol.ReconcileRequest`. It accepts disconnected clients for reconciliation,
without permitting them to mutate. Its response binds service, client, and a
SHA-256 digest of that request (a transport proof binding, not a persisted receipt
or durable request hash). The CLI validates these bindings and the typed result
against the original request before discarding private evidence.

- `committed` / `immutable_history`: a matching durable hash in any proposed
  changed owner's immutable history proves the atomic change. Later edits do
  not invalidate that proof. The response includes the whole changed-owner set.
- `already_satisfied` / `current_validated_state`: the exact no-op passes original
  revisions, guards, ownership and token conditions. No history is invented.
  A matching live claim token whose expiry already reaches the absolute target
  can satisfy renewal; its expiry is never recomputed. Empty-token release with
  no active claim is a validated no-op.
- Client desired state at its original revision or exact one-step successor can
  establish `already_satisfied`; the successor uses `current_state` proof. This
  says the requested effect is present, not which request wrote it. Later client
  revisions, reconnect, or incompatible state remain uncertain.
- `uncommitted` is a serialized snapshot conclusion. For durable changes it
  requires misses in every proposed changed owner's history plus valid original
  preconditions and an actual remaining change. A matching live claim token below
  the requested renewal target, or still present for release, also establishes a
  missing effect. This observation cannot promise that an already delayed network
  request will never arrive later; it never authorizes a fresh semantic mutation.
- `uncertain` retains evidence and makes the status command exit 4 with structured
  `items` on stdout. Acquisition without a received token, replaced/expired
  ownership, and nonempty-token release after the claim disappears cannot prove
  historical outcomes. First registration without its returned client ID remains
  unidentifiable; status never registers a replacement or invents a mapping.

Ordinary commands attempt the same read-only reconciliation within their original
network deadline after an uncertain response. Proven history/no-op results can be
returned without another mutation request. A definitely uncommitted observation
returns `mutation_uncommitted` (exit 1); unresolved uncertainty remains exit 4.
Corrupt local evidence fails closed, malformed success/error/proof responses retain
it, and no startup or status path replays a mutation. Private per-request OS locks
span transmission and automatic reconciliation, preventing another status process
from removing evidence while that CLI still owns the request. Lock files live
under `locks/pending/`, separately from versioned pending JSON.


## Storage growth

Requests read only the records they involve, through indexed queries, so their
cost no longer grows with the whole store. `lit grep` reads only the candidate
records that its trigram index (or, for queries under three folded characters,
a scan of the folded text) yields; completion still lists the records of one
type. Reads run concurrently in WAL snapshots and
writes are serialized by one write lock; the benchmark is later milestone work.
Startup integrity checks, degraded mode and `lit-server verify` are described
in [recovery](../recovery.md#damaged-tracker-database).

See [recovery limitations](../recovery.md) for uncertainty that retained evidence
cannot currently resolve; no human repair or retirement interface is shipped.
