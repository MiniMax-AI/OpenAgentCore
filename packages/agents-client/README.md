# Agents client

This package holds the typed clients that OpenAgentCore code uses to call Core:

- a TypeScript client, `@agents-core-web/agents-client` (`src/index.ts`), for the Agents API (`/v1`) and the Core API (`/core/v1`). The console (`apps/web`) and the example application under `example/` use it; it is a private workspace package;
- a Go client, `v1`, that configures the official openai-go SDK for the Agents API.

[API namespaces and credentials](../../docs/api/README.md) explains which credential each namespace takes.

## TypeScript client

| Class | Calls | Default base URL | Credential option |
| --- | --- | --- | --- |
| `OpenAIAgentsClient` | The Agents API: Agents, Sessions, Items, Turns, input, event streams, Environments and Environment templates, Files, Skills, Vaults | `/v1` | `token`: a Project API key |
| `AdminClient` | The Core API: installation, Projects and keys, summaries, each Project's resources, Session archive, diagnostics, execution configuration, Runtime observations and history, resource owners, audit log, write operations, executor credentials and installation commands, harness default models | `/core/v1` | `adminToken`: the Core key |
| `SandboxAdminClient` | Sandbox administration: deployment, reset, E2B discovery, nodes, allocations, enrollment | `/core/v1/sandbox` | `token`: the Core key |
| `CoreMetricsClient` | `GET /core/v1/metrics` | `/core/v1` | `token`: the Core key |

Every constructor also takes `baseUrl` and `fetch`. A token may be a string or a function that returns one. Without a token the clients send no `Authorization` header: the console constructs `AdminClient`, `SandboxAdminClient` and `CoreMetricsClient` without one, and the console server adds the Core key. The Core API clients send same-origin credentials and refuse redirects; `OpenAIAgentsClient` sends `OpenAI-Beta: agents=v1` on the Agents routes that require it.

Behavior shared by the clients:

- **Strict responses.** Session, history, event, Environment and Core API responses are checked against their pinned shapes before they are returned. A malformed one throws `AgentCoreError` with status 502 and a code such as `invalid_session_resource` or `invalid_admin_response` (`CoreMetricsClient`: status 0, `invalid_response`) instead of passing on a guessed value. `listSessionsTolerant` reports Sessions it cannot recognise in `unrecognized` instead of failing. Agent responses are typed but not checked at run time.
- **Errors.** A non-2xx response throws `AgentCoreError` with `status`, `code`, `param`, `errorType` and, from the Core API, the optional `details` of the [Core error envelope](../../contracts/agents-api/core-errors.md). Invalid caller input throws `TypeError` before any request.
- **No retries or timeouts.** No client retries a request. Pass `signal` to cancel one.
- **Idempotency.** `createSession` takes an idempotency key and generates one when omitted; pass your own to retry a creation safely. `sendMessage`, `submitEvents`, `cancelTurn` and `submitFunctionResult` require a key of at most 128 bytes. `createIdempotencyKey()` makes one.
- **Streams.** `streamEvents` and `createSessionStream` decode the live event stream with `createSSEDecoder` and validate each event. Recover missed events with ordinary reads; the decoder does not resume with `Last-Event-ID`.

### Saved Agent and deployment defaults

A saved Agent can carry a harness, native harness parameters and a complete model provider. Keep the provider key in private application configuration:

```ts
import { OpenAIAgentsClient } from "@agents-core-web/agents-client";

// apiBaseURL is the installation's API base URL, ending in /v1.
const client = new OpenAIAgentsClient({ baseUrl: apiBaseURL, token: projectAPIKey });
const agent = await client.createAgent({
  model: "requested-model",
  x_agents_core: {
    harness: "codex",
    harness_config: { model_reasoning_effort: "high" },
    model_provider: {
      protocol: "responses",
      base_url: modelBaseURL,
      api_key: modelAPIKey,
    },
  },
});

// Later Sessions inherit the saved defaults; saving does not execute anything.
const session = await client.createSession({
  agent_id: agent.id,
  environment: { type: "openai_hosted" },
  input: "Follow the saved Agent instructions.",
});

// Change only the model; the saved harness and provider stay.
await client.updateAgent(agent.id, { model: "another-model" });

// Clear only the provider; the harness stays.
await client.updateAgent(agent.id, { x_agents_core: { model_provider: null } });
```

