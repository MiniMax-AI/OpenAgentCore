// Browser acceptance fixture: the console service's routes (/console/**) and the
// management surfaces it forwards (/core/v1/admin/**, /core/v1/sandbox/**), with
// synthetic, deterministic data and in-memory writes. It never serves /v1; any
// /v1 request, and any browser-supplied Authorization header, is recorded so a
// test can assert that the console stays on its management boundary.
import http from "node:http";

import { buildAdmin } from "./data/admin.mjs";
import { coreMetrics } from "./data/core-metrics.mjs";
import { buildResources } from "./data/resources.mjs";
import { buildDemo } from "./data/routes.mjs";

const port = Number(process.env.AGENTS_FIXTURE_PORT ?? 18092);
const SESSION_COOKIE = "core_console=fixture-session";

let state;
const hex = (c) => c.repeat(64);
/** The Runtime release this fixture's distribution manifest describes. */
const release = { source_commit: "c0ffee".padEnd(40, "0"), image_id: `sha256:${hex("1")}`, image_manifest_digest: `sha256:${hex("2")}`, microsandbox_ref: `parsar-core-runtime@sha256:${hex("3")}`, runtime_sha256: hex("4"), firmware_sha256: hex("5") };
const manifest = { platform: "linux/amd64", source_commit: release.source_commit, images: { runtime: release.image_id }, image_manifest_digests: { runtime: release.image_manifest_digest }, runtime_ref: release.microsandbox_ref, microsandbox: { runtime_sha256: release.runtime_sha256, firmware_sha256: release.firmware_sha256 }, artifacts: {} };

function configuredDeployment() {
  return { installation_id: "7f3c2a90-fixture", provider: "docker", core_url: `http://127.0.0.1:${port}`, maintenance: false, owner_epoch: 3, generation: 1, mode: "nodes", resources: { allocations: 0, pending: 0 }, specification: { resources: { cpus: 2, memory_mib: 4096 }, runtime: release }, specification_digest: "fixture" };
}

function reset(mode = "setup", fresh = false, sandbox = "configured") {
  const base = buildDemo();
  const now = Math.floor(Date.now() / 1000);
  const resources = buildResources(now, base.agents, base.sessions);
  const admin = buildAdmin(now, base, resources);
  // A fresh install: no project yet, so the console starts first-run setup.
  if (fresh) admin.projects.splice(0);
  state = {
    ...base, resources, admin,
    auth: { mode, username: mode === "authenticated" ? "admin" : null, password: mode === "setup" ? null : "correct horse battery" },
    violations: [], writes: [], failNext: null, nextId: 1,
    // "none": the deployment is not configured yet, so the Nodes page offers setup.
    deployment: sandbox === "none" ? null : configuredDeployment(),
  };
}
reset();

function send(response, status, body, headers = {}) {
  response.writeHead(status, { "content-type": "application/json", "cache-control": "no-store", ...headers });
  response.end(JSON.stringify(body));
}
function error(response, status, message, code = null) {
  send(response, status, { error: { message, type: "invalid_request_error", code, param: null } });
}
async function body(request) {
  const chunks = [];
  for await (const chunk of request) chunks.push(chunk);
  const text = Buffer.concat(chunks).toString("utf8");
  return text ? JSON.parse(text) : {};
}
function list(all, url, max = 100) {
  // Collections are stored newest-first.
  const items = url.searchParams.get("order") === "asc" ? [...all].reverse() : all;
  const limit = Math.min(max, Number(url.searchParams.get("limit") ?? 20));
  const after = url.searchParams.get("after");
  const start = after ? items.findIndex((entry) => entry.id === after) + 1 : 0;
  const data = items.slice(start, start + limit);
  return { object: "list", data, has_more: start + limit < items.length, first_id: data[0]?.id ?? null, last_id: data.at(-1)?.id ?? null };
}
const id = (prefix) => `${prefix}${String(state.nextId++).padStart(8, "0")}`;
const uuid = () => `00000000-0000-4000-8000-${String(state.nextId++).padStart(12, "0")}`;

const startupConfiguration = {
  object: "agents.core.startup_configuration", schema_version: 1,
  supported: { harnesses: ["claude_sdk", "codex"], managed_sandbox_providers: ["docker", "microsandbox"] },
  configured: {
    default_harness: "codex", enabled_harnesses: ["claude_sdk", "codex"], daemon_gateway: true, self_hosted: true,
    managed_sandbox: { enabled: true, provider: "docker", maintenance: false },
    model_providers: [{ harness: "claude_sdk", endpoint_configured: true }, { harness: "codex", endpoint_configured: true }],
  },
};

