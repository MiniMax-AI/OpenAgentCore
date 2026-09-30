# Environment capability qualification, September 30, 2026

This record covers the existing Linux Runtime path. It does not qualify every
Harness, model, operating system or infrastructure provider. Current rules remain
in [Environments](environments.md), [structured output](structured-output.md) and
[deferred function discovery](tool-search.md).

## Verified public workflows

Real requests used the pinned official Python SDK against a dedicated Core and
PostgreSQL, the official native daemon installer, and MiniMax-M2.7 or Kimi K3. No test model
or replacement model/tool loop was used. Remote evidence is under
`zju_a100_2:~/.oac/acceptance/environment-capabilities-20260930`.

| Workflow | Evidence | Outcome |
| --- | --- | --- |
| Self-hosted Claude structured output | `claude-structured`, Session `2a70e3ca-b67c-4222-9c3a-2fa9b7db2b9d` | Native file read followed by schema-constrained proof; another completed Turn after a fresh daemon process resumed the same Session. |
| Self-hosted Claude deferred function discovery | `claude-discovery-qualified`, Session `7801d632-697f-419d-8531-848a60d9f1a8` | Native history records `ToolSearch` and the declared deferred callback. The callback used the random schema argument and the answer contained a fresh result supplied only by the client. Cold continuation completed another callback; cancellation during a third pending callback cancelled the Turn and settled the function call. |
| MiniMax installed HTTP MCP | `minimax-http-common` | Anonymous and explicit HTTPS bearer servers both returned fresh proofs, including after daemon restart. Cancelling an actual waiting HTTP tool call cancelled its Turn. The original scan covered only `config.yaml` and `mcp.json`; later review found an extra tool-environment copy in `workspace-profile.json`, so that scan did not establish secret-free native state. |

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

## MCP credential persistence follow-up

Independent review found that MiniMax's workspace profile copied the complete
tool environment, including selected bearer values. The adapter now persists only
the validated Runtime-owned snapshot path; the tool launcher reads it when starting
tools. Missing or malformed snapshots fail explicitly without parser contents in
errors. No historical profile or credential migration is provided.

A newly built daemon and MiniMax companion passed `minimax-http-private-env`:
anonymous and selected HTTPS bearer calls, cold continuation and cancellation of
an active HTTP call. After each Turn, the test scanned every native MiniMax state
file for the fresh token. A final check confirmed a real workspace profile with
no inline `toolEnv`, a snapshot reference outside native state, and no token in
any native state file. The earlier narrower scan above is not reused as evidence
for this guarantee. The Runtime's authorized private snapshot intentionally
retains the environment values required by tools.

## Funded visual-model validation

After restoring Kimi K3 credit, real pinned official-client requests used the
same Core/Runtime and native adapters on Linux user-managed machines:

| Workflow | Evidence | Verified behavior |
| --- | --- | --- |
| Claude image messages | `claude-image-funded`, Session `be8c8251-5c07-4c39-b71c-3ac74139bbb7` | Randomized PNG colors, a second PNG after daemon restart, and JPEG on the rebuilt final Core. |
| Codex image messages | `codex-image-funded`, Session `eed03095-b3e9-4c97-8e23-3343287c797d` | Randomized PNG colors, a second PNG after daemon restart, and JPEG on the rebuilt final Core. |
| Claude function image results | `claude-function-image-funded`, Session `fc0f373d-d5a2-41bb-9061-7c0da3997387` | PNG supplied only in the callback; remote/failed image rejection leaves the call pending; identical result retry succeeds, conflicting retry rejects, and Items retain submitted content. Cold continuation recalls the prior image, pending-call cancellation settles, and final-Core JPEG succeeds. |

The image follow-up changes operation admission, not native encoding, credential
selection, preparation or execution ownership. It removes the Environment-source
image gate for Codex and Claude; Runtime image support is still checked before
delivery. MiniMax image input remains rejected. This record adds no new managed
provider run, macOS/Windows live model run, or Codex function-result image
qualification. No production deployment was updated; only the owned acceptance
Core was restarted, preserving its database and Sessions.

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

- The first Kimi attempt returned HTTP 429/quota and was cancelled. After the
  account was funded, separate image workflows below used real visual requests;
  the failed quota attempt remains separate evidence.
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
  workspace discovery combined with Skills/Plugins/MCP, and MiniMax image
  execution are not qualified by this work.
