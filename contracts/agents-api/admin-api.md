# Administrator API

This Core extension uses deployment Bearer authentication under `/core/v1/admin`.
It does not change `/v1`, the fixed Python SDK, or native Runtime interfaces.
API keys cannot authenticate these routes; the administrator credential cannot
authenticate `/v1`. The console server supplies `X-Core-Console-Actor` from its
signed-in account. Core records it only as an unverified display label.

## Projects and keys

A Project owns one tenant and shared principal. Its keys have equal access to all
its assets. Projects and keys are database-owned; deployment configuration defines
neither. There are no API users, roles or configuration-managed business keys.
Core requires a separate deployment administrator credential at startup for
bootstrap and management.

| Operation | Path | Result |
| --- | --- | --- |
| List Projects | `GET /projects` | `{data, has_more}` |
| Create Project | `POST /projects` with `{name}` | Project metadata; HTTP 201 |
| Rename Project | `POST /projects/{project_id}` with `{name}` | Project metadata |
| Archive Project | `POST /projects/{project_id}/archive` | Project metadata |
| List keys | `GET /projects/{project_id}/keys` | `{data, has_more}` |
| Issue key | `POST /projects/{project_id}/keys` with `{name}` | Key metadata and one-time `key`; HTTP 201 |
| Revoke key | `DELETE /projects/{project_id}/keys/{key_id}` | `{id, deleted: true}` |

IDs are server-generated UUIDs. Project metadata contains `id`, `name`,
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
Projects cannot issue keys or receive copies; their assets remain available for
administrator inspection, deletion and copying to another active Project. There
is no Project deletion, unarchive, key reset or automatic write retry operation.
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
constraint. Skill and Artifact downloads and Runtime reads reject HEAD just as the
corresponding project operations do.

## Copies

`POST /copies` takes `source_project_id`, `target_project_id`, `resource_type`, `resource_id`,
`include_dependencies`, and optional `target_vault_id` for a standalone Credential.
Different source and target Projects are required. An archived target returns 409,
including retries of an earlier copy; an archived source remains readable. `Idempotency-Key` makes identical
retries return the committed result; a changed request conflicts. Without the
header a separate request may create another copy; clients must not retry an
uncertain copy automatically.

Response: `{mappings: [{type, source_id, target_id}], skipped: [{type, source_id,
reason}]}`. Supported types are `agent`, `skill`, `environment_template`, `file`,
`vault`, and `credential`.

- Agent configuration and model credentials are copied with new encryption
  bindings. Included referenced Vaults and supported Credentials receive new IDs.
  Uncopied/skipped MCP Credential references become null. Runtime matching still
  requires explicitly attached target Vaults.
- Skills retain every existing version number and their default/latest pointers;
  encrypted bundles are rebound to the new tenant and identifiers.
- Templates retain confidential env/setup, files, packages, inline Skills/Plugins,
  network and directory settings. Referenced files/Skills require
  `include_dependencies=true` and are rewritten to copied IDs.
- Files copy actual PostgreSQL large-object bytes, up to the existing 512 MiB limit.
- A Vault copies its allowed Credentials. `static_bearer` and non-refreshing
  `mcp_oauth` values are decrypted/re-encrypted internally. Refreshable OAuth
  credentials appear in `skipped`, with no usable copied token.
- Sessions and Artifacts are not copyable.

One transaction owns all mutations, the idempotency receipt and administrator
log. A failure rolls back every dependency and large object. Copies have
`api_key:null`, `source:"admin_copy"`, and `admin_audit_id` in resource ownership;
historical unknown resources have null source and audit ID.

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

`GET /runtime-observations` uses existing Session creation-order pagination and
returns `{object:"list", data:[{project_id, observation}], has_more, first_id, last_id}`.
It reuses the bounded read-only Runtime sampler and never provisions compute.
`GET /runtime-history/capabilities` and `GET /startup-configuration` reuse the
existing non-secret project projections.

`GET /audit-log` lists administrator writes newest first with `project_id`,
`resource_type`, `resource_id`, `action`, inclusive `created_after`, exclusive
`created_before`, `limit=1..100` (default 50), and opaque `after` filters. Response
is `{data, has_more, next_cursor}`. Each row has `id`, `created_at`,
`admin_credential_id` (credential digest prefix), `actor_label`, `action`,
`project_id`, `resource_type`, `resource_id`, `result_ids`, `request_id`,
`trace_id`. Non-copy mappings are an empty array. No credential values or request
bodies are recorded. Logs and copy ownership do not cascade away on resource
removal or key revocation.

## Private installation transition

The old configured business keys, inherited-binding issuer and key-space
management routes are removed. Deployment administrator credentials remain
separately configured. The
migration refuses an installation containing old issued key records instead of
silently changing their ownership or deleting data. Use a clean private deployment,
or explicitly retire old key records after preserving the assets and evidence you
need. There is no automatic data migration or historical ownership backfill.

The typed `AdminClient` in `packages/agents-client` uses this management surface.
Public SDK applications continue to use the existing Agents API client and their
own API key. See [design principles](../../docs/design-principles.md).
