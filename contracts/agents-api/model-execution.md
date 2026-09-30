# Model execution

Each Session runs one Harness with one model provider. Core selects them through three Core extensions that the pinned upstream protocol does not define: `x_agents_core.harness` chooses the Harness, `x_agents_core.model_provider` supplies the endpoint and key, and `x_agents_core.harness_config` carries native model parameters. Core has no provider catalog, model alias resolution or product permission model; besides Session and saved-Agent bundles, the only stored bundle is one [deployment default](#deployment-defaults) per Harness. This document is the Harness–model provider protocol: [`internal/modelprovider/config.go`](../../internal/modelprovider/config.go) validates the frozen provider connection, and each Harness declares its protocols and native parameters through [`internal/harnessconfig/harness.go`](../../internal/harnessconfig/harness.go).

## Harness selection

```json
{"x_agents_core": {"harness": "claude_sdk"}}
```

Saved Agents accept `x_agents_core.harness` on create, update and read, and Sessions accept it in the inline `agent.x_agents_core`. Identifiers come from the [Harness catalog](harness-catalog.md); unknown identifiers and unknown nested fields are rejected, as is an empty inline Session extension. Which Harnesses a deployment enables, and its default, are the process settings `core.harnesses` and `core.default_harness` ([configuration](../../docs/configuration.md#settings)).

- Omitted: a Session inherits its saved Agent's Harness; an inline Agent uses the deployment default.
- Explicit null on the Session's inline extension: resets to the deployment default Harness while keeping inherited provider bundles. On a saved Agent, a null extension clears its Harness and provider.
- A selected Harness must be enabled; Core never falls back to another one.
- Session creation resolves the selection after saved-Agent overrides, validates the Harness profile and stores the result as the Session's engine. Session reads report it when the effective Agent includes the extension; other Sessions keep the official Agent shape. Reads never consult the current Agent or deployment default.
- Creation retries with an explicit selector keep the caller's intent; changing the selector under an existing Idempotency-Key conflicts.

The Session's `environment` and Environment Templates select preparation, not Harnesses or providers. The extension is defined once in `contracts/agents-api/v1`; validators derive from the catalog, and no handler or schema keeps its own list of names.

## Saved defaults and precedence

A saved Agent is editable configuration, not a bound runtime. Create or update it with `model`, optional `x_agents_core.harness` and an optional complete `x_agents_core.model_provider`. Responses return the safe provider fields and the output-only `api_key_configured` flag, never `api_key`, ciphertext or a reusable credential reference. Agent JSON stores only the safe view; the secret bundle has its own encrypted row, bound to the Project and Agent with a distinct encryption purpose, and is written in the same transaction. A model-only edit needs no key.

Session creation resolves each explicit model or Harness override before saved defaults; without a selected Harness, the deployment default applies. Saved Agents require a model. An inline `openai_hosted` or `none` Session may omit its model to use the deployment model of the resolved Harness; `self_hosted` never uses deployment model settings. Core never infers a model from its name.

Provider precedence is: a complete Session bundle, then a complete saved bundle, then the deployment default for the resolved Harness. Core never merges a replacement endpoint with an inherited key; a model-only override reuses the whole inherited bundle. Each Harness connects only through its native protocols:

| Harness | Supported protocols, default first |
| --- | --- |
| Codex | `responses` |
| Claude SDK | `anthropic` |
| MiniMax Code | `anthropic`, `responses`, `chat_completions` |

MiniMax Code requires positive context and output limits. Core validates the resolved combination before writing the Session. Core and Runtime read the same ordered `protocols` declaration in `internal/harnessconfig`. There is no model API proxy, passthrough gateway or cross-protocol conversion, including inside a Harness. Unsupported saved configurations and Session snapshots fail when used; they are never rewritten, aliased or migrated.

Which sources apply depends on who owns the compute that receives the key:

| Environment | Session or saved-Agent bundle | Deployment default | No bundle resolved |
| --- | --- | --- | --- |
| `openai_hosted` | Accepted | Applied | 400 `model_provider_required` |
| `self_hosted` | Accepted | Never applied | 400 `model_provider_required` |
| `none` | Rejected with 400 | Applied when configured | Accepted; the device's own environment supplies the model |

The deployment default holds the operator's key, so it stays on operator compute: Core-managed sandboxes and operator-registered `none` devices. A `self_hosted` executor belongs to the application, which supplies its own bundle. Hosted and self-hosted Runtimes carry no model configuration of their own, so a Session there without a bundle is rejected before any write, with param `x_agents_core.model_provider` and a message saying what to configure.

| Operation | Omitted | Explicit null |
| --- | --- | --- |
| Agent update `x_agents_core` | Keep both defaults | Clear Harness and provider, including its secret |
| Agent update nested `model_provider` | Keep the bundle | Clear the whole saved bundle |
| Agent update nested `harness` | Keep the Harness | Rejected; use a null extension to reset |
| Session top-level `x_agents_core` | Inherit provider defaults | Inherit provider defaults |
| Session nested `model_provider` | Inherit provider defaults | Inherit provider defaults |
| Session inline `agent.x_agents_core` | Inherit the saved Harness | Reset to the deployment Harness |

An empty Session execution extension is invalid. An explicit null provider requests inheritance; an empty or partial provider object is invalid. Unknown, duplicate or output-only saved-provider fields are rejected. A saved Agent without a Harness may save a valid bundle; its Harness compatibility is checked at Session admission. A provider-only Agent update keeps the saved Harness and validates the merged combination under the row lock. The Session's inline `agent.x_agents_core` accepts `harness` and `harness_config`; the provider override belongs at the request's top level.

Core reads the Agent configuration and encrypted bundle from one database snapshot; an explicit complete Session override needs no decryption of the saved bundle. The Session's own encrypted snapshot is written atomically with the Session and its Environment. Existing Sessions never consult the Agent again: edits, key replacement, deletion, suspension and restarts cannot change their model, Harness or provider. A missing or wrong encryption key fails closed; keep the same [credential key](../../docs/configuration.md#installation-directory) across restarts. There is no Turn-level override.

New hosted requests, and requests that omit the inline model, record caller intent before resolving mutable defaults. Other inline requests, such as `none`, keep the resolved-request retry rule; that hash leaves out the deployment default, so changing the default does not change their retry identity. A matching creation retry recovers the committed Session before resolving the Agent or provider again and enqueues no further input. Streaming is outside the retry identity. The [TypeScript client](../../packages/agents-client/README.md#saved-agent-and-deployment-defaults) shows saved Agents and deployment defaults.

## Session override

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

- `protocol` names the upstream API (`anthropic`, `responses` or `chat_completions`), not an engine. The selected Harness must support it natively.
- `base_url` uses HTTPS with a valid host, without credentials, query or fragment.
- `api_key` is nonempty, at most 16 KiB and contains no NUL, CR or LF.
- `context_window` and `max_output_tokens` are optional nonnegative integers, with output no larger than context; both must be positive for MiniMax Code. Use the real model's limits.
- `agent.model` is the exact provider model ID; a supplied value always replaces the deployment model.
- The Session's `x_agents_core` accepts `model_provider`, `harness_config` and `environment` ([Environments](environments.md#preparation-order)); any other member, such as `sandbox_node_id`, is rejected with 400. Hosted node placement is automatic.

Unsupported protocol, Harness or Environment combinations are rejected before the Session is created. Provider availability is checked during execution, not by a probe.

The resolved provider configuration is frozen and encrypted in the Session creation transaction, with its own encryption purpose and Project and Session binding. Creation retries include it in their request hash, so a changed key or endpoint under the same Idempotency-Key conflicts; a key enters any stored hash only as a fingerprint keyed by the deployment credential key. No public Session, Agent, Environment, event or ordinary configuration contains the key. The top-level extension is write-only and cannot be updated.

At dispatch, Core sends the snapshot as one confidential provider bundle over the daemon connection bound to the Session, and the adapter applies it natively and connects directly to the provider. Core never falls back to other credentials when a snapshot is missing or cannot be decrypted. For `self_hosted`, the receiving daemon is the executor enrolled for the Session's own Environment with a current executor credential of the Session creator's principal; rotation or revocation closes the socket before further dispatch. The executor host stores the bundle in its native Harness home, as hosted Runtimes do. Native tools run with the starting account's permissions and can read what that account can read, and revocation does not erase a bundle already delivered.

## Native model parameters

`harness_config` holds the selected Harness's native model parameters. Saved Agents accept it in `x_agents_core`, Sessions in the inline `agent.x_agents_core` and in the top-level `x_agents_core`; the top-level value wins.

| Harness | Accepted fields | Applied as |
| --- | --- | --- |
| Codex | `model_reasoning_effort`: `none`, `minimal`, `low`, `medium`, `high`, `xhigh` | App-server `-c model_reasoning_effort=...` and each Turn's `collaborationMode.settings.reasoning_effort` |
| Claude SDK | `effort`: `low`, `medium`, `high`, `xhigh`, `max`; `thinking`: the SDK's `adaptive`, `enabled` or `disabled` object | SDK `Options.effort` and `Options.thinking` |
| MiniMax Code | Empty object only | Provider token limits remain required |

Claude `thinking` accepts `display` (`summarized` or `omitted`) for `adaptive` and `enabled`, and a positive integer `budgetTokens` only for `enabled`; `maxThinkingTokens` is rejected. These are native settings, not a shared reasoning vocabulary; model availability and provider support are the Harness's responsibility.

A supplied object replaces the whole object; `{}` clears it and null is invalid. A Session that selects a model or provider without supplying native parameters uses `{}` instead of inheriting another model's parameters. Otherwise a saved Agent supplies its object, and an inline Session using the deployment model uses the deployment object. Changing the Harness clears inherited parameters, and so does an Agent update of model, provider or Harness that does not supply `harness_config`. There is no deep merge. An inline extension that sets only native parameters keeps the saved Harness. Neither this object nor a deployment default enables public `reasoning` options.

Core validates the resolved configuration before writing the Session and freezes it in the Session's Agent configuration; the administrator execution-configuration read shows its value and source. Native parameters are not confidential; provider keys stay in the separate encrypted bundle. Reconnection uses the frozen configuration and cannot change the provider, protocol or parameters; an incompatible frozen configuration fails. Protocol support does not qualify structured output, tool discovery, web search, verbosity or image input by itself, and a supported connection does not prove that the remote model accepts a parameter.

## Deployment defaults

The deployment default is a runtime setting stored in Core: one complete model configuration per Harness, managed with the Core key through Web or `/core/v1`.

| Method and route | Result |
| --- | --- |
| `GET /core/v1/harnesses` | Every Harness this build supports, with `enabled` and `default` from the process configuration, its `model_configuration` (safe view) or null, and `model_configuration_support` |
| `GET /core/v1/harnesses/{harness}/model-configuration` | The safe view; 404 when none is set |
| `PUT /core/v1/harnesses/{harness}/model-configuration` | Replace `{model_provider, model, harness_config}` using the shared provider and native-parameter validators |
| `DELETE /core/v1/harnesses/{harness}/model-configuration` | Remove it; idempotent, 204 |

PUT requires `model` and a complete `model_provider`; `harness_config` defaults to `{}`. Reads return `model`, `harness_config`, the safe `model_provider` view (`protocol`, `base_url`, optional token limits and `api_key_configured`), observations and `updated_at`, never the key. The bundle is encrypted with its own purpose and bound to the Harness. Each write records an administrator audit entry (`resource_type: deployment_model_provider`, the Harness as `resource_id`, action `set` or `delete`, `project_id` null) without the key. A missing or wrong encryption key fails closed: writes, and Session creation that needs the default, return 503 `credential_storage_unavailable`.

Session creation decrypts the default of the resolved Harness and freezes it in the Session's encrypted snapshot like any other bundle, so changing or removing the default never reaches existing Sessions. The execution-configuration read shows the frozen safe view with source `deployment`. A missing or invalid provider snapshot fails closed, with no fallback to another model or provider.

`model_configuration_support` derives from the adapter declaration that Core and Runtime share: `protocols` lists the selectable native protocols with the default first, `accepts_harness_config` says whether native parameters are accepted and `token_limits_required` whether provider token limits are required. It describes the build, not a live Runtime or a remote model.

### Deployment default observations

The Harness and default-model reads include nullable `last_used_at`, `last_error_code` and `last_error_at`; PUT resets all three, even for the same bundle.

- Completed root Turns record use only when their Session froze the exact current default revision.
- Failed root Turns record only the native provider codes `authentication_error`, `connection_failed`, `rate_limit_exceeded`, `usage_limit_exceeded`, `server_overloaded`, `server_error`, `resource_not_found`, `request_timeout` and `invalid_request`. Input-policy, Core or Runtime, cancelled and waiting outcomes do not count.
- A success keeps the earlier error; comparing the timestamps is a display convention.
- Errors are throttled for 30 seconds regardless of code, and ordinary successes for 30 seconds; the first success after an error records recovery at once. For an unchanged revision this allows at most three effective writes in any 30-second window of database time.
- Each observation has a one-second budget, including pool and row-lock acquisition, and cannot change the committed Turn. A crash, failure or throttle can omit the latest observation.

These are best-effort Core receipt times after the terminal commit, not provider health or remote completion times. Old, explicit-provider and earlier Sessions cannot update a replacement default. Public Session and Turn fields and retry identity are unaffected. Core has no readiness probe, automatic refresh, observation history or read of credentials or raw errors.

## Acceptance

`TestNativeModelProtocolPublicExecution` (`services/core/internal/store/model_protocol_native_test.go`) with `services/core/tests/official_model_protocol_native.py` runs each Harness against real provider APIs through the pinned official client. It runs when `OAC_TEST_OFFICIAL_SDK_PYTHON`, `OAC_TEST_NATIVE_DAEMON_BIN`, `OAC_TEST_NATIVE_PROOF_DIR` and `OAC_TEST_MODEL_PROTOCOL_OPTIONS` are set; the last names a private model settings file. Never commit those settings or print their values.
