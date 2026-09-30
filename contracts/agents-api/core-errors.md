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
code. Operation-specific validation details are listed below; this contract does not
add diagnostics endpoints.

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

Existing operation-specific codes remain documented in the
[administrator contract](admin-api.md), [sandbox deployment contract](sandbox-deployment.md),
[executor credential contract](environment-executor-credentials.md) and related
resource contracts. The following validators refine Core operation failures only.

## E2B online changes

The same-provider deployment PUT uses fixed safe errors. No provider response
message, template name, key or unlisted-resource count is returned in details.

| HTTP | Code | Meaning | Param |
| --- | --- | --- | --- |
| 400 | `sandbox_credential_invalid` | Provider explicitly rejected authentication | `credential` |
| 400 | `sandbox_configuration_invalid` | Candidate immutable build is invalid or does not match resources | `configuration` |
| 409 | `sandbox_credential_ownership` | Candidate key does not prove ownership/manageability of the retained deployment | `credential` |
| 503 | `sandbox_verification_unconfirmed` | Verification, receipt settlement or bounded credential fencing could not be confirmed | null |

Missing or unsettled Create receipts are uncertainty, never evidence of a different
team or released compute. The typed client projects these codes to fixed local
messages. It preserves the three nonnull fields above only when the response
status, code and param exactly match the table; all other params on these fixed
errors become null. Numeric details remain limited to `current_generation` for
`sandbox_generation_stale` and `allocations`/`pending` for `sandbox_in_use`. Unknown
credential-bearing errors become `sandbox_configuration_unconfirmed` without replay.
It also projects `409 sandbox_configuration_error` to fixed public-URL
guidance, even when a PUT omitted its key; arbitrary upstream text is never echoed.

## Operation validation

All entries below return HTTP 400 with type `invalid_request_error`. Missing,
malformed or incorrectly typed model-provider bundles use `invalid_model_provider`
before field validation. JSON body parsing retains its existing errors. Other
malformed administration requests retain `invalid_request`.

| Code | Param | Details | Meaning |
| --- | --- | --- | --- |
| `invalid_name` | `name` | `max_length`: 128 for Projects/nodes, 80 for Project keys | Name failed the resource's existing validator |
| `invalid_node_capacity` | `max_active` or `max_retained` | `min`: 1, `max`: 1000000 | Capacity is invalid; retained capacity must also be at least active capacity |
| `invalid_model_provider` | null | omitted | A complete model-provider bundle is required |
| `model_provider_base_url_invalid` | `base_url` | omitted | Requires HTTPS without credentials, query or fragment |
| `model_provider_protocol_unsupported` | `protocol` | `harness` and `allowed_protocols`, from the build's adapter catalog | Protocol is unknown or unsupported by the selected harness |
| `model_provider_api_key_invalid` | `api_key` | `max_length`: 16384 | Key is empty, too long or contains a prohibited character |
| `model_provider_token_limits_invalid` | `context_window` or `max_output_tokens` | omitted | Limits are invalid or required positive limits are missing |
| `invalid_sandbox_configuration` | `resources.cpus` | `min`: 1, `max`: 255 | CPU count is outside the supported bounds |
| `invalid_sandbox_configuration` | `resources.memory_mib` | `min`: 512, `max`: 1048576 | Memory is outside the supported bounds |
| `invalid_sandbox_configuration` | `resources.root_disk_mib` or `resources.environment_disk_mib` | `min`: 1024 for microsandbox; `min`: 0, `max`: 0 for Docker/E2B | Disk capacity is missing or unsupported by the provider |
| `invalid_sandbox_configuration` | `runtime` | omitted | Runtime release is missing, mutable, invalid or prohibited for E2B |

Bounds describe validation constants, never submitted values. Node names retain
their existing byte limit and character rules; Project/key names retain their
trimmed Unicode character limit and control-character rules. No name rules are
widened or unified. Numeric checks retain their existing order, including
provider-dependent retained capacity validation inside the existing transaction.
Model-provider checks retain URL, protocol, key, general limits, harness protocol,
then required harness limits precedence. Resource checks retain CPU, memory, disk,
then Runtime precedence.

Shared validators preserve their original error strings and sentinel identity.
Only the marked Core router maps their typed field metadata to this catalog;
public `/v1` and machine routes retain their previous complete error bodies.
Unknown sandbox providers retain the existing untyped error. E2B provider errors
above retain their fixed redaction contract with no echoed template or key.

