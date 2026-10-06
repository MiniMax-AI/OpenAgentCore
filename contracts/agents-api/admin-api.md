---
title: "Core administration API"
---

The Core administration API (`/core/v1`) manages an installation: Projects and their API keys, reads and deletion of Project resources, executor credentials, deployment default models, the sandbox deployment and its nodes, monitoring and audit. Web's console server calls it for the signed-in administrator ([console server](../../docs/web/console-server.md#forwarding-to-core)); operators call it from scripts on the Core host ([script the Core API](../../docs/getting-started/operations.md#script-the-core-api)). The generated schema is [core.openapi.yaml](./core.openapi.yaml), and every error uses the [Core error envelope](./core-errors.md).

Applications never call `/core/v1`. It has no operation that creates or edits Agents, Sessions, templates, Files, Skills or Vaults, starts or cancels work, reads Source File content or streams events; applications do those through the [Agents API](../../docs/api/public-agent-api.md).

## Authentication

Every `/core/v1` request, including one for an unknown path, must send `Authorization: Bearer <Core key>`. Without it Core answers 401 `invalid_admin_key` with `WWW-Authenticate: Bearer`; an unknown path answers 404 `not_found` only after authentication.

- Core compares the SHA-256 of the bearer with the Core key digest it reads at startup from `generated/core-key-digests.json`, which `oac apply` derives from `secrets/core.key` ([Core key](../../docs/getting-started/operations.md#core-key)). A rotated Core key takes effect when Core restarts. Core serves no `/core/v1` route when no digest is configured.
- The Core key authenticates only `/core/v1`. Project API keys and machine credentials get 401 here, and the Core key gets 401 on `/v1` and `/api/v1` ([API namespaces and credentials](../../docs/api/index.md)).
- `X-Core-Console-Actor` is a label the caller asserts. Core records it as the audit `actor_label` without checking it. The console server sends `console`; scripts normally send none, which records an empty label. Never use it for authorization or as proof of origin.
- A `{project_id}` in a path selects the target Project, including an archived one; it grants nothing.

## Routes

Paths are relative to `/core/v1`.

| Routes | Purpose | Contract |
| --- | --- | --- |
| `installation` | Public URL, API base URL, source commit, the installer's process settings and what is bound to the public URL | [Installation facts](#installation-facts) |
| `projects`, `projects/{project_id}`, `projects/{project_id}/archive`, `projects/{project_id}/keys[/{key_id}]` | Projects and their API keys | [Projects and keys](#projects-and-keys) |
| `projects/{project_id}/{agents,environment-templates,skills,files,vaults,sessions}/**` | Resource reads and deletion, Session history and Artifacts | [Resource reads and deletion](#resource-reads-and-deletion) |
| `projects/{project_id}/sessions/{session_id}/archive` | Archive one hosted Session | [Session archive](#session-archive) |
| `projects/{project_id}/sessions/{session_id}/execution-configuration` | The Session's frozen model, Harness and provider selection | [Execution configuration](#execution-configuration) |
| `projects/{project_id}/sessions/{session_id}/diagnostics`, `…/turns/{turn_id}/diagnostics` | Failure categories and Item receipt timing | [Session diagnostics](./session-diagnostics.md) |
| `projects/{project_id}/sessions/{session_id}/runtime-observation`, `sandbox/runtime-observations` | Current Runtime observations | [Runtime observations](./runtime-observability-api.md), [the list's disk field](#runtime-observations) |
| `projects/{project_id}/sessions/{session_id}/runtime-history` | Stored Runtime history | [Runtime history](./runtime-observability-api.md#session-runtime-history) |
| `projects/{project_id}/environments/{environment_id}/installation` | Install commands of a `self_hosted` Environment | [Installation grant](./environment-executor-credentials.md#installation-grant) |
| `projects/{project_id}/environments/{environment_id}/executor-credentials[/{key_id}]` | Executor credentials of a `self_hosted` Environment | [Executor credentials](./environment-executor-credentials.md#core-key-routes) |
| `projects/{project_id}/resource-owners`, `projects/{project_id}/write-operations` | Which API key created a resource and each key's writes | [Write provenance](#write-provenance) |
| `harnesses`, `harnesses/{harness}/model-configuration` | Enabled Harnesses and each Harness's deployment default model | [Deployment defaults](./model-execution.md#deployment-defaults) |
| `sandbox/deployment`, `sandbox/deployment/reset`, `sandbox/providers/{provider}/discovery` | The sandbox provider, resources and Runtime, reset, and provider configuration discovery such as E2B templates | [Sandbox deployment](./sandbox-deployment.md#routes) |
| `sandbox/enrollment-tokens`, `sandbox/nodes[/{node_id}[/allocations]]` | Node enrollment tokens, nodes and their allocations and host history | [Nodes guide](../../docs/getting-started/nodes.md), [sandbox deployment](./sandbox-deployment.md), [node host history](./runtime-observability-api.md#node-host-observations-and-history) |
| `summary` | Session counts and usage by Project, Agent or key | [Summary](#summary) |
| `metrics` | Core's own process, execution, database and job metrics | [Core metrics](./core-metrics.md) |
| `audit-log` | Administrator writes | [Audit log](#audit-log) |

## Projects and keys

A Project owns one execution tenant; its keys share its principal and assets ([Projects own assets](../../docs/concepts.md#projects-own-assets)). Web's **Projects and keys** page uses these routes.

| Operation | Route | Result |
| --- | --- | --- |
| List Projects | `GET /projects` | `{data, has_more}` |
| Create a Project | `POST /projects` with `{name}` | 201 and the Project |
| Rename a Project | `POST /projects/{project_id}` with `{name}` | The Project |
| Archive a Project | `POST /projects/{project_id}/archive` | The Project |
| List keys | `GET /projects/{project_id}/keys` | `{data, has_more}` |
| Issue a key | `POST /projects/{project_id}/keys` with `{name}` | 201, the key metadata and the plaintext `key` |
| Revoke a key | `DELETE /projects/{project_id}/keys/{key_id}` | `{id, deleted: true}` |

- A Project has `id`, `name`, `created_at`, nullable `archived_at` and `active_key_count`. A key has `id`, `project_id`, `name`, `prefix`, `created_at` and nullable `revoked_at`. IDs are server-generated UUIDs.
- Project names have 1–128 characters and key names 1–80; names are labels and may repeat, and control characters are rejected.
- Lists order by ID with `order=asc|desc` (default `desc`), `limit=1..100` (default 20) and `after`.
- Only the issuance response contains the key's plaintext, in `key`; Core stores its digest. Show it once and never cache it. After an uncertain issuance response, list the keys and revoke any you cannot use before issuing another.
- Archive marks the Project archived, revokes all its keys and writes the audit entry in one transaction. Issuing a key in an archived Project returns 409 `project_archived`. There is no Project deletion, unarchive or key reset.

The [`/v1` authentication rules](./wire-semantics.md#authentication) define key lookup, revocation visibility, scope headers and authentication errors.

## Resource reads and deletion

Paths are relative to `/core/v1/projects/{project_id}`. Each read returns the same object, pagination and errors as the matching `/v1` operation, and each deletion has the same preconditions.

| Resource | Reads | Deletion |
| --- | --- | --- |
| Agents | `/agents`, `/agents/{agent_id}` | `/agents/{agent_id}` |
| Environment Templates | `/environment-templates`, `/environment-templates/{environment_template_id}` | The item route |
| Skills | `/skills`, `/skills/{skill_id}`, `/skills/{skill_id}/content`, `/skills/{skill_id}/versions`, `/skills/{skill_id}/versions/{version}` and its `/content` | Skill and version item routes |
| Files | `/files`, `/files/{file_id}` | The item route |
| Vaults | `/vaults`, `/vaults/{vault_id}`, `/vaults/{vault_id}/credentials`, `/vaults/{vault_id}/credentials/{credential_id}` | Vault and Credential item routes |
| Sessions | `/sessions`, `/sessions/{session_id}`, and under it `/turns`, `/turns/{turn_id}`, `/items`, `/artifacts`, `/artifacts/{artifact_id}` and its `/content` | Session and Artifact item routes |

- Administrator deletion never cancels work: a Session that `/v1` could not delete, because a Turn or input is pending, returns the same 409.
- Deleting a Credential removes Core's copy only; it does not revoke the authorization at the provider.
- `HEAD` on Skill and Artifact content, a Runtime observation, the Runtime observation list and Runtime history returns 405, so it never samples a provider or queries telemetry.
- Administrator deletions appear in the [audit log](#audit-log), not in key write history.

## Session archive

`POST /projects/{project_id}/sessions/{session_id}/archive` with `{"expected_generation": N}` releases one Core-managed `openai_hosted` Session's sandbox without a deployment reset. N is the current generation of the [sandbox deployment](./sandbox-deployment.md), a positive integer. The deployment must be configured in Web.

| Case | Result |
| --- | --- |
| Stale generation | 409 `sandbox_generation_stale` |
| Environment type other than `openai_hosted` | 400 |
| Session missing or in another Project | 404 |

One transaction marks the Environment expired (a failed Environment stays failed), requests cancellation of running work, revokes the Runtime's authority and writes the audit entry. Cleanup of the sandbox and its snapshot follows through the normal lifecycle; a resource whose release is uncertain stays owned until the provider confirms it. The Session is not deleted: its history and persisted Files and Artifacts stay readable, unpersisted workspace contents are lost and the Session cannot resume.

`POST` and `GET /projects/{project_id}/sessions/{session_id}/archive` return `{session_id, environment_id, state}`. `GET` is read-only and needs no generation. `state` is the resource's current disposition: `active`, `cleanup_pending` or `released`, whatever released it. `released` does not mean an active Turn has finished cancelling; read the Turn for that.

After an uncertain `POST` response, `GET` the archive before writing again. Repeating the `POST` has the same effect and records one audit entry per accepted request. To clear every hosted Session before changing the deployment, use the [deployment reset](./sandbox-deployment.md#reset).

## Execution configuration

`GET /projects/{project_id}/sessions/{session_id}/execution-configuration` reports the model, Harness, native parameters and model provider a Session froze at creation. It reads only stored configuration: it never contacts a provider, starts a Turn or wakes a sandbox. Responses carry `Cache-Control: no-store`.

```json
{
  "object": "agent.session.execution_configuration",
  "schema_version": 1,
  "session_id": "013773a9-44b9-4f84-baca-b51c04a01201",
  "model": {"value": "requested-model", "source": "session"},
  "harness": {"value": "codex", "source": "agent"},
  "harness_config": {"value": {"model_reasoning_effort": "high"}, "source": "agent"},
  "model_provider": {
    "source": "agent",
    "status": "available",
    "configuration": {"protocol": "responses", "base_url": "https://model.example/v1", "api_key_configured": true}
  }
}
```

Each `source` is `session`, `agent`, `deployment` or `unknown`, recorded independently: a Session can override the model and keep its Agent's Harness and provider. An explicit inline Harness is `session`; an inline `agent.x_agents_core: null` resets the Harness to `deployment` and keeps the inherited provider; a null Session provider inherits normally. `harness_config.value` is `{}` when no native parameters apply. [Model execution](./model-execution.md) owns how each value is resolved.

| `model_provider.status` | Meaning | `configuration` |
| --- | --- | --- |
| `available` | Core recorded a safe view of the frozen provider, including a deployment default (source `deployment`) | `protocol`, `base_url`, `api_key_configured` and, when set, `context_window` and `max_output_tokens` |
| `redacted` | A deployment selection recorded without a safe view | null |
| `unavailable` | Core has no trustworthy record of the provider (source `unknown`). Execution may still have succeeded | null |

Core writes this record in the same transaction that creates the Session. Later Agent edits or deletion, deployment default changes, restarts and same-key creation retries never change it. A Session without the record reports its stored model and Harness with source `unknown`, a null value where none is stored, and an `unavailable` provider. A missing Session and one in another Project return the same 404. The response never contains keys, ciphertext, secret references, native headers or query parameters.

## Installation facts

`GET /installation` reports what an administrator needs to call and change this installation. It answers before any sandbox deployment exists and calls no provider or model.

| Field | Meaning |
| --- | --- |
| `object` | `core.installation` |
| `installation_id` | The installation ID from `state.json` ([installation directory](../../docs/configuration.md#installation-directory)); null when Core runs without the sandbox manager |
| `public_url` | The [`public_url`](../../docs/configuration.md#settings) setting: the origin applications, nodes, sandboxes and self-hosted executors use. Null when unset |
| `api_base_url` | `public_url` followed by `/v1`, the `OPENAI_BASE_URL` for Project API keys. Null when `public_url` is null |
| `local_only` | True when `public_url` names a loopback host, which only the Core host reaches |
| `source_commit` | The full source commit Core was built from; null for development builds |
| `configuration` | The process settings Core loaded from its environment. `path` and `apply_command` are empty, and `applied_at` is null |
| `address_bindings` | What a change of `public_url` affects, counted on each read |

`configuration.settings` has one entry per setting Core loaded, with its dotted `key`, effective `value`, `default`, whether it is `changeable`, whether it is `sensitive`, and the services it `restarts` (`core`, `web`, `database`).

A sensitive setting has null `value` and `default` and a boolean `configured` instead; only sensitive settings have `configured`. `oac-core check-config` validates the same environment and exits without starting Core or printing a value. [Configuration](../../docs/configuration.md) describes each setting.

| `address_bindings` field | Meaning |
| --- | --- |
| `nodes` | Enrolled nodes that are not removed |
| `nodes_on_other_address` | Nodes enrolled with an address other than `public_url`. They receive no new sandboxes; remove and add them again. At most `nodes` |
| `hosted_sandboxes` | Retained and pending hosted sandboxes, which run with the address current when they started |
| `self_hosted_executors` | Unrevoked executor credentials, whose executors were installed with the `remote_url` then advertised |

## Write provenance

Core records which Project API key made each successful public write, in the same transaction as the write; if the record fails, the write fails. Reads, rejected requests and Core's own maintenance, such as OAuth token refresh and cleanup, are not recorded.

- A write is recorded when it commits. Session input counts once admitted, including input reserved for an Environment that is still preparing; a later execution failure or a lost response does not remove the record. An explicit empty input batch and a repeated creation or deletion are recorded without changing ownership.
- An Environment file upload is recorded when the Runtime confirms the write. Core saves the key, request and trace before sending the file, and records only confirmed uploads.
- Creating a resource also records its creation owner. Updates, retries and no-op writes never change it. A new Skill's first version and a new Session's Environment share the creating operation. Artifacts come from the Runtime and have no creation owner; deleting one is recorded.
- Revoking a key stops new writes but keeps its history. Deleting a resource keeps its creation owner and operation history.
- Each record holds its ID, time, key metadata, action, resource type and ID, parent ID, `request_id` and `trace_id`. `request_id` is server-generated per request; a shared `trace_id` is not an idempotency key. Records never hold request or response bodies, secrets, model credentials, tokens, file paths or file contents.

| Public write | `action` | `resource_type` (parent) |
| --- | --- | --- |
| Agent create, update, delete | `create`, `update`, `delete` | `agent` |
| Session create, update, delete | `create`, `update`, `delete` | `session` |
| Session events | `send_events` | `session` |
| Artifact delete | `delete` | `artifact` (`session`) |
| Environment file upload | `upload_file` | `environment` (`session`) |
| Environment Template create, update, delete | `create`, `update`, `delete` | `environment_template` |
| Skill create, default version change, delete | `create`, `update_default_version`, `delete` | `skill` |
| Skill version upload, delete | `upload_version`, `delete` | `skill_version` (`skill`) |
| Source File upload, delete | `create`, `delete` | `file` |
| Vault create, delete | `create`, `delete` | `vault` |
| Credential create, replace, delete (static and OAuth) | `create`, `update`, `delete` | `credential` (`vault`) |

Both routes accept only the parameters listed; an unknown, repeated or empty parameter returns 400, and a missing Project 404.

`GET /projects/{project_id}/resource-owners?resource_type=agent&resource_ids=id1,id2` returns the creating key of 1–100 resources of one type, in request order. `resource_type` is `agent`, `session`, `environment`, `environment_template`, `skill`, `skill_version`, `file`, `vault`, `credential` or `artifact`.

```json
{"data":[
  {"resource_id":"id1","api_key":{"id":"key-uuid","name":"SDK","prefix":"pc_example","kind":"issued","revoked_at":null},"source":"api_key","admin_audit_id":null},
  {"resource_id":"id2","api_key":null,"source":null,"admin_audit_id":null}
]}
```

`api_key` and `source` are null when Core has no creation record, including for resources in another Project. `source: "admin_copy"` with an `admin_audit_id` marks a resource recorded by a `copy` entry in the audit log; no current route writes one.

`GET /projects/{project_id}/write-operations` lists writes newest first by `(created_at, id)`. Filters: `key_id`, `resource_type`, `resource_id`, inclusive `created_after` and exclusive `created_before` (RFC 3339). `limit` is 1–100, default 50. Pass the previous `next_cursor` as `after` with unchanged filters. The response is `{data, has_more, next_cursor}`; each entry has `id`, `created_at`, `api_key`, `action`, `resource_type`, `resource_id`, `parent_id` (empty when absent), `request_id` and `trace_id`.

Creation records are kept for good, including after the resource is deleted. Other records are kept for [`core.write_audit_retention`](../../docs/configuration.md#settings), 90 days by default; every minute Core deletes up to 1,000 expired records, so a backlog drains over several passes. Revoking a key or deleting a resource never deletes records.

## Summary

`GET /summary` counts Sessions and usage.

| Parameter | Meaning |
| --- | --- |
| `project_id` | One Project; required for `group_by=agent` |
| `group_by` | `project` (default), `agent` or `key` |
| `created_after`, `created_before` | Inclusive and exclusive RFC 3339 bounds on Session creation |
| `after`, `limit`, `order` | Paginate Projects |

The response is `{data, has_more, next_cursor}`. Each row has `project_id`, nullable `agent_id` and `key_id`, current `assets` counts (null for Agent and key groups), `sessions` counts (`total`, `idle`, `in_progress`, `requires_action`, `failed`), summed `usage`, `coverage` (`measured_sessions`, `total_sessions`, nullable `ratio`) and nullable Unix `last_active_at`.

- A key group counts each Session under the key that created it, even when another key later sends input. Sessions without a recorded creator form a group with a null `key_id`.
- A Session whose public usage is null adds no tokens but counts in the coverage denominator.
- Each Project is read in one database snapshot; a page is not one snapshot of the whole deployment. Totals are operational counts, not billing records.

## Runtime observations

The [Runtime telemetry API](./runtime-observability-api.md) owns current observations, the [list-only disk field](./runtime-observability-api.md#disk), Session history and node host observations and history.

## Audit log

`GET /audit-log` lists administrator writes newest first. Filters: `project_id`, `resource_type`, `resource_id`, `action`, inclusive `created_after` and exclusive `created_before` (RFC 3339). `limit` is 1–100, default 50, with the opaque `after` cursor. The response is `{data, has_more, next_cursor}`.

Each entry has `id`, `created_at`, `admin_credential_id` (the first 8 hex characters of the Core key digest), `actor_label`, `action`, `project_id`, `resource_type`, `resource_id`, `result_ids`, `request_id` and `trace_id`. `result_ids` is an empty array except on `copy` entries. Deployment-wide entries have `project_id: null`, and a `project_id` filter excludes them.

| `resource_type` | `action` | `resource_id` |
| --- | --- | --- |
| `project` | `create`, `rename`, `archive` | Project ID |
| `api_key` | `create`, `revoke` | Key ID |
| `executor_credential` | `issue`, `rotate`, `revoke` | Key ID |
| `session` | `archive` | Session ID |
| `agent`, `environment_template`, `skill`, `skill_version`, `file`, `vault`, `credential`, `session`, `artifact` | `delete` | Resource ID |
| `deployment_model_provider` (deployment-wide) | `set`, `delete` | Harness |
| `sandbox_deployment` (deployment-wide) | `change`, `replace_credential` (a provider credential was submitted, even the same one), `reset_start`, `reset_force`, `reset_deadline`, `reset_cancel`, `reset_complete` | Installation ID |

An administrator write and its audit entry commit in one transaction; if the entry fails, the write fails. A reset's background archives and its `reset_deadline` and `reset_complete` entries carry the requester's credential, actor label, request and trace, and each archive keeps its Session's Project. Cancelling a reset does not undo archives already committed. Rejected provider verifications and no-op updates write no entry. Entries never contain credential values, request bodies or provider response text, and they survive the deletion of their resource and the revocation of keys.
