# Error codes

This registry lists every error `code` Core, the console (including the
installer rejections it relays) and the daemon transport write, the response shapes that carry no code, and the codes the
TypeScript client creates itself. Operation-specific triggers and `param` values
stay in the resource contracts; this page says what each code means and where it
can appear.

Two checks compare it with the code. `contract_conformance_test.go` in
`services/agents-api/internal/api` requires the status and code of every row to
be written somewhere and every written status and code to have a row, and the
statuses of the uncoded tables to equal the statuses their handlers write.
`apps/docs/scripts/verify-error-codes.mjs` does the same for client-generated
codes and for every code the client and Web compare against, and the Go test
also compares the installation domain setup codes with the installer. The
Namespaces and Meaning columns are maintained by review.

## Which "code" is meant

The word names eight different things. Only the first three are HTTP error codes.

| Layer | Where it appears | Examples | Reference |
| --- | --- | --- | --- |
| API envelope | `error.code` of a `/v1`, `/core/v1` or `/api/v1` JSON error, or of an error object inside a Session event | `invalid_request_error`, `project_archived`, `stream_interrupted` | [HTTP API codes](#http-api-codes), [Session event codes](#session-event-error-codes) |
| Console envelope | `error.code` of an error Web's server writes for `/core/*` | `console_sign_in_required`, `core_unreachable` | [Console codes](#console-codes), [Core errors](core-errors.md) |
| Daemon transport | `error` member of an `/api/v1/agent-daemon/*` JSON error | `missing_bearer`, `incompatible_version` | [Runtime daemon transport codes](#runtime-daemon-transport-codes) |
| Runtime protocol result | `error_code` of a Core–Runtime message after connection, such as a workspace, preparation or cancellation result; Core validates each value against its message and maps it to its own outcome or stored cause; no API response returns it | `write_rejected`, `read_unconfirmed`, `cancel_timeout` | [Core–Runtime protocol](../../docs/runtime-protocol.md) and its typed payloads; not listed here |
| Node diagnostic | `diagnostic` value inside a node payload; never an HTTP status | `docker_unavailable`, `kvm_unavailable` | [Sandbox deployment](sandbox-deployment.md), [nodes](../../docs/getting-started/nodes.md) |
| Session diagnostic category | `code` of a failure category inside a successful Session diagnostics snapshot | `harness_error`, `runtime_disconnected` | [Diagnostic failure categories](core-errors.md#diagnostic-failure-categories) |
| Turn error | `error.code` of a failed Turn or subagent Turn in a successful read; Core always publishes `internal_error`, and the other values of the pinned enum are never returned | `internal_error` | The cause is in the [diagnostic failure categories](core-errors.md#diagnostic-failure-categories); not listed here |
| Client identifier | `AgentCoreError.code` created by `packages/agents-client` without a response, or a local daemon/adapter error | `sandbox_configuration_unconfirmed`, `invalid_admin_response` | [Client-generated codes](#client-generated-codes), daemon and adapter guides |

`error.type` is not a second code. It follows one rule: `server_error` for any 5xx,
`conflict_error` for any 409, `not_found_error` or `invalid_beta` when the code is
that value, and `invalid_request_error` otherwise.

## HTTP API codes

`/v1`, `/core/v1` and `/api/v1` share one writer, so a code keeps its meaning in
every namespace; a row names a namespace-specific trigger where one differs. `/core/v1` adds optional `details` ([Core errors](core-errors.md)).
Clients branch on `code`, never on `message`. A `null` row is a response whose
`code` is null.

| Status | Code | Namespaces | Meaning |
| --- | --- | --- | --- |
| 400 | `invalid_request_error` | all | Request validation failed: body fields, list queries on Agents API Beta lists and on the Core resource lists under `/projects/{project_id}`, cursors, MCP credential selection or unstorable text. `param` names the field when known |
| 400 | `invalid_request` | all | Malformed body, identifier or local request limit outside the official fields, including invalid queries on the Core Project, key, audit log and summary lists |
| 400 | `invalid_value` | `/v1`, `/core/v1` | Skills: invalid `order` or Skill version `after` on `/v1`; on both namespaces, deletion of the default Skill version (param `version`) |
| 400 | `duplicate_parameter` | `/v1` | Skills list key supplied more than once; `param` is the key |
| 400 | `integer_below_min_value` | `/v1` | Skills list `limit` below 0 |
| 400 | `integer_above_max_value` | `/v1` | Skills list `limit` above 100 |
| 400 | `unsupported_parameter` | `/v1`, `/core/v1` | A body on a deletion that accepts none, a repeated Files list key, or query parameters Runtime observation and history reads do not accept |
| 400 | `invalid_beta` | `/v1` | Missing or wrong `OpenAI-Beta: agents=v1` on an Agents API Beta route |
| 400 | `unsupported_or_invalid_configuration` | `/v1` | The configuration or input is outside what the selected harness supports |
| 400 | `model_provider_required` | `/v1` | The Session resolved no model provider and cannot run |
| 400 | `invalid_sandbox_configuration` | `/core/v1` | Sandbox deployment configuration is invalid |
| 400 | `invalid_name` | `/core/v1`, `/api/v1` | A Project, key or node name, including the name a node enrolls with, fails its length or character rules; on `/core/v1`, `details.max_length` gives the limit |
| 400 | `invalid_node_capacity` | `/core/v1` | Node `max_active` or `max_retained` is outside 1-1000000, or retained is below active |
| 400 | `invalid_model_provider` | `/core/v1` | The model configuration body is missing or malformed; a complete bundle is required |
| 400 | `model_provider_base_url_invalid` | `/core/v1` | `base_url` is not HTTPS, or carries credentials, a query or a fragment |
| 400 | `model_provider_protocol_unsupported` | `/core/v1` | The protocol is unknown or unsupported by the harness; `details.allowed_protocols` lists the supported ones |
| 400 | `model_provider_api_key_invalid` | `/core/v1` | The key is empty, longer than 16384 characters or contains a prohibited character |
| 400 | `model_provider_token_limits_invalid` | `/core/v1` | `context_window` or `max_output_tokens` is invalid, or missing where the harness requires it |
| 400 | `model_configuration_model_invalid` | `/core/v1` | `model` is not a nonempty model identifier |
| 400 | `harness_config_invalid` | `/core/v1` | `harness_config` contains unsupported or invalid native model parameters |
| 400 | `e2b_api_key_invalid` | `/core/v1` | E2B rejected the API key (param `e2b.api_key`) |
| 400 | `e2b_template_build_invalid` | `/core/v1` | The E2B template build is not a ready immutable build with matching resources (param `e2b.template`) |
| 400 | null | `/v1`, `/core/v1` | Files list range and order errors on `/v1`, an unknown Files `purpose` filter (param `purpose`) on both namespaces, and public download of a `user_data` File |
| 401 | `invalid_api_key` | `/v1` | Files or Skills rejected a supplied Bearer Project API key |
| 401 | `invalid_admin_key` | `/core/v1` | The Core key is missing or wrong |
| 401 | `invalid_node_credential` | `/api/v1` | The node enrollment token or node credential is missing or wrong |
| 401 | `installation_authorization_invalid` | `/api/v1` | The native installation authorization is invalid or expired; get a new command from the Session |
| 401 | null | `/v1` | No valid Bearer Project API key on an Agents API Beta route, or none supplied to Files or Skills |
| 404 | `not_found_error` | `/v1`, `/core/v1`, `/api/v1` | The resource does not exist in the caller's or the selected Project's tenant, or the Environment of a native installation no longer exists |
| 404 | `not_found` | `/core/v1` | Unknown Core operation, unknown harness, or a harness without a deployment default model provider |
| 404 | `unsupported_operation` | `/v1` | Unknown `/v1` operation |
| 404 | null | `/v1` | Missing File or Skill on the public Files and Skills routes |
| 405 | `unsupported_operation` | all | Method not allowed, including HEAD on content downloads and Runtime reads |
| 409 | `conflict_error` | `/v1`, `/core/v1` | Official conflicts: Session not idle for deletion, pending input, MCP credential ambiguity, hosted environment failure or a different tool result |
| 409 | `turn_conflict` | `/v1` | The Session or Turn cannot accept this change in its current state, such as an Environment file write while Session input is pending |
| 409 | `idempotency_conflict` | `/v1`, `/api/v1` | The Idempotency-Key was used with different input; on sandbox node enrollment, the node ID is already enrolled |
| 409 | `environment_unavailable` | `/v1` | The Environment no longer accepts new input |
| 409 | `environment_input_expired` | `/v1` | The Environment input deadline passed before admission |
| 409 | `environment_input_cancelled` | `/v1` | The Environment input was cancelled before admission |
| 409 | `project_exists` | `/core/v1` | The Project ID already exists |
| 409 | `project_api_key_exists` | `/core/v1` | The API key ID already exists; list its metadata and revoke it if the secret was not saved |
| 409 | `project_archived` | `/core/v1` | The target Project is archived |
| 409 | `executor_credential_exists` | `/core/v1`, `/api/v1` | The executor key ID already exists; rotate it explicitly to replace the secret. On the native installation claim, the Environment already has another, rotated or revoked executor credential |
| 409 | `runtime_history_unsupported` | `/core/v1` | Runtime history is not supported for this Session |
| 409 | `sandbox_deployment_conflict` | `/core/v1`, `/api/v1` | The sandbox deployment cannot change in its current state |
| 409 | `sandbox_configuration_error` | `/core/v1` | The deployment cannot be served as configured, for example E2B with a loopback public URL |
| 409 | `sandbox_node_address_mismatch` | `/core/v1`, `/api/v1` | The node uses a different Core address than the installation public URL |
| 409 | `sandbox_specification_mismatch` | `/core/v1`, `/api/v1` | The node's resource limits or Runtime release do not match the active deployment |
| 409 | `runtime_node_in_use` | `/core/v1` | The node still holds allocations, snapshots, reservations or pending cleanup |
| 409 | `runtime_local_node_configured` | `/core/v1` | The local node is enabled in deployment configuration and cannot be removed |
| 409 | `sandbox_generation_stale` | `/core/v1` | The deployment generation changed; `details.current_generation` gives the new one. Refresh before submitting again |
| 409 | `sandbox_reset_required` | `/core/v1` | The change needs a reset first, such as another backend or E2B team; `details` names both providers |
| 409 | `sandbox_in_use` | `/core/v1` | Hosted sandbox resources still belong to the deployment; `details.allocations` and `details.pending` count them |
| 409 | `sandbox_reset_in_progress` | `/core/v1`, `/api/v1` | A sandbox reset is in progress, so the deployment cannot change and nodes cannot enroll or read their configuration |
| 409 | `sandbox_not_configured` | `/core/v1` | The operation needs a configured sandbox deployment |
| 409 | `e2b_team_mismatch` | `/core/v1` | The E2B key cannot manage the retained deployment; reset before changing teams (param `e2b.api_key`) |
| 413 | `request_too_large` | all | The body exceeds the operation's limit, or an uploaded File or Skill exceeds its content limit |
| 500 | `internal_error` | all | An unexpected persistence failure; no detail is exposed |
| 503 | `authentication_unavailable` | `/v1` | Project API key authentication is temporarily unavailable; written before any operation runs |
| 503 | `execution_unavailable` | `/v1`, `/core/v1` | Execution or Core Runtime observation is not available on this service, or a Core Runtime observation list exceeded its request budget |
| 503 | `stream_unavailable` | `/v1` | Live events or streaming creation are unavailable |
| 503 | `credential_storage_unavailable` | `/v1`, `/core/v1` | Credential encryption is not configured |
| 503 | `file_storage_unavailable` | `/v1`, `/core/v1` | Source File storage is not configured |
| 503 | `file_transfer_unavailable` | `/v1`, `/core/v1` | The bounded transfer deadline cannot be set for an upload or download |
| 503 | `skill_storage_unavailable` | `/v1`, `/core/v1` | Skill storage is not configured |
| 503 | `artifact_storage_unavailable` | `/v1`, `/core/v1` | Artifact storage is not configured |
| 503 | `subagent_storage_unavailable` | `/v1` | Subagent storage is not configured |
| 503 | `execution_configuration_unavailable` | `/core/v1` | Session execution configuration cannot be read |
| 503 | `runtime_history_unavailable` | `/core/v1` | Durable Runtime history is not configured or temporarily unavailable |
| 503 | `core_metrics_unavailable` | `/core/v1` | Core metrics are not configured or could not be read |
| 503 | `runtime_node_unavailable` | `/v1`, `/core/v1`, `/api/v1` | The selected sandbox node is unavailable, or no node has capacity for a new hosted Session |
| 503 | `sandbox_credential_unavailable` | `/core/v1`, `/api/v1` | Sandbox credentials cannot be decrypted; check the service credential encryption configuration |
| 503 | `sandbox_reset_in_progress` | `/v1` | Hosted admission is paused while a sandbox reset runs; nothing was admitted |
| 503 | `sandbox_nodes_preparing` | `/v1` | The nodes with free capacity are still preparing the deployment's Runtime |
| 503 | `e2b_request_unconfirmed` | `/core/v1` | E2B verification could not be confirmed; nothing is replayed |
| 503 | `provider_unavailable` | `/core/v1` | E2B template discovery is unavailable; check the credential, endpoint and connection |
| 503 | `diagnostics_unavailable` | `/core/v1` | The Session diagnostics reader is not configured |
| 503 | `installation_unavailable` | `/api/v1` | Matching native installation artifacts are unavailable on this Core |

The namespace column shows where each code is expected. Codes from the shared
stored-error mapping can appear on any operation whose storage reports that
condition. No 503 carries `Retry-After`.

Core's reverse-path canonicalization answers a path that cannot be decoded with a
plain-text 400 before any namespace is selected; a parsed request path always
decodes, so this is not expected in practice.

## Session event error codes

These codes appear inside Session events, in a live stream that has already
answered 200 and in the saved event history. Core's own interruption is an
`event: error` frame whose `error` object has `code`, `type` and `message` but no
`param`. A hosted provisioning failure records the pinned `error` event, with a
null `param`, and the Environment state `error` of
`agent.session.environment.failed`, which has no `param`. See
[history, events and usage](history-events-usage.md).

| Status | Code | Meaning |
| --- | --- | --- |
| 200 | `stream_interrupted` | The live stream was interrupted; reconnect, then read the Session and its saved Items to recover |
| 200 | `sandbox_error` | `error` event, type `environment_error`: the hosted Environment failed to provision; the message is a safe reason without command output |
| 200 | `environment_connection_failed` | Environment state error, type `environment_error`, in `agent.session.environment.failed` |

## Console codes

Web's server writes these for `/core/*` requests it rejects before forwarding,
and for the console-local `POST /console/installation/domain` HTTPS setup request.
See [Core errors](core-errors.md#console-generated-failures) and
[Web request boundaries](../../docs/web/architecture.md#request-boundaries).

| Status | Code | Meaning |
| --- | --- | --- |
| 400 | `console_request_invalid` | Request path, method or upgrade is unsafe |
| 400 | `domain_setup_unavailable` | Domain setup: this installation uses an external reverse proxy, so HTTPS is configured there |
| 400 | `invalid_request` | Domain setup: the request body exceeds 2 KiB |
| 401 | `console_sign_in_required` | Console session is missing or expired |
| 403 | `console_origin_rejected` | Host, Origin or Fetch Metadata checks failed |
| 502 | `core_unreachable` | Core transport failed or Core tried to redirect |
| 502 | `installation_unreachable` | Domain setup: the installer did not answer or returned an invalid or 5xx response; run `oac status` on the server |

## Installation domain setup codes

`POST /console/installation/domain` relays these installer rejections unchanged
in `{"error":{"code":"…","message":"…"}}`. The installation controller in
`deploy/install/ingress.py` writes them; see [Web management](../../docs/api/web-management.md)
and the [installer contract](../../docs/maintainers.md#managed-https-ownership).
Failures after the `202` acceptance are reported through the status `message`,
not as codes.

| Status | Code | Meaning |
| --- | --- | --- |
| 400 | `domain_setup_unavailable` | Managed HTTPS needs a combined Docker installation |
| 400 | `invalid_hostname` | The installer's hostname validation rejected the value |
| 400 | `invalid_confirmation` | `confirm_public_url_change` does not equal the new HTTPS URL |
| 400 | `invalid_request` | The body is missing, larger than 2 KiB, not JSON or has other members |
| 401 | `unauthorized` | The installer rejected the console's Core key |
| 409 | `configuration_pending` | `config.json` has pending edits; apply or revert them first |
| 409 | `installation_not_ready` | The installation has not been applied yet |
| 409 | `installation_not_running` | Core, Web, the gateway or the installation service is not running |
| 409 | `generated_files_edited` | Generated files were edited by hand; resolve them with `oac apply` |
| 409 | `public_url_confirmation_required` | The address changes existing bindings; resubmit with `confirm_public_url_change` |
| 409 | `installation_busy` | Another installation operation holds the lock, or the installer rejected the change |

## Console sign-in responses

`/console/auth`, `/console/auth/login`, `/console/auth/logout` and the other
signed-in console pages outside `/core/*` answer failures as
`{"error":"<message>"}` with no code. Clients branch on the status.

| Status | Code | Meaning |
| --- | --- | --- |
| 400 | null | The login body is not a JSON object with only a non-empty `core_key` |
| 401 | null | Wrong Core key, or a console page requested without a session |
| 403 | null | Host, Origin or Fetch Metadata checks failed outside `/core/*` |
| 404 | null | Unknown `/console/auth/*` route |
| 405 | null | Login or logout without POST (`Allow: POST`) |
| 415 | null | The login body is not `application/json` |
| 429 | null | Sign-in is busy (`Retry-After: 1`) or ten failed attempts in one minute (`Retry-After: 60`) |
| 503 | null | A session token could not be generated |

Outside `/core/*` the console also answers an unsafe path with a plain-text 400,
a wrong method on static pages and `/node-install/*` with a plain-text 405
(`Allow: GET, HEAD`), and unknown or direct `/v1` and `/api/v1` paths with a
plain-text 404. After sign-in, `/core` and `/core/*` paths outside `/core/v1`
also get a plain-text 404, `/console/api-keys` and its subpaths a plain-text 404,
and `/console/installation/domain` with a method other than GET or POST a
plain-text 405 (`Allow: GET, POST`). `GET /healthz` returns `200 ok` without
authentication.

## Runtime daemon transport codes

`/api/v1/agent-daemon/ws`, `/bootstrap` and `/device-status` answer the failures
their handlers detect as `{"error":"<code>","detail":"<text>"}`. `detail` is
diagnostic text, not a stable value. The router in front of them answers an
unknown `/api/v1/agent-daemon/*` path with a plain-text 404 and a wrong method with
an empty 405, and a failed WebSocket handshake on `ws` is answered as plain text by
the WebSocket library, so clients must not assume the JSON body on every
failure.

| Status | Code | Meaning |
| --- | --- | --- |
| 400 | `missing_params` | `device_id`, `version` or the Bearer credential is missing |
| 400 | `missing_device_id` | The bootstrap body or device-status query has no `device_id` |
| 400 | `bad_json` | The bootstrap body is not valid JSON |
| 401 | `missing_bearer` | No Bearer daemon credential |
| 401 | `unknown_device` | The device is not enrolled |
| 401 | `bad_credential` | The daemon credential does not match the device |
| 403 | `wrong_runtime_type` | The credential belongs to another Runtime type |
| 405 | `method_not_allowed` | Bootstrap without POST, if the handler is reached; the router's empty 405 normally answers first |
| 426 | `incompatible_version` | The daemon version is not supported by this Core |
| 500 | `internal` | Authentication failed unexpectedly |

## Plain-text transport responses

These routes answer failures with a `text/plain` body and no code.

| Status | Code | Routes | Meaning |
| --- | --- | --- | --- |
| 400 | null | `POST agent-daemon/enroll`, `GET agent-daemon/connection` | Invalid body or query |
| 401 | null | enroll, connection, `GET sandbox-node/connect` | Missing or rejected credential |
| 405 | null | enroll, connection | Wrong method (`Allow` names the method) |
| 409 | null | enroll, connection, sandbox-node/connect | The Environment is bound to another executor, or the node identity is already connected |
| 503 | null | enroll, connection, sandbox-node/connect | Enrollment storage, node authentication or the connection owner is unavailable |

The public installer artifacts under `/api/v1/agent-daemon/install/{version}/`
answer a method other than GET or HEAD with an empty 405 and an unknown file with
a plain-text 404.

## Client-generated codes

`packages/agents-client` creates these `AgentCoreError` codes itself; Core never
sends them.

Codes that report a malformed response use status 502 (or 0 for Core metrics)
and mean the client rejected what Core returned; they never indicate a request
error.

| Code | Meaning |
| --- | --- |
| `invalid_admin_response` | An administration or sandbox administration response has the wrong shape (`AdminClient`, `SandboxAdminClient`) |
| `invalid_response` | A Core metrics response has the wrong shape (`CoreMetricsClient`) |
| `sandbox_configuration_unconfirmed` | A sandbox configuration write failed without a confirmed outcome, or its reason was withheld because it could echo the key; refresh before submitting again |
| `credential_write_failed` | A Vault credential write was rejected; the client keeps the status but never parses the body, which could reflect the secret |
| `invalid_environment_template` | An Environment Template response has the wrong shape |
| `invalid_environment_template_list` | An Environment Template list has the wrong shape |
| `invalid_vault_resource` | A Vault response has the wrong shape or another ID |
| `invalid_vault_list` | A Vault list has the wrong shape |
| `invalid_vault_deletion` | A Vault deletion receipt has the wrong shape or another ID |
| `invalid_vault_credential` | Credential metadata has the wrong shape or another ID |
| `invalid_vault_credential_list` | A Credential list has the wrong shape |
| `invalid_vault_credential_deletion` | A Credential deletion receipt has the wrong shape or another ID |
| `invalid_session_vaults` | A Session's Vault attachments have the wrong shape |
| `invalid_environment_resource` | An Environment response has the wrong shape |
| `invalid_environment_file` | An Environment file response has the wrong shape |
| `invalid_environment_files` | An Environment files page has the wrong shape |
| `invalid_session_resource` | A Session response has the wrong shape |
| `invalid_session_list` | A Session list has the wrong shape |
| `invalid_history_resource` | A Turn, Item or other history response has the wrong shape |
| `invalid_runtime_observation` | A Runtime observation has the wrong shape |
| `invalid_stream_event` | An event stream frame has the wrong shape |
| `empty_stream` | An event stream closed before its first event |
| `invalid_source_file` | Source File metadata has the wrong shape |
| `invalid_source_file_list` | A Files list has the wrong shape |
| `invalid_source_file_content` | Source File content is incomplete or has the wrong headers |
| `invalid_skill_resource` | A Skill or Skill version response has the wrong shape |
| `invalid_skill_content` | Skill content is incomplete or has the wrong headers |
