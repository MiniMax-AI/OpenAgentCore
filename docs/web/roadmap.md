# Frontend handoff and acceptance

The backend management service and `AdminClient` are implemented. The frontend team
owns migration of the React application and development proxy. Existing execution
pages and their fixture tests do not establish administrator UI acceptance.

Use the [architecture](architecture.md), [connection guide](core-connection.md) and
[administrator API contract](../../contracts/agents-api/admin-api.md) as the handoff
contract. Public Agents API compatibility work is tracked in the
[public contract documentation](../../contracts/agents-api/README.md).

## Remaining frontend work

- Connect console login and same-origin `AdminClient` requests without exposing the
  deployment credential or requiring an application key.
- Implement Project selection and Project/key lifecycle actions, with one-time key
  display and explicit handling of uncertain writes.
- Provide resource inspection, permitted deletion and independent copies. Keep
  execution, resource editors, Session input and cancellation out of management.
- Present durable Session history, Runtime observations, summaries and audit using
  their distinct meanings. Preserve missing data as unknown.
- Replace the development proxy's public execution path with the console management
  boundary. Discard stale results when Project selection changes.

## Acceptance before calling the UI complete

Verify login, Project isolation, shared access across a Project's keys, revocation,
archive retention, deletion conflicts, copy results and audit attribution through
the production console service. Browser acceptance must also cover denied cross-origin
writes, absent `/v1` proxying, secret handling and uncertain write outcomes.

A backend test pass is evidence for the service it exercises. UI completion requires
separate browser evidence for the migrated screens; successful rendering alone is
insufficient. This handoff does not change native Runtime or application API ownership.
