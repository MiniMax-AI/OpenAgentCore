# Administrator API

This Core extension uses deployment Bearer authentication under `/core/v1/admin`.
It does not change `/v1`, the fixed Python SDK, or native Runtime interfaces.
API keys cannot authenticate these routes; the administrator credential cannot
authenticate `/v1`. The console server supplies `X-Core-Console-Actor` from its
signed-in account. Core records it only as an unverified display label.

## Key spaces

Each key owns one tenant. There are no API users or roles.

| Operation | Path | Result |
| --- | --- | --- |
| List | `GET /api-keys` | `{data, has_more}` |
| Create | `POST /api-keys` with `{id, name}` | Safe key metadata and one-time `key`; HTTP 201 |
| Retrieve | `GET /api-keys/{key_id}` | Safe metadata, including revoked keys |
| Reset secret | `POST /api-keys/{key_id}/reset` with `{request_id}` | Safe metadata and one-time replacement `key` |
| Revoke | `DELETE /api-keys/{key_id}` | `{id, deleted: true}` |

Creation and reset IDs are caller-generated UUIDs. A duplicate create/reset returns
409; the secret is never replayed. After an uncertain response, inspect safe state
and explicitly reset using a new request UUID rather than automatically retrying.
Reset preserves the key ID and tenant and immediately invalidates the old secret.
Revocation retains resources, provenance and accepted execution.

Safe metadata: `id`, `name`, `prefix`, `kind`, `tenant_id`, `organization_id`,
`project_id`, `created_at`, `revoked_at`. List ordering is lexical key ID,
`order=asc|desc` (default `desc`), `limit=1..100` (default 20), and `after` is the last
key ID. Static keys use `static:<SHA-256 digest>`, have no persisted creation time
(the timestamp is the zero time), and reject reset/revoke with 409. Their secrets
and spaces are managed through configuration. Issued keys have a UUID ID and
`kind=issued`. Neither response contains the stored secret digest.

## Resource reads and deletion

Paths below are relative to `/api-keys/{key_id}`. The key selects a space, including
a revoked space; it does not authenticate. Shared resource handlers preserve their
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

`POST /copies` takes `source_key_id`, `target_key_id`, `resource_type`, `resource_id`,
`include_dependencies`, and optional `target_vault_id` for a standalone Credential.
Different source and target spaces are required. `Idempotency-Key` makes identical
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

`GET /summary` supports optional `key_id`, `group_by=key|agent`, inclusive
`created_after` and exclusive `created_before` RFC3339 Session-creation bounds.
`after`, `limit`, `order` paginate key spaces; Agent grouping returns groups within
that key page. Response `{data, has_more, next_cursor}` rows contain `key_id`,
nullable `agent_id`, current `assets` counts (null for Agent groups), `sessions`
counts (`total`, `idle`, `in_progress`, `requires_action`, `failed`), cumulative
`usage`, `coverage` (`measured_sessions`, `total_sessions`, nullable `ratio`), and
nullable Unix `last_active_at`. Null public Session usage contributes no tokens but
counts in the coverage denominator. Each space is read from one database snapshot;
the page is not a simultaneous deployment-wide snapshot. Totals are not billing.

`GET /runtime-observations` uses existing Session creation-order pagination and
returns `{object:"list", data:[{key_id, observation}], has_more, first_id, last_id}`.
It reuses the bounded read-only Runtime sampler and never provisions compute.
`GET /runtime-history/capabilities` and `GET /startup-configuration` reuse the
existing non-secret project projections.

`GET /audit-log` lists administrator writes newest first with `key_id`,
`resource_type`, `resource_id`, `action`, inclusive `created_after`, exclusive
`created_before`, `limit=1..100` (default 50), and opaque `after` filters. Response
is `{data, has_more, next_cursor}`. Each row has `id`, `created_at`,
`admin_credential_id` (credential digest prefix), `actor_label`, `action`,
`target_key_id`, `resource_type`, `resource_id`, `result_ids`, `request_id`,
`trace_id`. Non-copy mappings are an empty array. No credential values or request
bodies are recorded. Logs and copy ownership do not cascade away on resource
removal or key revocation.

## Private installation transition

The old inherited-static-binding issuer and management routes are removed. The
migration refuses an installation containing old issued key records instead of
silently changing their ownership or deleting data. Use a clean private deployment,
or explicitly retire old key records after preserving the assets and evidence you
need. There is no automatic data migration or historical ownership backfill.

The typed `AdminClient` in `packages/agents-client` uses this management surface.
Public SDK applications continue to use the existing Agents API client and their
own API key. See [design principles](../../docs/design-principles.md).
