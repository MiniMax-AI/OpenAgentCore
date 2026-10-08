---
title: "Machine connection API"
---

Machines call Core under `/api/v1`: sandbox nodes, Runtime daemons, the Sandbox I/O service and the self-hosted installer. Each route accepts only the credential listed for it, never the Core key or a Project API key, and a console sign-in grants nothing here. The reverse proxy sends `/api/v1` directly to Core; Web never serves these routes.

## Routes

| Route | Caller | Credential | Contract |
| --- | --- | --- | --- |
| `GET sandbox-node/configuration` | Node installer and node | Enrollment token, or node credential with `X-OAC-Node-ID` | [Read the node configuration](#read-the-node-configuration) |
| `POST sandbox-node/enroll` | Node installer | Enrollment token | [Enroll a node](#enroll-a-node) |
| `GET sandbox-node/identity?node_id=` | Node | Node credential | [Recover a node's identity](#recover-a-nodes-identity) |
| WebSocket `GET sandbox-node/connect?node_id=` | Node | Node credential | [Node generation protocol](./node-generation-protocol.md) |
| `GET agent-daemon/install/{version}/…` | Self-hosted installer | None | [Installation grant](./environment-executor-credentials.md#installation-grant) |
| `POST agent-daemon/installation`, `POST agent-daemon/installation/claim` | Self-hosted installer | Installation grant | [Installation grant](./environment-executor-credentials.md#installation-grant) |
| `POST agent-daemon/enroll` | Self-hosted daemon | Executor credential | [Enroll a self-hosted daemon](#enroll-a-self-hosted-daemon) |
| `GET agent-daemon/connection?environment_id=` | Self-hosted installer | Executor credential | [Private connection confirmation](./environment-executor-credentials.md#private-connection-confirmation) |
| `POST agent-daemon/bootstrap` | Runtime daemon | Daemon credential | [Daemon bootstrap](#daemon-bootstrap) |
| `GET agent-daemon/device-status?device_id=` | Runtime daemon | Daemon credential | [Device status](#device-status) |
| WebSocket `GET agent-daemon/ws?device_id=&version=` | Runtime daemon | Daemon credential | [Core–Runtime protocol](../../docs/runtime-protocol.md) |
| WebSocket `GET sandbox-link` | Sandbox I/O service (serve peer) and agent-host Runtime (attach peer) | The resource's Serve credential or the agent host's Runtime credential, in the Link Hello | [Sandbox link protocol](../../docs/sandbox-link-protocol.md) |

Every credential travels in an `Authorization: Bearer` header, except on `sandbox-link`, where each peer sends it in its Link Hello after the upgrade. No credential travels in a URL. Core derives the [Link URL](../../docs/configuration.md#changing-the-public-url) from `OAC_PUBLIC_URL`.

The generated [`runtime.openapi.yaml`](./runtime.openapi.yaml) describes only the sandbox-node configuration, enroll and identity routes, the two installation routes and the `sandbox-link` upgrade, whose messages the Sandbox link protocol defines. The `sandbox-node/connect` and `agent-daemon/ws` WebSockets and the daemon bootstrap, device-status, enroll and connection routes are served outside the API router and have no generated schema; this document and the linked contracts are their only definition.

## Credentials

| Credential | Issued by | Accepted on |
| --- | --- | --- |
| Enrollment token | `POST /core/v1/sandbox/enrollment-tokens` (Web **Add node**), with the node's approved capacity. One use; it expires at the response's `expires_at` | `sandbox-node/configuration` without a node ID, `sandbox-node/enroll` |
| Node credential | The node itself: it generates a secret of 32 to 256 characters without whitespace and registers it at enrollment | `sandbox-node/configuration` with `X-OAC-Node-ID`, `sandbox-node/identity`, `sandbox-node/connect` |
| Installation grant | The `x_agents_core.installation` command of a `self_hosted` Session; short-lived | `agent-daemon/installation` and its `claim` |
| Executor credential | The installation claim, or the Core-key [executor credential routes](./environment-executor-credentials.md) | `agent-daemon/enroll` and `agent-daemon/connection`; after enrollment it is also the Serve credential of the Environment's enrollment on `sandbox-link` |
| Daemon credential of a hosted sandbox | Core, for each managed allocation, delivered in the [bootstrap file](../../docs/runtime-bootstrap.md) | `agent-daemon/bootstrap`, `device-status` and `ws` |
| Operator device profile | `oac-core-device`, run by an operator with database access | `agent-daemon/bootstrap`, `device-status` and `ws` |

Core keeps only a SHA-256 digest of each token and credential it stores; installation grants are signed and not stored. Credentials are not interchangeable: each works only on its own routes.

### Operator device profile

`oac-core-device` provisions a Runtime device profile directly in the database:

```sh
umask 077
mkdir -p ~/.oac/daemon/default
OAC_DATABASE_URL=... oac-core-device --tenant <tenant-uuid> --name 'engine host' --url https://core.example > ~/.oac/daemon/default/auth.json
oac-daemon connect --profile default
```

`--tenant` is the Project's execution tenant UUID and `--url` Core's origin without a path. The command prints the profile once: `server_url` (the origin plus `/api/v1`), `runtime_id` (the device ID), `runner_credential` and `device_name`. Use a new profile rather than overwriting another device's file, and copy it privately to the same path on a remote host. `oac-core-device --tenant <tenant-uuid> --revoke <device-uuid>` revokes the device: new connections are refused at once, and an open connection closes at its next heartbeat. Core binds Sessions only to the deployment's agent host ([Session assignments](../../docs/runtime-protocol.md#session-assignments)), so a device of this profile runs no Session.

## Node routes

### Read the node configuration

`GET /api/v1/sandbox-node/configuration` returns the active deployment for node installation and recovery. It never consumes an enrollment token.

- A new node sends its enrollment token without `X-OAC-Node-ID`. The token must be valid, unexpired, unconsumed and issued by this installation. An active reset refuses this read.
- A registered node sends its node credential and its UUID in `X-OAC-Node-ID`. Without a query it reads the current target. `?generation=N` reads only a generation this node may still need: the current target, its serving pin, or one held by an unreleased allocation or placement on it; any other generation is refused. This read stays available during a reset, for owned recovery.

The response has `installation_id`, `provider`, `core_url` (the installation public URL), `generation`, `specification`, `specification_digest`, `max_active` and `max_retained`. It never contains an administrator, Project or E2B credential, and exists only for node-backed providers. The [sandbox deployment contract](./sandbox-deployment.md#canonical-node-specification) defines the specification and its digest.

### Enroll a node

`POST /api/v1/sandbox-node/enroll` registers a node and consumes the token. The body has exactly these fields:

| Field | Value |
| --- | --- |
| `node_id` | A canonical UUID the node chose |
| `credential` | The node's secret, 32 to 256 characters without whitespace |
| `name` | Display name |
| `provider` | The deployment's provider |
| `backend_fingerprint` | The node's backend namespace digest |
| `deployment_generation`, `specification_digest` | The configuration the node read |
| `core_url` | The Core origin the node stores and connects to |

Core checks, in one transaction, that the token is valid, the deployment is initialized, node-backed and not resetting, the generation and digest match the current specification, `core_url` equals the installation public URL and the node ID is new. Only then does it register the node, with the capacity approved in the token, and consume the token. The 201 response is the node identity: `node_id`, `installation_id`, `provider`, `deployment_generation`, `specification_digest`, `max_active` and `max_retained`. The node cannot submit capacity; the enrolled generation and digest stay the node's immutable identity, and later generations use separate configurations.

### Recover a node's identity

`GET /api/v1/sandbox-node/identity?node_id=` returns the same identity plus `connected` and `provider_ready`, as Core currently sees them.

### Node route errors

| HTTP | Code | When |
| --- | --- | --- |
| 400 | `invalid_request_error`, `param: "core_url"` | Enrollment without `core_url` |
| 400 | `invalid_request` | A malformed body, node ID or generation, or a provider other than the deployment's |
| 401 | `invalid_node_credential` | A missing, invalid, expired, consumed or foreign token or node credential |
| 409 | `sandbox_specification_mismatch` | The node's generation or digest does not match |
| 409 | `sandbox_node_address_mismatch` | `core_url` is not the installation public URL; the token stays unused |
| 409 | `idempotency_conflict` | The node ID is already registered |
| 409 | `sandbox_reset_in_progress` | Enrollment or a new node's configuration read during a reset |
| 503 | `runtime_node_unavailable` | The deployment is not initialized, or storage is unavailable |

The credential is checked before any deployment state, so a rejected credential, including one issued for another installation, gets 401 even before initialization or under E2B. Until the deployment is initialized, the configuration read and enrollment answer an otherwise valid token with 503, and the identity read and node connection answer 401.

## Daemon routes

### Daemon bootstrap

`POST /api/v1/agent-daemon/bootstrap` with the daemon credential and `{"device_id": "…"}` returns `device_id`, `workspace_id`, `ws_url` (derived from `OAC_PUBLIC_URL`, never from request headers), `heartbeat_seconds` and `protocol_version`. The daemon then dials `ws_url` as the [Core–Runtime protocol](../../docs/runtime-protocol.md#ownership-and-connection) describes.

### Device status

`GET /api/v1/agent-daemon/device-status?device_id=` with the daemon credential returns `device_id` and `online`, which says whether the device has a live connection to Core.

The bootstrap, device-status and WebSocket routes share one error body, `{"error": code, "detail": text}`: 400 `missing_params`, `missing_device_id` or `bad_json`; 401 `missing_bearer`, `unknown_device` or `bad_credential`; 403 `wrong_runtime_type`; 500 `internal`; and on the WebSocket 426 `incompatible_version` when `version` is not Core's exact Runtime protocol version.

### Enroll a self-hosted daemon

`POST /api/v1/agent-daemon/enroll` with the executor credential and exactly `{"environment_id": "…"}` (no query) enrolls the machine as the Environment's [Link](../../docs/sandbox-link-protocol.md) resource and returns the [launch input](../../docs/sandbox-bootstrap.md#launch-input) fields Core owns: `link_url` and `resource` (`tenant_id`, `environment_id`, `kind` `enrollment`, `id` and `generation`). It never returns another credential: the executor credential is the resource's Serve credential. The first key to enroll the Environment keeps it, and a retry with that key returns the same resource at its current generation. A successful response carries `Cache-Control: no-store`.

| HTTP | When |
| --- | --- |
| 400 | A malformed body or any query |
| 401 | An invalid, revoked or foreign credential, a deleted Session, or an Environment without current executor authority |
| 409 | Another executor key already enrolled the Environment |
| 503 | `{"error": "no_sandbox_link", "detail": "a self_hosted sandbox needs an https public URL"}`, before the credential is checked, when the [public URL](../../docs/configuration.md#changing-the-public-url) is not https; otherwise storage is unavailable |

Enrollment creates no managed allocation, binds no Session and grants no Session API access. Core binds the Session to the deployment's agent host while the machine serves the resource ([Session assignments](../../docs/runtime-protocol.md#session-assignments)). The relay rechecks the credential's authority on every Serve and Open, so rotation, revocation and Session deletion end further use. The [self-hosted guide](../../docs/getting-started/self-hosted.md) gives the operator steps, and the [executor credential contract](./environment-executor-credentials.md#revoked-or-rotated-credential) describes how the daemon handles a permanent rejection.
