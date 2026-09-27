# Administrator API

These routes live under `/core/v1` and are called by Core Web's server and
operator scripts. Every `/core/v1` route requires the Core key as a Bearer
credential; an unknown `/core/v1` path returns 404 only after authentication.
They do not change `/v1`, the fixed Python SDK, or native Runtime interfaces.
Project API keys and machine credentials cannot authenticate these routes; the
Core key cannot authenticate `/v1` or `/api/v1`. Paths below are relative to
`/core/v1`. `X-Core-Console-Actor` is a caller-asserted, display-only label that
Core records without verifying. Web sends `console`; direct Core key scripts
normally send none but could set any label. Never use it for authorization or as
proof of origin.

## Projects and keys

A Project owns one tenant and shared principal. Its keys have equal access to all
its assets. Projects and keys are database-owned; deployment configuration defines
neither. There are no API users, roles or configuration-managed business keys.
Core requires the Core key digest file (`OAC_CORE_KEY_DIGESTS_FILE`) at
startup for bootstrap and management.

| Operation | Path | Result |
| --- | --- | --- |
| List Projects | `GET /projects` | `{data, has_more}` |
| Create Project | `POST /projects` with `{name}` | Project metadata; HTTP 201 |
| Rename Project | `POST /projects/{project_id}` with `{name}` | Project metadata |
| Archive Project | `POST /projects/{project_id}/archive` | Project metadata |
| List keys | `GET /projects/{project_id}/keys` | `{data, has_more}` |
| Issue key | `POST /projects/{project_id}/keys` with `{name}` | Key metadata and one-time `key`; HTTP 201 |
| Revoke key | `DELETE /projects/{project_id}/keys/{key_id}` | `{id, deleted: true}` |
| List executor credentials | `GET /projects/{project_id}/environments/{environment_id}/executor-credentials` | `{data}` metadata only |
| Issue or rotate executor credential | `POST /projects/{project_id}/environments/{environment_id}/executor-credentials` with `{key_id, rotate}` | One-time credential; HTTP 201 |
| Revoke executor credential | `DELETE /projects/{project_id}/environments/{environment_id}/executor-credentials/{key_id}` | HTTP 204 |

Executor credentials apply only to a `self_hosted` Environment of the Project
whose Session exists. An archived Project returns 409 `project_archived` for
issuance and rotation but still lists and revokes; see
[executor credentials](environment-executor-credentials.md).
Project and key IDs are server-generated UUIDs. Project metadata contains `id`, `name`,
`created_at`, nullable `archived_at`, and `active_key_count`. Key metadata contains
`id`, `project_id`, `name`, `prefix`, `created_at`, and nullable `revoked_at`.
Project names contain 1–128 characters; key names contain 1–80. Names are display
labels and may repeat; control characters are rejected. Lists use lexical ID
ordering, `order=asc|desc` (default `desc`), `limit=1..100` (default 20), and `after`.
No response includes the stored digest or an existing credential's plaintext.
The catalog UUID identifies management paths. Its public authentication scope uses
organization `core` and project `proj_<catalog UUID>`; optional OpenAI scope headers
must match those values. All keys share subject `service_account/project:<UUID>`.

Rotate by issuing a new key in the same Project and revoking the old one. Revoking
one key leaves other keys, assets and admitted work intact. Archive atomically
marks the Project archived, revokes all its keys and records audit. Archived
Projects cannot issue keys; their assets remain available for administrator
inspection and deletion. There is no Project deletion, unarchive, key reset or
automatic write retry operation.
After an uncertain issuance response, inspect metadata and explicitly revoke any
unusable key before issuing another; plaintext cannot be recovered.

## Resource reads and deletion

Paths below are relative to `/projects/{project_id}`. The Project selects a tenant, including
an archived Project; it does not authenticate. Shared resource handlers preserve their
public object serialization, pagination, errors and deletion preconditions. They
receive an explicit target tenant, not a fabricated caller identity.

| Resource | GET routes | DELETE routes |
| --- | --- | --- |
| Agents | `/agents`, `/agents/{agent_id}` | `/agents/{agent_id}` |
| Templates | `/environment-templates`, `/environment-templates/{environment_template_id}` | Item route |
| Skills | `/skills`, `/skills/{skill_id}`, item `/content`, item `/versions`, `/versions/{version}`, version `/content` | Skill and version item routes |
| Files | `/files`, `/files/{file_id}` | Item route |
| Vaults | `/vaults`, `/vaults/{vault_id}`, item `/credentials`, `/credentials/{credential_id}` | Vault and Credential item routes |
| Sessions | `/sessions`, `/sessions/{session_id}`, item `/turns`, `/turns/{turn_id}`, `/items`, `/artifacts`, `/artifacts/{artifact_id}`, Artifact `/content`, `/execution-configuration`, `/runtime-observation`, `/runtime-history` | Session and Artifact item routes |
| Provenance | `/resource-owners`, `/write-operations` | None |

Source File content, Session events/SSE, arbitrary creation/update and execution
operations are deliberately absent. Session deletion still requires idle state;
management deletion never cancels implicitly. Deleting a Credential does not revoke
its provider authorization. Deleting a default Skill version retains the public
constraint. Skill and Artifact downloads reject HEAD like the corresponding project
operations; the Runtime observation (single and list) and Runtime history reads also
reject HEAD with 405, so HEAD never samples a provider or queries telemetry.

## Administrative Session archive

