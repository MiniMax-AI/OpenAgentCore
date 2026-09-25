# User-managed V1 Runtime qualification

The V1 executor is Parsar's daemon, colocated with the selected native harness,
local tools and workspace. Core runs separately with its own PostgreSQL database.
A caller creates a public `self_hosted` Session and starts Runtime with its exact
Environment ID, returned `remote_url` and scoped executor credential. This private
transport does not interoperate with stock Codex `exec-server` or Noise.

For `openai_hosted`, Core uses the deployment-selected
[E2B, Docker or microsandbox provider](../../services/agents-api/HOSTED-SANDBOX-MANAGER.md).
This document qualifies the separate caller-managed path: Docker and E2B use the
same Runtime contract, but the application owns their compute. In that path, E2B
create, information, renewal and deletion use the official E2B SDK outside Core. Public execution and
file operations continue through Core and daemon, not E2B commands or files.

## Accepted scope

Recorded on 2026-09-22 against source candidate `8f0cd2530d7b58cb7fb3ea124a1fc43dacecca36`.
Documentation-only follow-up commits do not change the accepted binaries.

| Deployment | Codex | Claude Code | MiniMax Code |
| --- | --- | --- | --- |
| User-managed Linux amd64 Docker | Accepted | Accepted | Accepted |
| User-managed E2B, immutable Runtime template | Accepted | Accepted | Accepted |
| Real model | Kimi K3, Responses | Kimi K3, Anthropic-compatible API | MiniMax M2.7, Anthropic-compatible API |

Each complete deployment run uses official OpenAI Python SDK 3.13.0 at the pinned
upstream commit plus raw HTTP and live SSE. The common four-Turn acceptance checks:

- Public inline Files.create bytes, native command execution and two actual output
  files; Files.list path/order/pagination and immutable Artifact downloads through
  SDK/raw HTTP, including foreign-tenant denial.
- Daemon and Core restart with unchanged committed Items, preserved conversation
  history and outputs, and no repeated publication effects.
- Cancellation of a foreground native process, stopped file effects, idempotent
  repeat cancellation and continued execution without restarting cancelled work.
- Native tools cannot read executor credentials/private staging witnesses or
  inherit known private credential values. Public responses contain no known
  private credentials. Private witnesses remain unchanged across restart.

E2B additionally executes the native isolation probe through a fifth real model
Turn. It checks a witness in the actual native-history directory, the executor
key, staging and protected startup receipt, PID-namespace separation, inaccessible
outer process secrets, and denied envd/sudo/privileged-account access. Official
E2B SDK 2.51.0 receipts record creation, metadata-bound inspection, lease renewal
and explicit kill of each owned sandbox. No replacement Runtime is called recovery.

The separate real Docker credential lifecycle check covers caller/executor role
separation, principal/tenant/Environment binding, rejection of history rebinding,
key rotation fencing the old socket while retaining device identity, revocation,
and Session deletion without reclaiming caller-owned compute. These shared Core
checks are not claimed as a separate live rotation/revocation run on every E2B
profile. User-managed Sessions create no managed Runtime allocation.

## Evidence and verification boundaries

Evidence is retained on `zju_a100_2` under
`~/.parsar/remediation/20260921/self-hosted-onboarding/`:

- `source-candidate.json` and `source-verified.json`: exact source and binary hashes.
- `live/{codex,claude,mcode}/accepted.json`: complete Docker four-Turn runs before
  removal of unused private wire fields. `live/codex/security.json` records the shared
  real credential lifecycle checks.
- `live/final-{codex,claude,mcode}/final-smoke.json`: final private wire 0.3.0
  binaries, real native command and Files/Artifacts/tenant readback. This is a
  focused regression, not another complete Docker lifecycle run.
- `live-e2b/{codex-attempt3,claude-attempt1,mcode-attempt1}/`: final five-Turn
  E2B acceptance and cleanup (208.562s, 147.053s and 125.647s respectively).
- `e2b-builds/final-daemon/`: immutable template receipts and verified daemon hash.
- `make-check-final.log`: full gate at `ce11501`, including dedicated real
  PostgreSQL, fixed official client, all service/daemon packages, sqlc regeneration,
  independent builds, Claude/MiniMax packaging and retained Rust helper checks.
  The subsequent connection-cache cleanup fix at `8f0cd25` passed the full
  execution-package regression suite and independent Core build.
- `review-final.json`: independent Astra high review of the complete diff,
  performed with reused context under explicit user authorization. It is not a
  fresh-context blind review.

The final Docker smoke runner initially called a test helper with the wrong
signature after native execution. A read-only follow-up completed the assertions
against those exact Turns without model replay. Original failed logs are retained.
The first E2B Codex launch had an uncertain network result and was reconciled by
metadata without retrying that Create. The second run stopped on an operator
readiness-wrapper error after restart. Its owned VM was removed before the fresh
third run; the failed records remain failures, not accepted execution evidence.

## Limits

Qualification is bounded to these immutable Linux amd64 Runtime builds and their
recorded real models. It does not establish arbitrary-host isolation, high
availability, full Agents API conformance, Anthropic-model acceptance for Claude,
or identical optional capabilities across harnesses.

These runs received their model through the operator options file, which is now
retired. A self-hosted Session now carries its own model provider, from the request
or a saved Agent, frozen and delivered over the same daemon transport; see
[model execution](model-execution.md). Deployment default model providers never
reach self-hosted executors.

The public self-hosted profile accepts `/workspace` and empty capability
directories. Service-origin HTTP MCP on self-hosted remains explicitly rejected;
none-environment HTTP MCP and separately qualified hosted Plugin MCP keep their own
scope. Environment Templates remain hosted-only. Unspecified upstream defaults,
errors, lifecycle edge cases and broader resource semantics remain in the protocol
coverage ledger.

Runtime workspace and native history must survive restart. Expiry or loss of that
state cannot authorize silent replacement or replay. A successful disconnect,
revocation or Session deletion is not a guarantee that every native effect has
stopped; the compute owner remains responsible for termination and cleanup.
