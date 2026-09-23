# Saved Agent execution defaults

The TypeScript client accepts a complete provider bundle when creating or updating
a saved Agent. Keep its key in private application configuration:

```ts
import { OpenAIAgentsClient } from "@agents-core-web/agents-client";

const client = new OpenAIAgentsClient({ baseUrl: coreURL, token: tenantToken });
const agent = await client.createAgent({
  model: "requested-model",
  x_agents_core: {
    harness: "codex",
    model_provider: {
      protocol: "responses",
      base_url: modelBaseURL,
      api_key: modelAPIKey,
    },
  },
});

// Future Sessions inherit the saved defaults; saving itself does not execute.
await client.createSession({
  agent_id: agent.id,
  environment: { type: "openai_hosted" },
  input: "Follow the saved Agent instructions.",
});

// Change only the model; the saved harness and provider remain configured.
await client.updateAgent(agent.id, { model: "another-model" });

// Clear only the provider, retaining the harness.
await client.updateAgent(agent.id, { x_agents_core: { model_provider: null } });
```

Reads return `ModelProviderView`, containing safe endpoint/limit fields and
`api_key_configured`, never `api_key`. It is distinct from `ModelProviderInput`:
do not submit a read response as an update. Replacing a provider requires its full
protocol, endpoint and key; MiniMax Code also requires both token limits.

On update, omitting the extension preserves all defaults; omitting either nested
member preserves that member. A null provider clears its saved bundle, while
`x_agents_core: null` clears the extension and its secret. An omitted harness
defers protocol compatibility to Session admission. Existing Sessions retain their
configuration snapshots. Session inline `agent.x_agents_core` remains harness-only;
one-off provider overrides belong in the Session's top-level `x_agents_core`.
