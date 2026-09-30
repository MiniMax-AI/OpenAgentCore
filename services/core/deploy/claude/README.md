# Claude Code on the dedicated Runtime

Core is independently deployed. Each managed Session receives one Docker Runtime
containing the daemon, pinned Claude Agent SDK/native harness and local workspace.
The SDK owns the model/tool loop. Public clients use the same Agents API contract.

## Build and configure

The [maintainer guide](../../../../docs/maintainers.md#runtime-images-and-helpers)
builds the image. The bundle pins SDK `0.3.269` and native Claude Code `2.1.269`. The Dockerfile pins
Node's Linux amd64 manifest. Keep the exported SDK bundle immutable. Configure
Core's database-owned managed deployment with the resulting immutable Runtime
image. Existing Docker outer security settings are unchanged; the daemon and
native adapter do not add an inner sandbox. Tools run as UID/GID 1000 and can
access files available to that user.

Install required system dependencies while building the image/template. Runtime
has no automatic apt installation, sudo or elevated daemon permissions;
`system_packages` is unsupported and missing dependencies cause operation failure.
npm/Python package and setup commands run directly with the existing user's
permissions. The packaged image no longer needs bubblewrap or socat for an inner
sandbox. For native self-hosting and platform limits, see the
[self-hosted guide](../../../../docs/getting-started/self-hosted.md#platforms).

Set `OAC_DEFAULT_HARNESS=claude_sdk`. Configure the deployment default model provider
with the Core key, in Web or through Core's API:

```sh
curl -fsS -X PUT http://127.0.0.1:8091/core/v1/harnesses/claude_sdk/model-provider \
  -H "Authorization: Bearer $CORE_KEY" -H "Content-Type: application/json" \
  -d '{"protocol":"anthropic","base_url":"https://api.anthropic.com","api_key":"REPLACE_WITH_OPERATOR_SECRET"}'
```

A Session may instead supply its own `x_agents_core.model_provider`. Use the
endpoint and model supported by your actual provider. Do not bake keys into the
image. Core freezes the bundle in the Session's encrypted snapshot and delivers it
to the adapter; it is not a public Agent field. Ordinary environment projection
does not isolate locally stored credentials from tools running as the same user.
Follow the existing Core setup for independent PostgreSQL credentials, migrations,
API authentication and managed Runtime enrollment. Parsar is not a dependency.

The supported hosted profile accepts text execution with medium verbosity and
native Bash/Read/Edit and declared public functions with text results. Files and Artifacts use the shared public interfaces.
Check the current qualified engine profile for MCP, subagents and structured
output combinations. Historical hosted-profile acceptance does not qualify native
self-hosted platforms or every feature combination. The existing
`environment:none` function/MCP profile is separate. This is not complete upstream
protocol compatibility.

## Runtime and adapter rules

Native self-hosted installations use the same bundle and daemon protocol.
The shared local binding selects the actual workspace. SDK history, home and
scratch remain under `OAC_RUNTIME_HOME/runtime/claude-sdk` for session ownership,
without restricting tools. Authentication remains under `OAC_RUNTIME_HOME/daemon`.
The daemon requires neither nested sandbox privileges nor host security changes.

The `local_runtime_v2` capability identifies the current workspace and direct MCP
launcher contract. Reject earlier workspace bundles; matching SDK versions alone
do not establish adapter compatibility.
The adapter advertises `local_runtime_v2` after checking the installed bridge and
native binary. Windows additionally needs Git Bash for its Bash tool. Registration
combines that check with the local binding; Core consumes the same readiness
contract on every platform. The profile supports Bash/Read/Edit, preparation,
shared Files/Artifacts, cancellation and history continuation. Other capabilities
remain subject to their actual engine qualification; bypass execution does not
silently qualify a new MCP or function combination.

Recovery uses the SDK's history APIs. An explicitly supplied native identity must
exist. If Core requires existing history without having recorded an identity, the
adapter accepts only one nonempty native history for the exact bound cwd. Missing,
foreign, ambiguous or metadata-only history rejects before model input. The Runtime
volume and shared Environment/Session binding establish ownership; this lookup
cannot select another Session's home or infer ownership from a model response.

Claude hosted functions compose the existing SDK function bridge with the common
workspace execution profile. Only declared function tools and the verified native tool
inventory are available. The bundle advertises this combination separately from
basic workspace execution; function preparations require that verified combination.
Service-origin hosted MCP remains unqualified; Environment Plugin declarations
use the separately qualified initialization path above. Function callbacks do not change file,
credential, history, subagent or network authority.

## Adding another native engine

Follow [Add a native Harness](../../../../contracts/agents-api/harness-onboarding.md).
The Claude adapter is one of its [native references](../../../../contracts/agents-api/harness-onboarding.md#native-references):
it implements the shared `agent.ExecutorFactory`, `Executor` and `Turn`
interfaces and registers them in
[`cli/claude_sdk.go`](../../../../apps/daemon/internal/cli/claude_sdk.go).
Acceptance requirements are in
[Harness integration](../../../../contracts/agents-api/harnesses.md#acceptance-checklist).
