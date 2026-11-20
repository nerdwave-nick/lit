# Project context API

One read-only endpoint resolves a project reference, or validates a stored
project and service identity, without a connected logical session:

```text
GET /v1/project-context
```

It works without `X-Lit-Client-ID` so a caller can remember a project for a
checkout and validate that choice later, before it has a tracker client. It is
the only session-free project read. `/v1/projects`, `/v1/projects/*`, snapshots,
transactions, claims, search and every other route keep requiring a connected
client, and look-alike paths such as `/v1/project-context/feat/a` are not part of
the operation. Creating a disposable client to read a project is not needed and
not supported.

The operation never registers, reconnects or consults a client: a header it is
sent is ignored. It writes nothing, so no client, claim, mutation, pending
request or session file results from it, and it runs in one read snapshot like
every other read.

## Requests

A request has exactly one of two shapes. Anything else, including a mix, a
missing partner parameter, an unknown parameter or a repeated one, is
`422 validation_failed`. Only GET is accepted (`405 method_not_allowed`).

| Shape | Parameters | Meaning |
| --- | --- | --- |
| Resolve | `project` | Find the project a reference names. |
| Validate | `project_id` and `service_id` | Check a stored binding against the service that answers. |

`project` accepts what every project command accepts: an exact title (case
insensitive), a UUID, a unique UUID prefix, or the `title:TITLE` and `id:UUID_PREFIX`
forms. It resolves with the same resolver as `lit projects get`, so a remembered
project is the one the commands show. A reference must be valid UTF-8, nonempty,
contain no control characters and be at most 300 Unicode characters.

`project_id` and `service_id` must be full lowercase UUIDs. A prefix is not a
stored identity. The service compares `service_id` with its own identity first
and only then reads the project by exact ID, in the same request. Two unrelated
requests across a restart, or a replacement service answering at the old address,
therefore cannot make a binding look valid. A project that is gone from the
bound service is `404 not_found`; a different service is `409 wrong_service`
whether or not it has a project with that ID or title.

## Response

The normal response envelope wraps a dedicated projection under `data`:

```json
{
  "data": {
    "service_id": "11111111-1111-4111-8111-111111111111",
    "api_major": 1,
    "project": {
      "id": "22222222-2222-4222-8222-222222222222",
      "title": "feat/api"
    }
  },
  "next_cursor": null,
  "server_time": "2026-10-14T12:00:00Z"
}
```

`service_id` is the identity of the service that answered. `project.id` is the
stable identity and `project.title` the current display title, so a stored ID
still validates after a rename and reports the new title. The projection holds
nothing else: no description, repositories, issues, revision or history. The
explicit transport type (`protocol.ProjectContext`) keeps future record fields
from reaching this unauthenticated read by accident. The current loopback or
SSH-forwarded transport boundary is unchanged.

Errors use the normal envelope, `{"error": {"code", "message", "details"}}`, and
the API major version stays 1.

| Status | Code | When |
| --- | --- | --- |
| 404 | `not_found` | The reference names no project, or the stored project ID is gone. |
| 409 | `ambiguous_reference` | A UUID prefix names several projects. |
| 409 | `wrong_service` | `service_id` is not the answering service. `details.service_id` names the answering one. |
| 422 | `validation_failed` | Malformed, mixed, repeated, unknown or oversized parameters. |
| 405 | `method_not_allowed` | The method is not GET. |
| 503 | `storage_busy`, `shutting_down` | Retryable service conditions, as for every route. |

An older service does not know the path and sends it to the connected-client
gate (`400 client_required`). The Go caller reports that as `incompatible_service`.

## Archived projects

The tracker has no archive state yet, so there is nothing for this operation to
refuse: every project that exists is returned. Archive support is separate work
(tracked in issue `0c14ae79-2e81-4b42-86e9-0978b14c0ae2`). When archival lands,
resolve and validate must refuse an archived project with a distinct error
code instead of returning its identity, and callers must explain that code
without restoring or substituting the project. This is a deferred acceptance
criterion of the project-context operation, not an implemented behavior.

## Go caller

`internal/litclient.ProjectContextClient` is the reusable caller:

```go
c := litclient.ProjectContextClient{Endpoint: "http://127.0.0.1:7411"}
found, err := c.Resolve(ctx, "feat/api")                 // ProjectContext{ServiceID, ProjectID, Title}
valid, err := c.Validate(ctx, litclient.ProjectBinding{  // same service and project IDs, current Title
	ServiceID: found.ServiceID, ProjectID: found.ProjectID,
})
```

It sends one GET per call. It holds no mapping, lock or state directory, writes no
pending record and sends no client header, so it is safe from offline-first
commands and hooks. `Endpoint` is used as given; choosing among `--endpoint`,
`LIT_ENDPOINT`, a saved endpoint and the default is the caller's job
(`clientendpoint.Resolve`). `HTTP` is optional (default: a 30 second timeout); the
call's context bounds the request as well.

Errors are `*protocol.Error` values; branch on `litclient.Code(err)`. Besides the
service codes above, the caller adds:

| Code | When |
| --- | --- |
| `invalid_arguments` | The reference or binding is unusable (empty, prefix, uppercase); nothing was sent. |
| `invalid_endpoint` | `Endpoint` is not an HTTP(S) origin; nothing was sent. |
| `service_unavailable` | Transport failure, timeout or cancelled context. |
| `incompatible_service` | The service predates the operation, speaks another API major or reports an invalid service ID. |
| `invalid_response` | The answer is malformed, has no title, or names a different project than asked for. |
| `wrong_service` | The service that answered is not the bound one, even if it did not object. |

On any error the returned `ProjectContext` is the zero value. On `Validate`
success it equals the binding plus the current title.

`internal/checkoutcontext` builds on it for remembered projects: `Validate` checks a
saved `Record` and `ResolveProject` finds the `Preference` to save, both taking a
`checkoutcontext.Endpoint` (explicit endpoint, `LIT_ENDPOINT`, then the saved one
and the default) and returning explained `*protocol.Error` values that keep the
codes above. `lit context set` and `lit context show --validate` use them; see
[command help](cli-help.md#remembered-project-per-checkout).
