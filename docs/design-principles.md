# Core design principles

Parsar Core is an open-source implementation of the OpenAI Agents API. Its public
contract follows the repository's pinned upstream baseline; documented native
harness differences remain explicit. Core extensions must not silently change
upstream resource shapes or execution semantics.

## Separate data and management APIs

Applications use `/v1` with an API key. The administrator console uses
`/core/v1/admin` and the existing sandbox management API with a deployment
credential kept on the console server. Core enforces this separation: deployment
credentials cannot call the Agent API, and API keys cannot call administrator
operations. Node and daemon transport retain their own credentials.

This is a separation of API authority, not a claim that a deployment administrator
cannot possess application credentials. An administrator can issue an API key and
use it separately. The console itself has no impersonation or execution operation.
API-key plaintext is returned once on creation or secret reset, never on reads.

## One key, one space

A stable API-key resource owns one independent execution tenant. There is no
separate Core user, team, role or membership model. Creating a key creates a new
space. Resetting its secret preserves the key ID, tenant and assets and immediately
invalidates the old secret. Revoking it prevents new authentication while retaining
assets and accepted execution. Administrator queries continue to include revoked
spaces. A display name is a label, not an identity or permission.

Static configuration keys remain available for operators and service accounts.
Each binding must have its own tenant and cannot overlap an issued key's space or
credential. The management API lists their safe metadata, but rotation/removal is
owned by configuration; management reset and revoke reject explicitly.

Parsar product identities, workspaces, business permissions and collaboration
remain outside Core. Parsar is an ordinary API-key holder.

## Resource isolation and administrator authority

Agent API reads, writes and references are scoped to the authenticated key's space.
A foreign resource remains indistinguishable from an absent resource under the
existing public operation's rules. Deployment nodes, configured model endpoints
and startup settings are deployment infrastructure, not shared business assets.

Administrators can inspect resources and execution history, delete resources under
the same rules as their public deletion operations, copy supported assets between
spaces, manage API keys, and query operational counts and usage. They cannot use
management endpoints to create or edit arbitrary assets, start a Session, send an
event, cancel work, or read stored credentials. Copying is an explicit exception
to owner-only asset creation, with its own audit and provenance.

## Independent copies, no sharing

A copy receives new resource IDs and has no continuing link to its source. Core
rewrites selected dependency IDs and decrypts/re-encrypts protected values for the
new tenant and resource bindings internally. Content, dependencies, large objects,
idempotency results and administrator audit commit in one database transaction.
Refreshable OAuth credentials are skipped because two copies can invalidate each
other's rotating refresh token. Sessions and Artifacts cannot be copied.

Copied MCP credentials do not gain automatic visibility. A Session still searches
only its explicitly attached Vaults; copying an Agent never introduces a global
credential lookup or an implicit execution privilege.

## Secrets and evidence

Credential values, model credentials and confidential template initialization are
write-only through resource APIs, including administrator reads. Public metadata
projections are reused by the management API. This does not redact arbitrary
user-authored conversation text, Skill source or Artifact content: administrators
who inspect those records see their recorded contents.

Public writes retain their actual API-key provenance. Administrator writes retain
a separate audit identity and target space. The console account name is a label;
Core trusts the deployment credential, not that forwarded name. Audit failure rolls
back the business transaction. Reads are not audited. Copied-resource ownership is
explicitly distinguished from historical unknown ownership. No request bodies,
secrets or file contents enter audit records.

Do not add product users, RBAC, shared assets, administrator execution, or old
private-protocol compatibility to this management model. Existing public
execution and Runtime ownership rules remain in [CONTRIBUTING.md](../CONTRIBUTING.md).