async function consoleRoute(request, response, url) {
  const auth = state.auth;
  if (url.pathname === "/console/auth" && request.method === "GET") {
    if (auth.mode === "authenticated" && request.headers.cookie?.includes(SESSION_COOKIE)) return send(response, 200, { mode: "authenticated", username: auth.username });
    return send(response, 200, { mode: auth.mode === "setup" ? "setup" : "login" });
  }
  if (url.pathname === "/console/auth/setup" && request.method === "POST") {
    if (auth.mode !== "setup") return error(response, 409, "Setup is complete.");
    const { username, password } = await body(request);
    if (!username || !password || password.length < 12) return error(response, 400, "Invalid administrator.");
    Object.assign(auth, { mode: "authenticated", username, password });
    return send(response, 200, { mode: "authenticated", username }, { "set-cookie": `${SESSION_COOKIE}; Path=/; HttpOnly; SameSite=Strict` });
  }
  if (url.pathname === "/console/auth/login" && request.method === "POST") {
    const { username, password } = await body(request);
    if (username !== auth.username || password !== auth.password) return error(response, 401, "Sign-in failed.");
    auth.mode = "authenticated";
    return send(response, 200, { mode: "authenticated", username }, { "set-cookie": `${SESSION_COOKIE}; Path=/; HttpOnly; SameSite=Strict` });
  }
  if (url.pathname === "/console/auth/logout" && request.method === "POST") {
    auth.mode = "login";
    return send(response, 200, { mode: "login" }, { "set-cookie": `${SESSION_COOKIE.split("=")[0]}=; Path=/; Max-Age=0` });
  }
  if (url.pathname === "/console/config") {
    return send(response, 200, { api_keys: true, sandbox_admin: true, node_installer: true, node_installer_sha256: "a".repeat(64) });
  }
  return error(response, 404, "Not found.");
}

async function adminWrite(request, response, path) {
  const a = state.admin;
  const input = request.method === "POST" ? await body(request) : {};
  if (path === "/projects" && request.method === "POST") {
    const project = { id: id("proj_"), name: input.name, created_at: Math.floor(Date.now() / 1000), archived_at: null, keys: [] };
    a.projects.push(project);
    return send(response, 201, a.publicProject(project));
  }
  if (path === "/copies" && request.method === "POST") {
    const source = a.collections(input.source_project_id);
    const agent = source.agents.find((entry) => entry.id === input.resource_id);
    if (input.resource_type !== "agent" || !agent) return error(response, 404, "No such resource.");
    const copy = { ...agent, id: id("agent_") };
    state.agents.unshift(copy);
    a.owner.set(`agent:${copy.id}`, input.target_project_id);
    return send(response, 201, { mappings: [{ type: "agent", source_id: agent.id, target_id: copy.id }], skipped: [] });
  }
  const match = path.match(/^\/projects\/([^/]+)(\/.*)?$/);
  const project = match && a.projects.find((entry) => entry.id === match[1]);
  if (!project) return error(response, 404, "No such project.");
  const rest = match[2] ?? "";
  if (rest === "/archive" && request.method === "POST") {
    const at = Math.floor(Date.now() / 1000);
    project.archived_at = at;
    for (const key of project.keys) key.revoked_at ??= at;
    return send(response, 200, a.publicProject(project));
  }
  if (rest === "/keys" && request.method === "POST") {
    const key = { id: uuid(), name: input.name, prefix: `pc_live_${String(state.nextId).padStart(3, "0")}`, created_at: Math.floor(Date.now() / 1000), revoked_at: null };
    project.keys.push(key);
    return send(response, 201, { ...a.publicKey(project, key), key: `${key.prefix}_fixture-secret-${key.id.slice(-6)}` });
  }
  let m;
  if ((m = rest.match(/^\/keys\/([^/]+)$/)) && request.method === "DELETE") {
    const key = project.keys.find((entry) => entry.id === m[1]);
    if (!key) return error(response, 404, "No such key.");
    key.revoked_at ??= Math.floor(Date.now() / 1000);
    return send(response, 200, { id: key.id, deleted: true });
  }
  if ((m = rest.match(/^\/(agents|sessions)\/([^/]+)$/)) && request.method === "DELETE") {
    const [, kind, resourceId] = m;
    const collection = kind === "agents" ? state.agents : state.sessions;
    const index = collection.findIndex((entry) => entry.id === resourceId && a.owner.get(`${kind.slice(0, -1)}:${entry.id}`) === project.id);
    if (index < 0) return error(response, 404, "No such resource.");
    collection.splice(index, 1);
    return send(response, 200, { id: resourceId, object: kind === "agents" ? "agent.deleted" : "agent.session.deleted", deleted: true });
  }
  return error(response, 404, "Not found.");
}

