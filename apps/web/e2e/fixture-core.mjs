import http from "node:http";
import { handleSandboxFixture, resetSandboxFixture } from "./fixture-sandbox.mjs";

const host = "127.0.0.1";
const port = Number(process.env.AGENTS_FIXTURE_PORT ?? 18092);
const baseline = 1_789_438_800;
const canonicalEnvironmentUuid = "0f745b0d-b545-49cd-8d7e-4c31c80dc564";
const hostedEnvironmentUuid = "7a263c51-6bf0-4d53-8518-c792eb1f0d21";
const sourceFileUuid = "16e1f26e-8cf6-4272-9c31-d470b08d31af";
const canonicalUuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

function patchItems() {
  const longLine = `+export const longValue = "${"x".repeat(2_000)}";`;
  return [
    { id: "patch_intro", turn_id: "turn_patch_intro", type: "message", status: "completed", role: "user", content: [{ type: "input_text", text: "Review the fixture patch" }] },
    {
      id: "patch_completed", turn_id: "turn_patch_completed", type: "function_call", status: "completed", name: "apply_patch", call_id: "call_completed",
      arguments: { changes: [
        { path: "src/<safe>.ts", kind: { type: "add", move_path: null }, diff: `--- /dev/null\n+++ b/src/<safe>.ts\n@@ -0,0 +1,2 @@\n+<script>alert("safe")</script>\n${longLine}` },
        { path: "src/modify.ts", kind: { type: "update", move_path: null }, diff: "@@ -1 +1 @@\n-old value\n+new value" },
        { path: "src/delete.ts", kind: { type: "delete", move_path: null }, diff: "@@ -1 +0,0 @@\n-removed" },
      ] },
    },
    { id: "patch_output", turn_id: "turn_patch_completed", type: "function_call_output", status: "completed", call_id: "call_completed", output: '{"applied": true}' },
    { id: "patch_running", turn_id: "turn_patch_running", type: "function_call", status: "in_progress", name: "apply_patch", call_id: "call_running", arguments: { changes: [{ path: "src/running.ts", kind: { type: "update" }, diff: "@@ -1 +1 @@\n-wait\n+working" }] } },
    { id: "patch_failed", turn_id: "turn_patch_failed", type: "function_call", status: "failed", name: "apply_patch", call_id: "call_failed", arguments: { changes: [{ path: "src/failed.ts", kind: { type: "delete" }, diff: "@@ -1 +0,0 @@\n-failed" }] } },
    { id: "patch_failed_output", turn_id: "turn_patch_failed", type: "function_call_output", status: "failed", call_id: "call_failed", error: "fixture failure" },
    { id: "patch_alternate", turn_id: "turn_patch_alternate", type: "function_call", status: "in_progress", name: "apply_patch", call_id: "call_alternate", arguments: { patch: "*** Begin Patch\nmalformed alternate shape" } },
  ];
}

function observableTurns() {
  return [
    { id: "turn_queued", agent_id: "agent_a", session_id: "session_snapshot", object: "agent.session.turn", status: "queued", created_at: baseline - 18, started_at: null, completed_at: null, error: null, usage: null },
    { id: "turn_in_progress", agent_id: "agent_a", session_id: "session_snapshot", object: "agent.session.turn", status: "in_progress", created_at: baseline - 17, started_at: baseline - 16, completed_at: null, error: null, usage: null },
    { id: "turn_waiting", agent_id: "agent_a", session_id: "session_snapshot", object: "agent.session.turn", status: "waiting", created_at: baseline - 15, started_at: baseline - 14, completed_at: null, error: null, usage: null },
    { id: "turn_completed", agent_id: "agent_a", session_id: "session_snapshot", object: "agent.session.turn", status: "completed", created_at: baseline - 13, started_at: baseline - 12, completed_at: baseline - 5, error: null, usage: { input_tokens: 10, output_tokens: 3, total_tokens: 13, input_tokens_details: { cached_tokens: 4 }, output_tokens_details: { reasoning_tokens: 2 } } },
    { id: "turn_failed", agent_id: "agent_a", session_id: "session_snapshot", object: "agent.session.turn", status: "failed", created_at: baseline - 4, started_at: baseline - 3, completed_at: baseline - 2, error: { code: "internal_error", message: "The execution could not complete." }, usage: null },
    { id: "turn_cancelled", agent_id: "agent_a", session_id: "session_snapshot", object: "agent.session.turn", status: "cancelled", created_at: baseline - 1, started_at: baseline, completed_at: baseline + 1, error: null, usage: null },
    { id: "turn_terminal_refresh", agent_id: "agent_a", session_id: "session_snapshot", object: "agent.session.turn", status: "in_progress", created_at: baseline + 2, started_at: baseline + 3, completed_at: null, error: null, usage: null },
  ];
}

function observableTurnItems() {
  return [
    { id: "turn_message", turn_id: "turn_completed", type: "message", status: "completed", role: "assistant", content: [{ type: "output_text", text: "Completed Turn output remains in the conversation." }] },
    { id: "failed_input", turn_id: "turn_failed", type: "message", status: "completed", role: "user", content: [{ type: "input_text", text: "Persisted input before the Turn failed." }] },
    { id: "unassociated", turn_id: "turn_not_loaded", type: "message", status: "completed", role: "assistant", content: [{ type: "output_text", text: "This Item is waiting for its Turn page." }] },
  ];
}

function savedAgent(id, name, model, updatedAt) {
  return {
    id,
    object: "agent",
    model,
    name,
    instructions: `Instructions for ${name}`,
    metadata: { team: "web", fixture: "safe" },
    multi_agent: { enabled: id === "agent_a", max_concurrent_subagents: id === "agent_a" ? 2 : null },
    reasoning: id === "agent_a" ? { effort: "high", summary: "concise" } : {},
    service_tier: id === "agent_a" ? "priority" : "auto",
    text: { format: { type: "text" }, verbosity: id === "agent_a" ? "high" : "medium" },
    tools: id === "agent_a" ? [{ type: "tool_search" }] : [],
    created_at: baseline - 600,
    updated_at: updatedAt,
  };
}

