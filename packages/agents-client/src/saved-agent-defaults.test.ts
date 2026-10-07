import { describe, expect, it } from "vitest";

import { OpenAIAgentsClient } from "./client";
import type { ModelProviderInput, SavedAgentCore, WebSearchToolInput } from "./types";

describe("saved Agent execution defaults", () => {
  it.each(["anthropic", "responses", "chat_completions"] as const)("sends %s replacement inputs and preserves omission versus null", async (protocol) => {
    const calls: unknown[] = [];
    const safe: SavedAgentCore = {
      harness: "codex",
      harness_config: { model_reasoning_effort: "high" },
      model_provider: {
        protocol, base_url: "https://model.example", api_key_configured: true,
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
      protocol, base_url: "https://model.example", api_key: "write-only-fixture",
    };
    const agent = await client.createAgent({ model: "example-model", x_agents_core: { harness: "codex", harness_config: { model_reasoning_effort: "high" }, model_provider: provider } });
    expect(agent.x_agents_core).toEqual(safe);
    expect(JSON.stringify(agent)).not.toContain("write-only-fixture");
    await client.updateAgent(agent.id, { model: "new-model" });
    await client.updateAgent(agent.id, { x_agents_core: { harness: "codex" } });
    await client.updateAgent(agent.id, { x_agents_core: { model_provider: provider } });
    await client.updateAgent(agent.id, { x_agents_core: { model_provider: null } });
    await client.updateAgent(agent.id, { x_agents_core: { harness_config: {} } });
    await client.updateAgent(agent.id, { x_agents_core: null });
    expect(calls).toEqual([
      { model: "example-model", x_agents_core: { harness: "codex", harness_config: { model_reasoning_effort: "high" }, model_provider: provider } },
      { model: "new-model" },
      { x_agents_core: { harness: "codex" } },
      { x_agents_core: { model_provider: provider } },
      { x_agents_core: { model_provider: null } },
      { x_agents_core: { harness_config: {} } },
      { x_agents_core: null },
    ]);
  });

  it("sends saved web_search modes unchanged", async () => {
    const calls: unknown[] = [];
    const client = new OpenAIAgentsClient({
      token: "tenant-token",
      fetch: (async (_input: RequestInfo | URL, init?: RequestInit) => {
        calls.push(JSON.parse(String(init?.body)));
        return new Response(JSON.stringify({ id: "saved-agent", object: "agent" }), {
          status: 200, headers: { "Content-Type": "application/json" },
        });
      }) as typeof fetch,
    });
    const cached: WebSearchToolInput = {
      type: "web_search", mode: "cached", context_size: "high", allowed_domains: [], location: { country: "FR" },
    };
    await client.createAgent({ model: "example-model", tools: [{ type: "web_search" }] });
    await client.updateAgent("saved-agent", { tools: [{ type: "web_search", mode: null }, cached] });
    expect(calls).toEqual([
      { model: "example-model", tools: [{ type: "web_search" }] },
      { tools: [{ type: "web_search", mode: null }, cached] },
    ]);
  });
});


describe("native model configuration projections", () => {
  it("preserves explicit empty and nested native parameters on saved and inline Agent reads", async () => {
    const { projectAgentSnapshot, projectSavedAgentConfiguration } = await import("./client");
    const agent = {
      id: "test-agent", model: "test-model", name: null, instructions: null, multi_agent: { enabled: false, max_concurrent_subagents: null },
      reasoning: { effort: null, summary: null }, service_tier: "auto", text: { format: { type: "text" }, verbosity: "medium" }, tools: [],
    };
    for (const harness_config of [{}, { effort: "high", thinking: { type: "adaptive" } }]) {
      const value = { ...agent, x_agents_core: { harness_config } };
      expect(projectAgentSnapshot(value).x_agents_core).toEqual({ harness_config });
      expect(projectSavedAgentConfiguration(value).x_agents_core).toEqual({ harness_config });
    }
    for (const harness_config of [null, [], "invalid"]) {
      const value = { ...agent, x_agents_core: { harness_config } };
      expect(() => projectAgentSnapshot(value)).toThrow();
      expect(() => projectSavedAgentConfiguration(value)).toThrow();
    }
  });
});
