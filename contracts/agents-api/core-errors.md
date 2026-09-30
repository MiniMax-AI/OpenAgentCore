# Core administration errors

Errors on `/core/v1` use this envelope. `message` is safe English text; `code` and `param` are nullable. Clients act on the stable `code` and the optional `param`, show `message` for an unknown code, never parse messages and never retry a rejected write automatically.

```json
{"error":{"message":"A valid Core key is required as the bearer credential.","type":"invalid_request_error","code":"invalid_admin_key","param":null}}
```

Errors on `/v1` and `/api/v1` keep their own envelopes and never carry `details`.

## Optional details

`error.details`, when present, is a nonempty flat object. Its values are strings, finite numbers, booleans, null or arrays of strings (possibly empty). It holds only Core-owned facts: never submitted names, URLs or keys, echoed request values, native error text or provider response bodies. Each code that has details lists its exact keys below.

| Code | Details |
| --- | --- |
| `sandbox_generation_stale` | `current_generation` |
| `sandbox_in_use` | `allocations`, `pending` |
| `sandbox_reset_required` | `current_provider`, `requested_provider` |
| Operation validation codes | See [operation validation](#operation-validation) |

In the TypeScript client, `AgentCoreError.details` is the optional `CoreErrorDetails`. The Core clients accept only the value types above, copy string arrays, and ignore malformed or empty details without changing the error's message, status, code, param or type. The public `OpenAIAgentsClient` does not read `details`.

## Console-generated failures

Web's console server uses this envelope for its own failures on `/core` paths ([request boundary](../../docs/web/console-server.md#request-boundary)). It never exposes request values or transport exceptions, and it passes Core's responses through unchanged.

| HTTP status | Code | Meaning | `type` |
| --- | --- | --- | --- |
| 401 | `console_sign_in_required` | The console session is missing or expired | `invalid_request_error` |
| 403 | `console_origin_rejected` | Host, Origin or Fetch Metadata checks failed | `invalid_request_error` |
| 400 | `console_request_invalid` | The path, method or upgrade is unsafe | `invalid_request_error` |
| 502 | `core_unreachable` | Core could not be reached, or Core answered with a redirect | `server_error` |

These have null `param` and no `details`. A Core `401 invalid_admin_key` therefore stays distinguishable from a missing console sign-in. Console sign-in routes keep their `{"error":"…"}` errors ([sign-in](../../docs/web/console-server.md#sign-in)).

## Sandbox provider verification

A `POST` or `PUT /core/v1/sandbox/deployment` ([sandbox deployment](sandbox-deployment.md#reset)) whose provider verifies a credential or configuration, as E2B does, fails with these fixed errors. None returns provider text, a template name, a key or a resource count.

| HTTP | Code | Meaning | `param` |
| --- | --- | --- | --- |
| 400 | `sandbox_credential_invalid` | The provider rejected the candidate credential | `credential` |
| 400 | `sandbox_configuration_invalid` | The candidate configuration, such as an E2B template build, is not ready and immutable or does not match the resources | `configuration` |
| 409 | `sandbox_credential_ownership` | The candidate credential cannot manage the retained deployment; reset before changing accounts | `credential` |
| 503 | `sandbox_verification_unconfirmed` | Verification, receipt settlement or the credential fence could not be confirmed | null |

On every deployment write, the typed client replaces the message of these codes and of the other `sandbox_*` deployment codes with fixed local text. It keeps only the `current_generation`, `allocations`, `pending`, `min` and `max` details, and keeps `param` only when status, code and param match the table or the `invalid_sandbox_configuration` rows below exactly. `409 sandbox_configuration_error` becomes fixed public-URL guidance with a null `param`, even for a `PUT` without a key. Any other error becomes `sandbox_configuration_unconfirmed` and is not resent, because a rejection could echo the key.

## Operation validation

Each code returns HTTP 400 with `type: "invalid_request_error"`. A missing, malformed or wrongly typed model-provider bundle returns `invalid_model_provider` before any field check. JSON body parsing keeps its own errors, and other malformed administration requests return `invalid_request`.

| Code | Param | Details | Meaning |
| --- | --- | --- | --- |
| `invalid_name` | `name` | `max_length`: 128 for Projects and nodes, 80 for Project keys | The name failed the resource's validator |
| `invalid_node_capacity` | `max_active` or `max_retained` | `min`: 1, `max`: 1000000 | Capacity is invalid; retained capacity must also be at least active capacity |
| `invalid_model_provider` | null | omitted | A complete model-provider bundle is required |
| `model_provider_base_url_invalid` | `base_url` | omitted | Requires HTTPS without credentials, query or fragment |
| `model_provider_protocol_unsupported` | `protocol` | `harness` and `allowed_protocols`, from the build's adapter catalog | The protocol is unknown or unsupported by the selected Harness |
| `model_provider_api_key_invalid` | `api_key` | `max_length`: 16384 | The key is empty, too long or contains a prohibited character |
| `model_provider_token_limits_invalid` | `context_window` or `max_output_tokens` | omitted | Limits are invalid, or the Harness requires positive limits that are missing |
| `model_configuration_model_invalid` | `model` | omitted | The deployment default's model is not a nonempty model identifier |
| `harness_config_invalid` | `harness_config` | omitted | The deployment default's native parameters are unsupported or invalid |
| `invalid_sandbox_configuration` | `resources.cpus` | `min`: 1, `max`: 255 | The CPU count is outside the supported bounds |
| `invalid_sandbox_configuration` | `resources.memory_mib` | `min`: 512, `max`: 1048576 | Memory is outside the supported bounds |
| `invalid_sandbox_configuration` | `resources.root_disk_mib` or `resources.environment_disk_mib` | `min`: 1024 for microsandbox; `min`: 0, `max`: 0 for Docker and E2B | Disk capacity is missing or unsupported by the provider |
| `invalid_sandbox_configuration` | `runtime` | omitted | The Runtime release is missing, mutable, invalid or not allowed for E2B |

Bounds are validation constants, never submitted values. Node names are limited in bytes; Project and key names in trimmed Unicode characters without control characters. Only the first failure is reported, in this order: model provider URL, protocol, key, general limits, the Harness's protocol, then the Harness's required limits; sandbox resources CPU, memory, disk, then Runtime. Model-provider field errors inside a `model_provider` object keep that object's field as `param`. An unknown sandbox provider returns an error without these fields.

## Diagnostic failure categories

The [Session and Turn diagnostics reads](session-diagnostics.md) return these categories inside a successful 200 snapshot, not as an error envelope. Public `/v1` Turn errors do not change. `params` is `{}` unless the table says otherwise.

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

When the diagnostics reader is not configured, the reads return 503 `diagnostics_unavailable` without details. A database failure is an error, never an empty or healthy snapshot. Provisioning reasons and native messages are never parsed for categories or parameters.

Native categories apply only to a failed Turn whose outcome has `error_code: engine_failed`. Core accepts only the listed `engine_error_code` values; an unknown, malformed or absent value stays `harness_error`. Only `connection_failed` uses `engine_http_status`. Nested metadata and provider text never classify a failure. Core storage, incomplete-stream and cancellation failures take precedence, and cancelled or completed Turns have no failure. [Native error classification](../../docs/runtime-protocol.md#native-failure-classification) lists which adapters report each category.