function isRecord(value) {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function hasOnlyKeys(value, allowed) {
  return Object.keys(value).every((key) => allowed.includes(key));
}

function isEmptyObject(value) {
  return value == null || isRecord(value) && Object.keys(value).length === 0;
}

function isSafeMcpUrl(value) {
  if (
    typeof value !== "string"
    || /^\p{White_Space}|\p{White_Space}$/u.test(value)
    || /[\u0000-\u0020\u007f\\]/u.test(value)
    || value.includes("?")
    || value.includes("#")
    || /%(?![0-9A-Fa-f]{2})/u.test(value)
  ) return false;
  const schemeSeparator = value.indexOf("://");
  const authority = schemeSeparator >= 0 ? value.slice(schemeSeparator + 3).split("/", 1)[0] : "";
  if (!authority || authority.includes("@") || authority.includes("%") || /[{}\x60]/u.test(authority)) return false;
  try {
    const url = new URL(value);
    return ["http:", "https:"].includes(url.protocol) && Boolean(url.hostname) && !url.username && !url.password;
  } catch {
    return false;
  }
}

function isCanonicalExecutionFunction(tool) {
  return hasOnlyKeys(tool, ["type", "name", "description", "parameters", "defer_loading"])
    && typeof tool.name === "string"
    && typeof tool.description === "string"
    && isRecord(tool.parameters)
    && (tool.defer_loading === undefined || typeof tool.defer_loading === "boolean");
}

function isCanonicalExecutionMcp(tool) {
  const transport = tool.transport;
  const allowedTools = tool.allowed_tools;
  return hasOnlyKeys(tool, [
    "type", "server_label", "transport", "allowed_tools", "connection_origin",
    "credential_id", "request_metadata", "required",
  ])
    && typeof tool.server_label === "string"
    && !/^\p{White_Space}*$/u.test(tool.server_label)
    && isRecord(transport)
    && hasOnlyKeys(transport, ["type", "server_url", "headers"])
    && transport.type === "http"
    && isSafeMcpUrl(transport.server_url)
    && isEmptyObject(transport.headers)
    && tool.connection_origin === "service"
    && (tool.credential_id == null || typeof tool.credential_id === "string")
    && isEmptyObject(tool.request_metadata)
    && (tool.required === undefined || typeof tool.required === "boolean")
    && (allowedTools == null || Array.isArray(allowedTools) && allowedTools.every((name) => typeof name === "string" && name.length > 0));
}

function sessionAdmissionError(agent) {
  if (/^\p{White_Space}*$/u.test(agent.model)) return "Execution currently requires a nonempty model.";
  if (agent.multi_agent.enabled || agent.multi_agent.max_concurrent_subagents !== null) return "Enabled multi_agent execution is not supported by this service yet.";
  if (agent.reasoning.effort != null || agent.reasoning.summary != null) return "Explicit reasoning execution options are not supported by this service yet.";
  if (agent.service_tier !== "auto") return "Execution currently supports service_tier=auto only.";
  if (agent.text.format.type !== "text") return "Execution currently supports text.format.type=text only.";

  const functionNames = new Set();
  const mcpLabels = new Set();
  let functionCount = 0;
  for (const tool of agent.tools) {
    if (tool.type === "function") {
      functionCount += 1;
      if (!isCanonicalExecutionFunction(tool) || /^\p{White_Space}*$/u.test(tool.name) || Buffer.byteLength(tool.name, "utf8") > 512 || functionNames.has(tool.name) || tool.defer_loading === true) {
        return "Invalid execution function fields.";
      }
      functionNames.add(tool.name);
    } else if (tool.type === "mcp") {
      if (!isCanonicalExecutionMcp(tool) || mcpLabels.has(tool.server_label)) {
        return "Invalid execution MCP fields.";
      }
      mcpLabels.add(tool.server_label);
    } else {
      return "Execution currently supports non-deferred functions and the service-origin HTTP MCP profile only.";
    }
  }
  if (functionCount > 64) return "This service supports at most 64 function tools.";
  return null;
}

function sessionVaultError(agent, vaultIds) {
  if (!Array.isArray(vaultIds) || vaultIds.some((id) => typeof id !== "string" || !canonicalUuidPattern.test(id))) {
    return "Fixture Session Vault attachments are invalid.";
  }
  const attached = new Set(vaultIds);
  if (attached.size !== vaultIds.length || [...attached].some((id) => !state.vaults.some((vault) => vault.id === id))) {
    return "Fixture Session Vault attachments are unavailable.";
  }
  for (const tool of agent.tools) {
    if (tool.type !== "mcp") continue;
    const matching = state.credentials.filter((credential) => (
      attached.has(credential.vault_id) && credential.auth.mcp_server_url === tool.transport.server_url
    ));
    if (typeof tool.credential_id === "string") {
      const selected = matching.find((credential) => credential.id === tool.credential_id);
      if (!selected || !state.credentialTokens.has(selected.id)) return "Fixture explicit MCP Credential is unavailable.";
    } else if (matching.length > 1) {
      return "Fixture anonymous MCP selection is ambiguous.";
    }
  }
  return null;
}

function sessionSnapshot(agent) {
  const { object: _object, metadata: _metadata, created_at: _created, updated_at: _updated, ...snapshot } = agent;
  return snapshot;
}

function sessionEffectiveAgent(saved, override) {
  if (override === undefined) return saved;
  if (!isRecord(override) || !hasOnlyKeys(override, [
    "model", "instructions", "multi_agent", "reasoning", "service_tier", "text", "tools",
  ])) return null;
  if (Object.hasOwn(override, "model") && typeof override.model !== "string") return null;
  const effective = { ...saved };
  if (Object.hasOwn(override, "model")) effective.model = override.model;
  if (Object.hasOwn(override, "instructions")) {
    if (override.instructions !== null && typeof override.instructions !== "string") return null;
    effective.instructions = override.instructions;
  }
  if (Object.hasOwn(override, "multi_agent")) {
    if (override.multi_agent === null) effective.multi_agent = { enabled: false, max_concurrent_subagents: null };
    else if (isRecord(override.multi_agent) && typeof override.multi_agent.enabled === "boolean") {
      effective.multi_agent = {
        enabled: override.multi_agent.enabled,
        max_concurrent_subagents: override.multi_agent.enabled
          ? override.multi_agent.max_concurrent_subagents ?? 6
          : null,
      };
    } else return null;
  }
  if (Object.hasOwn(override, "reasoning")) {
    if (override.reasoning !== null && !isRecord(override.reasoning)) return null;
    effective.reasoning = override.reasoning ?? {};
  }
  if (Object.hasOwn(override, "service_tier")) {
    if (override.service_tier !== null && typeof override.service_tier !== "string") return null;
    effective.service_tier = override.service_tier ?? "auto";
  }
  if (Object.hasOwn(override, "text")) {
    if (override.text !== null && !isRecord(override.text)) return null;
    effective.text = {
      format: override.text?.format ?? { type: "text" },
      verbosity: override.text?.verbosity ?? "medium",
    };
  }
  if (Object.hasOwn(override, "tools")) {
    if (override.tools !== null && !Array.isArray(override.tools)) return null;
    effective.tools = override.tools ?? [];
  }
  return effective;
}

function sessionInlineAgent(input, id) {
  if (
    !isRecord(input)
    || !hasOnlyKeys(input, ["model", "instructions", "tools"])
    || typeof input.model !== "string"
    || /^\p{White_Space}*$/u.test(input.model)
    || input.model !== input.model.trim()
    || (Object.hasOwn(input, "instructions")
      && (typeof input.instructions !== "string"
        || /^\p{White_Space}*$/u.test(input.instructions)
        || input.instructions !== input.instructions.trim()))
    || (Object.hasOwn(input, "tools") && !Array.isArray(input.tools))
  ) return null;
  return {
    id,
    model: input.model,
    name: null,
    instructions: input.instructions ?? null,
    multi_agent: { enabled: false, max_concurrent_subagents: null },
    reasoning: {},
    service_tier: "auto",
    text: { format: { type: "text" }, verbosity: "medium" },
    tools: input.tools ?? [],
  };
}

function sessionInitialInputMessages(input) {
  if (typeof input === "string") {
    return /^\p{White_Space}*$/u.test(input)
      ? null
      : [{ type: "message", role: "user", content: [{ type: "input_text", text: input }] }];
  }
  if (!Array.isArray(input) || input.length === 0) return null;

  const messages = [];
  for (const message of input) {
    if (
      !isRecord(message)
      || !hasOnlyKeys(message, ["type", "role", "content"])
      || (message.type !== undefined && message.type !== "message")
      || message.role !== "user"
      || !Array.isArray(message.content)
      || message.content.length === 0
    ) return null;
    const content = [];
    for (const part of message.content) {
      if (
        !isRecord(part)
        || !hasOnlyKeys(part, ["type", "text"])
        || part.type !== "input_text"
        || typeof part.text !== "string"
      ) return null;
      content.push({ type: "input_text", text: part.text });
    }
    if (/^\p{White_Space}*$/u.test(content.map((part) => part.text).join(""))) return null;
    messages.push({ type: "message", role: "user", content });
  }
  return messages;
}

function sessionEnvironmentResponse(environment) {
  if (!isRecord(environment) || typeof environment.type !== "string") return null;
  if (environment.type === "none") {
    return hasOnlyKeys(environment, ["type"]) ? { type: "none" } : null;
  }
  if (environment.type === "openai_hosted") {
    if (!hasOnlyKeys(environment, ["type", "network", "environment_template_id"])) return null;
    let access = "enabled";
    let template = null;
    if (environment.environment_template_id !== undefined) {
      template = state.environmentTemplates.find((entry) => entry.id === environment.environment_template_id);
      // A referenced Template must exist, and an explicit null network is rejected
      // instead of guessing inheritance.
      if (!template || environment.network === null) return null;
      access = template.network.access;
    }
    if (environment.network !== undefined && environment.network !== null) {
      if (
        !isRecord(environment.network) ||
        !hasOnlyKeys(environment.network, ["access"]) ||
        (environment.network.access !== "enabled" && environment.network.access !== "disabled")
      ) return null;
      // A Session keeps or narrows the Template policy and never widens it.
      if (template && template.network.access === "disabled" && environment.network.access === "enabled") return null;
      access = environment.network.access;
    }
    return {
      type: "openai_hosted",
      id: hostedEnvironmentUuid,
      capability_directories: [],
      network: { access, allowed_domains: [] },
      packages: { npm: [], python: [], system: [] },
      files: [],
      plugins: [],
      skills: [],
    };
  }
  if (
    environment.type !== "self_hosted"
    || !hasOnlyKeys(environment, ["type", "workspace_directory", "capability_directories"])
  ) return null;

  const workspace = environment.workspace_directory;
  const capabilities = environment.capability_directories;
  if (
    typeof workspace !== "string"
    || !workspace
    || workspace.startsWith("~")
    || !workspace.startsWith("/")
    || /[\0\r\n\\]/u.test(workspace)
    || capabilities !== undefined && capabilities !== null
      && (!Array.isArray(capabilities) || capabilities.length !== 0)
  ) return null;

  return {
    type: "self_hosted",
    id: canonicalEnvironmentUuid,
    remote_url: "https://executor.example.test",
    workspace_directory: workspace,
    capability_directories: [],
  };
}

function initialState() {
  const first = savedAgent("agent_a", "Lifecycle Agent", "fixture/model-a", baseline - 60);
  const second = savedAgent("agent_b", "Second Agent", "fixture/model-b", baseline - 30);
  const savedOnlyTool = savedAgent("agent_tool_only", "Saved-only Tool Agent", "fixture/model-tool", baseline - 20);
  savedOnlyTool.tools = [{ type: "tool_search" }];
  return {
    agents: [first, second, savedOnlyTool],
    vaults: [],
    credentials: [],
    credentialTokens: new Set(),
    sessions: [{
      id: "session_snapshot",
      object: "agent.session",
      agent: sessionSnapshot(first),
      environment: { type: "none" },
      status: "idle",
      error: null,
      metadata: { fixture: "durable-agent-snapshot" },
      required_actions: [],
      vault_ids: [],
      usage: null,
      created_at: baseline - 20,
      last_active_at: baseline - 10,
    }],
    turns: [],
    createdSessionItems: new Map(),
    requests: [],
    sourceFiles: new Map(),
    hostedWorkspaceFiles: [],
    environmentTemplates: [],
    environmentTemplateSequence: 0,
    sessionCreateReceipts: new Map(),
    controls: {
      createAgentResponseVariant: "valid",
      sessionCreateDelayMs: 0,
      sessionCreateStatus: 201,
      sessionCreateResponseLoss: 0,
      sessionCreateStreamMissingIdentity: 0,
      sessionCreateStreamCloseDelayMs: 120,
      sessionListDelayMs: 0,
      sessionListStatus: 200,
      sessionListPageSize: 100,
      retrieveDelayMs: 0,
      retrieveStatus: 200,
      updateDelayMs: 0,
      updateStatus: 200,
      deleteDelayMs: 0,
      deleteStatus: 200,
      sendStatus: 202,
      sendResponseLoss: 0,
      itemsScenario: 0,
      turnsScenario: 0,
      turnsRetrieveDelayMs: 0,
      turnsRetrieveStatus: 200,
      turnsPageSize: 2,
      environmentScenario: 0,
      environmentRetrieveDelayMs: 0,
      environmentRetrieveStatus: 200,
      environmentFilesDelayMs: 0,
      environmentFilesStatus: 200,
      environmentFileCreateStatus: 200,
      environmentFileCreateResponseLoss: 0,
      environmentTemplateListStatus: 200,
      environmentTemplateListDelayMs: 0,
      environmentTemplatePageSize: 100,
      environmentTemplateCreateStatus: 200,
      environmentTemplateRetrieveStatus: 200,
      environmentTemplateRetrieveDelayMs: 0,
      environmentTemplateUpdateStatus: 200,
      environmentTemplateUpdateDelayMs: 0,
      environmentTemplateDeleteStatus: 200,
      environmentTemplateDeleteDelayMs: 0,
      environmentResourceStatus: "pending",
      environmentResourceVariant: "valid",
      environmentEventStatus: 0,
      environmentEventCount: 0,
      streamStatus: 200,
      streamOpenDelayMs: 0,
      streamCloseCount: 0,
      streamCloseDelayMs: 30,
      sessionRetrieveDelayMs: 0,
      sessionRetrieveStatus: 200,
      sessionRetrieveVariant: "valid",
      sessionUpdateDelayMs: 0,
      sessionUpdateStatus: 200,
      sessionUpdateResponseLoss: 0,
      sessionDeleteDelayMs: 0,
      sessionDeleteStatus: 200,
      sessionDeleteResponseLoss: 0,
      sessionDeleteStreamCloseDelayMs: 0,
      itemsRetrieveDelayMs: 0,
      itemsRetrieveStatus: 200,
      sourceUploadStatus: 200,
      sourceUploadResponseLoss: 0,
      sourceDeleteStatus: 200,
      sourceDeleteResponseLoss: 0,
    },
    aborts: {
      sessionListReads: 0,
      sessionReads: 0,
      itemReads: 0,
      turnReads: 0,
      streams: 0,
      environmentFileReads: 0,
    },
    sequence: 0,
  };
}

function applyTurnsScenario(value) {
  const session = state.sessions[0];
  if (!session) return;
  if (value === 1) {
    state.turns = observableTurns();
    session.usage = {
      input_tokens: 20,
      output_tokens: 6,
      total_tokens: 26,
      input_tokens_details: { cached_tokens: 8 },
      output_tokens_details: { reasoning_tokens: 4 },
    };
    return;
  }
  state.turns = [];
  session.usage = null;
}

function applyEnvironmentScenario(value) {
  const session = state.sessions[0];
  if (!session) return;
  const hostileRemote = "https://launcher:private@executor.example.test/connect?executor_token=secret#credential";
  if (value === 10) {
    session.environment = {
      type: "self_hosted",
      id: "environment_fixture",
      remote_url: hostileRemote,
      workspace_directory: `/workspace/<script>safe</script>/${"long/".repeat(45)}project`,
      capability_directories: ["/capabilities/read-only", `/capabilities/${"wide/".repeat(55)}`],
    };
    session.status = "requires_action";
    session.required_actions = [
      { type: "function_call", call_id: "call_fixture", turn_id: "turn_fixture", name: "confirm", arguments: { safe: true } },
    ];
    return;
  }
  if (value === 9) {
    session.environment = {
      type: "openai_hosted",
      id: hostedEnvironmentUuid,
      capability_directories: [],
      network: { access: "enabled", allowed_domains: [] },
      packages: { npm: [], python: [], system: [] },
      files: [],
      plugins: [],
      skills: [],
    };
    session.status = "failed";
    // A hosted provisioning failure reports the failed step and exit status only.
    session.error = 'Failed to provision environment: script "setup_commands[0]" failed with exit code 3';
    session.required_actions = [];
    return;
  }
  if (value === 8) {
    session.environment = {
      type: "openai_hosted",
      id: hostedEnvironmentUuid,
      capability_directories: [],
      network: { access: "disabled", allowed_domains: [] },
      packages: { npm: [], python: [], system: [] },
      files: [],
      plugins: [],
      skills: [],
    };
    session.status = "idle";
    session.required_actions = [];
    return;
  }
  if (value === 7) {
    session.environment = {
      type: "self_hosted",
      id: canonicalEnvironmentUuid,
      remote_url: "https://executor.example.test",
      workspace_directory: "/executor/workspace",
      capability_directories: [],
    };
    session.status = "idle";
    session.required_actions = [];
    return;
  }
  if (value === 1 || value === 4 || value === 5 || value === 6) {
    session.environment = {
      type: "self_hosted",
      id: value === 5 ? canonicalEnvironmentUuid.toUpperCase() : "environment_fixture",
      remote_url: hostileRemote,
      workspace_directory: `/workspace/<script>safe</script>/${"long/".repeat(45)}project`,
      capability_directories: ["/capabilities/read-only", `/capabilities/${"wide/".repeat(55)}`],
    };
    session.status = value === 1 || value === 6 ? "requires_action" : "idle";
    session.required_actions = value === 1
      ? [{ type: "environment_connection", environment_id: "environment_fixture" }]
      : value === 6
        ? [{ type: "environment_connection", environment_id: "environment_fixture" }]
        : [];
    return;
  }
  if (value === 2) {
    session.environment = { type: "future_remote", remote_url: "javascript:alert(1)", workspace_directory: "/must-not-render" };
    session.status = "idle";
    session.required_actions = [];
    return;
  }
  if (value === 3) {
    session.environment = { type: "self_hosted" };
    session.status = "idle";
    session.required_actions = [];
  }
}

let state = initialState();
const streamResponses = new Map();

function emitTurnLifecycle(status) {
  const index = state.turns.findIndex((turn) => turn.id === "turn_terminal_refresh");
  const existing = state.turns[index];
  if (!existing || !["completed", "failed", "cancelled"].includes(status)) return false;
  const terminal = {
    ...existing,
    status,
    completed_at: baseline + 10,
    error: status === "failed" ? { code: "internal_error", message: "The execution could not complete." } : null,
    usage: status === "completed" ? { input_tokens: 5, output_tokens: 2, total_tokens: 7, input_tokens_details: { cached_tokens: 1 }, output_tokens_details: { reasoning_tokens: 1 } } : null,
  };
  state.turns[index] = terminal;
  state.sequence += 1;
  const event = `id: turn_${state.sequence}\ndata: ${JSON.stringify({
    type: `agent.session.turn.${status}`,
    event_id: `turn_${state.sequence}`,
    session_id: "session_snapshot",
    turn_id: terminal.id,
    turn: terminal,
    usage: terminal.usage,
  })}\n\n`;
  for (const stream of streamResponses.keys()) stream.write(event);
  return true;
}

function emitSessionLifecycle(status) {
  if (!["in_progress", "idle", "failed"].includes(status)) return false;
  const session = state.sessions.find((candidate) => candidate.id === "session_snapshot");
  if (!session) return false;
  session.status = status;
  session.error = status === "failed" ? "The execution could not complete." : null;
  session.required_actions = [];
  state.sequence += 1;
  const event = `id: session_${state.sequence}\ndata: ${JSON.stringify({
    type: `agent.session.${status}`,
    event_id: `session_${state.sequence}`,
    session_id: session.id,
    session,
  })}\n\n`;
  for (const [stream, sessionId] of streamResponses) {
    if (sessionId === session.id) stream.write(event);
  }
  return true;
}

function sendJson(response, value, status = 200) {
  const body = JSON.stringify(value);
  response.writeHead(status, {
    "content-type": "application/json; charset=utf-8",
    "content-length": Buffer.byteLength(body),
    "cache-control": "no-store",
  });
  response.end(body);
}

function sendError(response, status, message, code = "fixture_failure", type = "fixture_error") {
  sendJson(response, {
    error: {
      code,
      type,
      message,
    },
  }, status);
}

async function readJson(request) {
  const chunks = [];
  for await (const chunk of request) chunks.push(chunk);
  if (!chunks.length) return {};
  return JSON.parse(Buffer.concat(chunks).toString("utf8"));
}

async function readBuffer(request) {
  const chunks = [];
  for await (const chunk of request) chunks.push(chunk);
  return Buffer.concat(chunks);
}

async function readSourceMultipart(request) {
  const contentType = request.headers["content-type"] ?? "";
  const match = /boundary=(?:"([^"]+)"|([^;]+))/i.exec(contentType);
  if (!match) return null;
  const boundary = match[1] ?? match[2];
  const raw = (await readBuffer(request)).toString("latin1");
  const parts = raw.split(`--${boundary}`).slice(1, -1);
  const result = { file: null, filename: null, purpose: null };
  for (const rawPart of parts) {
    const part = rawPart.replace(/^\r\n/, "").replace(/\r\n$/, "");
    const separator = part.indexOf("\r\n\r\n");
    if (separator < 0) return null;
    const header = part.slice(0, separator);
    const body = part.slice(separator + 4);
    const name = /\bname="([^"]+)"/i.exec(header)?.[1];
    if (name === "file") {
      if (result.file !== null) return null;
      result.filename = /\bfilename="([^"]*)"/i.exec(header)?.[1] ?? null;
      result.file = Buffer.from(body, "latin1");
    } else if (name === "purpose") {
      if (result.purpose !== null) return null;
      result.purpose = body;
    } else {
      return null;
    }
  }
  return result.file !== null && result.filename && result.purpose === "user_data" ? result : null;
}

