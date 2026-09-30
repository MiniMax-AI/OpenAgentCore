# Public Environment-origin MCP qualification

This record covers explicit public HTTP MCP declarations through the pinned
official client, Core, Runtime and native Harness adapters. The
[Environment contract](environments.md#public-mcp-connection-origin) owns origin,
credential and policy semantics. Earlier Plugin-only evidence does not qualify
this public entrypoint.

## Linux execution evidence (2026-09-30)

Tests use real Kimi K3 through Codex 0.153.4 and Claude SDK 0.3.269, and real
MiniMax-M2.7 through MiniMax Code 0.4.12. Each successful invocation obtains a
fresh random proof from the actual MCP HTTPS server; the model must return both
anonymous and authenticated proofs. Tests use public Session, input, Turn and
Items APIs. Authentication uses an explicitly selected credential from an
attached Project Vault. The anonymous server rejects nonempty Authorization.

| Harness | User-managed Linux | Core-managed Docker |
| --- | --- | --- |
| Codex | Anonymous/bearer, cold continuation, cancellation, tool failure | Same workflow passed |
| Claude SDK | Anonymous/bearer, cold continuation, cancellation, tool failure | Same workflow passed |
| MiniMax Code | Anonymous/bearer, cold continuation, cancellation, tool failure | Same workflow passed |

Codex and Claude declare a named tool allowlist and required initialization.
The fixture also offers an excluded tool; native inventory/call authorization
must preserve the allowlist. Entry-point tests additionally cover an empty
allowlist, unavailable required servers and holding input until initialization.
MiniMax uses null allowlists and required=false; raw HTTP and adapter tests
reject both an empty allowlist and required=true.

After stopping and restarting the daemon (or managed container), the same public
Session continues. Cancellation interrupts an in-flight native MCP call and
settles the Turn as cancelled. A controlled MCP isError response produces a
failed MCP Item while the model can finish its Turn. Codex exposes that failure
with output and a nullable error field; requiring error to be non-null was a
test assertion error, corrected by checking the durable public failed status.

Raw HTTP and official SDK Session creation preserve environment origin. Negative
cases reject service-origin relocation, an unattached selected credential and
MiniMax's unsupported policies. Store tests also verify anonymous and unique
implicit credential selection without writes on rejection. Native state scans
check for the selected bearer after normal calls, cold continuation and
cancellation; it must not appear in native files.

## Reproducible evidence

Source revisions used during acceptance:

- Core: 3448797d33026734d7f1f39d8b201d1af6cf7317, including the common
  preparation-failure fix from main.
- Runtime executable: 04fee4e3b2a4c44f124438b03dddb1be836f38ad for Codex/Claude;
  04883f329ebebf17e35944e39b6ca854eedb061d for final MiniMax observation qualification.
- Claude bridge: cb5a42253cac16921e17e115669cdc87798a3294.
- Private Runtime protocol: 0.10.0 on both peers.

These are development artifacts assembled from the recorded revisions, not a
published distribution. The acceptance image includes a private test CA and a
test model-network proxy. The final MiniMax fixture makes the test CA readable
by the native process; earlier root-only certificate copies did not qualify.
It is not a release image. Only owned acceptance
infrastructure was created or restarted; existing deployments and histories
were retained.

| Evidence | Public Session ID |
| --- | --- |
| Codex self-hosted | b7300fae-6e33-4ce7-8345-d69824986c5c |
| Claude self-hosted | ce1e7e3e-37d4-439a-b017-688523fec37f |
| MiniMax self-hosted | 89a63c73-dec5-49ef-974c-1548bbb82cd1 |
| Codex managed | 23389dd4-2afb-495b-9b14-c403362f5100 |
| Claude managed | 14e5a213-cd9a-4989-89d2-1b814a653395 |
| MiniMax managed | 633a8bc6-1c8c-4674-bb8d-e47425d2abac |

Private scripts, identities, durable Turn records, native-state checks and build
logs are retained under ~/.oac/acceptance/public-mcp-20260930 on the Linux
acceptance host. Credentials are excluded from repository evidence.

## Limits and checks

Real macOS/Windows model execution and E2B/microsandbox qualification are not
claimed by this batch. Service-origin workspace forwarding, public stdio,
literal headers/metadata, public functions and Subagent/MCP combinations are
outside scope. MiniMax's non-null allowlists and required initialization remain
explicitly unsupported. There is no service-network proxy or model-loop fallback.

Focused Core/Runtime tests, 170 Claude adapter tests and the pinned official
client workflow pass. Full make check, native platform CI and independent review
results are recorded on [PR #251](https://github.com/MiniMax-AI/parsar-core/pull/251).
The first local full run stopped because its new test database did not match the
required oac_*_tests naming rule. A later run passed Go/database/adapter checks
but reached an occupied browser fixture port; remaining checks use separate
ports. These interrupted attempts are not successful full gates.