Reads return `ModelProviderView`, which has `api_key_configured` and never `api_key`; writes take `ModelProviderInput`, so a read cannot be resubmitted as an update. [Model execution](../../contracts/agents-api/model-execution.md#saved-defaults-and-precedence) defines what omission and `null` mean on each field, which provider a Session uses and which protocols each harness accepts. [Harness selection](../../contracts/agents-api/harness-selection.md) defines the `harness` field.

With the Core key, `AdminClient` reads the configuration a Session froze at creation and sets each harness's deployment default:

```ts
import { AdminClient } from "@agents-core-web/agents-client";

// On the Core host; keep the Core key out of application code.
const admin = new AdminClient({ baseUrl: "http://127.0.0.1:8091/core/v1", adminToken: coreKey });

const frozen = await admin.retrieveSessionExecutionConfiguration(projectId, session.id);
console.log(frozen.model.value, frozen.model.source, frozen.harness.value);
if (frozen.model_provider.status === "available") {
  console.log(frozen.model_provider.configuration?.protocol);
}

await admin.setHarnessModelConfiguration("codex", {
  model_provider: { protocol: "responses", base_url: modelBaseURL, api_key: modelAPIKey },
  model: "requested-model",
  harness_config: { model_reasoning_effort: "high" },
});
const defaults = await admin.retrieveHarnessModelConfiguration("codex");
console.log(defaults.model, defaults.harness_config, defaults.model_provider.api_key_configured);
```

[Execution configuration queries](../../contracts/agents-api/execution-configuration.md) and [deployment defaults](../../contracts/agents-api/model-execution.md#deployment-defaults) define these reads and writes.

### Checks

```sh
pnpm --filter @agents-core-web/agents-client typecheck
pnpm --filter @agents-core-web/agents-client test
```

`pnpm test:web` and `make check-web` include them.

## Go client

`v1` configures the [official openai-go SDK](https://github.com/openai/openai-go/tree/v3.61.0), pinned in the root `go.mod`. `New` returns the SDK's Session service and `NewAgents` its complete Agents service. Request types, response parsing, cursor pagination, events and errors stay SDK-owned.

```go
import (
    agentsclient "github.com/MiniMax-AI-Dev/parsar/packages/agents-client/v1"
    "github.com/openai/openai-go/v3"
    "github.com/openai/openai-go/v3/option"
)

sessions, err := agentsclient.New(agentsclient.Config{
    BaseURL: serviceBaseURL, // Includes /v1; use TLS for remote connections.
    APIKey:  projectAPIKey,
})
if err != nil {
    return err
}
session, err := sessions.New(ctx, openai.BetaAgentSessionNewParams{
    Agent: openai.BetaAgentSessionNewParamsAgent{
        Model:        openai.String("requested-model"),
        Instructions: openai.String("Follow the supplied instructions."),
    },
    Environment: openai.EnvironmentParamUnion{
        OfParamNone: &openai.EnvironmentParamNone{},
    },
}, option.WithHeader("Idempotency-Key", operationID))
```

- `BaseURL` must be an absolute HTTP(S) URL without credentials, query or fragment; `APIKey` must be non-empty and contain no whitespace.
- SDK retries are disabled. To retry a creation, send the same request with the same stable, non-secret `Idempotency-Key`; without one, each call creates a new Session.
- Requests honor the caller's context. The default HTTP timeout is 30 seconds; an optional trusted `HTTPClient` sets the transport and timeout. The client ignores its cookie jar and rejects redirects, and reads no `OPENAI_*` environment credentials.
- Per-request SDK options are trusted application code; do not accept them from end users.
- Inspect errors with `errors.As(err, &apiErr)` and `*openai.Error`. They retain the request and response, so log selected status and code fields, not raw errors, request dumps or credentials.

The SDK has more methods than Core supports. The [Agents API contract](../../contracts/agents-api/README.md) lists the implemented routes; other methods receive explicit errors.

`make check-agents-api` runs the Go client's tests. The real-service harness, `services/agents-api/tests/official_client.py`, also runs `TestService` against a Core with fresh Projects and a dedicated PostgreSQL database, and checks the Go-created Sessions through the official Python SDK. It calls no model.
