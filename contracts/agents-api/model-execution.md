# Agent defaults and Session model execution

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
when no harness is selected, the deployment harness applies. The pinned API still
requires a model for an inline Agent and when creating a saved Agent. There is no
model-name inference. Provider precedence is: complete Session bundle, complete
saved bundle, then the deployment default for the resolved harness. Never merge a
replacement endpoint with an inherited key. A model-only override reuses the entire
inherited bundle. Codex requires `responses`; Claude SDK and MiniMax Code require
`anthropic`, with positive context/output limits for MiniMax Code. Validate the
resolved combination before writing a Session.

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
including inline requests that use deployment defaults. Other inline requests,
such as `none`, keep the resolved-request retry rule; that hash leaves out a
deployment default, so setting, replacing or removing the default does not change
their retry identity. Matching creation retries
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
a Session. The Session's `x_agents_core` accepts only `model_provider`; hosted node
placement is automatic, and the removed `sandbox_node_id` is rejected with 400 like
any other unknown member. Context/output limits are optional nonnegative integers, with output no
larger than context; both must be positive for MiniMax Code. Use the actual model's
limits. Native provider availability is checked during execution, not by a new probe.
`agent.model` retains its exact meaning; this extension never changes model identity.

The entire resolved provider configuration is frozen and encrypted in the Session creation
transaction, with a distinct credential-crypto purpose and tenant/Session binding.
Creation retries include this intent in their request hash; changing the key or
endpoint under the same idempotency key conflicts. A key enters any stored hash
only as a fingerprint keyed by the deployment credential key, never directly. Recovery reads the committed
Session before mutable Agent/template resolution. No public Session, Agent,
Environment, event or ordinary configuration contains the key. The top-level
extension is write-only and has no update endpoint.

At dispatch, Core resolves its encrypted snapshot into the existing native adapter
options. It does not fall back to other credentials when a snapshot is missing or
cannot decrypt. The same snapshot path serves every environment: Core sends the
options only over the daemon connection bound to the Session. For `self_hosted`,
that is the executor enrolled for the Session's own Environment with a current
executor credential of the Session creator's principal; rotation or revocation
closes the socket before further dispatch. The executor host stores the bundle in
its native harness home, as hosted Runtimes do; tools and public Files cannot reach
that home. Revocation does not erase a bundle already delivered.

## Deployment defaults

The deployment default is a runtime setting stored in Core, one complete bundle per
harness, managed with the Core key through Web or `/core/v1`:

| Method and route | Result |
| --- | --- |
| `GET /core/v1/harnesses` | Every harness this build supports, with `enabled` and `default` from the process configuration and its `model_provider` (safe view) or null |
| `GET /core/v1/harnesses/{harness}/model-provider` | The safe view; 404 when none is set |
| `PUT /core/v1/harnesses/{harness}/model-provider` | Replace it with a complete `x_agents_core.model_provider` bundle, validated for the harness |
| `DELETE /core/v1/harnesses/{harness}/model-provider` | Remove it; idempotent, 204 |

Reads return `protocol`, `base_url`, optional limits, `api_key_configured` and
`updated_at`, never the key. The bundle is encrypted with its own
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

The former `AGENTS_API_EXECUTION_OPTIONS_FILE` is retired: setting it stops Core at
startup with the replacement named; remove it and set the deployment defaults
in Web (System) or with `PUT /core/v1/harnesses/{harness}/model-provider`. Only its provider identity (endpoint, key,
protocol and MiniMax Code limits) has a home in the deployment default; its other
native options (headers, query parameters, environment, MCP servers, feature and
permission settings) are dropped. Sessions frozen from that file keep their
complete private snapshot. Historical `openai_hosted` and `self_hosted` Sessions
created without any snapshot cannot start new work: message input returns 400
`model_provider_required`, while cancellation and history reads keep working.
Input they reserved before the upgrade settles as failed with that reason, and the
Session reports it, instead of waiting for its deadline. A retry of a Session that
carried its own provider key, first sent before this release, conflicts once after
the upgrade, because its retry hash now holds a keyed fingerprint instead of the
key. Key-bearing retries likewise conflict after a credential key change, which is
not supported anyway.
Recreate them with a bundle. Run `deploy/install/model_provider_sessions.py`
against an installation before upgrading to count them; it only reads. Historical
`none` Sessions that relied on the retired options file now run with the device's
own environment, and the check does not count them.

Parsar manages its own workspace catalog and encrypted keys, sends this extension
only on the first Core Session request, and retains a private encrypted snapshot
for uncertain creation retries. Catalog updates and deletion affect new Sessions;
existing Sessions retain their original model, endpoint and key.
