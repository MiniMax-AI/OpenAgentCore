# Agent defaults and Session model execution

This page describes the implemented API. The
[unified model configuration design](model-configuration-design.md) owns the planned
common parameter vocabulary and replacement rules; its planned fields are not yet
accepted by these endpoints.

Core accepts optional `x_agents_core.model_provider` on saved Agent creation and
update, and the same top-level bundle as an explicit Session creation override.
This is a Core extension, not part of the pinned upstream protocol. It supplies
execution input only: there is no provider catalog, model alias resolution or
product permission model in Core. Besides Session and saved Agent bundles, the only
stored bundle is one [deployment default](#deployment-defaults) per harness.

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
when no harness is selected, the deployment harness applies. Saved Agent creation still requires a model. As a Core extension, an inline
`openai_hosted` or `none` Session may omit its model to use the deployment model
for the resolved harness. `self_hosted` never uses deployment model settings.
There is no model-name inference. Provider precedence is: complete Session bundle, complete
saved bundle, then the deployment default for the resolved harness. Never merge a
replacement endpoint with an inherited key. A model-only override reuses the entire
inherited bundle. Connections use only the selected Harness's native protocols:

| Harness | Supported protocols, in default order |
| --- | --- |
| Codex | `responses` |
| Claude SDK / Claude Code | `anthropic` |
| MiniMax Code | `anthropic`, `responses`, `chat_completions` |

The first protocol is the default. MiniMax Code requires positive context/output
limits. Validate the resolved combination before writing a Session. Core and
Runtime consume the same ordered `protocols` declaration in
`internal/harnessconfig`. There is no built-in model API proxy, passthrough
gateway or automatic cross-protocol conversion, including inside a Harness.
Unsupported saved configurations and Session snapshots fail explicitly when used;
they are never silently rewritten, aliased or migrated.

Where each source applies depends on who owns the compute that receives the key:

| Environment | Session or saved Agent bundle | Deployment default | No bundle resolved |
| --- | --- | --- | --- |
| `openai_hosted` | Accepted | Applied | 400 `model_provider_required` |
| `self_hosted` | Accepted | Never applied | 400 `model_provider_required` |
| `none` | Rejected with 400 | Applied when configured | Accepted; the device's own environment supplies the model |

The deployment default holds the operator's key, so it stays on operator compute:
Core-managed sandboxes and operator-registered `none` devices. A `self_hosted`
executor belongs to the application; the caller supplies its own bundle. Hosted and
self-hosted Runtimes carry no model configuration of their own, so a Session there
without a bundle is rejected before any write, with `param`
`x_agents_core.model_provider` and a message that says what to configure, instead
of starting a harness that would fall back to a built-in endpoint.

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
Session inline `agent.x_agents_core` accepts `harness` and `harness_config`;
the provider override belongs at the Session request's top level.

Read the Agent configuration and encrypted bundle from one coherent database
snapshot. Explicit complete Session overrides do not need to decrypt a saved
bundle. Persist a new Session-owned encrypted snapshot atomically with Session and
environment creation. Existing Sessions never consult the Agent again: edits, key
replacement, deletion, suspend/resume and process restarts cannot change their
model, harness or provider. A missing/wrong encryption key fails closed. Retain the
same deployment credential-encryption key across restarts. V1 has no Turn override,
provider catalog, Session migration or new execution loop.

All new hosted requests and requests that omit the inline model record caller intent before resolving mutable defaults,
including inline requests that use deployment defaults. Other inline requests,
such as `none`, keep the resolved-request retry rule; that hash leaves out a
deployment default, so setting, replacing or removing the default does not change
their retry identity. Matching creation retries
recover the committed Session before resolving the Agent or provider again and do
not enqueue another input. Streaming remains outside the retry identity. Existing
historical rows keep their documented retry limitations; this change does not
rewrite them. Omitted and explicit fields retain the existing local intent-hash
semantics rather than promising upstream equivalence.

See the [TypeScript client example](../../packages/agents-client/README.md#saved-agent-and-deployment-defaults).

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

`protocol` names the upstream API: `anthropic`, `responses` or `chat_completions`.
It does not select an engine. The selected Harness must support that protocol
natively, as listed above; a mismatch is rejected.
The endpoint must use HTTPS without embedded credentials, a query or a fragment.
Keys must be nonempty, at most 16 KiB, and contain no NUL/CR/LF. Unknown fields and
unsupported protocol/Harness/environment combinations are rejected before creating
a Session. The Session's `x_agents_core` accepts `model_provider` and `harness_config`; hosted node
placement is automatic, and the removed `sandbox_node_id` is rejected with 400 like
any other unknown member. Context/output limits are optional nonnegative integers, with output no
larger than context; both must be positive for MiniMax Code. Use the actual model's
limits. Native provider availability is checked during execution, not by a new probe.
`agent.model` retains its exact provider model identity; a supplied value always
replaces the deployment model.

The entire resolved provider configuration is frozen and encrypted in the Session creation
transaction, with a distinct credential-crypto purpose and tenant/Session binding.
Creation retries include this intent in their request hash; changing the key or
endpoint under the same idempotency key conflicts. A key enters any stored hash
only as a fingerprint keyed by the deployment credential key, never directly. Recovery reads the committed
Session before mutable Agent/template resolution. No public Session, Agent,
Environment, event or ordinary configuration contains the key. The top-level
extension is write-only and has no update endpoint.

At dispatch, Core delivers its encrypted snapshot as one common confidential
provider bundle. Runtime adapters apply native options and connect directly to the
configured provider. Core does not fall back to other credentials when a snapshot is missing or
cannot decrypt. The same snapshot path serves every environment: Core sends the
options only over the daemon connection bound to the Session. For `self_hosted`,
that is the executor enrolled for the Session's own Environment with a current
executor credential of the Session creator's principal; rotation or revocation
closes the socket before further dispatch. The executor host stores the bundle in
its native harness home, as hosted Runtimes do. Public Files remains scoped to the
bound workspace, but native tools use the starting account's permissions and can
access whatever that user can read. The daemon does not isolate its local
credentials from same-user tools. Revocation does not erase an already delivered
bundle.

## Native model parameters

The optional `harness_config` object uses the selected harness's native model
parameters. It is accepted on saved Agent `x_agents_core`, inline
`agent.x_agents_core`, and the Session's top-level `x_agents_core`. The Session
extension takes precedence over the inline extension. See
[Harness onboarding](harness-onboarding.md#native-model-configuration) for the
single supported-field reference and Runtime preparation contract.

An explicitly supplied object replaces the entire object; `{}` clears it, and
null is invalid. A Session that explicitly selects a model or provider without
supplying native parameters uses `{}` rather than inheriting another model's
parameters. Otherwise a saved Agent supplies its object. An inline Session using
the deployment model uses its deployment object. Changing the selected harness
also clears inherited parameters. Updating a saved Agent's model, provider or
harness follows the same rule unless that update supplies `harness_config`.
There is no deep merge. An inline extension that only sets native parameters
preserves the saved harness; a null inline extension resets harness selection. Neither this object nor a deployment default enables
public `reasoning` execution options that the service does not already support.

Core validates the resolved configuration before persistence and freezes it in the
Session Agent configuration. The administrator execution-configuration read records
its value and source. Native protocol support does not qualify structured output,
tool discovery, native web search, verbosity or image forms by itself. Existing
operation and input capability checks still apply before native submission,
including newly introduced images. A supported native connection does not
establish that the remote model accepts a parameter.

Model parameters are safe, non-confidential fields; provider
keys remain in the separate encrypted bundle. Reconnect uses the frozen logical
configuration and cannot change the selected provider, protocol or native
parameters. An incompatible frozen configuration fails explicitly.

## Deployment defaults

The deployment default is a runtime setting stored in Core, one complete model configuration per
harness, managed with the Core key through Web or `/core/v1`:

| Method and route | Result |
| --- | --- |
| `GET /core/v1/harnesses` | Every harness this build supports, with `enabled` and `default` from the process configuration and its `model_configuration` (safe view) or null |
| `GET /core/v1/harnesses/{harness}/model-configuration` | The safe view; 404 when none is set |
| `PUT /core/v1/harnesses/{harness}/model-configuration` | Replace `{model_provider, model, harness_config}` using the shared provider and native-parameter validators |
| `DELETE /core/v1/harnesses/{harness}/model-configuration` | Remove it; idempotent, 204 |

PUT requires `model` and a complete `model_provider` bundle; `harness_config`
defaults to `{}`. Reads return `model`, `harness_config`, the safe `model_provider`
view, observations and `updated_at`, never the key. The provider view owns
`protocol`, `base_url`, optional token limits and `api_key_configured`. The bundle is encrypted with its own
credential-encryption purpose, bound to the harness, and each write records an
administrator audit entry (`resource_type: deployment_model_provider`, the harness
as `resource_id`, action `set` or `delete`, `project_id` null) without the key. A
missing or wrong encryption key fails closed: writes and Session creation that
needs the default return 503 `credential_storage_unavailable`.

Session creation decrypts the default for the resolved harness and freezes it in the
Session's encrypted snapshot, like any other bundle. Changing or removing the
default never reaches existing Sessions, so a Session's first and later Turns always
use the same provider. The execution-configuration read shows the frozen safe view
with source `deployment`.

Configure deployment defaults through `PUT /core/v1/harnesses/{harness}/model-configuration`.
The operator options file and historical native-option snapshots are unsupported.
A missing or invalid provider snapshot fails closed; no upgrade reader, automatic
migration or fallback to another model/provider is provided.

Parsar manages its own workspace catalog and encrypted keys, sends this extension
only on the first Core Session request, and retains a private encrypted snapshot
for uncertain creation retries. Catalog updates and deletion affect new Sessions;
existing Sessions retain their original model, endpoint and key.

### Deployment default observations

The Core-only harness/default-provider reads include nullable `last_used_at`,
`last_error_code` and `last_error_at`. PUT resets all three, even for the same bundle.
Completed root Turns contribute use observations only when their Session froze
that exact current default revision. Failed root Turns contribute only the fixed
native provider codes `authentication_error`, `connection_failed`,
`rate_limit_exceeded`, `usage_limit_exceeded`, `server_overloaded`, `server_error`,
`resource_not_found`, `request_timeout` and `invalid_request`. Input-policy,
Core/runtime, cancelled and waiting outcomes do not contribute. Successful use
retains the earlier error; comparing timestamps is only a display convention.

These are best-effort Core receipt times after terminal commit, not provider health
or remote completion times. Private revision identity is independent of timestamps;
old, explicit-provider and historical Sessions cannot update a replacement default.
No public Session/Turn fields or retry identity change. The observation has a
one-second budget including pool/row-lock acquisition and cannot change the committed
Turn. A crash, failure or throttle can omit the last observation indefinitely.

Errors throttle for 30 seconds regardless of code. Ordinary successful writes
throttle for 30 seconds; the first success after an accepted error records recovery
immediately. For an unchanged revision this allows at most three effective metadata
writes in any half-open 30-second interval of nondecreasing DB-clock time. Equal or
backwards clock readings are preserved and can make timestamp-based display
ambiguous; recovery does not impose a total order. No readiness probe, automatic
refresh, observation history or credential/raw-error read is provided.

The harness list also returns `model_configuration_support`, derived from the
same adapter declaration used by Core and Runtime: `protocols` lists selectable
native upstream protocols in default order, `accepts_harness_config` reports
whether native parameters are accepted, and `token_limits_required` reports
required provider token limits. `protocols` is the sole protocol list; its first
entry is the configuration form's default. This descriptor describes the current
build, not a live Runtime or a remote model's availability.
