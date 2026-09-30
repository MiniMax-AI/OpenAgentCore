# Vaults and Credentials

A Vault is a Project-owned container of Credentials. A Credential holds the secret for one HTTPS MCP server: a `static_bearer` token or an `mcp_oauth` grant. A Session attaches Vaults in `vault_ids`; Core selects one Credential per HTTP MCP server when the Session is created and hands the decrypted token to the Runtime only when it dispatches work. Secrets are write-only: no read returns a token, refresh token, client secret or ciphertext.

The application owns OAuth authorization and consent, provider revocation and any approval policy. Core has no authorization redirect, callback, revocation or public refresh endpoint, and creating or replacing a Credential never contacts the MCP server or the OAuth provider.

## Store and use a credential

```python
vault = client.beta.agents.vaults.create(name="internal")
credential = client.beta.agents.vaults.credentials.create(
    vault.id,
    name="Internal MCP",
    auth={
        "type": "static_bearer",
        "mcp_server_url": "https://mcp.example.com/endpoint",
        "token": token_from_private_configuration,
    },
)
session = client.beta.agents.sessions.create(
    environment={"type": "none"},
    vault_ids=[vault.id],
    input="List the open incidents.",
    agent={
        "model": "your-model-id",
        "tools": [{
            "type": "mcp",
            "server_label": "internal",
            "transport": {"type": "http", "server_url": "https://mcp.example.com/endpoint"},
        }],
    },
)
```

