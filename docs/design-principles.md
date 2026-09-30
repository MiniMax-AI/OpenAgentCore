# Core design principles

OpenAgentCore implements the OpenAI Agents API under the
[public API rules](../AGENTS.md#public-api).

## Three namespaces, three credentials

Applications, administrators and machines each use their own namespace and
credential. The [API index](api/README.md) owns the complete matrix.

Separating API authority does not stop an administrator from holding application
credentials: they can issue a Project API key and use it like any application.

## Projects own assets

A Project owns one execution tenant and its assets.

- **Keys.** A Project has one or more named API keys. They share its principal,
  permissions and resources. Each write records the key that made it.
- **No users or roles.** Core has no users, roles, memberships or read-only keys.
  Names are labels; stable IDs identify Projects and keys.
- **Storage.** Projects and keys live only in PostgreSQL, never in deployment
  configuration. A key's plaintext is returned once, at issuance.
- **Rotation.** Issue a new key in the same Project, then revoke the old one.
  Revoking a key keeps assets, provenance and accepted work.
- **Archive.** Archiving a Project revokes every key and blocks new ones.
  Administrators can still inspect and delete its resources.

The Core key is a deployment credential, managed separately; see
[Core key](getting-started/operations.md#core-key). Product concepts such as users,
workspaces and business permissions stay outside Core:
a product like Parsar is an ordinary API-key holder in a Project.

## Resource isolation

Agents API reads, writes and references are scoped to the key's Project. A resource
in another Project is indistinguishable from an absent one. Nothing is shared or
copied across Projects. Nodes, configured model endpoints and startup settings are
deployment infrastructure, not business assets.

## What administrators can and cannot do

| Administrators can | Administrators cannot |
| --- | --- |
| Inspect resources and execution history | Create, copy or edit arbitrary assets |
| Delete resources, under the public deletion rules | Start a Session, send input or cancel work |
| Manage Projects and API keys | Read stored credentials |
| Issue node enrollment tokens and executor credentials | Impersonate an application |
| Query operational counts and usage | |

Web signs in with the Core key and keeps it on its server; signing in never creates
a Session or gives the browser a Project API key.

## Runtime and outer isolation

The same daemon and protocol serve self-hosted Linux, macOS and Windows. OS
differences belong to Runtime implementations; harness differences belong to
adapters. Managed Providers are Linux-only.

**The daemon is not a sandbox.** It runs tools with its launching user's
permissions and adds no filesystem, permission or network isolation, on any OS.
Isolation comes from the outer Environment: Docker, E2B or microsandbox for managed
Sessions, or whatever container or VM you choose for a self-hosted machine.
Authentication, private storage, locks and process cleanup still apply, but they do
not protect Runtime data from tools running as the same user.

## Secrets and audit

- **Write-only secrets.** Credential values, model keys and confidential template
  data are never returned by any read, including administrator reads. Conversation
  text, Skill source and Artifact content are not secrets: administrators see them.
- **Provenance.** Public writes record their API key. Administrator writes record a
  separate audit identity and the target Project. Web's actor label is display-only;
  Core trusts the Core key, not the label.
- **Audit is transactional.** An audit failure rolls back the write. Reads are not
  audited. Audit records never contain request bodies, secrets or file contents.
- **History.** Resources from the removed copy operation keep their `admin_copy`
  ownership, distinct from historical unknown ownership.

## Out of scope

Do not add product users, RBAC, cross-Project shared assets, administrator
execution or compatibility with old private protocols. Implementers follow the
[design rules](../AGENTS.md#design-principles) and the
[Core–Runtime protocol](runtime-protocol.md).

Native failure classification is adapter-owned and uses finite, structured native
values. Optional Runtime error metadata is normalized once; it never replaces Core's
terminal authority, cancellation receipts, Usage or native identity. See
[native failure classification](../contracts/agents-api/native-error-classification.md).