function adminRead(response, path, url) {
  const a = state.admin;
  if (path === "/projects") return send(response, 200, { data: a.projects.map(a.publicProject), has_more: false });
  if (path === "/summary") return send(response, 200, a.summary(url));
  if (path === "/runtime-observations") {
    const data = a.runtimeObservations();
    return send(response, 200, { object: "list", data, has_more: false, first_id: data[0]?.observation.id ?? null, last_id: data.at(-1)?.observation.id ?? null });
  }
  if (path === "/startup-configuration") return send(response, 200, startupConfiguration);
  if (path === "/core-metrics") return send(response, 200, coreMetrics(url.searchParams.get("range") ?? "1h"));
  const match = path.match(/^\/projects\/([^/]+)(\/.*)?$/);
  const project = match && a.projects.find((entry) => entry.id === match[1]);
  if (!project) return error(response, 404, "No such project.");
  const rest = match[2] ?? "";
  if (!rest) return send(response, 200, a.publicProject(project));
  if (rest === "/keys") return send(response, 200, { data: project.keys.map((key) => a.publicKey(project, key)), has_more: false });
  if (rest === "/resource-owners") return send(response, 200, a.resourceOwners(project, url));
  if (rest === "/write-operations") return send(response, 200, { data: [], has_more: false, next_cursor: "" });
  const own = a.collections(project.id);
  const one = (items, itemId) => { const found = items.find((entry) => entry.id === itemId); return found ? send(response, 200, found) : error(response, 404, "Not found."); };
  const collections = { agents: own.agents, skills: own.skills, "environment-templates": own.templates, files: own.files, vaults: own.vaults };
  let m;
  if ((m = rest.match(/^\/(agents|skills|environment-templates|files|vaults)$/))) return send(response, 200, list(collections[m[1]], url, m[1] === "files" ? 10000 : 100));
  if ((m = rest.match(/^\/(agents|skills|environment-templates|files|vaults)\/([^/]+)$/))) return one(collections[m[1]], m[2]);
  if ((m = rest.match(/^\/skills\/([^/]+)\/versions$/))) return send(response, 200, list(state.resources.skillVersions.get(m[1]) ?? [], url));
  if ((m = rest.match(/^\/vaults\/([^/]+)\/credentials$/))) return send(response, 200, list(state.resources.credentials.get(m[1]) ?? [], url));
  if (rest === "/sessions") {
    const agentId = url.searchParams.get("agent_id");
    return send(response, 200, list(agentId ? own.sessions.filter((entry) => entry.agent.id === agentId) : own.sessions, url));
  }
  if ((m = rest.match(/^\/sessions\/([^/]+)(?:\/(.+))?$/))) {
    const session = own.sessions.find((entry) => entry.id === m[1]);
    if (!session) return error(response, 404, "No such Session.");
    if (!m[2]) return send(response, 200, session);
    if (m[2] === "turns") return send(response, 200, list([...(state.turns.get(session.id) ?? [])].reverse(), url));
    if (m[2] === "items") return send(response, 200, list([...(state.items.get(session.id) ?? [])].reverse(), url));
    if (m[2] === "runtime-observation") {
      const observation = state.observations.find((entry) => entry.session_id === session.id);
      return observation ? send(response, 200, observation) : error(response, 404, "No Runtime.");
    }
    return error(response, 404, "Not found.");
  }
  return error(response, 404, "Not found.");
}

