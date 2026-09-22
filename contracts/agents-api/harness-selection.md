# Core harness selection extension

The pinned official Agent contract has no harness selector. Core adds one optional
`x_agents_core` field to saved Agent create/update/read and Session inline/effective
Agent configuration. This is a Core extension, not an upstream field.

```json
{"x_agents_core":{"harness":"claude_sdk"}}
```

Supported identifiers are `codex`, `claude_sdk` (Claude Code), and `mcode`
(MiniMax Code). Unknown identifiers, empty objects and unknown nested fields are
rejected. Omission inherits a saved Agent value, or uses `AGENTS_API_ENGINE` for
an inline Agent. An explicit null clears the saved selection or replaces it for
one Session, restoring deployment-default selection. Agent updates preserve omitted
fields and replace the entire supplied extension. Saved resources do not start
execution and can retain protocol configuration beyond the selected engine's
current execution profile.

Session creation resolves the extension after saved-Agent overrides, validates the
selected execution profile, and persists the resulting existing `Session.Engine`.
An explicitly selected harness must be enabled by the deployment; it never falls
back to a different engine. When the effective Agent includes the extension, Session
reads report its persisted engine. Sessions without the extension retain the official
Agent response shape, including historical Sessions. Reads never consult current
Agent defaults or the current deployment default. Explicit-selector creation retries
retain caller intent before mutable configuration resolution. Changing a selector
under an existing creation key conflicts.

The environment remains the separate official Session `environment` parameter.
Environment templates select startup configuration, not engines, containers or
providers. Multiple Sessions using the same Agent/template have separate Environment
resources and native histories. Agent edits do not change accepted Sessions.

## Operator configuration

`AGENTS_API_ENGINE` selects the default engine. `AGENTS_API_HARNESSES` explicitly
adds comma-separated deployment-supported engines, for example
`codex,claude_sdk,mcode`, without requiring a managed Provider. The default engine
remains enabled; unknown names fail startup.
This setting does not install a harness or qualify a native deployment.

Hosted placement uses one provider per deployment. Set `provider` to `docker` or
`microsandbox` in `AGENTS_API_MANAGED_RUNTIMES_FILE`, with a stable
`installation_id`, a `maintenance` boolean and one matching backend object.
For example:

```json
{
  "core_url": "http://core:8091/api/v1",
  "provider": "docker",
  "installation_id": "11111111-1111-4111-8111-111111111111",
  "maintenance": false,
  "docker": {
    "host": "unix:///var/run/docker.sock",
    "image": "sha256:<qualified-runtime-image>",
    "network": "bridge",
    "seccomp_file": "/private/seccomp.json",
    "nested_sandbox": true
  }
}
```

There is no engine-to-provider routing or mixed-provider configuration. Enabling
another harness does not choose another image or backend; the selected Runtime
image must contain and qualify each enabled harness. Capability checks still
apply. Legacy `default_provider`, `engine_providers` and provider maps are rejected.

Before changing provider or backend namespace, restart the original configuration
with `maintenance: true`, explicitly settle or delete its hosted resources and
confirm cleanup is complete. Restart the new provider configuration in maintenance
to validate the switch, then disable maintenance without changing its identity.
Switching never migrates Sessions or deletes resources automatically. See the
[deployment procedure](../../services/agents-api/deploy/microsandbox/README.md#change-the-deployment-provider).

For multiple engines, use an exclusive `by_harness` object in the private
`AGENTS_API_EXECUTION_OPTIONS_FILE`, with one existing adapter-options object per
engine. No engine inherits another engine's credentials. A legacy flat options
object is usable only by the deployment default engine. Missing options for a
selected engine fail execution. Credentials stay in private operator files and
transient adapter requests, never Agent defaults, metadata or effective responses.
A Session may instead provide the [write-only model execution extension](model-execution.md);
its frozen configuration takes precedence without operator fallback.

Model names remain explicit `agent.model` values. The selected native adapter uses
its configured provider and rejects unsupported model settings without changing
model identity. Core applies its existing engine/environment/tool/verbosity rules
before creating a Session; provider model availability is checked during native
startup/execution. There is no invented cross-provider model-name catalog.

Current profile limits remain in the [engine coverage table](README.md#public-engine-profiles).
