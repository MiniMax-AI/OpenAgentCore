import { describe, expect, it } from "vitest";

import { OpenAIAgentsClient } from "./client";
import type { ModelProviderInput, SavedAgentCore } from "./types";

describe("saved Agent execution defaults", () => {
  it("sends complete replacement inputs and preserves omission versus null", async () => {
    const calls: unknown[] = [];
    const safe: SavedAgentCore = {
      harness: "codex",
      model_provider: {
        protocol: "responses", base_url: "https://model.example/v1", api_key_configured: true,
      },
    };
    const client = new OpenAIAgentsClient({
      token: "tenant-token",
      fetch: (async (_input: RequestInfo | URL, init?: RequestInit) => {
        calls.push(JSON.parse(String(init?.body)));
        return new Response(JSON.stringify({ id: "saved-agent", object: "agent", x_agents_core: safe }), {
          status: 200, headers: { "Content-Type": "application/json" },
        });
      }) as typeof fetch,
    });
    const provider: ModelProviderInput = {
      protocol: "responses", base_url: "https://model.example/v1", api_key: "write-only-fixture",
    };
    const agent = await client.createAgent({ model: "example-model", x_agents_core: { harness: "codex", model_provider: provider } });
    expect(agent.x_agents_core).toEqual(safe);
    expect(JSON.stringify(agent)).not.toContain("write-only-fixture");
    await client.updateAgent(agent.id, { model: "new-model" });
    await client.updateAgent(agent.id, { x_agents_core: { harness: "codex" } });
    await client.updateAgent(agent.id, { x_agents_core: { model_provider: provider } });
    await client.updateAgent(agent.id, { x_agents_core: { model_provider: null } });
    await client.updateAgent(agent.id, { x_agents_core: null });
    expect(calls).toEqual([
      { model: "example-model", x_agents_core: { harness: "codex", model_provider: provider } },
      { model: "new-model" },
      { x_agents_core: { harness: "codex" } },
      { x_agents_core: { model_provider: provider } },
      { x_agents_core: { model_provider: null } },
      { x_agents_core: null },
    ]);
  });
});
