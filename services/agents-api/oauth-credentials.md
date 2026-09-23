# OAuth MCP credentials

Core implements the pinned `mcp_oauth` Credential variant alongside
`static_bearer`. The application obtains authorization and consent from its OAuth
provider, then stores the resulting grant through the ordinary Vault API. Core
has no authorization redirect, callback, provider-revocation or public refresh
endpoint. These responsibilities follow the [official Vault guide](https://developers.openai.com/api/docs/guides/agents-api/tools/vaults).
The protocol pin in `contracts/agents-api/upstream.json` remains authoritative.

## Store and use an authorized grant

```python
credential = client.beta.agents.vaults.credentials.create(
    vault.id,
    name="Authorized MCP account",
    auth={
        "type": "mcp_oauth",
        "mcp_server_url": mcp_url,
        "access_token": grant.access_token,
        "expires_at": grant.expires_at,
        "refresh": {
            "client_id": oauth_client_id,
            "refresh_token": grant.refresh_token,
            "token_endpoint": issuer_token_endpoint,
            "token_endpoint_auth": {
                "type": "client_secret_basic",
                "client_secret": oauth_client_secret,
            },
        },
    },
)
```

`token_endpoint_auth` accepts `none`, `client_secret_basic`, or
`client_secret_post`. The latter two require a write-only client secret.
`refresh.resource` and `refresh.scope` are optional nullable strings. Expiry is an
optional nullable RFC 3339 timestamp. Without refresh configuration Core can use a
known-valid or unknown-expiry access token, but rejects an expired token.

Attach the Vault and reference the exact HTTPS MCP destination as described in
[credential storage](credentials.md#use-a-credential-in-a-session). Static and
OAuth credentials share tenant checks, frozen selection and the existing bearer
Runtime contract. A unique implicit selection searches both authentication types;
ambiguity fails rather than preferring either type. Only existing qualified MCP
harness/placement profiles are supported. OAuth is not a new harness capability:
adapters receive the access token, never refresh tokens or client secrets.

Resource reads and lists select safe metadata without decryption. OAuth metadata
contains expiry and refresh client ID, endpoint, authentication method, resource
and scope. Access tokens, refresh tokens and client secrets never appear there.
Creation and manual replacement do not contact the OAuth provider or MCP server.

## Refresh and replace

At execution dispatch, Core rechecks the selected tenant, attached Vault,
Credential identity, auth type and exact MCP URL. A known-expired grant is refreshed
before native execution. Core uses the existing OAuth library with the declared
endpoint authentication method and stored scope/resource. Refresh is a single
bounded exchange, without authentication-method probing, redirects or automatic
401 retries. Provider error bodies are not returned or logged.

A PostgreSQL row lock serializes refresh and manual replacement with Credential
and Vault deletion. The complete grant is authenticated before use. Refreshed
access token, expiry and an optional replacement refresh token are committed
atomically before an access token is returned. An omitted refresh token retains
the previous value. A failed exchange or commit never returns the new token or
falls back to anonymous access. A provider may rotate its grant before a local
commit fails; that uncertain outcome can require application reauthorization.
Core does not retry an uncertain external grant exchange to hide it.

Use the pinned Credential update operation to replace grant material:

```python
client.beta.agents.vaults.credentials.update(
    credential.id,
    vault_id=vault.id,
    auth={"type": "mcp_oauth", "access_token": new_token},
)
```

Explicitly empty access tokens are rejected at creation and replacement. A
replacement must include a mutable grant field; a type-only or otherwise empty
patch is rejected before reading or changing secret material. Existing optional
null semantics below remain qualified separately.

Identity, name, auth type, destination, refresh endpoint/client ID/resource and
endpoint authentication method remain unchanged. A refresh configuration cannot
be added to a Credential that was created without one.

| Update field | Omitted | Explicit null |
| --- | --- | --- |
| `access_token` | Keep | Keep (local interpretation) |
| `expires_at` | Keep, or clear when a new access token is supplied | Clear |
| `refresh` | Keep | Keep (local interpretation) |
| `refresh.refresh_token` | Keep | Keep |
| `refresh.scope` | Keep | Stop sending scope |
| `refresh.token_endpoint_auth` | Keep | Keep (local interpretation) |
| `refresh.token_endpoint_auth.client_secret` | Keep | Keep |

Supplying `token_endpoint_auth` during replacement requires the existing
`client_secret_basic` or `client_secret_post` method. Read the Credential again to
observe updated safe metadata. Whole-object null rules marked above are not proven
hosted behavior. Exact refresh timing, retry/error equivalence and unspecified
field-edge behavior are not complete protocol qualification.

## Provider network policy

Refresh endpoints must be HTTPS. By default Core rejects nonpublic destinations,
including loopback, private, link-local and shared-address ranges. Resolution is
checked before dialing the resolved IP, retaining TLS hostname verification and
preventing a second DNS lookup from changing the destination. Ambient HTTP proxies
are not used for grant exchange.

For an operator-controlled private issuer, configure exact HTTPS origins:

```sh
export AGENTS_API_OAUTH_TRUSTED_ORIGINS='https://issuer.internal:8443'
```

The comma-separated list is server configuration, not a tenant parameter. It
allows private addresses only for those exact host/port origins; it does not
allow HTTP, redirects or invalid certificates. Install the issuer CA in the
service trust store (for example with Go's `SSL_CERT_FILE`). An invalid explicit
origin fails service startup. Trust only destinations authorized to receive tenant
grants; this setting is not an unrestricted network bypass.

## Revocation and limits

Deleting the stored Credential or Vault blocks later Core lookups while preserving
Session history and frozen selection. It does not revoke the provider grant,
withdraw an access token already delivered to a native process, or cancel running
Sessions. The application performs provider revocation and cancellation when
required. Revoked or invalid refresh grants fail closed until replaced; they do
not cause automatic resource deletion or credential reselection.

This dispatch-time refresh does not promise mid-turn hot replacement, immediate
provider revocation detection, native 401 recovery or transparent replay. Unknown
expiry does not trigger proactive refresh. Storage-key rotation, archive lifecycle
and arbitrary provider/harness combinations remain separate work.

Keycloak is used only by private real-acceptance infrastructure. No production
code depends on its realm, endpoints, token format or administrative APIs.

## Acceptance scope

The 2026-09-22 credential lifecycle batch uses a standalone Core database, genuine
Keycloak 26.7.4 authorization-code grants with S256 PKCE, a TLS MCP server checking
issuer/audience/expiry through provider introspection, and real Kimi model calls.
Keycloak's public, Basic and POST client authentication flows exercise consent,
refresh-token rotation, rejected reuse, revocation and code/PKCE rejection. All
three grant variants also exercise fixed-SDK/raw-HTTP resource operations.

Codex with Basic client authentication exercises initial MCP access, dispatch
refresh, another refresh after Core restart, manual replacement, refusal after
refresh-grant revocation, reauthorization and refusal after Credential deletion.
Claude with POST client authentication exercises initial access and refresh
through the same Core path. Revocation/deletion failures are checked before new
native or MCP work. These are the existing trusted `environment:none` public
MCP profiles; no new hosted/user-managed/MiniMax profile is qualified.

Refresh acceptance explicitly moves the stored declared expiry into the past;
it does not claim waiting for the provider JWT to expire. Reads, public histories
and owned logs are checked for known grant/model secrets. Controlled PostgreSQL
concurrency and failed-commit tests supplement these live checks. The browser
console reads mixed static/OAuth metadata and permits deletion; application-owned
OAuth authorization/replacement is performed through the public API, while its
existing static-token replacement UI remains static-only.

This evidence does not qualify Google, GitHub or arbitrary OAuth services,
provider-independent error equivalence, or the complete Agents API protocol.