The MCP tool may name the Credential in `credential_id`; without it, Core picks the attached Credential whose `mcp_server_url` equals the tool's `server_url` ([selection](#credential-selection-in-a-session)). Which Harness and placement can connect to the server depends on the tool's `connection_origin` ([MCP connection origin](environments.md#public-mcp-connection-origin)).

## Routes

All routes are under `/v1`, take a Project API key and require `OpenAI-Beta: agents=v1`. The Project's keys share its Vaults; another Project's Vault or Credential answers 404, the same as a missing one.

| Operation | Route | Result |
| --- | --- | --- |
| Create a Vault | `POST /vaults` | 201 Vault |
| Retrieve a Vault | `GET /vaults/{vault_id}` | Vault |
| List Vaults | `GET /vaults` | List of Vaults |
| Delete a Vault | `DELETE /vaults/{vault_id}` | `{id, object: "vault.deleted", deleted: true}` |
| Create a Credential | `POST /vaults/{vault_id}/credentials` | 201 Credential |
| Retrieve a Credential | `GET /vaults/{vault_id}/credentials/{credential_id}` | Credential |
| List Credentials | `GET /vaults/{vault_id}/credentials` | List of Credentials |
| Replace secrets | `POST /vaults/{vault_id}/credentials/{credential_id}` | Credential |
| Delete a Credential | `DELETE /vaults/{vault_id}/credentials/{credential_id}` | `{id, object: "vault.credential.deleted", deleted: true}` |

A malformed, missing or foreign ID returns 404 `not_found_error`, and so does a Credential addressed through a Vault that does not own it. Both lists order by creation time, then ID, and filter by `status` (`active`, `archived` or both); the [list rules](wire-semantics.md#lists) give the paging and filter details. Core stores the status privately, defaults it to `active` and has no operation that archives a Vault or Credential. A Credential's status is independent of its Vault's.

## Vaults

| Field | Rules |
| --- | --- |
| `name` | Optional. Omitted stays `null`; explicit `null` is rejected. A string is trimmed and must then hold 1–256 UTF-8 bytes |
| `metadata` | Omitted or `null` becomes `{}`. Values must be strings; another type returns 400 `invalid_request_error` with param `metadata.<key>`. The encoded object is limited to 64 KiB, with no pair-count or length limits |

A Vault reads as `id`, `object: "vault"`, `created_at`, `name` and `metadata`. There is no update route.

Deleting a Vault removes it and all its Credentials in one transaction, whatever their status. It needs no storage key and sends no request to any provider. The effects on Sessions are under [Deletion](#deletion).

## Credentials

Creation takes a required `name` (trimmed, 1–256 UTF-8 bytes) and an `auth` object whose `type` is `static_bearer` or `mcp_oauth`. `mcp_server_url` must be an absolute HTTPS URL without userinfo or fragment; Core keeps it byte for byte, including any query, and performs no DNS or HTTP request.

A Credential reads as `id`, `vault_id`, `name`, `object: "vault.credential"`, `created_at`, `updated_at` and `auth`. `auth` holds `type` and `mcp_server_url`; an OAuth Credential adds `expires_at` and `refresh` with `client_id`, `token_endpoint`, `token_endpoint_auth.type`, `resource` and `scope`. Reads and lists need no storage key.

### Static bearer

`auth` is `{type: "static_bearer", mcp_server_url, token}`. `token` is a required nonempty string. Core stores it as opaque bytes without trimming. To run, the token must be an RFC 6750 `b64token`; a Session that selects a stored token with other characters, such as whitespace, fails at dispatch.

Replace the token with `POST /vaults/{vault_id}/credentials/{credential_id}` and exactly `{"auth": {"type": "static_bearer", "token": "…"}}`. A missing, null or empty token or any other field is rejected. Only the token and `updated_at` change; the ID, Vault, name, type, `mcp_server_url`, `created_at` and every Session binding stay. A failed replacement leaves the old token in place.

### OAuth

`auth` is `{type: "mcp_oauth", mcp_server_url, access_token, expires_at, refresh}`:

| Field | Rules |
| --- | --- |
| `access_token` | Required, nonempty |
| `expires_at` | Optional, nullable RFC 3339 timestamp. Core accepts an expired value at creation |
| `refresh` | Optional, nullable: `client_id`, `refresh_token`, HTTPS `token_endpoint` and `token_endpoint_auth` are required; `resource` and `scope` are optional nullable strings |
| `refresh.token_endpoint_auth` | `{type: "none"}` with no `client_secret` member, or `client_secret_basic` / `client_secret_post` with a write-only `client_secret` |

Replace grant material with the same update route and `auth.type: "mcp_oauth"`. The patch must change at least one of `access_token`, `expires_at`, `refresh.refresh_token`, `refresh.token_endpoint_auth.client_secret` or `refresh.scope`; an empty `access_token` is rejected. The ID, name, type, `mcp_server_url`, `client_id`, `token_endpoint`, `resource` and endpoint authentication method never change, and a `refresh` block cannot be added to a Credential created without one. `token_endpoint_auth`, when sent, must name the stored method, which must be `client_secret_basic` or `client_secret_post`.

| Update field | Omitted | `null` |
| --- | --- | --- |
| `access_token` | Keep | Keep |
| `expires_at` | Keep; cleared when a new `access_token` is sent | Clear |
| `refresh` | Keep | Keep |
| `refresh.refresh_token` | Keep | Keep |
| `refresh.scope` | Keep | Stop sending a scope |
| `refresh.token_endpoint_auth` | Keep | Keep |
| `refresh.token_endpoint_auth.client_secret` | Keep | Keep |

Changing a Credential's `auth.type` through an update returns 400.

## Credential selection in a Session

`vault_ids` on Session creation lists the Vaults whose Credentials the Session may use. Omitted, `null` and `[]` attach none; a `null` entry is invalid; every Vault must belong to the Project, or creation returns 404 `not_found_error`. Saving `credential_id` on an Agent authorizes nothing; only the Session's attachments do.

For each HTTP MCP tool, Core selects a Credential among the attached Vaults when the Session is created:

- With `credential_id`, that Credential must be in an attached Vault and its `mcp_server_url` must equal the tool's `server_url`.
- Without it (omitted or `null`), the one static or OAuth Credential whose `mcp_server_url` equals `server_url` is selected. With none, the tool connects anonymously. With several, creation fails; Core never prefers one type.

Selection runs after the inline Agent's validation and the input requirement, and before anything is written. A rejected creation writes nothing. Failures have a null `param`:

| Case | Response |
| --- | --- |
| `credential_id` with no attached Vault | 400 `invalid_request_error`: `MCP credential_id requires an attached vault` |
| `credential_id` missing, malformed, in another Project or in a Vault not attached | 400 `invalid_request_error`: `MCP credential_id <id> was not found in an attached vault` |
| Credential in an attached Vault for another URL | 400 `invalid_request_error`: `MCP credential_id <id> does not match server_url <url>` |
| Several Credentials match without `credential_id` | 409 `conflict_error`: `multiple attached vault credentials match MCP server_url <url>; specify credential_id` |
| Unknown or foreign Vault in `vault_ids` | 404 `not_found_error` |

`<id>` and `<url>` repeat the request's values only when they are at most 256 bytes of printable UTF-8; otherwise the message leaves them out. The missing, foreign, unattached and malformed cases give byte-identical responses for the same ID, so a reference reveals nothing about Vaults the caller did not attach. The URL in a mismatch is the tool's.

The Session freezes its attachments and each selection, anonymous ones included. Later changes to the Vaults never reselect: an identical creation retry returns the original Session and selection. Session reads, lists and event snapshots show an implicitly selected Credential's ID in the tool's `credential_id`, also after that Credential is deleted; anonymous tools show `null` and an explicit ID reads as sent. The stored request keeps the caller's value, so retries compare the original request.

At each dispatch Core rechecks the Project, the attached Vault, the selected Credential, its type and the exact URL, then decrypts the token only into the Runtime's execution request. Reads of the Session, its configuration and its history never contain it. A server with a selected Credential runs only on a Runtime that advertises `mcp_http_bearer_auth`. A missing Credential, a failed decryption or a failed refresh fails the work; Core never falls back to another Credential or to an anonymous connection.

## OAuth refresh

At dispatch, an OAuth Credential whose `expires_at` has passed is refreshed before Core sends the work to the Runtime. A grant with a null `expires_at` is used as is and never refreshed proactively. An expired grant without `refresh` fails the work.

The refresh is one `refresh_token` exchange with the stored endpoint authentication method, `scope` and `resource`, with no redirects, no method probing and no retry after a failure. The provider must return a `Bearer` token whose expiry, if any, is in the future. Core commits the new access token, the new expiry (or none) and any rotated refresh token before it uses the token; an omitted refresh token keeps the old one. A PostgreSQL row lock serializes refresh with manual replacement and with deletion, so a stale refresh never restores a deleted or replaced grant. A failed exchange or commit returns nothing and is not retried. When a provider rotates the grant and the local commit then fails, the application must reauthorize. Provider error bodies are neither returned nor logged, and Harnesses receive only the access token, never the refresh token or client secret.

Token endpoints must be HTTPS. Core resolves the host, rejects loopback, private, link-local and shared (`100.64.0.0/10`) addresses, and dials the checked address so a second DNS answer cannot redirect it; TLS still verifies the host name. It ignores ambient HTTP proxies. For a private issuer, the operator lists its exact HTTPS origin in [`core.oauth_trusted_origins`](../../docs/configuration.md#settings); that allows private addresses for that host and port only, never plain HTTP, redirects or invalid certificates. The issuer's CA must be in Core's trust store.

## Storage key

Core seals every token, refresh token and client secret with AES-256-GCM under the installation's [`secrets/credential.key`](../../docs/configuration.md#installation-directory), bound to the Project, Vault, Credential, auth type and `mcp_server_url`. A wrong key, a modified row or a row moved to another binding fails to decrypt. Names are metadata outside the binding. The key and plaintext tokens exist in trusted service memory; encryption protects stored secrets and does not protect against a compromised service host.

Without a configured key, Credential creation and replacement return 503 `credential_storage_unavailable` before writing; reads, lists, deletion and Vault operations still work. An unreadable or malformed key file stops Core at startup. Losing or replacing the key makes every stored secret unusable; Core supports one key, with no rotation or re-encryption.

## Deletion

Deleting a Credential, or its Vault, removes the credential rows. It does not erase secrets from PostgreSQL pages, write-ahead logs, backups or native history. Afterwards its reads, updates and repeated deletion return 404, lists omit it, new Sessions cannot select it and new Credentials cannot be created in a deleted Vault (a missing storage key still answers 503 first).

Existing Sessions keep their frozen attachments and selections, and their history stays readable. The next dispatch that needs a deleted Credential fails; Core neither selects another Credential nor connects anonymously. Deletion does not cancel running work, withdraw a token already sent to a Runtime or revoke the grant at its provider: cancel the Session and revoke the grant yourself when needed. Revoked or invalid OAuth grants likewise fail until replaced.
