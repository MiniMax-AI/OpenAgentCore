# Core harness selection extension

The pinned official Agent contract has no harness selector. Core adds one optional
`x_agents_core` field to saved Agent create/update/read and Session inline/effective
Agent configuration. This is a Core extension, not an upstream field.

```json
{"x_agents_core":{"harness":"claude_sdk"}}
```

Supported identifiers come from the [generated Harness catalog](harness-catalog.md).
Unknown identifiers, empty objects and unknown nested fields are
rejected. Omission inherits a saved Agent value, or uses `OAC_DEFAULT_HARNESS` for
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

`OAC_DEFAULT_HARNESS` selects the default engine. `OAC_HARNESSES` explicitly
adds comma-separated deployment-supported engines, for example
`codex,claude_sdk,mcode`, without requiring a managed Provider. The default engine
remains enabled; unknown names fail startup.
This setting does not install a harness or qualify a native deployment.

Harness selection is independent of Environment provisioning. Provider selection,
node generations and reset behavior are owned by
[Sandbox deployment](sandbox-deployment.md); node wire compatibility is owned by
[the node protocol](node-generation-protocol.md). Enabling a Harness does not
install or qualify it on an existing Runtime.

Model/provider defaults, source precedence and credential handling are owned by
[Model execution](model-execution.md#saved-defaults-and-precedence). They do not
introduce another Harness selector.

Current profile limits remain in the [engine coverage table](README.md#public-engine-profiles).

## Implementation rules

Core accepts the optional `agent.x_agents_core.harness` extension through the
saved Agent and inline Session configuration paths. Define the extension once in
`contracts/agents-api/v1`; never use metadata or a competing top-level selector.
Use the catalog-derived validators for identifiers and the selection semantics
above. Do not maintain another accepted-name list in handlers or schemas.
