# Parsar Core Web

Core Web is the administrator console for a Core deployment. Its Go service
provides Core key login and forwards signed-in, same-origin `/core/v1` requests to
Core. Applications use Core's public Agents API directly with their own Project API
keys.

The React console (`apps/web`) uses this contract: every browser request goes through
the console's same-origin management routes with `AdminClient` and the sandbox
management client, and it sends nothing to `/v1`.

![Core Web overview](images/overview.png)

## Console pages

| Group | Page | Purpose |
| --- | --- | --- |
| Monitor | Overview | Service status, running Sessions, sandbox slots and work needing attention; 24-hour Session activity; Core and its nodes as a topology, each with a popover glance; Sessions needing attention; usage by Project |
| Monitor | Core metrics | The Core process: execution slots, the Turn queue, connected daemons, database latency and pool, background jobs |
| Monitor | Agent metrics | Requests, errors, duration, tokens, models, tools, Agents and API keys over 1 h, 6 h, 24 h or 7 d |
| Monitor | Sandbox metrics | Node capacity and hosted Runtime CPU and memory across Projects |
| Monitor | Session log | Every Session, opening one Session's read-only conversation, trace and Turns; a self-hosted Session's page also manages its executor credentials |
| Resources | Agents, Environment templates, Skills, Files, Vaults | Inspection and permitted deletion |
| Platform | Projects and keys, Nodes, System | Project and key lifecycle; sandbox deployment and nodes; the sandbox configuration every Project shares: provider, sandbox size, Runtime or E2B template build, idle suspension, Core address and maintenance |

Missing data is shown as missing (—), never as zero. How each figure is read and
bounded is recorded in [management interface coverage](protocol-coverage.md).

## Management scope

Administrators can create, rename and archive Projects; issue and revoke their
keys; inspect resources and execution history; delete supported resources; and
issue, rotate and revoke the executor credentials of a self-hosted Session's
environment on its Session page.
They can also read summaries, Runtime observations and audit history, and manage
deployment sandbox nodes. Deployment sandbox management selects E2B, Docker or
microsandbox; caller-managed `self_hosted` Runtimes remain a separate application
path.

A Project owns one tenant and one principal. All its keys share assets and
permissions; writes record the individual key as provenance. Projects and keys
live only in PostgreSQL. Key issuance returns plaintext once and stores its digest.
Rotate by issuing another key and revoking the old one. Archive disables every key
in the Project while retaining assets and admitted work.

The management API cannot create or edit arbitrary application resources, start
Sessions, submit input or cancel execution. Applications perform those operations
through `/v1`. Core has no API users, roles or memberships.

## Connect and develop

Follow the [installation guide](../getting-started/install.md) for Core, Web and
PostgreSQL with zero execution nodes. Installation creates no Project or application
key; an administrator creates them on the console's **Projects and keys** page or
through the management API. The browser signs in to the console with the Core key;
only the console server sends it to Core.

- [Connection and authentication](core-connection.md)
- [Architecture and ownership](architecture.md)
- [Management interface coverage](protocol-coverage.md)
- [Frontend handoff and acceptance](roadmap.md)
- [React application](../../apps/web/README.md)

The [administrator API contract](../../contracts/agents-api/admin-api.md) defines
management routes and resource behavior. The [public API contracts](../../contracts/agents-api/README.md)
define the separate application interface. See the [design principles](../design-principles.md)
for ownership and [contributor guide](../../CONTRIBUTING.md) for required checks.

Parsar Core Web is available under the [MIT License](../../LICENSE).
