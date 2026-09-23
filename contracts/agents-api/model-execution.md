# Agent defaults and Session model execution

Core accepts optional `x_agents_core.model_provider` on saved Agent creation and
update, and the same top-level bundle as an explicit Session creation override.
This is a Core extension, not part of the pinned upstream protocol. It supplies
execution input only: there is no Provider CRUD, catalog, model alias resolution or
product permission model in Core.

## Saved defaults and precedence

A saved Agent is editable configuration, not a permanently bound runtime. Create
or update it with `model`, optional `x_agents_core.harness`, and an optional complete
`x_agents_core.model_provider`. Create/update/retrieve/list responses return safe
provider fields and the output-only `api_key_configured` flag. They never return
`api_key`, ciphertext or a reusable credential reference. Normal Agent JSON stores
only the safe view; the secret bundle has a separate encrypted row bound to the
tenant and Agent with a distinct encryption purpose. Agent writes commit safe
configuration and ciphertext together. A model-only edit does not require a key.

Session creation resolves each explicit model/harness override before saved defaults;
when no harness is selected, the deployment harness applies. The pinned API still
requires a model for an inline Agent and when creating a saved Agent. There is no
model-name inference. Provider precedence is: complete Session bundle, complete
saved bundle, then configured deployment bundle. Never merge a replacement endpoint
with an inherited key. A model-only override reuses the entire inherited bundle.
Codex requires `responses`; Claude SDK and MiniMax Code require `anthropic`, with
positive context/output limits for MiniMax Code. Validate the resolved combination
before writing a Session. Core-supplied provider credentials remain hosted-only.

| Operation | Omitted | Explicit null |
| --- | --- | --- |
| Agent update `x_agents_core` | Preserve both defaults | Clear harness and provider, including its secret |
| Agent update nested `model_provider` | Preserve the existing bundle | Clear the entire saved bundle |
| Agent update nested `harness` | Preserve existing harness | Reject; use extension null to reset |
| Session top-level `x_agents_core` | Inherit provider defaults | Inherit provider defaults |
| Session nested `model_provider` | Inherit provider defaults | Inherit provider defaults |
| Session inline `agent.x_agents_core` | Inherit saved harness | Reset to deployment harness |

An empty Session execution extension remains invalid. An explicitly null provider
is a defined inheritance request; an empty/partial provider object is invalid.
Unknown, duplicate or output-only saved-provider input fields are rejected. Saved
Agent creation without a harness may save a valid bundle, with final harness
compatibility checked at Session admission. Provider-only Agent updates preserve
the saved harness and validate their merged compatibility under the row lock.
Session inline `agent.x_agents_core` remains harness-only; the provider override
belongs at the Session request's top level.

Read the Agent configuration and encrypted bundle from one coherent database
snapshot. Explicit complete Session overrides do not need to decrypt a saved
bundle. Persist a new Session-owned encrypted snapshot atomically with Session and
environment creation. Existing Sessions never consult the Agent again: edits, key
replacement, deletion, suspend/resume and process restarts cannot change their
model, harness or provider. A missing/wrong encryption key fails closed. Retain the
same deployment credential-encryption key across restarts. V1 has no Turn override,
provider catalog, Session migration or new execution loop.

All new hosted requests record caller intent before resolving mutable defaults,
including inline requests that use deployment defaults. Matching creation retries
recover the committed Session before resolving the Agent or provider again and do
not enqueue another input. Streaming remains outside the retry identity. Existing
historical rows keep their documented retry limitations; this change does not
rewrite them. Omitted and explicit fields retain the existing local intent-hash
semantics rather than promising upstream equivalence.

See the [TypeScript client example](../../packages/agents-client/saved-agent-defaults.md).

## Session override example

```json
{
  "agent": {"model": "exact-provider-model", "x_agents_core": {"harness": "mcode"}},
  "environment": {"type": "openai_hosted"},
  "x_agents_core": {
    "model_provider": {
      "protocol": "anthropic",
      "base_url": "https://provider.example/anthropic",
      "api_key": "<private key>",
      "context_window": 200000,
      "max_output_tokens": 8000
    }
  }
}
```

`protocol` is `anthropic` for Claude Code/MiniMax Code or `responses` for Codex.
The endpoint must use HTTPS without embedded credentials, a query or a fragment.
Keys must be nonempty, at most 16 KiB, and contain no NUL/CR/LF. Unknown fields and
unsupported protocol/Harness/environment combinations are rejected before creating
a Session. Context/output limits are optional nonnegative integers, with output no
larger than context; both must be positive for MiniMax Code. Use the actual model's
limits. Native provider availability is checked during execution, not by a new probe.
`agent.model` retains its exact meaning; this extension never changes model identity.

The entire resolved provider configuration is frozen and encrypted in the Session creation
transaction, with a distinct credential-crypto purpose and tenant/Session binding.
Creation retries include this intent in their request hash; changing the key or
endpoint under the same idempotency key conflicts. Recovery reads the committed
Session before mutable Agent/template resolution. No public Session, Agent,
Environment, event or ordinary configuration contains the key. The top-level
extension is write-only and has no update endpoint.

At dispatch, Core resolves its encrypted snapshot into the existing native adapter
options. It does not fall back to operator credentials when a snapshot is missing
or cannot decrypt. For hosted Sessions, omission resolves saved defaults first and then the configured
deployment provider bundle. Deployment provider defaults from
`AGENTS_API_EXECUTION_OPTIONS_FILE` are converted at the server composition boundary
and frozen using the same encrypted Session snapshot. That snapshot also retains
the complete selected private operator options, including native request headers,
query parameters and permission settings; dispatch does not reconstruct a lossy
subset or reread those defaults. New hosted Sessions validate this deployment
bundle using the same HTTPS/key/protocol rules: legacy operator HTTP endpoints
must move to HTTPS; invalid defaults fail admission rather than being silently
rewritten. Explicit and saved provider bundles continue to
use the existing typed adapter mapping. Other environments and
historical Sessions retain their existing operator-options behavior.
Core needs its configured credential encryption key to accept and resume these
Sessions; retaining the same key is required across restarts. Native harness homes
may contain private provider configuration under the existing qualified hosted
isolation rules; tools and public Files must not access those homes. This extension
does not qualify a new runtime placement or self-hosted credential path.

Parsar manages its own workspace catalog and encrypted keys, sends this extension
only on the first Core Session request, and retains a private encrypted snapshot
for uncertain creation retries. Catalog updates and deletion affect new Sessions;
existing Sessions retain their original model, endpoint and key.
