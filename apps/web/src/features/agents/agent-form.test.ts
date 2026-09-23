import { describe, expect, it } from "vitest";

import { createRequestGate, validateAgentForm, valuesFromAgent } from "./agent-form";

describe("Agent form contract", () => {
  it("uses the current Session-safe defaults and omits implicit reasoning", () => {
    const result = validateAgentForm({
      ...valuesFromAgent(),
      model: "  provider/model  ",
      name: "   ",
      instructions: "",
      metadata: '{"team":"web","owner":"sam"}',
    });

    expect(result).toEqual({
      input: {
        model: "provider/model",
        name: null,
        instructions: null,
        metadata: { team: "web", owner: "sam" },
        service_tier: "auto",
        text: { format: { type: "text" }, verbosity: "medium" },
      },
    });
    expect(result.input).not.toHaveProperty("reasoning");
  });

  it.each([
    ["not json", "Metadata must be valid JSON."],
    ["[]", "Metadata must be a JSON object."],
    ['{"retries":3}', "Every metadata value must be a string."],
    ['{"enabled":true}', "Every metadata value must be a string."],
  ])("rejects unsafe or unsupported metadata %s", (metadata, message) => {
    expect(validateAgentForm({ ...valuesFromAgent(), model: "model", name: "", instructions: "", metadata })).toEqual({
      metadataError: message,
    });
  });

  it("matches Core's Unicode whitespace rule for required models", () => {
    expect(validateAgentForm({ ...valuesFromAgent(), model: "\u0085" })).toEqual({ modelError: "Enter a model ID." });
  });

  it("matches Core's Unicode name and metadata boundaries", () => {
    const base = { ...valuesFromAgent(), model: "model" };
    const sixteen = Object.fromEntries(Array.from({ length: 16 }, (_, index) => [`key-${index}`, "value"]));

    expect(validateAgentForm({ ...base, name: "😀".repeat(128), metadata: JSON.stringify(sixteen) }).input).toBeDefined();
    expect(validateAgentForm({ ...base, name: "😀".repeat(129) })).toEqual({
      nameError: "Name must be at most 128 characters.",
    });
    expect(validateAgentForm({
      ...base,
      metadata: JSON.stringify({ ...sixteen, extra: "value" }),
    })).toEqual({ metadataError: "Agent metadata supports at most 16 pairs." });
    expect(validateAgentForm({
      ...base,
      metadata: JSON.stringify({ ["😀".repeat(64)]: "😀".repeat(512) }),
    }).input).toBeDefined();
    expect(validateAgentForm({
      ...base,
      metadata: JSON.stringify({ ["😀".repeat(65)]: "value" }),
    })).toEqual({ metadataError: "Metadata keys must be at most 64 characters and values at most 512 characters." });
    expect(validateAgentForm({
      ...base,
      metadata: JSON.stringify({ key: "😀".repeat(513) }),
    })).toEqual({ metadataError: "Metadata keys must be at most 64 characters and values at most 512 characters." });
  });

  it.each([
    [{ reasoningEffort: "none" as const }, "Current Core Sessions require both reasoning fields to use Core default."],
    [{ reasoningSummary: "auto" as const }, "Current Core Sessions require both reasoning fields to use Core default."],
    [{ serviceTier: "priority" as const }, "Current Core Sessions support service tier auto only."],
    [{ textFormat: { type: "json_schema" as const, schema: {} } }, "Current Core Sessions support text format only."],
    [{ textVerbosity: "high" as const }, "Low and high verbosity require a discovered compatible Codex model; use medium for this Web flow."],
  ])("fails closed before creating a saved Agent that the Web cannot safely continue", (change, message) => {
    const result = validateAgentForm({ ...valuesFromAgent(), model: "model", ...change });
    expect(result).toEqual({ configurationError: message });
    expect(result.input).toBeUndefined();
  });

  it("round-trips existing nullable values into editable fields without inventing data", () => {
    expect(valuesFromAgent({
      id: "agent_1",
      object: "agent",
      model: "model",
      name: null,
      instructions: null,
      metadata: { scope: "test" },
      multi_agent: { enabled: false, max_concurrent_subagents: null },
      reasoning: {},
      service_tier: "auto",
      text: { format: { type: "text" }, verbosity: "medium" },
      tools: [],
      created_at: 1,
      updated_at: 2,
    })).toEqual({
      name: "",
      harness: "",
      harnessModified: false,
      model: "model",
      instructions: "",
      metadata: '{\n  "scope": "test"\n}',
      reasoningEffort: "",
      reasoningSummary: "",
      serviceTier: "auto",
      textFormat: { type: "text" },
      textVerbosity: "medium",
      tools: [],
      toolsModified: false,
    });
  });

  it("serializes an explicit create harness and preserves omitted update selection", () => {
    const create = validateAgentForm({
      ...valuesFromAgent(),
      harness: "claude_sdk",
      model: "provider/model",
    });
    expect(create.input).toMatchObject({ x_agents_core: { harness: "claude_sdk" } });

    const existing = valuesFromAgent({
      id: "agent_1",
      object: "agent",
      x_agents_core: { harness: "claude_sdk" },
      model: "provider/model",
      name: null,
      instructions: null,
      metadata: {},
      multi_agent: { enabled: false, max_concurrent_subagents: null },
      reasoning: {},
      service_tier: "auto",
      text: { format: { type: "text" }, verbosity: "medium" },
      tools: [],
      created_at: 1,
      updated_at: 2,
    });
    expect(existing.harness).toBe("claude_sdk");
    expect(validateAgentForm(existing, "update").input).not.toHaveProperty("x_agents_core");
    expect(validateAgentForm({ ...existing, harness: "codex", harnessModified: true }, "update").input)
      .toMatchObject({ x_agents_core: { harness: "codex" } });
    expect(validateAgentForm({ ...existing, harness: "", harnessModified: true }, "update").input)
      .toMatchObject({ x_agents_core: null });
  });

  it("serializes only non-deferred Function and anonymous service-origin HTTP MCP profiles", () => {
    const result = validateAgentForm({
      ...valuesFromAgent(),
      model: "provider/model",
      tools: [
        {
          kind: "function",
          name: "lookup_customer",
          description: "Look up a customer.",
          parameters: '{"type":"object","properties":{"id":{"type":"string"}}}',
        },
        {
          kind: "mcp",
          serverLabel: "docs",
          serverUrl: "https://mcp.example/tools",
          allowedToolsMode: "list",
          allowedTools: "search\nread_document",
          allowedToolsValue: [],
          required: true,
        },
      ],
    });

    expect(result.input?.tools).toEqual([
      {
        type: "function",
        name: "lookup_customer",
        description: "Look up a customer.",
        parameters: { type: "object", properties: { id: { type: "string" } } },
        defer_loading: false,
      },
      {
        type: "mcp",
        server_label: "docs",
        transport: { type: "http", server_url: "https://mcp.example/tools" },
        connection_origin: "service",
        allowed_tools: ["search", "read_document"],
        required: true,
      },
    ]);
  });

  it("keeps null or omitted MCP allow-lists distinct from an explicit empty list", () => {
    const base = { ...valuesFromAgent(), model: "provider/model" };
    const all = validateAgentForm({
      ...base,
      tools: [{ kind: "mcp", serverLabel: "all", serverUrl: "https://mcp.example/all", allowedToolsMode: "all", allowedTools: "", allowedToolsValue: null, required: false }],
    });
    const none = validateAgentForm({
      ...base,
      tools: [{ kind: "mcp", serverLabel: "none", serverUrl: "https://mcp.example/none", allowedToolsMode: "list", allowedTools: "", allowedToolsValue: null, required: false }],
    });
    expect(all.input?.tools?.[0]).toMatchObject({ allowed_tools: null });
    expect(none.input?.tools?.[0]).toMatchObject({ allowed_tools: [] });
  });

  it("projects and serializes a Credential only with an exact current Vault catalog match", () => {
    const catalog = {
      vaults: [{ id: "11111111-1111-4111-8111-111111111111", object: "vault" as const, created_at: 1, name: "Runtime", metadata: {} }],
      credentials: [{
        id: "22222222-2222-4222-8222-222222222222",
        vault_id: "11111111-1111-4111-8111-111111111111",
        object: "vault.credential" as const,
        name: "Private MCP",
        auth: { type: "static_bearer" as const, mcp_server_url: "https://mcp.example/private" },
        created_at: 2,
        updated_at: 2,
      }],
    };
    const tool = {
      type: "mcp",
      server_label: "private",
      transport: { type: "http", server_url: "https://mcp.example/private" },
      connection_origin: "service",
      credential_id: "22222222-2222-4222-8222-222222222222",
    };
    const agent = {
      id: "agent", object: "agent" as const, model: "model", name: null, instructions: null, metadata: {},
      multi_agent: { enabled: false, max_concurrent_subagents: null }, reasoning: {}, service_tier: "auto" as const,
      text: { format: { type: "text" as const }, verbosity: "medium" as const }, tools: [tool], created_at: 1, updated_at: 1,
    };
    const values = valuesFromAgent(agent, catalog);
    const credentialedTool = values.tools[0];
    expect(credentialedTool).toMatchObject({ kind: "mcp", credentialId: tool.credential_id, serverUrl: tool.transport.server_url });
    if (!credentialedTool || credentialedTool.kind !== "mcp") {
      throw new Error("Expected the exact catalog match to project an editable MCP tool.");
    }
    expect(validateAgentForm({ ...values, toolsModified: true }, "update", catalog).input?.tools).toEqual([tool]);

    expect(valuesFromAgent(agent, null).tools[0]).toMatchObject({ kind: "read-only" });
    expect(validateAgentForm({
      ...values,
      toolsModified: true,
      tools: [{ ...credentialedTool, serverUrl: "https://mcp.example/other" }],
    }, "update", catalog)).toEqual({ toolsError: "MCP server private must use the selected Credential's exact URL." });
  });

  it.each([
    [{ kind: "function", name: "", description: "", parameters: "{}" } as const, "Every Function needs a non-empty name."],
    [{ kind: "function", name: "lookup", description: "", parameters: "[]" } as const, "Function lookup parameters must be a JSON object schema."],
    [{ kind: "mcp", serverLabel: "docs", serverUrl: "https://user:secret@mcp.example/tools", allowedToolsMode: "all", allowedTools: "", allowedToolsValue: null, required: false } as const, "MCP server docs needs a valid anonymous HTTP(S) URL."],
  ])("rejects an unsafe supported tool draft", (tool, toolsError) => {
    const result = validateAgentForm({ ...valuesFromAgent(), model: "provider/model", tools: [tool] });
    expect(result).toEqual({ toolsError });
  });

  it("enforces Function uniqueness, count, Unicode whitespace, and 512 UTF-8-byte names", () => {
    const baseFunction = { kind: "function" as const, name: "lookup", description: "", parameters: "{}" };
    const validateName = (name: string) => validateAgentForm({
      ...valuesFromAgent(),
      model: "provider/model",
      tools: [{ ...baseFunction, name }],
    });

    expect(validateName("a".repeat(512)).input).toBeDefined();
    expect(validateName("a".repeat(513))).toEqual({ toolsError: "Function names must be at most 512 UTF-8 bytes." });
    expect(validateName("😀".repeat(128)).input).toBeDefined();
    expect(validateName(`${"😀".repeat(128)}a`)).toEqual({ toolsError: "Function names must be at most 512 UTF-8 bytes." });
    expect(validateName("\u0085")).toEqual({ toolsError: "Every Function needs a non-empty name." });
    expect(validateAgentForm({ ...valuesFromAgent(), model: "provider/model", tools: [baseFunction, baseFunction] }))
      .toEqual({ toolsError: "Function names must be unique." });
    expect(validateAgentForm({
      ...valuesFromAgent(),
      model: "provider/model",
      tools: Array.from({ length: 65 }, (_, index) => ({ ...baseFunction, name: `lookup_${index}` })),
    })).toEqual({ toolsError: "At most 64 Functions can be configured." });
  });

  it("omits read-only saved tools for unrelated updates and rejects deliberate Tool changes", () => {
    const opaque = { type: "mcp", credential_id: "vault-bound", headers: { Authorization: "not-rendered" } };
    const agent = {
      id: "agent_1", object: "agent" as const, model: "provider/model", name: null, instructions: null, metadata: {},
      multi_agent: { enabled: false, max_concurrent_subagents: null }, reasoning: {}, service_tier: "auto" as const,
      text: { format: { type: "text" as const }, verbosity: "medium" as const }, tools: [opaque], created_at: 1, updated_at: 1,
    };
    const values = valuesFromAgent(agent);
    expect(values.tools[0]).toMatchObject({ kind: "read-only", label: "Credentialed MCP is unresolved or URL-mismatched in the current Vault catalog" });
    expect(validateAgentForm({ ...values, model: "provider/model" }, "update").input).not.toHaveProperty("tools");

    expect(validateAgentForm({
      ...values,
      model: "provider/model",
      toolsModified: true,
      tools: [...values.tools, { kind: "function", name: "lookup", description: "", parameters: "{}" }],
    }, "update")).toEqual({ toolsError: "Tools cannot be changed while this Agent contains read-only saved tool definitions." });
  });

  it("explicitly clears reasoning defaults during an update instead of preserving stale values", () => {
    const result = validateAgentForm({
      ...valuesFromAgent(),
      model: "provider/model",
      reasoningEffort: "",
      reasoningSummary: "",
    }, "update");

    expect(result.input?.reasoning).toEqual({ effort: null, summary: null });
  });

  it("rejects an older detail response after a newer request or dialog close", () => {
    const gate = createRequestGate();
    const first = gate.begin();
    const second = gate.begin();

    expect(gate.isCurrent(first)).toBe(false);
    expect(gate.isCurrent(second)).toBe(true);

    gate.invalidate();
    expect(gate.isCurrent(second)).toBe(false);
  });
});
