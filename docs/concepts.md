# Concepts and ownership

A Project is the execution tenant in OpenAgentCore. Applications use its API keys; operators manage the installation with a separate Core key. The [API index](api/README.md) maps each caller to its namespace and credential.

## Projects own assets

A Project owns its Agents, Sessions, Environments, Skills, Files and Vaults. Its named API keys share the same principal, permissions and resources. Core has no product users, roles, memberships or read-only keys. Names are labels; stable IDs identify Projects and keys.

Projects and keys live in PostgreSQL. Core returns a key's plaintext once, when it is issued. To rotate a key, issue a new one in the same Project, then revoke the old one. Revocation preserves assets, write provenance and accepted work. Archiving a Project revokes all its keys and prevents new keys; administrators retain access to inspect and delete its resources. The [administration contract](../contracts/agents-api/admin-api.md#projects-and-keys) defines these operations.

The [Core key](getting-started/operations.md#core-key) is a separate deployment credential. An administrator who needs to use the application API issues a Project key and uses that Project's authority. Product users, workspaces and business permissions belong to the application.

## Resource isolation

Application reads, writes and references are scoped to the key's Project. A resource in another Project is indistinguishable from an absent one. Core does not share or copy assets across Projects. Nodes, configured model endpoints and startup settings are deployment infrastructure.

## What administrators can and cannot do

Administrators manage Projects and keys, sandbox deployments, nodes, executor credentials and deployment default models. They inspect resources, execution history, operational counts and usage, and delete resources under their deletion rules. Archiving a hosted Session requests cancellation and sandbox reclamation; see [Session archive](../contracts/agents-api/admin-api.md#session-archive).

Creating or editing application assets, starting Sessions and submitting input require a Project API key. The Core key gives no application identity and cannot read stored secrets. The [administration API](../contracts/agents-api/admin-api.md) defines its operations; the [console server](web/console-server.md) owns Web sign-in and credential handling.

## Runtime and outer isolation

The Runtime daemon runs on Linux, macOS and Windows. Native platform behavior belongs to the Runtime and its Harness adapters; managed Sandbox Providers run Linux environments.

Tools run with the permissions of the account that launches the daemon. The daemon adds no filesystem, permission or network isolation. Use the outer Environment for isolation: a managed Docker, E2B or microsandbox environment, or a container or VM around a self-hosted machine's Runtime. Authentication, private storage, locks and process cleanup protect the connection and lifecycle, but tools running as the same user can access Runtime data.

## Secrets and audit

Credential values, model keys and confidential template data are write-only: application and administrator reads omit them. Conversation text, Skill source and Artifact content are readable resource data, including to administrators.

Public writes record the API key responsible. Administrator writes record a separate audit identity and the target Project. Web's actor label is display-only; Core authorizes the Core key. An audit failure rolls back the write. Reads are not audited, and audit records contain no request bodies, secrets or file contents. The [write provenance](../contracts/agents-api/admin-api.md#write-provenance) and [audit log](../contracts/agents-api/admin-api.md#audit-log) contracts define the stored records.
