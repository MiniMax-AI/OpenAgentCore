# Execution configuration queries

These read-only Core extensions describe configuration, not execution health.
They require the existing project bearer authentication and `OpenAI-Beta:
agents=v1`. They never contact a model provider, start a Turn or wake a sandbox.

## Frozen Session selections

`GET /v1/agents/sessions/{session_id}/execution-configuration` returns:

```json
{
  "object": "agent.session.execution_configuration",
  "schema_version": 1,
  "session_id": "013773a9-44b9-4f84-baca-b51c04a01201",
  "model": {"value": "requested-model", "source": "session"},
  "harness": {"value": "codex", "source": "agent"},
  "model_provider": {
    "source": "agent",
    "status": "available",
    "configuration": {
      "protocol": "responses",
      "base_url": "https://model.example/v1",
      "api_key_configured": true
    }
  }
}
```

`source` is `session`, `agent`, `deployment` or `unknown`. Model and harness
sources are independent; a model-only override can retain an Agent's harness and
whole provider bundle. Explicit inline harness selection is `session`; a null
inline Agent extension resets the harness to `deployment` without clearing the
inherited provider. A null Session provider inherits normally. Model is required
inline or inherited from a saved Agent; it has no implicit deployment default.

The provider's `status` describes visibility:

- `available`: a safe Session- or Agent-supplied bundle. Configuration contains
  protocol, endpoint, configured-key flag and optional token limits.
- `redacted`: deployment-owned configuration. Source is `deployment` and
  configuration is null. Project bearer credentials do not grant deployment
  secret access; this API has no endpoint-reveal override.
- `unavailable`: no trustworthy safe provider projection was recorded. Source is
  `unknown` and configuration is null. This does not mean that execution failed.

New public Session creations save a minimal safe projection and provenance in the
same transaction as the Session and its private credential snapshot. Reads select
only that safe row and immutable Session configuration, without decrypting secrets
or re-resolving an Agent or deployment defaults. Agent edits/deletion, deployment
changes, Core restart and suspend/resume cannot change the view. Same-key retries
return the original Session and do not repair or overwrite its provenance.

Historical Sessions without the projection report their persisted model/harness
when present, with `unknown` sources; a missing value is null. Their provider view
is `unavailable`, even if a private credential row exists. No backfill guesses
provenance. Missing, deleted and foreign-tenant Session IDs share the existing
not-found response. Responses are `Cache-Control: no-store`. Keys, ciphertext,
secret references, native headers, query parameters and permissions are excluded.

## Supported provider configuration

`GET /v1/agents/core/startup-configuration?include=configuration_capabilities`
adds a `configuration_capabilities` field to the existing startup view. Without
that exact optional query, the legacy response shape is unchanged, including its
schema version. Duplicate, unknown or malformed startup queries are rejected.
The extension has its own `schema_version: 1`:

- `scope: core_build_provider_configuration` identifies the limited subject.
- `runtime_availability: unknown` explicitly excludes live peer readiness.
- `admission` declares Core's hosted-only credential boundary, HTTPS endpoint
  restrictions and nonnegative ordered token limits.
- `harnesses` is sorted by registered harness name. Each entry contains
  `support` (`supported` or `unknown`), deployment `enabled` and `default` flags,
  plus provider protocol declarations. An unknown declaration has no providers.
- Each provider lists `required_fields` and `positive_fields`. Codex declares
  Responses; Claude SDK and MiniMax Code declare Anthropic. MiniMax Code also
  requires positive `context_window` and `max_output_tokens`.

Adapter-owned declarations in `internal/harnessconfig` have one immutable registry
used by provider validation and discovery. Native adapter registration attaches
the same optional safe provider descriptor to the common Runtime contract. The
separate engine catalog still owns qualification of public operations. Core
composition adds deployment enablement and Core-owned admission restrictions, then projects
only public fields. It does not serialize native capability or operator objects.
Adding a declaration does not authorize a new public operation. Runtime versions
may differ; actual selection/admission still checks the chosen Runtime's features.
No node, binary, endpoint, model or key readiness is implied by support or enablement.

The endpoints do not mutate configuration, rotate keys, migrate Sessions, expose
a model catalog or accept arbitrary native options. See
[model-execution.md](model-execution.md) for write and inheritance semantics.
