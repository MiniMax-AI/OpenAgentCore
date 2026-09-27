# Core administration errors

Errors on `/core/v1` retain the envelope below. `message` is safe English text;
`code` and `param` are nullable. Clients use stable `code` values and the optional
field `param`, and fall back to `message` for an unknown code. They do not parse
messages or retry rejected mutations automatically.

```json
{"error":{"message":"A valid Core key is required as the bearer credential.","type":"invalid_request_error","code":"invalid_admin_key","param":null}}
```

## Optional details

`error.details`, when present, is a nonempty flat object. Its values may only be
strings, finite numbers, booleans, null or arrays of strings (including empty
arrays). It contains documented Core-owned facts, never submitted names, URLs,
keys, echoed request values, native error text or provider response bodies. Each
operation that adds details must document its exact keys alongside its error
code. This foundation does not add operation-specific validators, diagnostics
endpoints or new details to existing operations.

The typed Go `CoreErrorDetails` values have string, number, boolean, null and
string-array constructors. `writeCoreError` emits them only through the marked
Core router; empty or invalid details are omitted as a whole. The mark preserves
error observation, flushing and `http.ResponseController` access. A shared
handler or a Core-looking request path alone cannot change a public or machine
error envelope. Core authentication still runs before operation configuration
checks, and unknown paths retain their existing status and admission rules.

`AgentCoreError.details` is optional `CoreErrorDetails` in the TypeScript client.
The Core clients accept only the flat value types above, snapshot string arrays,
and ignore malformed or empty details without changing the error's message,
status, code, param or type. The public `OpenAIAgentsClient` does not read this
Core-only field. `/v1` and `/api/v1` response shapes remain unchanged.

## Console-generated failures

The console uses the same envelope for its sign-in, origin, unsafe-request and
transport failures in the `/core` namespace. It does not expose request values
or transport exceptions. Core responses pass
through the proxy; the console does not reinterpret their codes or details.

| HTTP status | Code | Meaning | Param / details |
| --- | --- | --- | --- |
| 401 | `console_sign_in_required` | Console session is missing or expired | null / omitted |
| 403 | `console_origin_rejected` | Host, Origin or Fetch Metadata checks failed | null / omitted |
| 400 | `console_request_invalid` | Request path, method or upgrade is unsafe | null / omitted |
| 502 | `core_unreachable` | Core transport failed or Core tried to redirect | null / omitted |

The first three use `type: "invalid_request_error"`; the last uses
`type: "server_error"`. A Core `401 invalid_admin_key` remains distinguishable
from a missing console sign-in. `/console/auth` keeps its existing
`{"error":"…"}` errors. Bare, retired and direct public/machine paths do not
become proxyable operations. Host, origin, authentication, credential stripping,
path checks and the no-retry rule are unchanged.

Existing operation-specific codes remain documented in the [administrator
contract](admin-api.md), [sandbox deployment contract](sandbox-deployment.md),
[executor credential contract](environment-executor-credentials.md) and related
resource contracts. This change does not reclassify their validation failures.
