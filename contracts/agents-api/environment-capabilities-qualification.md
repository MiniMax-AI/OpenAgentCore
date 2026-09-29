# Environment capability qualification, September 30, 2026

This record covers the existing Linux Runtime path. It does not qualify every
Harness, model, operating system or infrastructure provider. Current rules remain
in [Environments](environments.md), [structured output](structured-output.md) and
[deferred function discovery](tool-search.md).

## Verified public workflows

Real requests used the pinned official Python SDK against a dedicated Core and
PostgreSQL, the official native daemon installer, and MiniMax-M2.7. No test model
or replacement model/tool loop was used. Remote evidence is under
`zju_a100_2:~/.oac/acceptance/environment-capabilities-20260930`.

| Workflow | Evidence | Outcome |
| --- | --- | --- |
| Self-hosted Claude structured output | `claude-structured`, Session `2a70e3ca-b67c-4222-9c3a-2fa9b7db2b9d` | Native file read followed by schema-constrained proof; another completed Turn after a fresh daemon process resumed the same Session. |
| Self-hosted Claude deferred function discovery | `claude-discovery-qualified`, Session `7801d632-697f-419d-8531-848a60d9f1a8` | Native history records `ToolSearch` and the declared deferred callback. The callback used the random schema argument and the answer contained a fresh result supplied only by the client. Cold continuation completed another callback; cancellation during a third pending callback cancelled the Turn and settled the function call. |
| MiniMax installed HTTP MCP | `minimax-http-common` | Anonymous and explicit HTTPS bearer servers both returned fresh proofs, including after daemon restart. Cancelling an actual waiting HTTP tool call cancelled its Turn. Native configuration files contained no bearer token. |

The HTTP fixture implements MCP, not model responses. It verifies that the
anonymous server does not receive the other server's Authorization header and
that the authenticated server receives its selected token. It does not establish
cross-origin redirect qualification for arbitrary custom headers; those remain
rejected for MiniMax and Claude.

The structured-output continuation and the first discovery result were recovered
through durable public reads. The initial test scripts made assumptions about an
initial SSE snapshot or optional assistant phase, so their failed script records
are retained separately. They are not presented as clean end-to-end SSE evidence.
The recovered results and native history verify the actual completed work; inputs
were not replayed to obtain those results.

## Runtime boundaries

All three adapters consume the same transient effective MCP binding resolver.
Unit and adapter tests retain nil versus empty tool allowlists, required flags,
service versus Environment origin, credential authority, reserved/duplicate
identities, fixed installed stdio launchers, secret-free generated configuration,
and existing failure/cancellation settlement. Public service-origin HTTP remains
restricted to its existing service execution host. This change does not add a
Core proxy or claim public `connection_origin=environment` support.

The daemon CLI now waits up to ten seconds for confirmed shutdown, allowing the
native process owner's three-second grace period and subsequent pipe/owner
cleanup. A real MiniMax HTTP Session reproduced the old three-second CLI timeout;
its process exited shortly afterward. The new timeout passed cold restart and
cancellation. Genuine timeout still fails and retains process ownership records.

## Limits and retained failures

- The available Kimi visual-model account returned HTTP 429/quota. The image
  Turn was cancelled, and self-hosted image admission remains unchanged. No
  image qualification is inferred from text or schema tests.
- An earlier ToolSearch bundle did not advertise workspace discovery. Its input
  remained pending without a Turn; public cancellation returned 409. That Runtime
  was stopped and the records retained. The corrected bundle advertises discovery
  only with the explicit workspace/function features. This does not claim a fix
  for the general pending-input cancellation gap.
- These model runs use Linux user-managed environments. The common managed
  preparation flow has separate evidence in
  [preparation qualification](environment-preparation-qualification.md); this
  record does not add a new Docker/E2B/microsandbox model run.
- No Claude installation was performed on the Mac. Native platform CI, including
  Windows, is required for the final PR. Windows manual acceptance remains absent.
- Public MiniMax MCP, Plugin tool allowlists/required flags, custom HTTP headers,
  workspace discovery combined with Skills/Plugins/MCP, and self-hosted image
  execution are not qualified by this work.
