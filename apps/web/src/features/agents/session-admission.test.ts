import { describe, expect, it } from "vitest";

import type { SavedAgent } from "@agents-core-web/agents-client";

import {
  effectiveSessionAgent,
  knownSessionAdmissionBlocker,
  knownSessionAdmissionBlockers,
  sessionAdmissionBlocker,
  sessionEnvironmentAdmissionBlocker,
} from "./session-admission";

const executableAgent: SavedAgent = {
  id: "agent_1",
  object: "agent",
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
  updated_at: 1,
};

const executableMcp = {
  type: "mcp",
  server_label: "docs",
  transport: { type: "http", server_url: "https://mcp.example/tools", headers: {} },
  allowed_tools: null,
  connection_origin: "service",
  credential_id: null,
  request_metadata: {},
  required: false,
};

describe("known Session admission blockers", () => {
  it("resolves whole-field Session overrides without mutating the saved Agent", () => {
    const saved = {
      ...executableAgent,
      instructions: "saved",
      multi_agent: { enabled: true, max_concurrent_subagents: 3 },
      reasoning: { effort: "high" as const },
      service_tier: "priority" as const,
      text: { format: { type: "json_schema" as const, schema: { type: "object" } }, verbosity: "high" as const },
      tools: [{ type: "tool_search" }],
    };
    const effective = effectiveSessionAgent(saved, {
      instructions: null,
      multi_agent: null,
      reasoning: null,
      service_tier: null,
      text: null,
      tools: null,
    });

    expect(effective).toMatchObject({
      instructions: null,
      multi_agent: { enabled: false, max_concurrent_subagents: null },
      reasoning: {},
      service_tier: "auto",
      text: { format: { type: "text" }, verbosity: "medium" },
      tools: [],
    });
    expect(saved.tools).toEqual([{ type: "tool_search" }]);
    expect(knownSessionAdmissionBlocker(effective)).toBeNull();
  });

  it("inherits omitted fields and applies supplied model and executable tools", () => {
    const tools = [{ type: "function" as const, name: "lookup", description: "", parameters: {}, defer_loading: false as const }];
    const effective = effectiveSessionAgent(executableAgent, { model: "provider/override", tools });
    expect(effective.model).toBe("provider/override");
    expect(effective.instructions).toBe(executableAgent.instructions);
    expect(effective.tools).toEqual(tools);
    expect(knownSessionAdmissionBlocker(effective)).toBeNull();
  });

  it("accepts the current safe profile without claiming runtime readiness", () => {
    expect(knownSessionAdmissionBlockers(executableAgent)).toEqual([]);
    expect(knownSessionAdmissionBlocker(executableAgent)).toBeNull();
  });

  it.each([
    [{ reasoning: { effort: "none" as const } }, "explicit reasoning options are saved-only"],
    [{ reasoning: { summary: "auto" as const } }, "explicit reasoning options are saved-only"],
    [{ service_tier: "priority" as const }, "service tier must be auto"],
    [{ text: { format: { type: "json_schema" as const, schema: { type: "object" } }, verbosity: "medium" as const } }, "text format must be text"],
    [{ multi_agent: { enabled: true, max_concurrent_subagents: 2 } }, "multi-agent execution is not supported"],
    [{ tools: [{ type: "tool_search" }] }, "tool_search is saved-only"],
    [{ tools: [{ type: "web_search", mode: "live", context_size: "medium", allowed_domains: null, location: null }] }, "web_search mode must be disabled because Core does not run enabled search"],
    [{ tools: [{ type: "web_search", mode: "cached" }] }, "web_search mode must be disabled because Core does not run enabled search"],
    [{ tools: [{ type: "web_search" }] }, "web_search mode must be disabled because Core does not run enabled search"],
    [{ tools: [{ type: "web_search", mode: "disabled", location: { extra: "x" } }] }, "the saved web_search tool is incomplete or malformed"],
    [{ tools: [{ type: "web_search", mode: "disabled" }, { type: "web_search", mode: "disabled" }] }, "at most one web_search tool can execute"],
    [{ tools: [{ type: "function", name: "later", description: "", parameters: {}, defer_loading: true }] }, "deferred functions are saved-only"],
    [{ tools: [{ type: "function", name: " ", description: "", parameters: {}, defer_loading: false }] }, "function names must be non-empty and at most 512 bytes"],
    [{ tools: [{ type: "function", name: "\u0085", description: "", parameters: {}, defer_loading: false }] }, "function names must be non-empty and at most 512 bytes"],
    [{ tools: [{ type: "function", name: "😀".repeat(129), description: "", parameters: {}, defer_loading: false }] }, "function names must be non-empty and at most 512 bytes"],
    [{ tools: [{ ...executableMcp, credential_id: "credential-id" }] }, "attached MCP credentials are unavailable in this Web Session flow"],
    [{ tools: [{ type: "function" }] }, "the saved function tool is incomplete or malformed"],
    [{ tools: [{ type: "function", name: "run", description: "", parameters: [] }] }, "the saved function tool is incomplete or malformed"],
    [{ tools: [{ type: "function", name: "run", description: "", parameters: {}, defer_loading: "false" }] }, "the saved function tool is incomplete or malformed"],
    [{ tools: [{ type: "function", name: "run", description: "", parameters: {}, extra: true }] }, "the saved function tool is incomplete or malformed"],
    [{ tools: [{ type: "mcp" }] }, "the saved MCP tool is incomplete or malformed"],
    [{ tools: [{ ...executableMcp, transport: { ...executableMcp.transport, server_url: "https://user:secret@mcp.example/tools" } }] }, "the saved MCP tool is incomplete or malformed"],
    [{ tools: [{ ...executableMcp, transport: { ...executableMcp.transport, server_url: " https://mcp.example/tools" } }] }, "the saved MCP tool is incomplete or malformed"],
    [{ tools: [{ ...executableMcp, transport: { ...executableMcp.transport, server_url: "https:\\mcp.example\\tools" } }] }, "the saved MCP tool is incomplete or malformed"],
    [{ tools: [{ ...executableMcp, transport: { ...executableMcp.transport, server_url: "https://@mcp.example/tools" } }] }, "the saved MCP tool is incomplete or malformed"],
    [{ tools: [{ ...executableMcp, transport: { ...executableMcp.transport, server_url: "https://mcp.example/%zz" } }] }, "the saved MCP tool is incomplete or malformed"],
    [{ tools: [{ ...executableMcp, transport: { ...executableMcp.transport, server_url: "https://mcp.example/%" } }] }, "the saved MCP tool is incomplete or malformed"],
    [{ tools: [{ ...executableMcp, transport: { ...executableMcp.transport, server_url: "https://%65xample.com/tools" } }] }, "the saved MCP tool is incomplete or malformed"],
    [{ tools: [{ ...executableMcp, transport: { ...executableMcp.transport, server_url: "https://exa{mple}.com/tools" } }] }, "the saved MCP tool is incomplete or malformed"],
    [{ tools: [{ ...executableMcp, transport: { ...executableMcp.transport, headers: { Authorization: "secret" } } }] }, "the saved MCP tool is incomplete or malformed"],
    [{ tools: [{ ...executableMcp, connection_origin: "browser" }] }, "the saved MCP tool is incomplete or malformed"],
    [{ tools: [{ ...executableMcp, request_metadata: { secret: "value" } }] }, "the saved MCP tool is incomplete or malformed"],
    [{ tools: [{ ...executableMcp, allowed_tools: [""] }] }, "the saved MCP tool is incomplete or malformed"],
    [{ tools: [{ ...executableMcp, required: "false" }] }, "the saved MCP tool is incomplete or malformed"],
  ])("blocks a known unsupported saved configuration", (change, message) => {
    const agent = { ...executableAgent, ...change } as SavedAgent;
    expect(knownSessionAdmissionBlockers(agent)).toContain(message);
  });

  it("admits saved disabled web_search, which Core runs with search off", () => {
    const tools = [{ type: "web_search", mode: "disabled", context_size: "medium", allowed_domains: [], location: { city: "Paris", country: null, region: null, timezone: null } }];
    expect(knownSessionAdmissionBlocker({ ...executableAgent, tools })).toBeNull();
  });

  it("does not turn conditional Codex verbosity support into a deterministic blocker", () => {
    const agent = { ...executableAgent, text: { ...executableAgent.text, verbosity: "high" as const } };
    expect(knownSessionAdmissionBlocker(agent)).toBeNull();
  });

  it("accepts the canonical anonymous service-origin HTTP MCP shape", () => {
    expect(knownSessionAdmissionBlocker({ ...executableAgent, tools: [executableMcp] })).toBeNull();
  });

  it("admits a credentialed MCP only through the complete exact-URL Vault plan", () => {
    const credentialId = "22222222-2222-4222-8222-222222222222";
    const vaultId = "11111111-1111-4111-8111-111111111111";
    const credentialed = { ...executableAgent, tools: [{ ...executableMcp, credential_id: credentialId }] };
    const catalog = {
      vaults: [{ id: vaultId, object: "vault" as const, created_at: 1, name: "Runtime", metadata: {} }],
      credentials: [{
        id: credentialId,
        vault_id: vaultId,
        object: "vault.credential" as const,
        name: "Private MCP",
        auth: { type: "static_bearer" as const, mcp_server_url: executableMcp.transport.server_url },
        created_at: 2,
        updated_at: 2,
      }],
    };
    expect(sessionAdmissionBlocker(credentialed, null)).toContain("not fully loaded");
    expect(sessionAdmissionBlocker(credentialed, catalog)).toBeNull();
    expect(sessionAdmissionBlocker({
      ...credentialed,
      tools: [{ ...credentialed.tools[0], transport: { ...executableMcp.transport, server_url: "https://other.example/tools" } }],
    }, catalog)).toContain("URL-mismatched");
  });

  it("accepts canonical function and MCP variants that Core admits", () => {
    expect(knownSessionAdmissionBlocker({
      ...executableAgent,
      tools: [{ type: "function", name: "lookup", description: "", parameters: {}, defer_loading: false }],
    })).toBeNull();
    expect(knownSessionAdmissionBlocker({
      ...executableAgent,
      tools: [{ ...executableMcp, required: true, transport: { ...executableMcp.transport, server_url: "https://mcp.example/a%2Fb" } }],
    })).toBeNull();
    const { allowed_tools: _allowedTools, credential_id: _credentialId, request_metadata: _requestMetadata, required: _required, ...mcpWithDefaultsOmitted } = executableMcp;
    expect(knownSessionAdmissionBlocker({ ...executableAgent, tools: [mcpWithDefaultsOmitted] })).toBeNull();
  });

  it("blocks managed hosted MCP while retaining Function-only admission", () => {
    expect(sessionEnvironmentAdmissionBlocker({ ...executableAgent, tools: [executableMcp] }, "openai_hosted"))
      .toContain("do not yet support MCP");
    expect(sessionEnvironmentAdmissionBlocker({
      ...executableAgent,
      tools: [{ type: "function", name: "lookup", description: "", parameters: {}, defer_loading: false }],
    }, "openai_hosted")).toBeNull();
    expect(sessionEnvironmentAdmissionBlocker({ ...executableAgent, tools: [executableMcp] }, "none")).toBeNull();
  });

  it("matches Core's case-insensitive URL scheme parsing", () => {
    const uppercaseScheme = {
      ...executableMcp,
      transport: { ...executableMcp.transport, server_url: "HTTPS://mcp.example/tools" },
    };
    expect(knownSessionAdmissionBlocker({ ...executableAgent, tools: [uppercaseScheme] })).toBeNull();
  });

  it("blocks duplicate and over-limit execution tool identifiers", () => {
    const fn = { type: "function", name: "lookup", description: "", parameters: {}, defer_loading: false };
    expect(knownSessionAdmissionBlockers({ ...executableAgent, tools: [fn, fn] })).toContain("function names must be unique");
    expect(knownSessionAdmissionBlockers({
      ...executableAgent,
      tools: Array.from({ length: 65 }, (_, index) => ({ ...fn, name: `lookup_${index}` })),
    })).toContain("at most 64 function tools can execute");
    expect(knownSessionAdmissionBlockers({ ...executableAgent, tools: [executableMcp, executableMcp] })).toContain("MCP server labels must be unique");
  });

  it("reports every deterministic blocker without implying they are runtime-discovered", () => {
    const blockers = knownSessionAdmissionBlockers({
      ...executableAgent,
      model: " ",
      reasoning: { effort: "high", summary: "detailed" },
      service_tier: "flex",
      text: { format: { type: "json_schema", schema: {} }, verbosity: "low" },
    });
    expect(blockers).toEqual([
      "a non-empty model is required",
      "explicit reasoning options are saved-only",
      "service tier must be auto",
      "text format must be text",
    ]);
  });
});