function page(data) {
  return {
    object: "list",
    data,
    has_more: false,
    first_id: data[0]?.id ?? null,
    last_id: data.at(-1)?.id ?? null,
  };
}

function queryPage(data, url, maximumPageSize = 100) {
  const ordered = url.searchParams.get("order") === "asc" ? [...data].reverse() : [...data];
  const after = url.searchParams.get("after");
  const start = after ? ordered.findIndex((value) => value.id === after) + 1 : 0;
  if (after && start === 0) return null;
  const requestedLimit = Number(url.searchParams.get("limit") ?? 20);
  const size = Math.max(1, Math.min(requestedLimit, maximumPageSize));
  const values = ordered.slice(start, start + size);
  return {
    object: "list",
    data: values,
    has_more: start + values.length < ordered.length,
    first_id: values[0]?.id ?? null,
    last_id: values.at(-1)?.id ?? null,
  };
}

function recordRequest(request, url, body) {
  let safeBody = body;
  if (
    request.method === "POST" &&
    /^\/v1\/vaults\/[^/]+\/credentials(?:\/[^/]+)?$/u.test(url.pathname) &&
    isRecord(body) && isRecord(body.auth) && Object.hasOwn(body.auth, "token")
  ) {
    const { token: _token, ...safeAuth } = body.auth;
    safeBody = { ...body, auth: { ...safeAuth, token_present: true } };
  }
  state.requests.push({
    method: request.method,
    path: url.pathname,
    query: url.search,
    beta: request.headers["openai-beta"] ?? null,
    authorizationPresent: Boolean(request.headers.authorization),
    idempotencyKeyPresent: Boolean(request.headers["idempotency-key"]),
    idempotencyKey: request.headers["idempotency-key"] ?? null,
    body: safeBody,
  });
}

