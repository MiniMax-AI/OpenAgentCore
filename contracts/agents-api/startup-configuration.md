# Core startup configuration extension

`GET /v1/agents/core/startup-configuration` returns a read-only, project-authenticated
snapshot of safe configuration facts established when the Core process starts. It
is a Core extension outside the pinned upstream Agents API.

The response separates:

- `supported`: harness and managed sandbox provider kinds compiled into this Core
  build; and
- `configured`: the default and enabled harnesses, daemon gateway/self-hosted
  composition, selected managed provider and maintenance state, plus whether an
  operator-supplied model endpoint is present for each enabled harness.

Model endpoint reporting is boolean. Core never returns the URL, credentials,
headers, query parameters, raw execution options or their file path. Managed
sandbox output is limited to `docker`, `microsandbox` or no selected provider; it
does not expose installation IDs, socket/runtime paths, image references or
provider-native identifiers.

This snapshot never reads or aggregates daemon heartbeats, Runtime registrations,
Sessions, Environments or allocations. `configured` does not mean reachable,
authenticated, ready, isolated or successfully executed. Those observations belong
to the scoped Session or Environment. A Session may also supply the separate
[write-only model execution extension](model-execution.md); that private input is
not reflected in this process-level endpoint.

The response has a fixed `schema_version`. Clients should reject unknown or
incomplete shapes rather than infer configuration. The endpoint rejects query
parameters, returns `Cache-Control: no-store`, and requires the standard project
bearer key plus `OpenAI-Beta: agents=v1`.
