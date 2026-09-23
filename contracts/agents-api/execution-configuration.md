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

## Discovery boundary

Provider configuration discovery is not exposed. The Core startup-configuration
extension retains its basic supported/configured deployment snapshot and accepts
no query parameters, including the retired `include=configuration_capabilities`.
Provider inputs are still validated against internal adapter-owned rules and Core
admission policy. Removing discovery does not change the supported inputs or
create/update/execute behavior.

This Session query and the startup view are Core extensions, not OpenAI Agents
API operations. Ordinary Agent and Session operations retain their pinned upstream
contracts. Neither query mutates configuration, rotates keys, migrates Sessions,
exposes a model catalog or accepts arbitrary native options. See
[model-execution.md](model-execution.md) for write and inheritance semantics.