async function sandboxRoute(request, response, path) {
  if (path === "/deployment" && request.method === "POST") {
    const input = await body(request);
    if (state.deployment) return error(response, 409, "The sandbox deployment is already configured.", "sandbox_deployment_conflict");
    if (!input.resources || (input.provider !== "e2b" && !input.runtime)) return error(response, 400, "resources and runtime are required.", "invalid_sandbox_configuration");
    state.deployment = { ...configuredDeployment(), provider: input.provider, core_url: input.core_url, mode: input.provider === "e2b" ? "direct" : "nodes", specification: { resources: input.resources, ...(input.runtime ? { runtime: input.runtime } : {}) } };
    return send(response, 201, state.deployment);
  }
  if (path === "/deployment") {
    return send(response, 200, state.deployment ?? { installation_id: "7f3c2a90-fixture", provider: "", core_url: `http://127.0.0.1:${port}`, maintenance: false, owner_epoch: 3, generation: 0, mode: "", resources: { allocations: 0, pending: 0 } });
  }
  if (path === "/nodes") return send(response, 200, { data: state.nodes });
  if (path === "/enrollment-tokens" && request.method === "POST") {
    return send(response, 201, { token: `enroll_fixture_${state.nextId++}`, expires_at: new Date(Date.now() + 15 * 60_000).toISOString() });
  }
  let m;
  if ((m = path.match(/^\/nodes\/([^/]+)\/allocations$/))) return send(response, 200, { data: state.allocations.filter((entry) => entry.node_id === m[1]) });
  if ((m = path.match(/^\/nodes\/([^/]+)$/)) && request.method === "DELETE") {
    const index = state.nodes.findIndex((node) => node.id === m[1]);
    if (index < 0) return error(response, 404, "No such node.");
    state.nodes.splice(index, 1);
    return send(response, 200, { id: m[1], deleted: true });
  }
  return error(response, 404, "Not found.");
}

/** Test controls: reset state, inject one failure, and read what the browser sent. */
async function fixtureRoute(request, response, url) {
  if (url.pathname === "/__fixture/health") return send(response, 200, { ok: true });
  if (url.pathname === "/__fixture/reset" && request.method === "POST") {
    reset(url.searchParams.get("auth") ?? "setup", url.searchParams.get("projects") === "none", url.searchParams.get("sandbox") ?? "configured");
    return send(response, 200, { ok: true });
  }
  if (url.pathname === "/__fixture/fail-next" && request.method === "POST") {
    state.failNext = await body(request); // { method, path, status, code?, message? }
    return send(response, 200, { ok: true });
  }
  if (url.pathname === "/__fixture/requests") return send(response, 200, { violations: state.violations, writes: state.writes });
  return error(response, 404, "Not found.");
}

http.createServer(async (request, response) => {
  const url = new URL(request.url, `http://127.0.0.1:${port}`);
  try {
    if (url.pathname.startsWith("/__fixture/")) return await fixtureRoute(request, response, url);
    // The console service serves its distribution manifest to anyone, as nodes download it.
    if (url.pathname === "/node-install/manifest.json") return send(response, 200, manifest);
    if (url.pathname === "/v1" || url.pathname.startsWith("/v1/")) {
      state.violations.push(`${request.method} ${url.pathname}`);
      return error(response, 404, "The console does not serve /v1.");
    }
    if (request.headers.authorization) state.violations.push(`Authorization header on ${request.method} ${url.pathname}`);
    if (url.pathname.startsWith("/console/")) return await consoleRoute(request, response, url);
    const signedIn = state.auth.mode === "authenticated" && request.headers.cookie?.includes(SESSION_COOKIE);
    if (!signedIn) return error(response, 401, "Sign in to the console.");
    const write = request.method !== "GET" && request.method !== "HEAD";
    if (write) state.writes.push(`${request.method} ${url.pathname}`);
    const fail = state.failNext;
    if (fail && fail.method === request.method && url.pathname.includes(fail.path)) {
      state.failNext = null;
      return error(response, fail.status, fail.message ?? "Injected failure.", fail.code ?? null);
    }
    if (url.pathname.startsWith("/core/v1/admin/")) {
      const path = url.pathname.slice("/core/v1/admin".length);
      return write ? await adminWrite(request, response, path) : adminRead(response, path, url);
    }
    if (url.pathname.startsWith("/core/v1/sandbox/")) return await sandboxRoute(request, response, url.pathname.slice("/core/v1/sandbox".length));
    return error(response, 404, "Not found.");
  } catch (caught) {
    error(response, 500, String(caught));
  }
}).listen(port, "127.0.0.1", () => console.log(`Console acceptance fixture on http://127.0.0.1:${port}`));

