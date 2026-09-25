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
API-key plaintext is returned once on issuance, never on reads.

## Projects own assets

A Project owns one execution tenant and its assets. Multiple named API keys belong
to the Project and share its principal, permissions and resources. Each write keeps
its actual key identity for provenance. There are no Core users, roles, memberships
or read-only keys. Names are labels; stable IDs identify Projects and keys.

Projects and application API keys live only in PostgreSQL. The console and
management API use the same records. Configuration contains deployment settings,
not application credentials or Project definitions. Issue a new key in the same
Project and revoke the previous one to rotate credentials. Revocation affects only
that key and preserves assets, provenance and accepted execution. Explicit Project
archive revokes every key and blocks new issuance and copies into that Project.
Administrators can still inspect, delete and copy from archived Projects.

Store only API-key digests and necessary metadata. Return plaintext once, on
issuance, and never replay it after an uncertain response. Project/key mutations
and administrator audit commit together. Database authentication failures fail
closed. Deployment administrator credentials remain separately managed.

Parsar product identities, workspaces, business permissions and collaboration
remain outside Core. Parsar is an ordinary API-key holder in a Project.

## Resource isolation and administrator authority

Agent API reads, writes and references are scoped to the authenticated key's Project.
A foreign resource remains indistinguishable from an absent resource under the
existing public operation's rules. Deployment nodes, configured model endpoints
and startup settings are deployment infrastructure, not shared business assets.

Administrators can inspect resources and execution history, delete resources under
the same rules as their public deletion operations, copy supported assets between
Projects, manage Projects and API keys, and query operational counts and usage. They cannot use
management endpoints to create or edit arbitrary assets, start a Session, send an
event, cancel work, or read stored credentials. Copying is an explicit exception
to owner-only asset creation, with its own audit and provenance.

## Shared within a Project, copied across Projects

Project keys already share all assets. A cross-Project copy receives new resource IDs and has no continuing link to its source. Core
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
a separate audit identity and target Project. The console account name is a label;
Core trusts the deployment credential, not that forwarded name. Audit failure rolls
back the business transaction. Reads are not audited. Copied-resource ownership is
explicitly distinguished from historical unknown ownership. No request bodies,
secrets or file contents enter audit records.

Do not add product users, RBAC, cross-Project shared assets, administrator execution, or old
private-protocol compatibility to this management model. Existing public
execution and Runtime ownership rules remain in [CONTRIBUTING.md](../CONTRIBUTING.md).
