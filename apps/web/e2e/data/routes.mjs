// Synthetic deployment data for the browser acceptance fixture: Sessions, Turns, Items, nodes.
import { agentConversations, agentDefinitions } from "./agents.mjs";
let seed = 42;
const rand = () => ((seed = (seed * 1664525 + 1013904223) % 4294967296) / 4294967296);
const pick = (list) => list[Math.floor(rand() * list.length)];
const uuid = () => "xxxxxxxx-xxxx-4xxx-8xxx-xxxxxxxxxxxx".replace(/x/g, () => Math.floor(rand() * 16).toString(16));

export function buildDemo(now = Math.floor(Date.now() / 1000)) {
  seed = 42;
  const models = ["gpt-5.1-codex", "claude-sonnet-5", "MiniMax-M2", "gpt-5.1-mini"];
  const agents = agentDefinitions.map((definition, index) => ({
    id: `agent_${index + 1}${uuid().slice(0, 6)}`, object: "agent", model: definition.model, name: definition.name, instructions: definition.instructions,
    metadata: definition.metadata, multi_agent: { enabled: false, max_concurrent_subagents: null }, reasoning: definition.reasoning, service_tier: "auto",
    text: { format: { type: "text" }, verbosity: "medium" }, tools: definition.tools,
    x_agents_core: { harness: definition.harness },
    created_at: now - 86400 * (10 + index * 2), updated_at: now - 3600 * (index * 5 + 2),
  }));
  const sessions = [];
  const turns = new Map();
  const items = new Map();
  const statuses = [...Array(40).fill("idle"), "in_progress", "in_progress", "in_progress", "requires_action", "requires_action", "failed", "failed", "failed"];
  statuses.forEach((status, index) => {
    const agent = agents[Math.floor(rand() * agents.length)];
    const envType = index % 4 === 0 ? "openai_hosted" : index % 7 === 0 ? "self_hosted" : "none";
    const created = now - Math.floor(rand() * 86400 * 1.2);
    const id = uuid();
    const sessionTurns = [];
    const sessionItems = [];
    const turnCount = 1 + Math.floor(rand() * 9);
    let cursor = created;
    let input = 0; let output = 0; let cached = 0; let reasoning = 0;
    for (let t = 0; t < turnCount; t += 1) {
      cursor += 60 + Math.floor(rand() * 2400);
      if (cursor > now - 5) break;
      const last = t === turnCount - 1;
      const failed = (status === "failed" && last) || rand() < 0.04;
      const running = status === "in_progress" && last;
      const duration = Math.floor(4 + rand() * rand() * 180);
      const turnInput = Math.floor(2000 + rand() * 30000);
      const turnOutput = Math.floor(200 + rand() * 4000);
      const turnId = uuid();
      const turn = {
        id: turnId, agent_id: agent.id, session_id: id, object: "agent.session.turn",
        status: running ? "in_progress" : failed ? "failed" : rand() < 0.03 ? "cancelled" : "completed",
        created_at: cursor, started_at: cursor + Math.floor(rand() * 4), completed_at: running ? null : cursor + duration,
        error: failed ? { code: "internal_error", message: "The execution could not complete." } : null,
        usage: running ? null : { input_tokens: turnInput, output_tokens: turnOutput, total_tokens: turnInput + turnOutput, input_tokens_details: { cached_tokens: Math.floor(turnInput * 0.4) }, output_tokens_details: { reasoning_tokens: Math.floor(turnOutput * 0.3) } },
      };
      if (!running) { input += turnInput; output += turnOutput; cached += Math.floor(turnInput * 0.4); reasoning += Math.floor(turnOutput * 0.3); }
      sessionTurns.push(turn);
      const exchange = (agentConversations[agent.name] ?? [["Investigate", "Done"]])[t % (agentConversations[agent.name]?.length ?? 1)];
      sessionItems.push({ id: uuid(), turn_id: turnId, type: "message", status: "completed", role: "user", content: [{ type: "input_text", text: exchange[0] }] });
      const calls = Math.floor(rand() * 6);
      for (let c = 0; c < calls; c += 1) {
        const kind = pick(["function", "function", "mcp", "command", "command", "web"]);
        const itemFailed = rand() < 0.06;
        if (kind === "function") sessionItems.push({ id: uuid(), turn_id: turnId, type: "function_call", status: itemFailed ? "failed" : "completed", call_id: uuid(), name: pick(["apply_patch", "read_file", "search_docs"]), arguments: {} });
        if (kind === "mcp") sessionItems.push({ id: uuid(), turn_id: turnId, type: "mcp_call", status: itemFailed ? "failed" : "completed", server_label: pick(["github", "linear"]), name: pick(["search_issues", "get_file"]), arguments: {}, output: null, error: null });
        if (kind === "command") sessionItems.push({ id: uuid(), turn_id: turnId, type: "command_execution", status: "completed", command: "pytest -q", exit_code: itemFailed ? 1 : 0, duration_ms: 1200 });
        if (kind === "web") sessionItems.push({ id: uuid(), turn_id: turnId, type: "web_search_call", status: "completed" });
      }
      if (!running && !failed) sessionItems.push({ id: uuid(), turn_id: turnId, type: "message", status: "completed", role: "assistant", phase: "final_answer", content: [{ type: "output_text", text: exchange[1] }] });
    }
    const lastActive = sessionTurns.length ? Math.max(...sessionTurns.map((turn) => turn.completed_at ?? turn.created_at)) : created;
    const { object: _o, metadata: _m, created_at: _c, updated_at: _u, ...snapshot } = agent;
    sessions.push({
      id, object: "agent.session", agent: snapshot,
      environment: envType === "none" ? { type: "none" } : envType === "self_hosted" ? { type: "self_hosted", id: uuid(), remote_url: "wss://core.example.internal/executor", workspace_directory: "/srv/work", capability_directories: [] } : { type: "openai_hosted", id: uuid(), capability_directories: [], network: { access: "disabled", allowed_domains: [] }, packages: { npm: [], python: [], system: [] }, files: [], plugins: [], skills: [] },
      status, error: status === "failed" ? pick(["Sandbox allocation failed: node unavailable.", "Model provider returned 429 Too Many Requests.", "Tool call timed out after 300 s."]) : null,
      metadata: {}, required_actions: status === "requires_action" ? [{ type: "function_call", call_id: uuid(), turn_id: sessionTurns.at(-1)?.id ?? uuid(), name: "approve_refund", arguments: {} }] : [],
      vault_ids: [], usage: status === "in_progress" ? null : sessionTurns.length ? { input_tokens: input, output_tokens: output, total_tokens: input + output, input_tokens_details: { cached_tokens: cached }, output_tokens_details: { reasoning_tokens: reasoning } } : null,
      created_at: created, last_active_at: lastActive,
    });
    turns.set(id, sessionTurns);
    items.set(id, sessionItems);
  });
  sessions.sort((a, b) => b.created_at - a.created_at);
  const nodes = [
    { id: "node-local", name: "core-01", provider: "docker", online: true, provider_ready: true, diagnostic: "", cpu_count: 16, available_memory_bytes: 38 * 2 ** 30, available_disk_bytes: 410 * 2 ** 30, running: 5, snapshots: 2, last_seen_at: new Date((now - 8) * 1000).toISOString(), max_active: 8, max_retained: 16, active: 5, reserved: 1, retained: 2, cleanup_pending: 0, created_at: new Date((now - 86400 * 30) * 1000).toISOString() },
    { id: "node-gpu", name: "gpu-worker-02", provider: "docker", online: true, provider_ready: true, diagnostic: "", cpu_count: 32, available_memory_bytes: 12 * 2 ** 30, available_disk_bytes: 96 * 2 ** 30, running: 7, snapshots: 5, last_seen_at: new Date((now - 12) * 1000).toISOString(), max_active: 8, max_retained: 16, active: 7, reserved: 0, retained: 5, cleanup_pending: 1, created_at: new Date((now - 86400 * 12) * 1000).toISOString() },
    { id: "node-edge", name: "edge-03", provider: "docker", online: false, provider_ready: false, diagnostic: "", cpu_count: 8, available_memory_bytes: null, available_disk_bytes: null, running: 0, snapshots: 0, last_seen_at: new Date((now - 5400) * 1000).toISOString(), max_active: 4, max_retained: 8, active: 0, reserved: 0, retained: 0, cleanup_pending: 0, created_at: new Date((now - 86400 * 3) * 1000).toISOString() },
  ];
  const hosted = sessions.filter((session) => session.environment.type === "openai_hosted");
  const allocations = hosted.map((session, index) => ({
    id: uuid(), node_id: index % 2 ? "node-gpu" : "node-local", tenant_id: "project", session_id: session.id, environment_id: session.environment.id,
    state: "active", compute_phase: index % 3 === 0 ? "suspended" : "running", diagnostic: "", initialization: "ready", created_at: new Date(session.created_at * 1000).toISOString(),
  }));
  const observations = sessions.map((session) => {
    const base = { id: session.id, object: "agent.runtime_observation", session_id: session.id, resolved_at: now };
    if (session.environment.type === "none") return { ...base, environment_id: null, mode: "none", provider_type: null, instance: { kind: "none", allocation_id: null, device_id: null, connection_generation: null }, lifecycle_state: null, status: "unsupported", reason: "runtime_mode_not_observable", allocation_created_at: null, observed_at: null, started_at: null, cpu: null, memory: null };
    if (session.environment.type === "self_hosted") return { ...base, environment_id: session.environment.id, mode: "self_hosted", provider_type: null, instance: { kind: "self_hosted_connection", allocation_id: null, device_id: null, connection_generation: null }, lifecycle_state: null, status: "unsupported", reason: "runtime_mode_not_observable", allocation_created_at: null, observed_at: null, started_at: null, cpu: null, memory: null };
    const allocation = allocations.find((entry) => entry.session_id === session.id);
    const sleeping = allocation.compute_phase === "suspended";
    return { ...base, environment_id: session.environment.id, mode: "openai_hosted", provider_type: "docker", instance: { kind: "managed_allocation", allocation_id: allocation.id, device_id: null, connection_generation: null }, lifecycle_state: sleeping ? "sleeping" : "active", status: "observed", reason: null, allocation_created_at: session.created_at, observed_at: now - 2, started_at: session.created_at, cpu: sleeping ? null : { usage_seconds_total: 600 + Math.floor(rand() * 4000), capacity_cores: 2, usage_cores: Number((rand() * 1.6).toFixed(2)), utilization_ratio: null }, memory: sleeping ? null : { usage_bytes: Math.floor((0.4 + rand() * 1.4) * 2 ** 30), limit_bytes: 2 * 2 ** 30 } };
  });
  return { agents, sessions, turns, items, nodes, allocations, observations };
}