`POST /projects/{project_id}/sessions/{session_id}/archive` takes
`{"expected_generation": N}`, where N is a positive uint64 from the current
sandbox deployment. It requires the Core key, a
Web-managed deployment in maintenance and the current generation. Missing
maintenance or a stale generation returns 409. Only Core-managed
`openai_hosted` Sessions are eligible; other environment types return 400. A
Session outside the selected Project returns the same 404 as a missing Session.

One transaction marks the Environment expired (retaining an existing failed
state), requests cancellation, revokes Runtime authority and records the
administrator audit. The existing lifecycle owns compute and snapshot cleanup;
no provider operation runs inside that transaction. Unknown outcomes retain
ownership until matching provider receipts confirm release. The Session itself
is not deleted. Public history and persisted Files/Artifacts remain available;
unpersisted workspace contents are lost and the original Session cannot resume.

Both POST and `GET /projects/{project_id}/sessions/{session_id}/archive` return
`{session_id, environment_id, state}`. GET is read-only and does not require
maintenance or an expected generation. `state` describes current resource
disposition: `active`, `cleanup_pending`, or `released`. It is not archive
provenance: resources may already have expired through their normal lifecycle.
`released` does not prove that an active Turn has finished cancellation or
terminal publication; inspect that Turn separately when needed.

After an uncertain POST response, GET this resource before choosing another
write. A repeated POST has an idempotent state effect, with a separate audit
record for each accepted request. Clients never automatically retry the mutation.
`AdminClient.archiveSession` sends the generation and
`AdminClient.retrieveSessionArchive` reads the disposition; the TypeScript client
accepts only positive safe integer generations to avoid rounding JSON numbers.

Archive each retained hosted Session explicitly, then verify deployment allocation
and pending counts are zero before changing provider, resources or Runtime.
Snapshots and uncertain cleanup remain blockers. This is not a deployment-wide
bulk operation, Session migration or public Session deletion.

## Historical copy provenance

Cross-Project asset copying has been removed; no route creates copies. Resources
copied before the removal keep `api_key:null`, `source:"admin_copy"` and their
`admin_audit_id` in resource ownership, and their `copy` audit entries keep their
`result_ids` mappings. Historical unknown resources have null source and audit ID.

## Monitoring and audit

`GET /summary` supports optional `project_id`, `group_by=project|agent|key`
(default `project`), inclusive `created_after` and exclusive `created_before`
RFC3339 Session-creation bounds. Agent grouping requires `project_id`. `after`,
`limit`, `order` paginate Projects. Response `{data, has_more, next_cursor}` rows
contain `project_id`, nullable `agent_id` and `key_id`, current `assets` counts
(null for Agent/key groups), `sessions` counts (`total`, `idle`, `in_progress`,
`requires_action`, `failed`), cumulative `usage`, `coverage` (`measured_sessions`,
`total_sessions`, nullable `ratio`), and nullable Unix `last_active_at`.
Key groups attribute the entire Session to its recorded creation key, even if a
different key later sends input. Missing provenance becomes a null-key group.
Null public Session usage contributes no tokens but counts in the coverage
denominator. Each Project is read from one database snapshot; the page is not a
simultaneous deployment-wide snapshot. Totals are not billing records.

`GET /sandbox/runtime-observations` uses existing Session creation-order pagination and
returns `{object:"list", data:[{project_id, observation}], has_more, first_id, last_id}`.
It reuses the bounded read-only Runtime sampler and never provisions compute; a
provider with a batch metrics read (E2B) samples the page in one bounded request.
Each `observation` is the Runtime observation plus `disk:
{usage_bytes, limit_bytes}` with memory's null rules: E2B fills it from its
reported disk usage and capacity, Docker returns null, and microsandbox returns
null until its disk semantics are designed. The per-Session administrator
observation read keeps the shape without `disk`. The per-Session execution
configuration, Runtime observation and Runtime history exist only here; `/v1` has
no equivalents.

`GET /metrics?range=1h|6h|24h|7d` returns Core's own process metrics; see
[Core metrics](core-metrics.md).

`GET /audit-log` lists administrator writes newest first with `project_id`,
`resource_type`, `resource_id`, `action`, inclusive `created_after`, exclusive
`created_before`, `limit=1..100` (default 50), and opaque `after` filters. Response
is `{data, has_more, next_cursor}`. Each row has `id`, `created_at`,
`admin_credential_id` (credential digest prefix), `actor_label` (the caller-asserted
display label: normally `console` from Web and empty from direct Core key requests), `action`,
`project_id`, `resource_type`, `resource_id`, `result_ids`, `request_id`,
`trace_id`. `result_ids` is an empty array except on historical `copy` entries.
Executor credential writes appear with `resource_type:"executor_credential"`,
the key ID as `resource_id` and action `issue`, `rotate` or `revoke`. Deployment
default model provider writes are deployment-wide: `project_id` is null,
`resource_type:"deployment_model_provider"`, the harness as `resource_id` and
action `set` or `delete`; a `project_id` filter excludes them.
No credential values or request bodies are recorded. Logs and historical copy
ownership do not cascade away on resource removal or key revocation.

## Private installation transition

The old configured business keys, inherited-binding issuer and key-space
management routes are removed. The Core key remains separately configured. The
migration refuses an installation containing old issued key records instead of
silently changing their ownership or deleting data. Use a clean private deployment,
or explicitly retire old key records after preserving the assets and evidence you
need. There is no automatic data migration or historical ownership backfill.

The typed `AdminClient`, `SandboxAdminClient` and `CoreMetricsClient` in
`packages/agents-client` use `/core/v1`.
Public SDK applications continue to use the existing Agents API client and their
own API key. See [design principles](../../docs/design-principles.md).
