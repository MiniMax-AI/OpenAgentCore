# Workspace placement

The daemon, selected harness, native tools and workspace are colocated in one Runtime.
Our daemon is the user-side executor for `self_hosted`. Enrollment freezes the exact
Session/Environment/device/key binding; it creates no managed allocation and cannot
move a Session to another device. Core-managed hosting uses the deployment-selected E2B, Docker or microsandbox
provider. Users manage
local or E2B Runtime creation, renewal and destruction through the official SDK.

All three harnesses reuse typed `LocalEnvironment`, existing preparation/start/
cancel ownership and authorized local Files/Artifacts. Native tools use the
starting account's permissions and may access that account's Runtime credentials
and history. The daemon adds no inner sandbox; managed isolation belongs to the
outer Environment. Strict resume requires retained history; connectivity alone
establishes neither readiness nor isolation. Current
implementation is recorded in the [Environment profile](environments.md#initial-public-self-hosted-profile);
[real qualification](user-managed-runtime-v1.md) identifies accepted deployments and limits.

The pinned public Environment resources and `remote_url` remain unchanged, while
that URL names our private daemon transport, not stock `exec-server`.
Service-origin HTTP MCP is rejected on `self_hosted`; qualified `none` MCP and
hosted Template Plugin MCP retain their separate boundaries.

## Contract owners

- [Environments](environments.md#ownership-and-placement-decision) owns placement,
  preparation and authorization.
- [Core–Runtime protocol](../../docs/runtime-protocol.md) owns workspace operation
  messages, bounds and operation lifetimes.
- [Environment Files](environment-files.md) records the public file behavior and
  its qualification evidence.
- [Harness onboarding](harness-onboarding.md) owns native adapter integration.

## Recorded output limitation

The earlier Codex placement assessment recorded `NATIVE-COMMAND-OUTPUT-001`,
a retained-command-output gap. That failed run is historical evidence, not a
current installation prerequisite or a claim that subsequent implementations
fixed output fidelity. Current behavior must be qualified through the shared
Runtime path; do not fabricate missing output.
