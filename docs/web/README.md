# Parsar Core Web

Parsar Core Web is the administrator console shipped with Core. Applications use
Core's public Agents API with their own API keys; the console uses a separate
management API and deployment credential.

**Integration status:** the backend management contract and `AdminClient` are
implemented in this batch. The existing React screens still need to switch to
that client before this batch can ship as a complete console. Older screenshots
and fixture tests describe the previous UI, not completed management acceptance.

## Administrator workflows

The management contract supports:

- Create, rename or archive a Project. Issue or revoke named keys within it; all
  keys share the Project assets and execution principal. Issuance displays plaintext
  once and stores its digest in the database. Rotate by issuing a replacement and
  revoking the old key. Archiving disables every key and retains assets.
- Select a Project to inspect Agents, Skills, Environment Templates, Source File
  metadata, Vault/Credential metadata and Session history. Existing resource
  serializers and deletion constraints are shared with the public API.
- Delete supported resources with confirmation. Copy supported assets into another
  active Project as independent resources, optionally including dependencies. Copies
  never share subsequent changes or expose stored credential values.
- Read global, Project, Agent and key summaries, usage coverage, Runtime observations and
  administrator or API-key write history. Missing usage remains unknown; these
  totals are not billing records. Key grouping attributes whole Sessions to their
  creation keys, with unknown creators grouped separately.
- Manage deployment sandbox nodes through the existing Hosted Sandbox Manager.

The console has no execution, resource creation/editing, Session-event or model
calling authority. Administrators who need to use Agents API can separately use
an issued API key in their own application. Business collaboration remains in
Parsar.

## Install and connect

Follow the [installation guide](../getting-started/install.md). The default is
Core, Web and PostgreSQL with zero execution nodes. The paired installation gives
only the Web server its deployment credential. It creates no Project or application
key. The administrator creates both through the management API. Configuration files
hold deployment settings; business identities live only in the database.

For a separate Web installation, use `--web-only --core-url ...
--admin-token-file /absolute/private/file`. The Core origin must be loopback or
HTTPS. The browser signs in using the console account; it never receives the
server's deployment credential. Full configuration is in
[Connecting Core Web](core-connection.md).

Public `/v1` traffic must go directly to Core through deployment routing. The
console returns 404 for it, even when the request supplies its own Bearer token.
Fixed node and daemon transport routes retain their independent authentication.

## Client and validation

React management screens must use `AdminClient` from `packages/agents-client`.
It shares public resource projections but exposes only finite administrator
operations. Keep safe resource metadata in the browser; never persist key
plaintext, deployment credentials, model tokens or copied secret fields.

Development requires Node 22, pnpm 10.30.3 and the repository's required toolchains.
`make check` covers client, service and fixture Web tests. Real management
acceptance must separately prove Project isolation, shared access by keys within a
Project, auditing and copied asset use.
Fixture screenshots and HTTP deserialization alone do not prove those properties.

## References

- [Design principles](../design-principles.md)
- [Administrator API](../../contracts/agents-api/admin-api.md)
- [Architecture and ownership](architecture.md)
- [API-key write provenance](../../contracts/agents-api/write-audit.md)
- [Public API coverage](../../contracts/agents-api/README.md)
- [Contributor rules](../../CONTRIBUTING.md)

Parsar Core Web is available under the [MIT License](../../LICENSE).
