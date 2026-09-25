# Core design principles

Parsar Core is an open-source implementation of the OpenAI Agents API. Its public
contract follows the repository's pinned upstream baseline; documented native
harness differences remain explicit. Core extensions must not silently change
upstream resource shapes or execution semantics.

## Three namespaces, three credentials

Core serves three namespaces, each with one kind of caller and credential:

- `/v1`: applications with a Project API key. Exactly the pinned official routes;
  Core-only fields live only in `x_agents_core`.
- `/core/v1`: Core Web's server and operator scripts with the Core key. Everything
  about operating the deployment, including credential issuance.
- `/api/v1`: nodes, Runtime daemons and self-hosted executors with machine
  credentials issued through `/core/v1`; each works only on its own routes.

Core enforces the separation: the Core key cannot call `/v1`, API keys cannot call
`/core/v1`, and neither works on `/api/v1`. Web calls only `/core/v1` and keeps
the Core key on its server; users sign in to Web with it.

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
archive revokes every key and blocks new issuance in that Project.
Administrators can still inspect and delete resources in archived Projects.

Store only API-key digests and necessary metadata. Return plaintext once, on
issuance, and never replay it after an uncertain response. Project/key mutations
and administrator audit commit together. Database authentication failures fail
closed. The Core key is managed separately, in deployment configuration.

Parsar product identities, workspaces, business permissions and collaboration
remain outside Core. Parsar is an ordinary API-key holder in a Project.

## Resource isolation and administrator authority

Agent API reads, writes and references are scoped to the authenticated key's Project.
A foreign resource remains indistinguishable from an absent resource under the
existing public operation's rules. Deployment nodes, configured model endpoints
and startup settings are deployment infrastructure, not shared business assets.

Administrators can inspect resources and execution history, delete resources under
the same rules as their public deletion operations, manage Projects and API keys,
issue node enrollment tokens and self-hosted executor credentials, and query
operational counts and usage. They cannot use management endpoints to
create, copy or edit arbitrary assets, start a Session, send an event, cancel
work, or read stored credentials. Project keys share all assets within their
Project; nothing is shared or copied across Projects.

## Secrets and evidence

Credential values, model credentials and confidential template initialization are
write-only through resource APIs, including administrator reads. Public metadata
projections are reused by the management API. This does not redact arbitrary
user-authored conversation text, Skill source or Artifact content: administrators
who inspect those records see their recorded contents.

Public writes retain their actual API-key provenance. Administrator writes retain
a separate audit identity and target Project. The console's fixed actor label is
display-only; Core trusts the Core key, not that forwarded label. Audit failure rolls
back the business transaction. Reads are not audited. Resources from the removed
copy operation keep their `admin_copy` ownership, distinct from historical unknown
ownership. No request bodies, secrets or file contents enter audit records.

Do not add product users, RBAC, cross-Project shared assets, administrator execution, or old
private-protocol compatibility to this management model. Existing public
execution and Runtime ownership rules remain in [CONTRIBUTING.md](../CONTRIBUTING.md).