Core administration error details are scoped by the `/core/v1` router writer mark,
not a request path test. Use `writeCoreError` with typed `CoreErrorDetails` values
and document fixed keys in `contracts/agents-api/core-errors.md` when adding a
code. Include only safe Core-owned facts; never pass submitted values, secrets,
native text or provider bodies. Invalid/empty details are omitted. Preserve the
public and machine error serializers, observer callbacks and streaming interfaces.
The Core client ignores malformed optional details and never retries a mutation.

Core operation validators preserve the original error text, sentinel identity,
and validation precedence. Package-owned typed errors carry fixed field metadata;
only the marked Core error mapper translates it to operation codes and safe
bounds/catalog details. Preserve Project/key rune limits and node byte limits
separately. Keep sandbox validation metadata through its existing store wrapper
without changing transaction or provider authority. Public Session provider
validation remains byte-compatible; cover it with handler-level golden responses.

## Diagnostic failure categories

The [root diagnostics reads](session-diagnostics.md) return these categories
inside a successful HTTP 200 snapshot, not the operation error envelope. Public
`/v1` Turn errors remain unchanged. `params` is `{}` unless specified.

| Code | Stored cause or safe meaning |
| --- | --- |
| `harness_error` | `engine_failed` without a native classification |
| `authentication_error` | Native provider authentication rejected |
| `rate_limit_exceeded` | Native rate limit classification |
| `usage_limit_exceeded` | Native billing or usage limit classification |
| `server_overloaded` | Native overload classification |
| `server_error` | Native server failure classification |
| `invalid_request` | Native request rejection |
| `resource_not_found` | Native resource/model not found |
| `request_timeout` | Reserved neutral timeout category; no current adapter producer |
| `context_length_exceeded` | Native context limit classification |
| `cyber_policy` | Native cyber policy rejection |
| `connection_failed` | Native connection failure; params contain `http_status`, an integer in 100–599 or null |
| `model_provider_required` | Missing frozen model provider |
| `runtime_unavailable` | `execution_device_unavailable`, `execution_unavailable` |
| `runtime_disconnected` | `device_disconnected`, `event_stream_incomplete` |
| `runtime_preparation_failed` | `preparation_start_failed`, `preparation_interrupted` |
| `execution_interrupted` | Core execution interrupted |
| `delivery_unconfirmed` | `delivery_unknown`, `input_outcome_unknown`, `cancel_unconfirmed`, `cancel_outcome_unavailable`, `function_result_unconfirmed` |
| `input_rejected` | `invalid_input`, `input_not_applied`, `message_input_unsupported`, and the exact steering outcomes `input_invalid_input`, `input_run_inactive`, `input_input_conflict`, `input_input_limit`, `input_unsupported`, `input_rejected`, `input_not_ready`, `input_busy` |
| `executor_protocol_error` | `invalid_executor_result`, `interaction_not_supported`, `execution_state_unavailable`, `execution_state_changed`, `function_call_invalid`, `function_result_invalid` |
| `core_storage_failed` | `event_persistence_failed`, `artifact_capture_failed` |
| `internal_error` | Unknown or malformed outcome; no raw value is returned |
| `environment_connection_timeout` | Initial input connection deadline expired |
| `environment_unavailable` | Environment unavailable for initial input |
| `environment_provisioning_failed` | Hosted provisioning failure; params contain nullable `step`, `index`, `exit_code` from a sanitized receipt |

`diagnostics_unavailable` is the HTTP 503 operation error when the diagnostic
reader is not configured; it has no details. A database failure remains an error,
never a healthy or empty diagnostic snapshot. Historical provisioning reasons and
private native messages are not parsed for categories or parameters.

Native categories apply only to a failed Turn with top-level
`error_code: engine_failed`. Only the finite `engine_error_code` allowlist is
accepted; unknown,
malformed and absent metadata retains `harness_error`. Only `connection_failed`
uses `engine_http_status`. Nested metadata and provider prose never classify a
failure. Core persistence, incomplete-stream and cancellation failures retain
priority, and cancelled/completed Turns have no failure. See
[native classification](native-error-classification.md) for adapter coverage.

Model configuration writes additionally return `model_configuration_model_invalid`
with `param: model`, or `harness_config_invalid` with `param: harness_config`.
Both carry fixed messages without submitted values. Existing provider field errors
retain their field params within the `model_provider` object.
