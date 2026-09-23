import { describe, expect, it } from "vitest";

import { buildAgentRequestPreview, previewTokenPlaceholder } from "./agent-preview";
import { valuesFromAgent } from "./agent-form";

describe("Agent request preview", () => {
  it("uses only a documented placeholder and a separate JSON body", () => {
    const preview = buildAgentRequestPreview({
      ...valuesFromAgent(),
      model: "provider/model",
      name: "Builder",
    }, "https://core.example/v1");

    expect(preview.curl).toContain("https://core.example/v1/agents");
    expect(preview.curl).toContain(previewTokenPlaceholder);
    expect(preview.curl).toContain("@agent.json");
    expect(preview.curl).not.toContain("live-caller-secret");
    const body = JSON.parse(preview.json);
    expect(body).toMatchObject({
      model: "provider/model",
      service_tier: "auto",
      text: { format: { type: "text" }, verbosity: "medium" },
    });
    expect(body).not.toHaveProperty("reasoning");
  });

  it("shows an explicitly saved-only value instead of silently deleting it", () => {
    const preview = buildAgentRequestPreview({
      ...valuesFromAgent(),
      model: "provider/model",
      reasoningEffort: "high",
    }, "/v1");

    expect(JSON.parse(preview.json)).toMatchObject({ reasoning: { effort: "high" } });
  });

  it("shows the selected Core harness in the request body", () => {
    const preview = buildAgentRequestPreview({
      ...valuesFromAgent(),
      harness: "claude_sdk",
      model: "provider/model",
    }, "/v1");

    expect(JSON.parse(preview.json)).toMatchObject({
      x_agents_core: { harness: "claude_sdk" },
    });
  });

  it("previews an existing Agent update at the exact resource path", () => {
    const preview = buildAgentRequestPreview({
      ...valuesFromAgent(),
      model: "provider/model",
      name: "Existing Agent",
    }, "/v1", "agent/with space");

    expect(preview.curl).toContain("/agents/agent%2Fwith%20space");
    expect(JSON.parse(preview.json)).toMatchObject({
      model: "provider/model",
      name: "Existing Agent",
      reasoning: { effort: null, summary: null },
    });
  });

  it("includes the exact bounded Function and anonymous HTTP MCP request profiles", () => {
    const preview = buildAgentRequestPreview({
      ...valuesFromAgent(),
      model: "provider/model",
      tools: [
        { kind: "function", name: "lookup", description: "Lookup", parameters: '{"type":"object"}' },
        {
          kind: "mcp",
          serverLabel: "docs",
          serverUrl: "https://mcp.example/tools",
          allowedToolsMode: "list",
          allowedTools: "search",
          allowedToolsValue: null,
          required: true,
        },
      ],
    }, "/v1");

    expect(JSON.parse(preview.json).tools).toEqual([
      { type: "function", name: "lookup", description: "Lookup", parameters: { type: "object" }, defer_loading: false },
      {
        type: "mcp",
        server_label: "docs",
        transport: { type: "http", server_url: "https://mcp.example/tools" },
        allowed_tools: ["search"],
        connection_origin: "service",
        required: true,
      },
    ]);
  });

  it("does not echo unsafe URL credentials or query values", () => {
    const preview = buildAgentRequestPreview(
      valuesFromAgent(),
      "https://user:live-caller-secret@core.example/v1?token=live-caller-secret",
    );

    expect(preview.curl).toContain("${AGENTS_CORE_BASE_URL}/agents");
    expect(preview.curl).not.toContain("live-caller-secret");
    expect(preview.curl).not.toContain("user:");
  });

  it("single-quotes a direct Core URL so copied shell commands cannot expand its path", () => {
    const preview = buildAgentRequestPreview(
      valuesFromAgent(),
      "https://core.example/$(id)/a'b",
    );

    expect(preview.curl.split("\n")[0]).toBe(
      `curl --request POST 'https://core.example/$(id)/a'"'"'b/agents' \\`,
    );
  });
});
