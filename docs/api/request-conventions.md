# Request conventions

These rules apply to every `/v1` operation. Operation pages state only what differs
from them. Descriptions inherited verbatim from the pinned upstream contract are the
exception: they keep upstream's wording, so the Files and Skills operations repeat the
`OpenAI-Beta` rule above and Create a reusable Agent repeats the JSON body checks.

## Headers

| Header | Rule |
| --- | --- |
| `Authorization` | `Bearer <project-api-key>` on every operation. See [API namespaces and credentials](README.md). |
| `OpenAI-Beta` | Exactly one `agents=v1` value on every operation except Files (`/files`) and Skills (`/skills`). Otherwise 400 `invalid_beta`. The pinned SDK sends it. |
| `OpenAI-Organization`, `OpenAI-Project` | Optional. If present they must be `core` and `proj_<project UUID>`; otherwise the request gets the same 401 as a rejected key. |
| `Idempotency-Key` | Optional on Session creation and on event submission, up to 128 bytes. A Core extension: a retry with the same key and request returns the original result, and the same key with a different request returns 409 `idempotency_conflict`. A streamed Session creation retry is the exception; see Create an execution Session. The hosted service returned distinct Sessions for repeated creation keys; its event submission behavior is not documented. |

## JSON request bodies

Every operation with a JSON body checks it in this order, before any field
validation or resource lookup:

1. The `Content-Type` must be `application/json` or another `application/*+json`
   type, case-insensitive, with well-formed parameters.
2. The body must fit the operation's limit: 1 MiB, or 16 MiB for Session creation and
   for creating or updating an Environment Template. Environment file uploads have
   their own limits. A larger body returns 413 `request_too_large` with a Core
   message naming the limit.
3. The body must be valid UTF-8 and one JSON value, with no unpaired surrogate
   escape and no repeated key at any depth, and its root must be an object. An empty
   body or `null` is treated as `{}`.

Failures of checks 1 and 3 return 400 `invalid_request_error` with a null `param`
and the official message.

Updating a Skill's default version (`POST /v1/skills/{skill_id}`) is the one
exception: it reads its body without these checks, up to 64 KiB, and an unreadable
body returns 400 `invalid_request`.

Member names match exactly; a case variant is an unknown member. Text that contains
U+0000 or cannot be stored as UTF-8 returns 400 `invalid_request_error`. This is a
limit of Core's storage, not of the official API.

## Lists

Lists take `after`, `limit` and `order`; the Environment files list takes `path`,
`limit` and `order` and continues with an opaque `page` token instead of `after`. `order` is `asc` or `desc`; omitting it uses the
operation's default, and an explicitly empty value is invalid. Unknown query keys
are ignored, and a supported scalar key given twice is rejected. Array parameters,
such as the Vault and Credential `status[]` filter, may repeat.

The `limit` bounds, the default order and the fields of each error differ between
the Agents API lists, Files and Skills. Each operation page states its bounds and how
an unresolved `after` cursor fails. The
[list query record](../../contracts/agents-api/list-query-semantics.md) has the
evidence for each family.

## Errors

Clients branch on the HTTP status and `error.code`, never on `message`. The
[error code registry](../../contracts/agents-api/error-codes.md) lists every code.

## Compatibility notes

Core implements the pinned OpenAI Agents API. Where an operation page says a
behavior is not yet verified against the hosted service, Core's behavior is
documented but has not been compared with the official service. The
[coverage record](../../contracts/agents-api/README.md) and
[operation evidence](../../contracts/agents-api/operation-evidence.md) hold the
details and the request evidence.
