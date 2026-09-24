# API-key write provenance

This Core extension does not change the pinned public `/v1` protocol. It serves
administrator consoles; it is not an HTTP access log, execution event stream or
replacement for a Session's creator identity.

## Authentication and ownership

Authentication carries the actual issued key UUID, independently of its inherited
principal. Static configured keys use `static:<lowercase SHA-256 digest>` as a stable,
non-secret identifier and the digest's first eight characters as the display prefix.
Optional `name` labels a static key; optional `kind: "console"` identifies a trusted
console credential (`static` is the default). Newly generated installer bindings use
`name: "Console", kind: "console"`. These fields describe the credential, not the
HTTP client: using that same key in an SDK retains its console label. Client headers
cannot assert a key identity or console origin. Display renaming does not change ID.

True creation stores ownership in the same transaction as the resource and operation.
Updates, retries and successful no-ops never replace it. Initial Skill versions and
new Session Environments share their parent creation operation's provenance. Runtime
produced Artifacts have no API-key creation anchor; deleting one is still recorded.
Missing historical or foreign ownership is `null`. No historical resources are backfilled.

An issued key's current `revoked_at` comes from its retained key row. Revocation
prevents new authentication but does not invalidate already admitted work or erase
history. Removing a static key from configuration stops authentication; there is no
static-key revocation timestamp, so `revoked_at` remains null. Recorded static key
name/prefix/kind are immutable snapshots. Deleting a resource does not delete its
operation history or creation anchor.

## Transaction and success boundary

A successful write means a durable business commit, not successful HTTP delivery or
successful subsequent model execution. Each request has a server-generated
`request_id` for deduplication and the request's diagnostic `trace_id`. A shared
trace across requests is not an idempotency key. Business rollback also rolls back
the operation and ownership. Failure to persist the audit fails the transaction.
Reads, failed validation/authorization and internal maintenance writes are not logged.

Session input is recorded when durably admitted, including prepared-Environment
reservations. Later execution failure or response disconnection does not undo that
accepted write. Explicit successful empty-input and creation/deletion retries get
one operation without changing ownership. Automatic promotion/refresh/cleanup does
not create another public operation.

Environment file bytes are written by the existing Runtime, outside PostgreSQL.
Preparation durably stores only the safe key/request/trace origin. The confirmed
success receipt and audit commit together; uncertain/failed uploads are not reported
as successful operations. Recovery uses the saved origin. This is not a claim of
atomic transactions across the filesystem and PostgreSQL.

Recorded fields are ID, timestamp, safe key metadata, action, resource type/ID,
parent ID, request ID and trace ID. Never store request/response bodies, bearer
secrets, model credentials, tokens, file paths or file contents in these tables.

| Public write | Action | Resource / parent |
| --- | --- | --- |
| Agent create/update/delete | create/update/delete | agent |
| Session create/update/delete | create/update/delete | session |
| Session events | send_events | session |
| Session Artifact delete | delete | artifact / session |
| Environment file upload | upload_file | environment / session |
| Environment Template create/update/delete | create/update/delete | environment_template |
| Skill create/default/delete | create/update_default_version/delete | skill |
| Skill version upload/delete | upload_version/delete | skill_version / skill |
| Source File upload/delete | create/delete | file |
| Vault create/delete | create/delete | vault |
| Credential create/replace/delete | create/update/delete | credential / vault |

Explicit public OAuth Credential writes are covered. Automatic OAuth refresh is not
a separate public write. Public resource IDs retain their existing formats.

## Console queries

Both endpoints require the deployment Bearer credential used by
`/core/v1/project-api-keys`. Project keys, including console project credentials,
cannot call them. Required `binding_digest` selects an existing configured static
binding and its tenant/project; it does not authenticate. There is no arbitrary
tenant selector. Two bindings for the same project see the same project history.

### Batch ownership

`GET /core/v1/resource-owners?binding_digest=...&resource_type=agent&resource_ids=id1,id2`

`resource_type` is one of `agent`, `session`, `environment`,
`environment_template`, `skill`, `skill_version`, `file`, `vault`, `credential`,
`artifact`. Pass 1–100 comma-separated public IDs. Results preserve input order:

```json
{"data":[
  {"resource_id":"id1","api_key":{"id":"key-uuid","name":"SDK","prefix":"pc_example","kind":"issued","revoked_at":null}},
  {"resource_id":"id2","api_key":null}
]}
```

### Operations

`GET /core/v1/write-operations?binding_digest=...&limit=50`

Optional filters: `key_id`, `resource_type`, `resource_id`, `created_after`
(inclusive RFC3339 timestamp), `created_before` (exclusive RFC3339 timestamp).
`limit` is 1–100, default 50. Pass the previous `next_cursor` as `after`, keeping
filters unchanged. Results sort by descending `(created_at, id)` with keyset
pagination; a cursor is not an authorization token or a snapshot of future writes.
The response is `{ "data": [...], "has_more": false, "next_cursor": "" }`.
Each operation contains `id`, `created_at`, `api_key`, `action`, `resource_type`,
`resource_id`, `parent_id`, `request_id`, `trace_id`. An absent parent is an empty
string. Empty pages contain `data: []`.

Malformed/duplicate/unknown query parameters return 400, missing configured binding
404, invalid deployment authentication 401. These rules belong only to the new
Core routes and do not alter public list parsing or errors. Creation records remain
queryable after resource deletion; expired non-creation records do not.

## Retention

`AGENTS_API_WRITE_AUDIT_RETENTION` accepts a Go duration of at least one hour;
default `2160h` (90 days). Every minute Core removes at most 1,000 expired
non-creation records in a bounded transaction. Creation records and anchors are
retained permanently, which includes the entire lifetime of a resource and its
history after deletion. Key revocation/resource deletion never triggers cleanup.
Retention removes only aged non-creation operations; it does not erase key metadata,
resource rows or ownership. A retention backlog can take multiple passes to drain.

## Validation

The batch's Store tests cover each mutation, induced audit-write failure with
business rollback, tenant isolation, ownership immutability, revocation, cursor
filters and retention. HTTP tests cover authenticated source propagation and
administrator-only query validation. Live evidence and remaining limitations are
recorded separately; passing these tests does not claim full Agents API compatibility.
