# Management interface coverage

This matrix describes the implemented console service and management API contract.
It is not React UI acceptance or proof of complete OpenAI-hosted compatibility.
The frontend team owns migration to these interfaces. Existing pages and fixtures
must not be used as evidence that the management workflows have shipped.

## Interface boundaries

| Caller | Interface | Authentication | Purpose |
| --- | --- | --- | --- |
| Administrator browser | `/console/auth` and its setup/login/logout routes | Console account and session cookie | Console sign-in |
| Administrator browser via console | `/core/v1/admin` | Console session; server supplies deployment credential | Project and key management, resource inspection/deletion/copy, monitoring and audit |
| Administrator browser via console | Allowed `/core/v1/sandbox` routes | Console session; server supplies deployment credential | Deployment, maintenance, node and enrollment-token management |
| Application or official SDK directly to Core | `/v1` | Project API key | Application resource creation, editing and execution |
| Node and daemon transports | Fixed transport routes | Their own transport credentials | Existing enrollment and Runtime communication |

The console denies `/v1` with 404 regardless of a supplied Bearer token. Core
rejects Project keys on administrator routes and deployment credentials on `/v1`.
An administrator-selected Project is a target scope, not a caller identity.

## Administrator resources

Paths below are relative to `/core/v1/admin`. Exact routes, payloads, list bounds
and deletion preconditions are defined in the
[administrator contract](../../contracts/agents-api/admin-api.md).

| Capability | Management surface | Limits |
| --- | --- | --- |
| Projects and keys | `/projects`, Project rename/archive, Project `/keys` | Database-owned; key plaintext only at issuance; rotate by issue and revoke |
| Asset inspection and deletion | `/projects/{project_id}/agents`, `/skills`, `/environment-templates`, `/files`, `/vaults` and documented item routes | No arbitrary creation or editing; existing deletion rules apply |
| Session inspection and deletion | Project `/sessions` and documented Turn, Item, Artifact, configuration and Runtime reads | No Session creation, events/SSE, input or cancellation; no Session/Artifact copies |
| Content reads | Documented Skill/version and Artifact content routes | No Source File content route; credential values and confidential configuration remain write-only |
| Independent copies | `/copies` | Supported assets only, different Projects, active destination; dependencies, receipt and audit commit atomically |
| Summary and observations | `/summary`, `/runtime-observations`, `/runtime-history/capabilities`, `/startup-configuration` | Missing usage stays unknown; summaries are not billing; observation never provisions compute |
| Provenance and audit | Project `/resource-owners`, `/write-operations`; global `/audit-log` | Key provenance and administrator audit are distinct; no tokens or request bodies |

`AdminClient` shares public resource parsers where wire shapes match. It exposes
finite management operations, uses same-origin cookies in the browser and never
retries writes automatically. Server/test callers may supply an explicit deployment
credential; the browser must not configure one.

## Evidence and changes

The route allowlists live in [admin_routes.go](../../services/core-console/admin_routes.go)
and [sandbox_admin.go](../../services/core-console/sandbox_admin.go). Authentication,
origin checks and header handling live in [server.go](../../services/core-console/server.go).
Update this guide when those boundaries change; keep detailed wire semantics in the
administrator contract.

Backend HTTP tests, real console login/proxy checks and Project isolation/copy
acceptance establish backend behavior. React integration needs separate browser
acceptance after migration. Fixture screenshots, health responses and deserialization
tests do not prove execution readiness or copied asset usability.

The [public protocol inventory](../../contracts/agents-api/README.md) owns pinned SDK
versions and public API compatibility evidence. Runtime and native adapter behavior
remain governed by the [design principles](../design-principles.md).
