# Record completion API

These read-only endpoints work without `X-Lit-Client-ID`:

```text
GET /v1/completions/projects
GET /v1/completions/issues
GET /v1/completions/milestones
GET /v1/completions/comments
```

They expose identifiers and display metadata only. They do not expose descriptions,
bodies, history, comment text, repository contents or full record projections.
Ordinary record reads and mutations retain their connected-client requirements.

| Query | Meaning |
| --- | --- |
| `prefix` | Optional case-insensitive reference prefix; empty discovers candidates. Supports `id:` and, for titled records, `title:`. Issues also support project-qualified references. |
| `project` | Optional project reference for issues, milestones and comments. An explicit empty or unknown project is an error. |
| `limit` | Positive result limit, default `min(100, limits.max_page)` and maximum `limits.max_page`. |

Unknown and repeated parameters are rejected. Prefixes must be valid UTF-8, at
most 300 Unicode characters, and contain no control characters. Only GET is
accepted. Queries run in one read snapshot, like every other read route.

If supplied, a connected client's ID provides its saved project as a default.
Unknown or disconnected clients supply no defaults and are never registered or
reconnected. Explicit project selection wins over the default; an issue prefix's
project qualifier wins over either. Without project context, discovery spans
projects. Projects themselves are never filtered by a session's selected project.

The normal response envelope wraps a dedicated projection under `data`:

```json
{
  "data": {
    "service_id": "11111111-1111-4111-8111-111111111111",
    "api_major": 1,
    "items": [
      {
        "value": "feat/api:Fix timeout",
        "id": "22222222-2222-4222-8222-222222222222",
        "title": "Fix timeout",
        "project_id": "33333333-3333-4333-8333-333333333333",
        "project_title": "feat/api",
        "state": "open"
      }
    ],
    "has_more": false
  },
  "next_cursor": null,
  "server_time": "2026-10-01T12:00:00Z"
}
```

`value` is one raw reference argument; shell adapters handle escaping. Every item
has `value` and `id`. Titles and project metadata appear where applicable, issues
include `state`, and comments include the owning `issue_id`. Comment bodies and
authors are not used for suggestions. The explicit transport type prevents future
record fields from accidentally becoming part of this unauthenticated projection.

Issue values are qualified when no project is selected. Milestone values use
IDs in that case because their resolver requires project context for titles.
Comments always complete by ID. A title that looks like an ID or special argument
uses `title:`. Prefix filtering matches the emitted reference, never record bodies.

Matches are sorted deterministically and filtered before limiting. `has_more`
means the caller should narrow the prefix; there is no cursor or automatic page
scan by the shell. The existing snapshot-byte limit also applies. The implementation
reads existing records internally under the service lock; this is a smaller wire
projection, not a new indexed storage engine.

Service identity is included so the CLI can validate a saved endpoint binding
without a second HTTP request. No session mapping means there is no prior binding
to compare. The current loopback/SSH-forwarded transport boundary is unchanged.
