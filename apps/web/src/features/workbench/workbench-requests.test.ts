import { describe, expect, it } from "vitest";

import type { EnvironmentTemplate, SavedAgent } from "@agents-core-web/agents-client";

import type { VaultCatalog } from "../vaults/vault-catalog";
import {
  buildWorkbenchRequest,
  emptyWorkbenchForm,
  metadataFromRows,
  workbenchCurl,
  workbenchJson,
  WORKBENCH_METADATA,
  type WorkbenchContext,
  type WorkbenchForm,
} from "./workbench-requests";

const KEY = "3f1c9a52-8a8e-4a57-9d51-0f2c7c9a1b11";
const VAULT = "7d232a5a-3005-4374-8745-b94743001111";
const OTHER_VAULT = "86f23cb4-8c53-41f0-8e29-59f2946fb203";
const TEMPLATE = "0caaca4f-f2a1-402a-8015-cc44995241d6";

const agent: SavedAgent = {
  id: "agent_1",
  object: "agent",
  model: "provider/model",
  name: "Support",
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

const credentialedAgent: SavedAgent = {
  ...agent,
  id: "agent_mcp",
  tools: [{
    type: "mcp",
    server_label: "docs",
    transport: { type: "http", server_url: "https://mcp.example/tools" },
    connection_origin: "service",
    credential_id: "cred_1",
  }],
};

const catalog: VaultCatalog = {
  vaults: [
    { id: VAULT, object: "vault", created_at: 1, name: "Docs", metadata: {} },
    { id: OTHER_VAULT, object: "vault", created_at: 1, name: "Other", metadata: {} },
  ],
  credentials: [{
    id: "cred_1",
    vault_id: VAULT,
    name: "docs token",
    object: "vault.credential",
    auth: { type: "static_bearer", mcp_server_url: "https://mcp.example/tools" },
    created_at: 1,
    updated_at: 1,
  }],
};

const template = { id: TEMPLATE, object: "agent.environment.template", name: "Offline", network: { access: "disabled" } } as unknown as EnvironmentTemplate;

const context: WorkbenchContext = {
  agents: [agent, credentialedAgent, { ...agent, id: "agent_tier", service_tier: "priority" }],
  vaultCatalog: catalog,
  templates: [template],
  environments: { self_hosted: true, openai_hosted: true },
};

const base = emptyWorkbenchForm("gpt-test");
const withAgent = (patch: Partial<WorkbenchForm["agent"]>): WorkbenchForm => ({ ...base, agent: { ...base.agent, ...patch } });
const withSession = (patch: Partial<WorkbenchForm["session"]>): WorkbenchForm => ({ ...base, session: { ...base.session, agentId: "agent_1", ...patch } });
const lookup = (patch: Partial<WorkbenchForm["lookup"]>): WorkbenchForm => ({ ...base, lookup: { ...base.lookup, ...patch } });

describe("API workbench: create Agent", () => {
  it("sends the Agent page's Session-safe profile with the workbench tag, in form order", () => {
    const request = buildWorkbenchRequest("agent.create", withAgent({ harness: "codex" }), KEY, context);
    expect(request).toMatchObject({ method: "POST", path: "/agents", beta: true, problem: null });
    expect(request.idempotencyKey).toBeUndefined();
    expect(request.body).toEqual({
      model: "gpt-test",
      name: "Playground Agent",
      instructions: "Be helpful and concise.",
      x_agents_core: { harness: "codex" },
      service_tier: "auto",
      text: { format: { type: "text" }, verbosity: "medium" },
      metadata: WORKBENCH_METADATA,
    });
    expect(Object.keys(request.body!)).toEqual(["model", "name", "instructions", "x_agents_core", "service_tier", "text", "metadata"]);
  });

  it("reports the model and name with the Agent page's rules", () => {
    expect(buildWorkbenchRequest("agent.create", withAgent({ model: " " }), KEY, context).problem?.code).toBe("model");
    expect(buildWorkbenchRequest("agent.create", withAgent({ name: "x".repeat(129) }), KEY, context).problem?.code).toBe("name");
    // An empty name and instructions are sent as null, as the Agent page does.
    expect(buildWorkbenchRequest("agent.create", withAgent({ name: "", instructions: " " }), KEY, context).body).toMatchObject({ name: null, instructions: null });
  });

  it("serializes Function and anonymous HTTP MCP tools, and keeps an invalid one visible", () => {
    const request = buildWorkbenchRequest("agent.create", withAgent({
      tools: [
        { kind: "function", name: "lookup", description: "Find a record", parameters: '{"type":"object"}' },
        { kind: "mcp", serverLabel: "docs", serverUrl: "https://mcp.example/tools", allowedToolsMode: "list", allowedTools: "search\n\nread\n", allowedToolsValue: null, required: true, credentialId: null },
      ],
    }), KEY, context);
    expect(request.problem).toBeNull();
    expect((request.body as { tools: unknown[] }).tools).toEqual([
      { type: "function", name: "lookup", description: "Find a record", parameters: { type: "object" }, defer_loading: false },
      { type: "mcp", server_label: "docs", transport: { type: "http", server_url: "https://mcp.example/tools" }, connection_origin: "service", allowed_tools: ["search", "read"], required: true },
    ]);

    const invalid = buildWorkbenchRequest("agent.create", withAgent({
      tools: [{ kind: "function", name: "lookup", description: "", parameters: "{ not json" }],
    }), KEY, context);
    expect(invalid.problem?.code).toBe("tools");
    expect(invalid.problem?.message).toContain("lookup");
    expect((invalid.body as { tools: Array<{ parameters: unknown }> }).tools[0]?.parameters).toBe("{ not json");
  });

  it("adds metadata rows beside the fixed tag and applies the shared limits", () => {
    const request = buildWorkbenchRequest("agent.create", withAgent({ metadata: [{ key: " team ", value: "ops" }, { key: "", value: "" }] }), KEY, context);
    expect(request.body).toMatchObject({ metadata: { created_by: "console-playground", team: "ops" } });
    expect(buildWorkbenchRequest("agent.create", withAgent({ metadata: [{ key: "", value: "orphan" }] }), KEY, context).problem?.code).toBe("metadataKey");
    expect(buildWorkbenchRequest("agent.create", withAgent({ metadata: [{ key: "created_by", value: "me" }] }), KEY, context).problem?.code).toBe("metadataReserved");
    expect(buildWorkbenchRequest("agent.create", withAgent({ metadata: [{ key: "a", value: "1" }, { key: "a", value: "2" }] }), KEY, context).problem?.code).toBe("metadataDuplicate");
    const sixteen = Array.from({ length: 16 }, (_, index) => ({ key: `k${index}`, value: "v" }));
    expect(buildWorkbenchRequest("agent.create", withAgent({ metadata: sixteen }), KEY, context).problem?.code).toBe("metadata");
    expect(metadataFromRows([])).toEqual({ metadata: { ...WORKBENCH_METADATA }, problem: null });
  });
});

describe("API workbench: create Session", () => {
  it("builds the Session page's payload with an Idempotency-Key", () => {
    const request = buildWorkbenchRequest("session.create", withSession({ title: "Smoke test", metadata: [{ key: "run", value: "1" }] }), KEY, context);
    expect(request).toMatchObject({ method: "POST", path: "/agents/sessions", beta: true, idempotencyKey: KEY, problem: null });
    expect(request.body).toEqual({
      agent_id: "agent_1",
      environment: { type: "none" },
      input: "Hello",
      metadata: { created_by: "console-playground", run: "1", title: "Smoke test" },
      vault_ids: [],
    });
  });

  it("requires a loaded Agent that Core Sessions accept", () => {
    expect(buildWorkbenchRequest("session.create", withSession({ agentId: "" }), KEY, context).problem).toEqual({ code: "agent" });
    const blocked = buildWorkbenchRequest("session.create", withSession({ agentId: "agent_tier" }), KEY, context).problem;
    expect(blocked?.code).toBe("agent");
    expect(blocked?.message).toBeTruthy();
  });

  it("applies the environment, input and metadata rules", () => {
    expect(buildWorkbenchRequest("session.create", withSession({ input: "  " }), KEY, context).problem?.code).toBe("input");
    expect(buildWorkbenchRequest("session.create", withSession({ environment: "self_hosted", workspaceDirectory: "workspace" }), KEY, context).problem?.code).toBe("environment");
    const selfHosted = buildWorkbenchRequest("session.create", withSession({ environment: "self_hosted", input: "" }), KEY, context);
    expect(selfHosted.problem).toBeNull();
    expect(selfHosted.body).toMatchObject({ environment: { type: "self_hosted", workspace_directory: "/workspace", capability_directories: [] } });
    expect(selfHosted.body).not.toHaveProperty("input");
    const disabled = { ...context, environments: { self_hosted: false, openai_hosted: true } };
    expect(buildWorkbenchRequest("session.create", withSession({ environment: "self_hosted" }), KEY, disabled).problem).toEqual({ code: "environment" });
    expect(buildWorkbenchRequest("session.create", withSession({ metadata: [{ key: "title", value: "x" }] }), KEY, context).problem?.code).toBe("metadata");
  });

  it("sends managed options and refuses to widen a Template's network", () => {
    const managed = buildWorkbenchRequest("session.create", withSession({ environment: "openai_hosted", templateId: TEMPLATE, network: "disabled", sandboxNodeId: "node_1" }), KEY, context);
    expect(managed.problem).toBeNull();
    expect(managed.body).toMatchObject({
      environment: { type: "openai_hosted", environment_template_id: TEMPLATE, network: { access: "disabled" } },
      x_agents_core: { sandbox_node_id: "node_1" },
    });
    expect(buildWorkbenchRequest("session.create", withSession({ environment: "openai_hosted", templateId: TEMPLATE, network: "enabled" }), KEY, context).problem?.code).toBe("environment");
    // The node choice only applies to managed sandboxes.
    expect(buildWorkbenchRequest("session.create", withSession({ sandboxNodeId: "node_1" }), KEY, context).body).not.toHaveProperty("x_agents_core");
    // Managed Sessions do not run MCP tools yet.
    expect(buildWorkbenchRequest("session.create", withSession({ agentId: "agent_mcp", environment: "openai_hosted" }), KEY, context).problem?.code).toBe("environment");
  });

  it("attaches chosen Vaults and the Vault a tool Credential requires", () => {
    const request = buildWorkbenchRequest("session.create", withSession({ agentId: "agent_mcp", vaultIds: [OTHER_VAULT] }), KEY, context);
    expect(request.problem).toBeNull();
    expect(request.body).toMatchObject({ vault_ids: [VAULT, OTHER_VAULT].sort() });
    const unloaded = buildWorkbenchRequest("session.create", withSession({ agentId: "agent_mcp" }), KEY, { ...context, vaultCatalog: null });
    expect(unloaded.problem?.code).toBe("agent");
  });
});

describe("API workbench: send message and look up", () => {
  it("sends a message as one Session input event", () => {
    const request = buildWorkbenchRequest("session.message", { ...base, message: { sessionId: "sess/1", text: "Hi" } }, KEY, context);
    expect(request).toMatchObject({ path: "/agents/sessions/sess%2F1/events", idempotencyKey: KEY, problem: null });
    expect(request.body).toEqual({ events: [{ type: "agent.session.input.message", input: [{ role: "user", content: [{ type: "input_text", text: "Hi" }] }] }] });
    expect(buildWorkbenchRequest("session.message", base, KEY, context)).toMatchObject({ path: "/agents/sessions/{session_id}/events", problem: { code: "session" } });
    expect(buildWorkbenchRequest("session.message", { ...base, message: { sessionId: "s", text: " \n" } }, KEY, context).problem?.code).toBe("text");
  });

  it("looks objects up by ID, with parents and client-side ID rules", () => {
    expect(buildWorkbenchRequest("object.retrieve", lookup({ kind: "vault", id: "vlt_1" }), KEY, context)).toMatchObject({ method: "GET", path: "/vaults/vlt_1", beta: true, problem: null });
    expect(buildWorkbenchRequest("object.retrieve", lookup({ kind: "turn", id: "turn_1" }), KEY, context)).toMatchObject({ path: "/agents/sessions/{session_id}/turns/turn_1", problem: { code: "parent" } });
    expect(buildWorkbenchRequest("object.retrieve", lookup({ kind: "credential", id: "cred_1", parentId: VAULT }), KEY, context)).toMatchObject({ path: `/vaults/${VAULT}/credentials/cred_1`, problem: null });
    expect(buildWorkbenchRequest("object.retrieve", lookup({ kind: "execution_configuration", id: "s1" }), KEY, context).path).toBe("/agents/sessions/s1/execution-configuration");
    expect(buildWorkbenchRequest("object.retrieve", lookup({ kind: "file", id: "file_1" }), KEY, context)).toMatchObject({ path: "/files/file_1", beta: false, problem: { code: "fileId" } });
    expect(buildWorkbenchRequest("object.retrieve", lookup({ kind: "file", id: `file-${VAULT}` }), KEY, context).problem).toBeNull();
    expect(buildWorkbenchRequest("object.retrieve", lookup({ kind: "skill", id: "not a skill" }), KEY, context)).toMatchObject({ beta: false, problem: { code: "skillId" } });
    expect(buildWorkbenchRequest("object.retrieve", lookup({ id: " " }), KEY, context).problem?.code).toBe("id");
  });
});

describe("API workbench: curl", () => {
  it("renders the exact body inline with credential placeholders only", () => {
    const request = buildWorkbenchRequest("session.create", withSession({ input: "it's fine" }), KEY, context);
    const curl = workbenchCurl(request, "/v1");
    expect(curl).toContain('curl --request POST "${AGENTS_CORE_BASE_URL}/agents/sessions"');
    expect(curl).toContain('--header "Authorization: Bearer ${AGENTS_CORE_API_KEY}"');
    expect(curl).toContain(`--header "Idempotency-Key: ${KEY}"`);
    // The inline body parses back to exactly what Send transmits.
    const literal = curl.slice(curl.indexOf("--data '") + "--data ".length);
    const unquoted = literal.slice(1, -1).replaceAll(`'"'"'`, "'");
    expect(JSON.parse(unquoted)).toEqual(request.body);
    expect(JSON.parse(workbenchJson(request)!)).toEqual(request.body);
  });

  it("uses a plain base URL but never embeds credentials", () => {
    const request = buildWorkbenchRequest("session.create", withSession({}), KEY, context);
    expect(workbenchCurl(request, "https://core.example/v1/")).toContain("'https://core.example/v1/agents/sessions'");
    expect(workbenchCurl(request, "https://user:secret@core.example/v1")).not.toContain("secret");
    const file = workbenchCurl(buildWorkbenchRequest("object.retrieve", lookup({ kind: "file", id: `file-${VAULT}` }), KEY, context), "/v1");
    expect(file).not.toContain("OpenAI-Beta");
    expect(file).not.toContain("Content-Type");
    expect(file).not.toContain("--data");
  });
});
