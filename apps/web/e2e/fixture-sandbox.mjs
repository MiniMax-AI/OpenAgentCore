const now = "2026-09-23T08:00:00Z";
const node = (id, name, online = true) => ({ id, name, provider: "docker", online, provider_ready: online, diagnostic: "",
  last_seen_at: now, max_active: 4, max_retained: 16, active: id === "node-local" ? 1 : 0, reserved: 0,
  retained: 0, cleanup_pending: 0, created_at: now, running: id === "node-local" ? 1 : 0, snapshots: 0,
  cpu_count: online ? 8 : null, available_memory_bytes: online ? 8589934592 : null, available_disk_bytes: online ? 34359738368 : null,
});
let nodes = [];
let calls = [];
let provider = "docker";
let diagnostic = "";
let coreUrl = "";
export function resetSandboxFixture() {
  nodes = [node("node-local", "Core server"), node("node-offline", "Offline host", false)]; calls = []; provider = "docker"; diagnostic = ""; coreUrl = "";
}
resetSandboxFixture();
export function handleSandboxFixture(request, response, url, sendJson, sendError) {
  const path = url.pathname;
  if (path === "/__fixture/sandbox-diagnostic") {
    const value = url.searchParams.get("value") ?? "";
    if (!["", "node_unavailable", "resource_missing", "compute_unconfirmed", "ownership_mismatch", "provider_unavailable"].includes(value)) { sendError(response, 400, "Unknown diagnostic"); return true; }
    diagnostic = value;
    nodes = nodes.map((entry) => entry.id === "node-local" ? { ...entry, online: value !== "node_unavailable", provider_ready: value !== "provider_unavailable", diagnostic: value === "provider_unavailable" ? value : "" } : entry);
    sendJson(response, {}); return true;
  }
  if (path === "/__fixture/sandbox") { sendJson(response, { nodes, calls, provider, core_url: coreUrl }); return true; }
  if (path === "/__fixture/sandbox-microsandbox") { provider = "microsandbox"; sendJson(response, {}); return true; }
  if (path === "/__fixture/sandbox-uninitialized") { provider = ""; coreUrl = ""; nodes = []; sendJson(response, {}); return true; }
  if (path === "/__fixture/sandbox-add-node") { nodes.push({ ...node("node-enrolled", "Enrolled host"), provider }); sendJson(response, {}); return true; }
  const projectRoute = path === "/v1/sandbox/nodes" || /^\/v1\/agents\/sessions\/[^/]+\/sandbox-placement$/.test(path);
  if (projectRoute && request.headers["openai-beta"] !== "agents=v1") {
    sendError(response, 400, "To access the Agents API, set the 'OpenAI-Beta' header to 'agents=v1'.", "invalid_beta"); return true;
  }
  if (path === "/v1/sandbox/nodes") { sendJson(response, { data: nodes.map(({ id, name, online }) => ({ id, name, available: online })) }); return true; }
  if (/^\/v1\/agents\/sessions\/[^/]+\/sandbox-placement$/.test(path)) {
    sendJson(response, { node_id: "node-local", node_name: "Core server", available: !diagnostic, state: "active", compute_phase: "running", diagnostic }); return true;
  }
  if (!path.startsWith("/core/v1/sandbox/")) return false;
  calls.push({ path, method: request.method, authorization: request.headers.authorization ?? null });
  // This fixture represents authenticated console routes, not direct Core administration.
  if (request.headers.authorization) { sendError(response, 400, "Browser admin credentials are not accepted.", "unexpected_authorization"); return true; }
  const deployment = () => ({ installation_id: "fixture-installation", provider, core_url: coreUrl, maintenance: false, owner_epoch: 1, generation: provider ? 1 : 0, mode: provider ? "nodes" : "", resources: { allocations: nodes.some((entry) => entry.id === "node-local") ? 1 : 0, pending: 0 } });
  if (path.endsWith("/deployment") && request.method === "POST") {
    let body = "";
    request.on("data", (chunk) => { body += chunk; });
    request.on("end", () => {
      try {
        const input = JSON.parse(body);
        if (provider && (provider !== input.provider || coreUrl !== input.core_url)) {
          sendError(response, 409, "Sandbox deployment is already configured.", "sandbox_deployment_conflict"); return;
        }
        provider = input.provider; coreUrl = input.core_url;
        sendJson(response, deployment());
      } catch { sendError(response, 400, "Invalid setup request."); }
    });
  }
  else if (path.endsWith("/deployment")) sendJson(response, deployment());
  else if (path.endsWith("/enrollment-tokens")) sendJson(response, { token: "fixture-once-token", expires_at: new Date(Date.now() + 15 * 60 * 1000).toISOString() });
  else if (path.endsWith("/allocations")) sendJson(response, { data: path.includes("node-local") ? [{ id: "allocation-1", node_id: "node-local", session_id: "session_snapshot", tenant_id: "fixture-project", environment_id: "environment-1", state: "active", compute_phase: "running", initialization: "ready", diagnostic, created_at: now }] : [] });
  else if (request.method === "DELETE") {
    const id = path.split("/").at(-1);
    if (id === "node-local") sendError(response, 409, "Node has active allocations or retained resources.", "runtime_node_in_use");
    else { nodes = nodes.filter((entry) => entry.id !== id); sendJson(response, { id, deleted: true }); }
  } else if (path.endsWith("/nodes")) sendJson(response, { data: nodes });
  else sendError(response, 404, "Unknown sandbox fixture route.");
  return true;
}
