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
- Run the browser acceptance below against the production console service and a
  real Core; the automated suite uses a synthetic Core upstream.

## Acceptance before calling the UI complete

Verify login, Project isolation, shared access across a Project's keys, revocation,
archive retention, deletion conflicts, copy results and audit attribution through
the production console service. Browser acceptance must also cover denied cross-origin
writes, absent `/v1` proxying, secret handling and uncertain write outcomes.

`apps/web/e2e` builds and runs the production `services/core-console` with its
real account store, session cookies, host/origin checks and management allowlist.
`fixture-core.mjs` supplies only synthetic Core data and failure responses; it
contains no console authentication or proxy implementation. The suite covers
administrator setup/sign-in, Project creation, one-time key display, revocation
and archive, uncertain key issuance without replay, copies, deletion conflicts,
monitor pages, unknown metrics, read-only Session history, node enrollment/removal
and failed reads. Browser traffic is checked for `/v1` and Authorization headers;
the upstream verifies the server credential, signed-in actor label and absence of
browser cookies. Separate requests exercise rejected cross-origin writes, denied
`/v1` proxying and forged browser credentials/actor labels.

See the [test guide](../../apps/web/e2e/README.md) for commands and isolation. Core
Project isolation, shared key access, archive retention and transaction/audit
semantics remain covered by Core's backend tests and require real deployment
acceptance; synthetic records do not prove those behaviors or model execution.

A backend test pass is evidence for the service it exercises. UI completion requires
separate browser evidence for the migrated screens; successful rendering alone is
insufficient. This handoff does not change native Runtime or application API ownership.
