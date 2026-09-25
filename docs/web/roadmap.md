# Frontend handoff and acceptance

The backend management service, `AdminClient` and the React console that uses them
are implemented (PR #96). The console signs in through the console service, sends
same-origin management requests only and never calls `/v1`.

Use the [architecture](architecture.md), [connection guide](core-connection.md) and
[administrator API contract](../../contracts/agents-api/admin-api.md) as the
contract. Public Agents API compatibility work is tracked in the
[public contract documentation](../../contracts/agents-api/README.md).

## Delivered

- Console login and same-origin `AdminClient` and sandbox management requests; the
  browser holds no deployment credential or application key.
- Projects and keys: create, rename, archive, issue with one-time display, revoke;
  uncertain writes are reported, never replayed.
- Resource inspection, permitted deletion and independent copies across Projects;
  no execution, resource editors, Session input or cancellation.
- Monitoring: Overview, Core metrics, Agent metrics, Sandbox metrics and the Session
  log, keeping missing data unknown and summaries distinct from billing.

## Remaining frontend work

- The Vite development proxy still forwards `/v1` with a local bearer for older
  tooling (`scripts/core-doctor.mjs`, `.env.example`). The console no longer sends
  `/v1`; remove the path together with that tooling.
- Run the browser acceptance below through the production console service.

## Acceptance before calling the UI complete

Verify login, Project isolation, shared access across a Project's keys, revocation,
archive retention, deletion conflicts, copy results and audit attribution through
the production console service. Browser acceptance must also cover denied cross-origin
writes, absent `/v1` proxying, secret handling and uncertain write outcomes.

A backend test pass is evidence for the service it exercises. UI completion requires
separate browser evidence for the migrated screens; successful rendering alone is
insufficient. This handoff does not change native Runtime or application API ownership.