function fixtureUuid(sequence) {
  return `10000000-0000-4000-8000-${String(sequence).padStart(12, "0")}`;
}

function consumeControl(prefix, successStatus = 200) {
  const delayMs = state.controls[`${prefix}DelayMs`];
  const status = state.controls[`${prefix}Status`];
  state.controls[`${prefix}DelayMs`] = 0;
  state.controls[`${prefix}Status`] = successStatus;
  return { delayMs, status };
}

function wait(milliseconds) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds));
}

function trackAbort(response, key) {
  let finished = false;
  response.once("finish", () => {
    finished = true;
  });
  response.once("close", () => {
    if (!finished) state.aborts[key] += 1;
  });
}

const server = http.createServer(async (request, response) => {
  try {
    const url = new URL(request.url ?? "/", `http://${host}:${port}`);

    if (handleSandboxFixture(request, response, url, sendJson, sendError)) return;

    if (request.method === "GET" && url.pathname === "/__fixture/health") {
      return sendJson(response, { ready: true });
    }
    if (request.method === "POST" && url.pathname === "/__fixture/reset") {
      for (const stream of streamResponses.keys()) stream.end();
      streamResponses.clear();
      state = initialState();
      resetSandboxFixture();
      return sendJson(response, { reset: true });
    }
    if (request.method === "POST" && url.pathname === "/__fixture/control") {
      state.controls = { ...state.controls, ...await readJson(request) };
      applyEnvironmentScenario(state.controls.environmentScenario);
      applyTurnsScenario(state.controls.turnsScenario);
      return sendJson(response, state.controls);
    }
    if (request.method === "POST" && url.pathname === "/__fixture/emit-turn") {
      const input = await readJson(request);
      return emitTurnLifecycle(input.status)
        ? sendJson(response, { emitted: true })
        : sendError(response, 400, "Fixture terminal Turn is unavailable.");
    }
    if (request.method === "POST" && url.pathname === "/__fixture/emit-session") {
      const input = await readJson(request);
      return emitSessionLifecycle(input.status)
        ? sendJson(response, { emitted: true })
        : sendError(response, 400, "Fixture Session lifecycle status is unavailable.");
    }
    if (request.method === "GET" && url.pathname === "/__fixture/requests") {
      return sendJson(response, state.requests);
    }
    if (request.method === "GET" && url.pathname === "/__fixture/state") {
      return sendJson(response, {
        sessions: state.sessions,
        aborts: state.aborts,
        openStreams: [...streamResponses.values()],
      });
    }
    if (request.method === "POST" && url.pathname === "/__fixture/session-metadata") {
      const input = await readJson(request);
      const target = state.sessions.find((session) => session.id === input.id);
      if (!target) return sendError(response, 404, "Fixture Session not found.");
      target.metadata = input.metadata;
      return sendJson(response, target);
    }
    if (request.method === "POST" && url.pathname === "/__fixture/remove-session") {
      const input = await readJson(request);
      const before = state.sessions.length;
      state.sessions = state.sessions.filter((session) => session.id !== input.id);
      return sendJson(response, { removed: state.sessions.length !== before });
    }

    if (request.method === "POST" && url.pathname === "/v1/files") {
      const upload = await readSourceMultipart(request);
      recordRequest(request, url, upload ? {
        filename: upload.filename,
        bytes: upload.file.length,
        purpose: upload.purpose,
      } : { multipart: "invalid" });
      if (request.headers["openai-beta"] != null) return sendError(response, 400, "Source Files do not accept the Agents beta header in this fixture.");
      if (state.controls.sourceUploadStatus !== 200) {
        const status = state.controls.sourceUploadStatus;
        state.controls.sourceUploadStatus = 200;
        return sendError(response, status, "Fixture Source upload failed.");
      }
      if (!upload) return sendError(response, 400, "Fixture Source multipart is invalid.");
      const id = `file-${sourceFileUuid}`;
      const metadata = {
        id,
        object: "file",
        bytes: upload.file.length,
        created_at: baseline,
        filename: upload.filename,
        purpose: "user_data",
        status: "processed",
        expires_at: null,
        status_details: null,
      };
      state.sourceFiles.set(id, { metadata, data: upload.file });
      const lose = state.controls.sourceUploadResponseLoss;
      state.controls.sourceUploadResponseLoss = 0;
      if (lose) {
        response.destroy();
        return;
      }
      return sendJson(response, metadata);
    }

    const sourceContentMatch = url.pathname.match(/^\/v1\/files\/([^/]+)\/content$/);
    if (request.method === "GET" && sourceContentMatch) {
      const id = decodeURIComponent(sourceContentMatch[1]);
      recordRequest(request, url, undefined);
      if (request.headers["openai-beta"] != null) return sendError(response, 400, "Source Files do not accept the Agents beta header in this fixture.");
      const source = state.sourceFiles.get(id);
      if (!source) return sendError(response, 404, "Fixture Source File not found.");
      return sendJson(response, { error: {
        message: "Not allowed to download files of purpose: user_data",
        type: "invalid_request_error", code: null, param: null,
      } }, 400);
    }

    const sourceFileMatch = url.pathname.match(/^\/v1\/files\/([^/]+)$/);
    if (sourceFileMatch && (request.method === "GET" || request.method === "DELETE")) {
      const id = decodeURIComponent(sourceFileMatch[1]);
      recordRequest(request, url, undefined);
      if (request.headers["openai-beta"] != null) return sendError(response, 400, "Source Files do not accept the Agents beta header in this fixture.");
      const source = state.sourceFiles.get(id);
      if (!source) return sendError(response, 404, "Fixture Source File not found.");
      if (request.method === "GET") return sendJson(response, source.metadata);
      if (state.controls.sourceDeleteStatus !== 200) {
        const status = state.controls.sourceDeleteStatus;
        state.controls.sourceDeleteStatus = 200;
        return sendError(response, status, "Fixture Source delete failed.");
      }
      state.sourceFiles.delete(id);
      const lose = state.controls.sourceDeleteResponseLoss;
      state.controls.sourceDeleteResponseLoss = 0;
      if (lose) {
        response.destroy();
        return;
      }
      return sendJson(response, { id, object: "file", deleted: true });
    }

    const body = request.method === "GET" || request.method === "DELETE" ? undefined : await readJson(request);
    recordRequest(request, url, body);

    if (request.method === "GET" && url.pathname === "/v1/agents/core/startup-configuration") {
      if (url.search) return sendError(response, 400, "Fixture startup configuration does not accept query parameters.");
      response.setHeader("cache-control", "no-store");
      return sendJson(response, {
        object: "agents.core.startup_configuration",
        schema_version: 1,
        supported: {
          harnesses: ["claude_sdk", "codex", "mcode"],
          managed_sandbox_providers: ["docker", "microsandbox"],
        },
        configured: {
          default_harness: "codex",
          enabled_harnesses: ["claude_sdk", "codex"],
          daemon_gateway: true,
          self_hosted: true,
          managed_sandbox: { enabled: true, provider: "docker", maintenance: false },
          model_providers: [
            { harness: "claude_sdk", endpoint_configured: false },
            { harness: "codex", endpoint_configured: true },
          ],
        },
      });
    }

    if (url.pathname === "/v1/vaults") {
      if (request.method === "GET") return sendJson(response, page(state.vaults));
      if (request.method === "POST") {
        if (!isRecord(body) || typeof body.name !== "string" || !body.name.trim() || !isRecord(body.metadata ?? {})) {
          return sendError(response, 400, "Fixture Vault fields are invalid.");
        }
        state.sequence += 1;
        const vault = {
          id: fixtureUuid(state.sequence),
          object: "vault",
          created_at: baseline + state.sequence,
          name: body.name,
          metadata: body.metadata ?? {},
        };
        state.vaults.unshift(vault);
        return sendJson(response, vault, 201);
      }
    }

    const credentialsMatch = url.pathname.match(/^\/v1\/vaults\/([^/]+)\/credentials$/u);
    if (credentialsMatch) {
      const vaultId = decodeURIComponent(credentialsMatch[1]);
      if (!state.vaults.some((vault) => vault.id === vaultId)) return sendError(response, 404, "Fixture Vault not found.");
      if (request.method === "GET") {
        return sendJson(response, page(state.credentials.filter((credential) => credential.vault_id === vaultId)));
      }
      if (request.method === "POST") {
        if (
          !isRecord(body) || typeof body.name !== "string" || !body.name.trim() ||
          !isRecord(body.auth) || body.auth.type !== "static_bearer" ||
          typeof body.auth.mcp_server_url !== "string" || !body.auth.mcp_server_url.startsWith("https://") ||
          typeof body.auth.token !== "string"
        ) return sendError(response, 400, "Fixture Credential fields are invalid.");
        state.sequence += 1;
        const credential = {
          id: fixtureUuid(state.sequence),
          vault_id: vaultId,
          name: body.name,
          object: "vault.credential",
          auth: { type: "static_bearer", mcp_server_url: body.auth.mcp_server_url },
          created_at: baseline + state.sequence,
          updated_at: baseline + state.sequence,
        };
        state.credentials.unshift(credential);
        state.credentialTokens.add(credential.id);
        return sendJson(response, credential, 201);
      }
    }

    const credentialMatch = url.pathname.match(/^\/v1\/vaults\/([^/]+)\/credentials\/([^/]+)$/u);
    if (credentialMatch) {
      const vaultId = decodeURIComponent(credentialMatch[1]);
      const credentialId = decodeURIComponent(credentialMatch[2]);
      const credential = state.credentials.find((candidate) => candidate.vault_id === vaultId && candidate.id === credentialId);
      if (!credential) return sendError(response, 404, "Fixture Credential not found.");
      if (request.method === "GET") return sendJson(response, credential);
      if (request.method === "POST") {
        if (!isRecord(body) || !isRecord(body.auth) || body.auth.type !== "static_bearer" || typeof body.auth.token !== "string") {
          return sendError(response, 400, "Fixture Credential replacement is invalid.");
        }
        credential.updated_at += 1;
        state.credentialTokens.add(credential.id);
        return sendJson(response, credential);
      }
      if (request.method === "DELETE") {
        state.credentials = state.credentials.filter((candidate) => candidate.id !== credentialId);
        state.credentialTokens.delete(credentialId);
        return sendJson(response, { id: credentialId, object: "vault.credential.deleted", deleted: true });
      }
    }

    const vaultMatch = url.pathname.match(/^\/v1\/vaults\/([^/]+)$/u);
    if (vaultMatch) {
      const vaultId = decodeURIComponent(vaultMatch[1]);
      const vault = state.vaults.find((candidate) => candidate.id === vaultId);
      if (!vault) return sendError(response, 404, "Fixture Vault not found.");
      if (request.method === "GET") return sendJson(response, vault);
      if (request.method === "DELETE") {
        const deletedCredentialIds = state.credentials.filter((credential) => credential.vault_id === vaultId).map((credential) => credential.id);
        state.vaults = state.vaults.filter((candidate) => candidate.id !== vaultId);
        state.credentials = state.credentials.filter((credential) => credential.vault_id !== vaultId);
        for (const credentialId of deletedCredentialIds) state.credentialTokens.delete(credentialId);
        return sendJson(response, { id: vaultId, object: "vault.deleted", deleted: true });
      }
    }

    if (request.method === "GET" && url.pathname === "/v1/agents") {
      const listed = state.agents.map((agent, index) => index === 0
        ? { ...agent, name: `${agent.name} · stale list`, updated_at: agent.updated_at - 10 }
        : agent);
      return sendJson(response, page(listed));
    }

    if (request.method === "POST" && url.pathname === "/v1/agents") {
      state.sequence += 1;
      const defaults = savedAgent(`agent_created_${state.sequence}`, body.name ?? null, body.model, baseline + state.sequence);
      const created = {
        ...defaults,
        ...(body.x_agents_core === undefined ? {} : { x_agents_core: body.x_agents_core }),
        instructions: body.instructions ?? null,
        metadata: body.metadata ?? {},
        multi_agent: body.multi_agent ?? { enabled: false, max_concurrent_subagents: null },
        reasoning: body.reasoning ?? {},
        service_tier: body.service_tier ?? "auto",
        text: {
          format: body.text?.format ?? { type: "text" },
          verbosity: body.text?.verbosity ?? "medium",
        },
        tools: body.tools ?? [],
      };
      if (state.controls.createAgentResponseVariant === "reasoning") {
        created.reasoning = { effort: "high" };
        state.controls.createAgentResponseVariant = "valid";
      }
      state.agents.unshift(created);
      return sendJson(response, created, 201);
    }

    // The legacy Web fixture uses human-readable Session IDs for interaction
    // assertions. Runtime observation resources require canonical UUIDs, so this
    // fixture advertises an empty, valid collection instead of inventing a false
    // identity join. Positive Runtime rendering is covered by the typed component
    // and coordinator tests with canonical identities.
    if (request.method === "GET" && url.pathname === "/v1/agents/runtime-observations") {
      return sendJson(response, page([]));
    }

    if (request.method === "GET" && url.pathname === "/v1/agents/sessions") {
      trackAbort(response, "sessionListReads");
      const control = consumeControl("sessionList");
      if (control.delayMs) await wait(control.delayMs);
      if (control.status !== 200) return sendError(response, control.status, "Fixture Session list failed.");
      const agentId = url.searchParams.get("agent_id");
      const filtered = agentId === null
        ? state.sessions
        : state.sessions.filter((session) => session.agent.id === agentId);
      const listed = queryPage(filtered, url, state.controls.sessionListPageSize);
      return listed
        ? sendJson(response, listed)
        : sendError(response, 400, "Fixture Session cursor is outside the selected Agent filter.");
    }

    if (request.method === "POST" && url.pathname === "/v1/agents/sessions") {
      const hasInitialInput = body.input !== undefined && body.input !== null;
      const initialInputMessages = hasInitialInput ? sessionInitialInputMessages(body.input) : [];
      const requiresInitialInput = body.environment?.type === "none"
        || (body.stream === true && body.environment?.type !== "self_hosted");
      if ((requiresInitialInput && !hasInitialInput) || (hasInitialInput && !initialInputMessages)) {
        return sendError(response, 400, "Fixture Session requires valid initial input for this Environment and response mode.");
      }
      const idempotencyKey = request.headers["idempotency-key"];
      const { stream: _streamResponseMode, ...creationIntent } = body;
      const fingerprint = JSON.stringify(creationIntent);
      const receipt = typeof idempotencyKey === "string"
        ? state.sessionCreateReceipts.get(idempotencyKey)
        : undefined;
      if (receipt) {
        if (receipt.fingerprint !== fingerprint) {
          return sendError(response, 409, "Fixture idempotency key was reused with a different Session request.");
        }
        if (body.stream === true) {
          response.writeHead(201, {
            "content-type": "text/event-stream; charset=utf-8",
            "cache-control": "no-cache, no-transform",
            connection: "keep-alive",
          });
          // Like Core, a same-key stream retry sends no events and ends at once;
          // clients recover the Session with stream=false.
          response.end(": connected\n\n");
          return;
        }
        return sendJson(response, receipt.session, 201);
      }

      const control = consumeControl("sessionCreate", 201);
      const responseLoss = state.controls.sessionCreateResponseLoss;
      state.controls.sessionCreateResponseLoss = 0;
      const missingIdentity = state.controls.sessionCreateStreamMissingIdentity;
      state.controls.sessionCreateStreamMissingIdentity = 0;
      if (control.delayMs) await wait(control.delayMs);
      if (control.status !== 201) return sendError(response, control.status, "Fixture Session create failed.");
      const savedAgent = typeof body.agent_id === "string"
        ? state.agents.find((candidate) => candidate.id === body.agent_id)
        : undefined;
      const agent = body.agent_id === undefined
        ? sessionInlineAgent(body.agent, `inline_agent_${state.sequence + 1}`)
        : savedAgent
          ? sessionEffectiveAgent(savedAgent, body.agent)
          : null;
      if (body.agent_id !== undefined && !savedAgent) {
        return sendError(response, 404, "Fixture Agent not found for Session.");
      }
      if (!agent) return sendError(response, 400, "Fixture Session Agent override is invalid.");
      const admissionError = sessionAdmissionError(agent);
      if (admissionError) return sendError(response, 400, admissionError);
      const vaultError = sessionVaultError(agent, body.vault_ids ?? []);
      if (vaultError) return sendError(response, 400, vaultError);
      const environment = sessionEnvironmentResponse(body.environment);
      if (!environment) return sendError(response, 400, "Fixture Session environment is unsupported.");
      if (environment.type === "openai_hosted" && agent.tools.some((tool) => tool.type === "mcp")) {
        return sendError(response, 400, "Fixture managed hosted MCP combination is not qualified.");
      }
      if (
        body.metadata !== undefined &&
        (!isRecord(body.metadata) || Object.entries(body.metadata).some(([key, value]) => (
          typeof value !== "string" || [...key].length > 64 || [...value].length > 512
        )) || Object.keys(body.metadata).length > 16)
      ) return sendError(response, 400, "Fixture Session metadata is invalid.");
      state.sequence += 1;
      const created = {
        id: `session_created_${state.sequence}`,
        object: "agent.session",
        agent: sessionSnapshot(agent),
        environment,
        status: "idle",
        error: null,
        metadata: body.metadata ?? {},
        required_actions: [],
        vault_ids: body.vault_ids ?? [],
        usage: null,
        created_at: baseline + state.sequence,
        last_active_at: baseline + state.sequence,
      };
      let initialTurn = null;
      let initialItems = [];
      if (hasInitialInput && initialInputMessages) {
        state.sequence += 1;
        const turn = {
          id: `turn_created_${state.sequence}`,
          agent_id: created.agent.id,
          session_id: created.id,
          object: "agent.session.turn",
          status: "queued",
          created_at: baseline + state.sequence,
          started_at: null,
          completed_at: null,
          error: null,
          usage: null,
        };
        const items = initialInputMessages.map((message, index) => ({
          id: `item_created_${state.sequence}_${index + 1}`,
          turn_id: turn.id,
          type: "message",
          status: "completed",
          role: "user",
          content: message.content,
        }));
        state.turns.push(turn);
        state.createdSessionItems.set(created.id, items);
        initialTurn = turn;
        initialItems = items;
        created.status = "in_progress";
        created.last_active_at = baseline + state.sequence;
      }
      // As in Core, the created event repeats this fixture's JSON 201 body. The
      // fixture queues a Turn for any initial input, so both show in_progress;
      // Core instead shows requires_action for self_hosted and idle while an
      // openai_hosted Environment provisions.
      const createdSnapshot = structuredClone(created);
      state.sessions.unshift(created);
      if (typeof idempotencyKey === "string") {
        state.sessionCreateReceipts.set(idempotencyKey, { fingerprint, session: created });
      }
      if (responseLoss) {
        response.destroy();
        return;
      }
      if (body.stream === true) {
        response.writeHead(201, {
          "content-type": "text/event-stream; charset=utf-8",
          "cache-control": "no-cache, no-transform",
          connection: "keep-alive",
        });
        response.write(": connected\n\n");
        if (missingIdentity) {
          response.end();
          return;
        }
        state.sequence += 1;
        response.write(`event: agent.session.created\nid: create_${state.sequence}\ndata: ${JSON.stringify({
          type: "agent.session.created",
          event_id: `create_${state.sequence}`,
          session: createdSnapshot,
        })}\n\n`);
        if (initialTurn) {
          const turn = initialTurn;
          const items = initialItems;
          response.write(`event: agent.session.turn.created\nid: turn_${state.sequence}\ndata: ${JSON.stringify({
            type: "agent.session.turn.created",
            event_id: `turn_${state.sequence}`,
            session_id: created.id,
            turn_id: turn.id,
            turn,
          })}\n\n`);
          for (const [index, item] of items.entries()) {
            response.write(`event: agent.session.turn.item.added\nid: item_${state.sequence}_${index + 1}\ndata: ${JSON.stringify({
              type: "agent.session.turn.item.added",
              event_id: `item_${state.sequence}_${index + 1}`,
              session_id: created.id,
              turn_id: turn.id,
              item,
            })}\n\n`);
          }
          response.write(`event: agent.session.in_progress\nid: progress_${state.sequence}\ndata: ${JSON.stringify({
            type: "agent.session.in_progress",
            event_id: `progress_${state.sequence}`,
            session_id: created.id,
            session: created,
          })}\n\n`);
        }
        setTimeout(() => response.end(), state.controls.sessionCreateStreamCloseDelayMs);
        return;
      }
      return sendJson(response, created, 201);
    }

    const agentMatch = url.pathname.match(/^\/v1\/agents\/([^/]+)$/);
    if (agentMatch) {
      const id = decodeURIComponent(agentMatch[1]);
      const agent = state.agents.find((candidate) => candidate.id === id);
      if (!agent) return sendError(response, 404, "Fixture Agent not found.");

      if (request.method === "GET") {
        const control = consumeControl("retrieve");
        if (control.delayMs) await wait(control.delayMs);
        if (control.status !== 200) return sendError(response, control.status, "Fixture retrieve failed.");
        return sendJson(response, agent);
      }

      if (request.method === "POST") {
        const control = consumeControl("update");
        if (control.delayMs) await wait(control.delayMs);
        if (control.status !== 200) return sendError(response, control.status, "Fixture update failed.");
        const updated = { ...agent, ...body, updated_at: agent.updated_at + 100 };
        state.agents = state.agents.map((candidate) => candidate.id === id ? updated : candidate);
        return sendJson(response, updated);
      }

      if (request.method === "DELETE") {
        const control = consumeControl("delete");
        if (control.delayMs) await wait(control.delayMs);
        if (control.status !== 200) return sendError(response, control.status, "Fixture delete failed.");
        state.agents = state.agents.filter((candidate) => candidate.id !== id);
        return sendJson(response, { id, object: "agent.deleted", deleted: true });
      }
    }

    const sessionMatch = url.pathname.match(/^\/v1\/agents\/sessions\/([^/]+)$/);
    if (sessionMatch) {
      const id = decodeURIComponent(sessionMatch[1]);
      const session = state.sessions.find((candidate) => candidate.id === id);
      if (!session) return sendError(response, 404, "Fixture Session not found.");

      if (request.method === "GET") {
        trackAbort(response, "sessionReads");
        const delayMs = state.controls.sessionRetrieveDelayMs;
        const status = state.controls.sessionRetrieveStatus;
        const variant = state.controls.sessionRetrieveVariant;
        state.controls.sessionRetrieveDelayMs = 0;
        state.controls.sessionRetrieveStatus = 200;
        state.controls.sessionRetrieveVariant = "valid";
        const retrievedSession = variant === "wrong_id"
          ? { ...session, id: "another_session" }
          : variant === "malformed"
            ? { id, object: "agent.session", metadata: session.metadata }
            : variant === "deep_malformed"
              ? { ...session, agent: { model: session.agent.model } }
              : session;
        if (delayMs && status === 200) {
          const payload = JSON.stringify(retrievedSession);
          response.writeHead(200, {
            "content-type": "application/json; charset=utf-8",
            "content-length": Buffer.byteLength(payload) + 1,
            "cache-control": "no-store",
          });
          response.write(" ");
          await wait(delayMs);
          if (response.destroyed) return;
          response.end(payload);
          return;
        }
        if (delayMs) await wait(delayMs);
        if (response.destroyed) return;
        if (status !== 200) return sendError(response, status, "Fixture Session retrieve failed.");
        return sendJson(response, retrievedSession);
      }

      if (request.method === "POST") {
        const delayMs = state.controls.sessionUpdateDelayMs;
        const status = state.controls.sessionUpdateStatus;
        const responseLoss = state.controls.sessionUpdateResponseLoss;
        state.controls.sessionUpdateDelayMs = 0;
        state.controls.sessionUpdateStatus = 200;
        state.controls.sessionUpdateResponseLoss = 0;
        if (delayMs) await wait(delayMs);
        if (status !== 200) return sendError(response, status, "Fixture Session update failed.");
        session.metadata = body.metadata ?? session.metadata;
        if (responseLoss) {
          response.destroy();
          return;
        }
        return sendJson(response, session);
      }

      if (request.method === "DELETE") {
        const delayMs = state.controls.sessionDeleteDelayMs;
        const status = state.controls.sessionDeleteStatus;
        const responseLoss = state.controls.sessionDeleteResponseLoss;
        state.controls.sessionDeleteDelayMs = 0;
        state.controls.sessionDeleteStatus = 200;
        state.controls.sessionDeleteResponseLoss = 0;
        if (delayMs) await wait(delayMs);
        if (status !== 200) return sendError(response, status, "Fixture Session delete failed.");
        if (responseLoss === 2) {
          response.destroy();
          return;
        }
        state.sessions = state.sessions.filter((candidate) => candidate.id !== id);
        state.turns = state.turns.filter((turn) => turn.session_id !== id);
        const targetStreams = [...streamResponses]
          .filter(([, streamSessionId]) => streamSessionId === id)
          .map(([stream]) => stream);
        const closeStreams = () => {
          for (const stream of targetStreams) {
            if (!stream.destroyed) stream.end();
          }
        };
        if (state.controls.sessionDeleteStreamCloseDelayMs) {
          setTimeout(closeStreams, state.controls.sessionDeleteStreamCloseDelayMs);
        } else {
          closeStreams();
        }
        if (responseLoss) {
          response.destroy();
          return;
        }
        return sendJson(response, { id, object: "agent.session.deleted", deleted: true });
      }
    }

    // Declared before the single-Environment route so that the reusable
    // configuration collection is never read as an Environment identifier.
    if (url.pathname === "/v1/agents/environments/templates") {
      if (request.headers["openai-beta"] !== "agents=v1") {
        return sendError(response, 400, "Fixture Environment Templates require the Agents beta header.");
      }
      if (request.method === "GET") {
        const status = state.controls.environmentTemplateListStatus;
        if (status !== 200) {
          return sendError(
            response,
            status,
            status === 400 ? "Environment retrieval does not accept query parameters." : "Fixture Environment Template list failed.",
            status === 400 ? "unsupported_parameter" : "fixture_failure",
            status === 400 ? "invalid_request_error" : "fixture_error",
          );
        }
        const limit = Number(url.searchParams.get("limit") ?? 20);
        const order = url.searchParams.get("order") ?? "desc";
        if (limit < 1 || limit > 100 || !["asc", "desc"].includes(order)) {
          return sendError(response, 400, "Fixture Environment Template query is invalid.");
        }
        const templates = [...state.environmentTemplates]
          .sort((left, right) => left.created_at - right.created_at || left.id.localeCompare(right.id));
        if (order === "desc") templates.reverse();
        const after = url.searchParams.get("after");
        const before = url.searchParams.get("before");
        const start = after ? templates.findIndex((template) => template.id === after) + 1 : 0;
        const end = before ? templates.findIndex((template) => template.id === before) : templates.length;
        if ((after && start === 0) || end < 0) return sendError(response, 404, "Fixture Template cursor not found.");
        const page = templates.slice(start, Math.min(end, start + Math.min(limit, state.controls.environmentTemplatePageSize)));
        const delayMs = state.controls.environmentTemplateListDelayMs;
        state.controls.environmentTemplateListDelayMs = 0;
        if (delayMs) await wait(delayMs);
        if (response.destroyed) return;
        return sendJson(response, {
          object: "list",
          data: page,
          has_more: start + page.length < end,
          first_id: page[0]?.id ?? null,
          last_id: page.at(-1)?.id ?? null,
        });
      }
      if (request.method === "POST") {
        const status = state.controls.environmentTemplateCreateStatus;
        if (status !== 200) {
          state.controls.environmentTemplateCreateStatus = 200;
          return sendError(response, status, "Fixture Environment Template create failed.");
        }
        if (!isRecord(body) || !hasOnlyKeys(body, ["name", "network"])) {
          return sendError(response, 400, "Fixture Environment Template body is invalid.");
        }
        let access = "enabled";
        if (body.network !== undefined && body.network !== null) {
          if (
            !isRecord(body.network) || !hasOnlyKeys(body.network, ["access"]) ||
            (body.network.access !== "enabled" && body.network.access !== "disabled")
          ) return sendError(response, 400, "Fixture Environment Template network is unsupported.");
          access = body.network.access;
        }
        if (body.name !== undefined && body.name !== null && typeof body.name !== "string") {
          return sendError(response, 400, "Fixture Environment Template name is invalid.");
        }
        const index = ++state.environmentTemplateSequence;
        const created = Math.floor(Date.now() / 1000);
        const template = {
          id: `4${String(index).padStart(7, "0")}-1111-4111-8111-111111111111`,
          object: "agent.environment.template",
          name: body.name === undefined ? null : body.name,
          network: { access, allowed_domains: [] },
          capability_directories: [],
          packages: { npm: [], python: [], system: [] },
          files: [],
          plugins: [],
          skills: [],
          created_at: created,
          updated_at: created,
        };
        state.environmentTemplates.push(template);
        return sendJson(response, template, 201);
      }
      return sendError(response, 405, "This API method is not supported.", "unsupported_operation");
    }

    const templateMatch = url.pathname.match(/^\/v1\/agents\/environments\/templates\/([^/]+)$/u);
    if (templateMatch) {
      if (request.headers["openai-beta"] !== "agents=v1") {
        return sendError(response, 400, "Fixture Environment Templates require the Agents beta header.");
      }
      const template = state.environmentTemplates.find((entry) => entry.id === decodeURIComponent(templateMatch[1]));
      if (!template) return sendError(response, 404, "Fixture Environment Template not found.");
      const operation = { GET: "Retrieve", POST: "Update", DELETE: "Delete" }[request.method];
      if (!operation) return sendError(response, 405, "This API method is not supported.", "unsupported_operation");
      const control = consumeControl(`environmentTemplate${operation}`);
      if (control.delayMs) await wait(control.delayMs);
      if (response.destroyed) return;
      if (control.status !== 200) return sendError(response, control.status, `Fixture Environment Template ${operation.toLowerCase()} failed.`);
      if (request.method === "GET") return sendJson(response, template);
      if (request.method === "DELETE") {
        state.environmentTemplates = state.environmentTemplates.filter((entry) => entry.id !== template.id);
        return sendJson(response, { id: template.id, object: "agent.environment.template.deleted", deleted: true });
      }
      if (!isRecord(body) || !hasOnlyKeys(body, ["name", "network"]) ||
        (body.name !== undefined && body.name !== null && typeof body.name !== "string")) {
        return sendError(response, 400, "Fixture Environment Template patch is invalid.");
      }
      if (body.network !== undefined && body.network !== null &&
        (!isRecord(body.network) || !hasOnlyKeys(body.network, ["access"]) || !["enabled", "disabled"].includes(body.network.access))) {
        return sendError(response, 400, "Fixture Environment Template network is unsupported.");
      }
      if (Object.hasOwn(body, "name")) template.name = body.name;
      if (Object.hasOwn(body, "network")) template.network = { access: body.network?.access ?? "enabled", allowed_domains: [] };
      template.updated_at = Math.max(template.updated_at + 1, Math.floor(Date.now() / 1000));
      return sendJson(response, template);
    }

    const environmentFilesMatch = url.pathname.match(/^\/v1\/agents\/environments\/([^/]+)\/files$/);
    if (request.method === "POST" && environmentFilesMatch) {
      const id = decodeURIComponent(environmentFilesMatch[1]);
      if (id !== hostedEnvironmentUuid) return sendError(response, 503, "Fixture Environment is not a writable hosted placement.");
      if (request.headers["openai-beta"] !== "agents=v1") return sendError(response, 400, "Fixture Environment Files requires the Agents beta header.");
      if (state.controls.environmentFileCreateStatus !== 200) {
        const status = state.controls.environmentFileCreateStatus;
        state.controls.environmentFileCreateStatus = 200;
        return sendError(response, status, "Fixture Environment copy failed.");
      }
      if (!isRecord(body) || typeof body.path !== "string" || !body.path.startsWith("/workspace/")) {
        return sendError(response, 400, "Fixture Environment copy body is invalid.");
      }
      let data;
      if (body.type === "file_id" && Object.keys(body).sort().join(",") === "file_id,path,type" && typeof body.file_id === "string") {
        const source = state.sourceFiles.get(body.file_id);
        if (!source) return sendError(response, 404, "Fixture Source File not found for copy.");
        data = source.data;
      } else if (body.type === "inline" && Object.keys(body).sort().join(",") === "data,path,type" && typeof body.data === "string") {
        if (!/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/u.test(body.data)) {
          return sendError(response, 400, "Fixture inline Environment data is invalid.");
        }
        data = Buffer.from(body.data, "base64");
        if (data.toString("base64") !== body.data) return sendError(response, 400, "Fixture inline Environment data is invalid.");
      } else {
        return sendError(response, 400, "Fixture Environment copy body is invalid.");
      }
      if (data.length > 50 * 1024 * 1024) return sendError(response, 413, "Fixture Environment copy is too large.");
      const result = {
        environment_id: hostedEnvironmentUuid,
        object: "agent.environment.file",
        path: body.path,
        size_bytes: data.length,
      };
      state.hostedWorkspaceFiles = [
        ...state.hostedWorkspaceFiles.filter((file) => file.path !== body.path),
        result,
      ];
      const lose = state.controls.environmentFileCreateResponseLoss;
      state.controls.environmentFileCreateResponseLoss = 0;
      if (lose) {
        response.destroy();
        return;
      }
      return sendJson(response, result, 201);
    }
    if (request.method === "GET" && environmentFilesMatch) {
      trackAbort(response, "environmentFileReads");
      const control = consumeControl("environmentFiles");
      if (control.delayMs) await wait(control.delayMs);
      if (response.destroyed) return;
      if (control.status !== 200) {
        return sendError(
          response,
          control.status,
          control.status === 404 ? "This API operation is not supported." : "Fixture Environment files read failed.",
          control.status === 404 ? "unsupported_operation" : "fixture_failure",
          control.status === 404 ? "invalid_request_error" : "fixture_error",
        );
      }

      const id = decodeURIComponent(environmentFilesMatch[1]);
      if (id === hostedEnvironmentUuid) {
        const directory = url.searchParams.get("path") ?? "/workspace";
        const limit = Number(url.searchParams.get("limit") ?? 20);
        const order = url.searchParams.get("order") ?? "desc";
        if (url.searchParams.get("page") !== null || limit < 1 || limit > 100 || !["asc", "desc"].includes(order)) {
          return sendError(response, 400, "Fixture hosted Environment files query is invalid.");
        }
        const files = state.hostedWorkspaceFiles
          .filter((file) => file.path.slice(0, file.path.lastIndexOf("/")) === directory)
          .sort((left, right) => left.path.localeCompare(right.path));
        if (order === "desc") files.reverse();
        return sendJson(response, { object: "page", data: files.slice(0, limit), next: null, has_more: false });
      }
      const sessionEnvironment = state.sessions[0]?.environment;
      const expectedId = sessionEnvironment?.type === "self_hosted" ? sessionEnvironment.id : null;
      if (id !== expectedId) return sendError(response, 404, "Fixture Environment not found.");
      const directory = url.searchParams.get("path") ?? sessionEnvironment.workspace_directory;
      const limit = Number(url.searchParams.get("limit") ?? 20);
      const order = url.searchParams.get("order") ?? "desc";
      const cursor = url.searchParams.get("page");
      if (
        directory !== sessionEnvironment.workspace_directory ||
        limit !== 20 ||
        !["asc", "desc"].includes(order) ||
        (cursor !== null && cursor !== "fixture-page-2")
      ) return sendError(response, 400, "Fixture Environment files query is invalid.");

      const allFiles = Array.from({ length: 21 }, (_, index) => ({
        environment_id: canonicalEnvironmentUuid,
        object: "agent.environment.file",
        path: `${sessionEnvironment.workspace_directory}/file-${String(index + 1).padStart(2, "0")}.txt`,
        size_bytes: (index + 1) * 128,
      }));
      if (order === "desc") allFiles.reverse();
      const start = cursor === "fixture-page-2" ? 20 : 0;
      const next = start + limit < allFiles.length ? "fixture-page-2" : null;
      return sendJson(response, {
        object: "page",
        data: allFiles.slice(start, start + limit),
        next,
        has_more: next !== null,
      });
    }

    const environmentMatch = url.pathname.match(/^\/v1\/agents\/environments\/([^/]+)$/);
    if (request.method === "GET" && environmentMatch) {
      if (state.controls.environmentRetrieveDelayMs) await wait(state.controls.environmentRetrieveDelayMs);
      if (state.controls.environmentRetrieveStatus !== 200) {
        return sendError(response, state.controls.environmentRetrieveStatus, "Fixture Environment retrieve failed.");
      }
      const id = decodeURIComponent(environmentMatch[1]);
      if (id === hostedEnvironmentUuid) {
        const resource = {
          id,
          object: "agent.environment",
          type: "openai_hosted",
          status: state.controls.environmentResourceStatus,
          files: [],
          plugins: [],
          skills: [],
        };
        if (state.controls.environmentResourceVariant === "missing_skills") delete resource.skills;
        if (state.controls.environmentResourceVariant === "wrong_id") resource.id = canonicalEnvironmentUuid;
        if (state.controls.environmentResourceVariant === "extra_field") resource.extra = true;
        if (state.controls.environmentResourceVariant === "wrong_type") resource.type = "self_hosted";
        if (state.controls.environmentResourceVariant === "populated") resource.files = [{ id: "unsupported-install" }];
        return sendJson(response, resource);
      }
      const sessionEnvironment = state.sessions[0]?.environment;
      const expectedId = sessionEnvironment?.type === "self_hosted" ? sessionEnvironment.id : null;
      if (id !== expectedId) return sendError(response, 404, "Fixture Environment not found.");
      const canonicalId = id.toLowerCase();
      const resource = {
        id: canonicalUuidPattern.test(canonicalId) ? canonicalId : id,
        object: "agent.environment",
        type: "self_hosted",
        status: state.controls.environmentResourceStatus,
        files: [],
        plugins: [],
        skills: [],
      };
      if (state.controls.environmentResourceVariant === "missing_skills") delete resource.skills;
      if (state.controls.environmentResourceVariant === "wrong_id") resource.id = "another_environment";
      if (state.controls.environmentResourceVariant === "extra_field") resource.extra = true;
      if (state.controls.environmentResourceVariant === "wrong_type") resource.type = "openai_hosted";
      return sendJson(response, resource);
    }

    const itemsMatch = url.pathname.match(/^\/v1\/agents\/sessions\/([^/]+)\/items$/);
    if (request.method === "GET" && itemsMatch) {
      trackAbort(response, "itemReads");
      if (state.controls.itemsRetrieveDelayMs) await wait(state.controls.itemsRetrieveDelayMs);
      if (response.destroyed) return;
      if (state.controls.itemsRetrieveStatus !== 200) {
        return sendError(response, state.controls.itemsRetrieveStatus, "Fixture Items retrieve failed.");
      }
      const sessionId = decodeURIComponent(itemsMatch[1]);
      if (!state.sessions.some((candidate) => candidate.id === sessionId)) {
        return sendError(response, 404, "Fixture Session not found for Items.");
      }
      const items = sessionId !== "session_snapshot"
        ? state.createdSessionItems.get(sessionId) ?? []
        : state.controls.itemsScenario === 2
          ? [...observableTurnItems(), ...patchItems()]
          : state.controls.itemsScenario
            ? patchItems()
            : state.controls.turnsScenario
              ? observableTurnItems()
              : [];
      return sendJson(response, page(items));
    }

    const turnsMatch = url.pathname.match(/^\/v1\/agents\/sessions\/([^/]+)\/turns$/);
    if (request.method === "GET" && turnsMatch) {
      trackAbort(response, "turnReads");
      if (state.controls.turnsRetrieveDelayMs) await wait(state.controls.turnsRetrieveDelayMs);
      if (response.destroyed) return;
      if (state.controls.turnsRetrieveStatus !== 200) {
        return sendError(response, state.controls.turnsRetrieveStatus, "Fixture Turns retrieve failed.");
      }
      const sessionId = decodeURIComponent(turnsMatch[1]);
      if (!state.sessions.some((candidate) => candidate.id === sessionId)) {
        return sendError(response, 404, "Fixture Session not found for Turns.");
      }
      const sessionTurns = state.turns.filter((turn) => turn.session_id === sessionId);
      const after = url.searchParams.get("after");
      const start = after ? sessionTurns.findIndex((turn) => turn.id === after) + 1 : 0;
      if (after && start === 0) return sendError(response, 400, "Fixture Turn cursor not found.");
      const requestedLimit = Number(url.searchParams.get("limit") ?? 20);
      const size = Math.max(1, Math.min(requestedLimit, state.controls.turnsPageSize));
      const data = sessionTurns.slice(start, start + size);
      return sendJson(response, {
        object: "list",
        data,
        has_more: start + data.length < sessionTurns.length,
        first_id: data[0]?.id ?? null,
        last_id: data.at(-1)?.id ?? null,
      });
    }

    const eventsMatch = url.pathname.match(/^\/v1\/agents\/sessions\/([^/]+)\/events$/);
    if (request.method === "POST" && eventsMatch) {
      const status = state.controls.sendStatus;
      const responseLoss = state.controls.sendResponseLoss;
      state.controls.sendStatus = 202;
      state.controls.sendResponseLoss = 0;
      if (responseLoss) {
        response.destroy();
        return;
      }
      if (status !== 202) return sendError(response, status, "Fixture send failed.");
      response.writeHead(202);
      response.end();
      return;
    }
    if (request.method === "GET" && eventsMatch) {
      trackAbort(response, "streams");
      const sessionId = decodeURIComponent(eventsMatch[1]);
      if (state.controls.streamOpenDelayMs) await wait(state.controls.streamOpenDelayMs);
      if (response.destroyed) return;
      if (!state.sessions.some((candidate) => candidate.id === sessionId)) {
        return sendError(response, 404, "Fixture Session not found for stream.");
      }
      if (state.controls.streamStatus !== 200) {
        return sendError(response, state.controls.streamStatus, "Fixture stream rejected.");
      }
      response.writeHead(200, {
        "content-type": "text/event-stream; charset=utf-8",
        "cache-control": "no-cache, no-transform",
        connection: "keep-alive",
      });
      streamResponses.set(response, sessionId);
      response.write(": fixture stream open\n\n");
      const statuses = [null, "pending", "ready", "connected", "disconnected", "failed", "expired"];
      const environmentStatus = statuses[state.controls.environmentEventStatus] ?? null;
      if (environmentStatus && state.controls.environmentEventCount > 0) {
        state.controls.environmentEventCount -= 1;
        state.sequence += 1;
        const streamSession = state.sessions.find((candidate) => candidate.id === sessionId);
        const sessionEnvironment = streamSession?.environment;
        const supportedEnvironment = sessionEnvironment?.type === "self_hosted" || sessionEnvironment?.type === "openai_hosted"
          ? sessionEnvironment
          : null;
        const rawEnvironmentId = supportedEnvironment?.id ?? "environment_fixture";
        const canonicalEnvironmentId = rawEnvironmentId.toLowerCase();
        const environment = {
          id: canonicalUuidPattern.test(canonicalEnvironmentId) ? canonicalEnvironmentId : rawEnvironmentId,
          type: supportedEnvironment?.type ?? "self_hosted",
          status: environmentStatus,
          error: environmentStatus === "failed" ? {
            code: "environment_failed",
            type: "environment_error",
            message: "Safe failure; Authorization: Bearer fixture-secret X-API-Key: fixture-key",
          } : null,
        };
        response.write(`id: environment_${state.sequence}\ndata: ${JSON.stringify({
          type: `agent.session.environment.${environmentStatus}`,
          event_id: `environment_${state.sequence}`,
          session_id: sessionId,
          environment,
        })}\n\n`);
      }
      if (state.controls.streamCloseCount > 0) {
        state.controls.streamCloseCount -= 1;
        setTimeout(() => response.end(), state.controls.streamCloseDelayMs);
      }
      const heartbeat = setInterval(() => response.write(": fixture heartbeat\n\n"), 10_000);
      request.on("close", () => {
        clearInterval(heartbeat);
        streamResponses.delete(response);
      });
      return;
    }

    return sendError(response, 404, `No fixture route for ${request.method} ${url.pathname}`);
  } catch (error) {
    return sendError(response, 500, error instanceof Error ? error.message : "Unknown fixture failure.");
  }
});

server.listen(port, host, () => {
  process.stdout.write(`AGENT_FIXTURE_READY http://${host}:${port}\n`);
});

for (const signal of ["SIGINT", "SIGTERM"]) {
  process.on(signal, () => server.close(() => process.exit(0)));
}
