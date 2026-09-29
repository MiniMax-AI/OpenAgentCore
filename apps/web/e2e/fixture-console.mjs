// Browser acceptance fixture: the console service's routes (/console/**) and the
// Core management tree it forwards (/core/v1/**: installation, projects, summary,
// audit log, metrics, harness default models and /core/v1/sandbox/**), with synthetic, deterministic data and
// in-memory writes. It never serves /v1; any /v1 request, and any
// browser-supplied Authorization header, is recorded so a test can assert that
// the console stays on its management boundary.
import http from "node:http";

import { domainState, domainRoute } from "./data/domain.mjs";
import { buildAdmin } from "./data/admin.mjs";
import { coreMetrics } from "./data/core-metrics.mjs";
import { buildResources } from "./data/resources.mjs";
import { buildDemo } from "./data/routes.mjs";
import { buildScreenshotDemo } from "../screenshots/data.mjs";

const port = Number(process.env.AGENTS_FIXTURE_PORT ?? 18092);
const SESSION_COOKIE = "core_console=fixture-session";
/** The deployment's Core key in this fixture; the same value as FIXTURE_CORE_KEY in console.ts. */
const CORE_KEY = "fixture-core-key-3f9a2c71";
/** Consecutive wrong keys before sign-in is refused for a while, and for how long (seconds). */
const LOCKOUT_AFTER = 3;
const LOCKOUT_SECONDS = 30;

let state;
const hex = (c) => c.repeat(64);
/** The Runtime release this fixture's distribution manifest describes. */
const release = { source_commit: "c0ffee".padEnd(40, "0"), image_id: `sha256:${hex("1")}`, image_manifest_digest: `sha256:${hex("2")}`, microsandbox_ref: `oac-runtime@sha256:${hex("3")}`, runtime_sha256: hex("4"), firmware_sha256: hex("5") };
const manifest = { platform: "linux/amd64", source_commit: release.source_commit, images: { runtime: release.image_id }, image_manifest_digests: { runtime: release.image_manifest_digest }, runtime_ref: release.microsandbox_ref, microsandbox: { runtime_sha256: release.runtime_sha256, firmware_sha256: release.firmware_sha256 }, artifacts: {} };

/**
 * config.json's public_url. "public": an HTTPS address, so applications get an API base URL;
 * "local": the installer's loopback default, reachable only on the Core machine;
 * "stale": public, with a node still enrolled with an earlier address.
 */
const PUBLIC_URL = "https://core.example.com";
const LOCAL_URL = "http://127.0.0.1:8091";
/** The address a node enrolled with before public_url last changed. */
const OLD_URL = "https://core-old.example.com";
const publicUrl = () => (state.installation === "local" ? LOCAL_URL : PUBLIC_URL);
/** The digest the console reports for its self-hosted executor installer; the same value as in monitoring.spec.ts. */
/** Core reports one installation ID, a canonical UUID, in the installation and the deployment. */
const INSTALLATION_ID = "7f3c2a90-5b1e-4c2d-9e3f-0a1b2c3d4e5f";

/** GET /core/v1/installation: the address, the startup settings from config.json and what is bound to the address. */
function installation() {
  const local = state.installation === "local";
  const setting = (key, value, fallback, restarts, extra = {}) => ({ key, value, default: fallback, changeable: true, sensitive: false, restarts, ...extra });
  const secret = (key, restarts) => ({ key, value: null, default: null, changeable: true, sensitive: true, restarts, configured: true });
  const nodes = state.deployment?.provider && state.deployment.provider !== "e2b" ? state.nodes : [];
  return {
    // As Core: api_base_url is always public_url followed by /v1; local_only marks a loopback public_url.
    object: "core.installation", installation_id: INSTALLATION_ID, public_url: publicUrl(), api_base_url: `${publicUrl()}/v1`,
    local_only: local, source_commit: release.source_commit,
    configuration: {
      path: "/opt/oac/config.json", apply_command: "sudo oac apply", applied_at: "2026-09-24T09:30:00Z",
      settings: [
        setting("public_url", publicUrl(), LOCAL_URL, ["core", "web"]),
        setting("listen_address", "127.0.0.1:8091", "127.0.0.1:8091", ["core"]),
        setting("web_listen_address", "127.0.0.1:4173", "127.0.0.1:4173", ["web"]),
        setting("data_dir", "/var/lib/oac", "/var/lib/oac", [], { changeable: false }),
        setting("log_level", "debug", "info", ["core", "web"]),
        secret("core_key", ["core", "web"]),
        secret("database_url", ["core"]),
      ],
    },
    // As Core counts them: the deployment's retained and pending hosted sandboxes, and the unrevoked executor credentials.
    address_bindings: {
      nodes: nodes.length, nodes_on_other_address: nodes.filter((node) => node.core_url !== publicUrl()).length,
      hosted_sandboxes: (state.deployment?.resources.allocations ?? 0) + (state.deployment?.resources.pending ?? 0),
      self_hosted_executors: [...state.executorCredentials.values()].flat().filter((credential) => credential.revoked_at === null).length,
    },
  };
}

const noNodeRollout = () => ({ state: "settled", previous_generation_sandboxes: 0, nodes: null });
function nodeRollout(previous = 0) {
  const nodes = { ready: 0, preparing: 0, failed: 0, update_required: 0, unknown: 0 };
  for (const node of state.nodes) nodes[node.rollout.state]++;
  return { state: nodes.preparing > 0 ? "preparing" : "settled", previous_generation_sandboxes: previous, nodes };
}

function unconfiguredDeployment(generation = 0, ownerEpoch = 3) {
  return { installation_id: INSTALLATION_ID, provider: "", core_url: publicUrl(), reset: null, rollout: noNodeRollout(), owner_epoch: ownerEpoch, generation, mode: "", resources: { allocations: 0, pending: 0 }, suspension: null };
}

function configuredDeployment() {
  return { installation_id: INSTALLATION_ID, provider: "docker", core_url: publicUrl(), reset: null, rollout: nodeRollout(), owner_epoch: 3, generation: 1, mode: "nodes", resources: { allocations: 0, pending: 0 }, specification: { resources: { cpus: 2, memory_mib: 4096 }, runtime: release }, specification_digest: "fixture", suspension: null };
}
/** The E2B template build as Core read it when the selection was saved. */
const templateBuild = { status: "ready", resources: { cpus: 2, memory_mib: 2048, root_disk_mib: 10240 } };

// E2B runs sandboxes in its cloud: no nodes, only what Core holds there.
function e2bDeployment() {
  return { ...configuredDeployment(), provider: "e2b", mode: "direct", rollout: noNodeRollout(), resources: { allocations: 3, pending: 1 }, specification: { resources: { cpus: 2, memory_mib: 2048 } }, e2b: { template: "oac-runtime:0f1e2d3c-4b5a-6978-8a9b-0c1d2e3f4a5b", api_url: "https://api.e2b.app", domain: "e2b.app", credential_configured: true, template_build: templateBuild } };
}

function reset(mode = "login", fresh = false, sandbox = "configured", nodes = "demo", address = "public", credentials = "configured", installers = true, artifacts = "docker,microsandbox") {
  // Self-hosted Sessions get their remote_url from public_url, as in Core.
  const screenshots = process.env.OAC_WEB_SCREENSHOT_DEMO === "1";
  const now = Math.floor(Date.now() / 1000);
  const base = (screenshots ? buildScreenshotDemo : buildDemo)(now, address === "local" ? LOCAL_URL : PUBLIC_URL);
  const resources = buildResources(now, base.agents, base.sessions);
  const admin = buildAdmin(now, base, resources);
  // A fresh install: no project, Session or Runtime yet; Getting started leads.
  if (fresh) for (const list of [admin.projects, base.sessions, base.observations, base.allocations]) list.splice(0);
  // "none": no node has enrolled yet.
  if (nodes === "none") base.nodes.splice(0);
  state = {
    ...base, resources, admin, domain: domainState(),
    // "authenticated": the console holds a session for the fixture cookie; "login": it holds none.
    auth: { mode: mode === "authenticated" ? "authenticated" : "login", failures: 0, lockedUntil: 0 },
    violations: [], writes: [], failNext: null, nextId: 1,
    // Executor credential metadata by environment ID; tokens are never kept.
    executorCredentials: new Map(),
    // How config.json's public_url is set: "public", "local" or "stale".
    installation: address,
    // "none": Core has no credential encryption key, so it cannot store a provider's key.
    credentialKey: credentials !== "none",
    // Startup state and deployment default model provider per harness; API keys are never kept.
    // The demo deployment's default harness has a default model; a fresh install has none.
    harnesses: {
      claude_sdk: { enabled: true, default: false, provider: null },
      codex: { enabled: true, default: true, provider: fresh ? null : { object: "core.model_configuration", harness: "codex", model: "fixture-model", harness_config: {}, model_provider: { protocol: "responses", base_url: "https://model.example/v1", api_key_configured: true }, last_used_at: null, last_error_code: null, last_error_at: null, updated_at: new Date((now - 86400) * 1000).toISOString().replace(/\.\d{3}Z$/, "Z") } },
      mcode: { enabled: false, default: false, provider: null },
    },
    // "none": the deployment is not configured yet, so the Nodes page offers setup.
    deployment: null,
    // Whether the console has its node installation payload, and so serves both installers.
    installers,
    // The providers whose node files the console serves (/console/config node_artifacts).
    nodeArtifacts: artifacts.split(",").filter(Boolean),
  };
  state.deployment = sandbox === "none" ? unconfiguredDeployment() : sandbox === "e2b" ? e2bDeployment() : configuredDeployment();
  // Each node reports the address it enrolled with; in "stale" mode the first one enrolled before public_url changed.
  // The seeded nodes enrolled before Core recorded enrollment IDs.
  state.nodes.forEach((node, index) => { node.core_url = address === "stale" && index === 0 ? OLD_URL : publicUrl(); node.enrollment_id = null; });
  if (sandbox === "e2b") Object.assign(state, { nodes: [], allocations: [] });
}
reset();

function send(response, status, body, headers = {}) {
  response.writeHead(status, { "content-type": "application/json", "cache-control": "no-store", ...headers });
  response.end(JSON.stringify(body));
}
function error(response, status, message, code = null) {
  // Derive `type` as Core's writeError does (services/agents-api/internal/api/errors.go).
  const type = status >= 500 ? "server_error" : status === 409 ? "conflict_error"
    : code === "not_found_error" || code === "invalid_beta" ? code : "invalid_request_error";
  send(response, status, { error: { message, type, code, param: null } });
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

/** The console service's authentication errors: `{ "error": message }`. */
function authError(response, status, message, headers = {}) {
  send(response, status, { error: message }, headers);
}

/** Sign-in with the Core key, as the console server does it: the key is compared first, so the right key
 * always signs in; only wrong keys count, and repeated wrong keys are refused for a while. */
async function login(request, response) {
  const auth = state.auth;
  if (!request.headers["content-type"]?.startsWith("application/json")) return authError(response, 415, "Use application/json");
  let input;
  try { input = await body(request); } catch { return authError(response, 400, "Invalid authentication request"); }
  if (typeof input?.core_key !== "string" || !input.core_key) return authError(response, 400, "Invalid authentication request");
  if (input.core_key !== CORE_KEY) {
    const wait = Math.ceil((auth.lockedUntil - Date.now()) / 1000);
    if (wait > 0) return authError(response, 429, "Too many authentication attempts", { "retry-after": String(wait) });
    auth.failures += 1;
    if (auth.failures < LOCKOUT_AFTER) return authError(response, 401, "Invalid Core key");
    auth.lockedUntil = Date.now() + LOCKOUT_SECONDS * 1000;
    return authError(response, 429, "Too many authentication attempts", { "retry-after": String(LOCKOUT_SECONDS) });
  }
  Object.assign(auth, { mode: "authenticated", failures: 0 });
  return send(response, 200, { mode: "authenticated" }, { "set-cookie": `${SESSION_COOKIE}; Path=/; HttpOnly; SameSite=Strict` });
}

async function consoleRoute(request, response, url) {
  const auth = state.auth;
  if (url.pathname === "/console/auth" && request.method === "GET") {
    const signedIn = auth.mode === "authenticated" && request.headers.cookie?.includes(SESSION_COOKIE);
    return send(response, 200, { mode: signedIn ? "authenticated" : "login" });
  }
  if (url.pathname === "/console/auth/login" && request.method === "POST") return login(request, response);
  if (url.pathname === "/console/auth/logout" && request.method === "POST") {
    auth.mode = "login";
    return send(response, 200, { mode: "login" }, { "set-cookie": `${SESSION_COOKIE.split("=")[0]}=; Path=/; Max-Age=0` });
  }
  if (url.pathname === "/console/installation/domain") {
    if (auth.mode !== "authenticated" || !request.headers.cookie?.includes(SESSION_COOKIE)) return error(response, 401, "Sign in to the console.");
    return domainRoute(request, response, state, { send, error, body });
  }
  if (url.pathname === "/console/config") {
    // Console assets cover node enrollment only; native self-hosted installation is independent.
    const served = state.installers;
    return send(response, 200, {
      node_installer: served, node_installer_sha256: served ? "a".repeat(64) : "",
      node_artifacts: served ? state.nodeArtifacts : [],
    });
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
  if (path === "/installation") return send(response, 200, installation());
  if (path === "/projects") return send(response, 200, { data: a.projects.map(a.publicProject), has_more: false });
  if (path === "/summary") return send(response, 200, a.summary(url));
  if (path === "/metrics") return send(response, 200, coreMetrics(url.searchParams.get("range") ?? "1h"));
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
    if (m[2] === "diagnostics") {
      const failedTurn = (state.turns.get(session.id) ?? []).findLast((turn) => turn.status === "failed");
      return send(response, 200, { object: "core.session_diagnostics", session_id: session.id, status: session.status,
        failure: session.status === "failed" ? { source: failedTurn ? "turn" : "environment_input", ...(failedTurn ? { turn_id: failedTurn.id } : {}), code: "internal_error", params: {}, failed_at: null } : null });
    }
    const diagnostics = m[2].match(/^turns\/([^/]+)\/diagnostics$/);
    if (diagnostics) {
      const turn = (state.turns.get(session.id) ?? []).find((entry) => entry.id === diagnostics[1]);
      if (!turn) return error(response, 404, "No such Turn.");
      return send(response, 200, { object: "core.turn_diagnostics", session_id: session.id, turn_id: turn.id, status: turn.status,
        failure: turn.status === "failed" ? { code: "internal_error", params: {}, failed_at: null } : null,
        items: (state.items.get(session.id) ?? []).filter((item) => item.turn_id === turn.id).slice(0, 1000).map((item) => ({ item_id: item.id, started_at: new Date((turn.started_at ?? turn.created_at) * 1000).toISOString(), completed_at: null, observed_duration_ms: null })),
        items_truncated: false });
    }
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

// A node as Core details it: its last heartbeat and an hour of 60-second host buckets.
function nodeDetail(node) {
  const total = 64 * 2 ** 30;
  const minute = Math.floor(Date.now() / 60_000) * 60;
  const points = Array.from({ length: 60 }, (_, index) => ({
    start: new Date((minute - (60 - index) * 60) * 1000).toISOString(),
    cpu_utilization_max: node.online ? 0.3 + 0.1 * Math.sin(index / 6) : null,
    memory_used_bytes_max: node.online ? total - node.available_memory_bytes : null,
    available_disk_bytes_min: node.available_disk_bytes,
  }));
  return {
    ...node,
    host: { effective_cpu_cores: node.cpu_count, cpu_utilization: node.online ? 0.35 : null, total_memory_bytes: node.online ? total : null, available_memory_bytes: node.available_memory_bytes, available_disk_bytes: node.available_disk_bytes, observed_at: node.last_seen_at },
    history: { resolution_seconds: 60, points },
  };
}

async function sandboxRoute(request, response, path, url) {
  const e2bTemplates = ["/e2b/templates", "/e2b/templates/template/builds"];
  if (e2bTemplates.includes(path)) {
    if (request.method !== "POST") return error(response, 405, "Method not allowed.");
    const input = await body(request);
    const knownEndpoint = [
      ["https://sandbox.sandbase.ai", "sandbox.sandbase.ai"],
      ["https://api.e2b.app", "e2b.app"],
    ].some(([apiURL, domain]) => input.api_url === apiURL && input.domain === domain);
    if (input.api_key !== "fixture-private-key" || !knownEndpoint) return error(response, 400, "Invalid E2B connection.");
    if (path === "/e2b/templates") return send(response, 200, { templates: [{ id: "template", names: ["fixture-runtime"] }] });
    return send(response, 200, { builds: [{ id: "94be54a1-138c-4f30-bc87-b13686272dbe", cpus: 2, memory_mib: 2048 }] });
  }
  // Retired even for authenticated callers; never reinterpret maintenance as reset.
  if (path === "/deployment/maintenance") return error(response, 404, "Not found.");
  if (path === "/deployment/reset" && (request.method === "POST" || request.method === "DELETE")) {
    const input = request.method === "POST" ? await body(request) : { expected_generation: Number(url.searchParams.get("expected_generation")) };
    if (!Number.isInteger(input.expected_generation) || input.expected_generation < 0 || (request.method === "DELETE" && !url.searchParams.has("expected_generation"))) return error(response, 400, "expected_generation is required.", "invalid_request_error");
    if (input.expected_generation !== state.deployment.generation) return error(response, 409, "The sandbox configuration changed. Refresh before submitting again.", "sandbox_generation_stale");
    if (request.method === "DELETE") {
      state.deployment.reset = null;
      return send(response, 200, state.deployment);
    }
    if (input.clear !== "auto" && input.clear !== "force") return error(response, 400, "clear must be auto or force.", "invalid_request_error");
    const seconds = input.deadline_seconds ?? 3600;
    if (!Number.isInteger(seconds) || seconds < 300 || seconds > 86400 || (input.clear === "force" && input.deadline_seconds !== undefined)) return error(response, 400, "Invalid reset deadline.", "invalid_request_error");
    if (state.deployment.reset?.clear === "force" && input.clear === "auto") return error(response, 409, "A force reset cannot return to auto clear.", "sandbox_reset_in_progress");
    if (!state.deployment.provider || state.deployment.reset?.clear === input.clear) return send(response, 200, state.deployment);
    const now = new Date().toISOString();
    const previous = state.deployment.reset;
    state.deployment.reset = {
      clear: input.clear, requested_at: previous?.requested_at ?? now,
      deadline_at: previous?.deadline_at ?? (input.clear === "auto" ? new Date(Date.now() + seconds * 1000).toISOString() : null),
      forced_at: input.clear === "force" ? now : null,
      remaining: previous?.remaining ?? { busy: state.deployment.resources.allocations, idle: state.deployment.resources.pending, cleanup: 0, on_offline_nodes: 0, offline_nodes: [] },
    };
    // The fixture has no cleanup worker or deadline timer. Tests explicitly supply
    // subsequent Core projections; a browser clock never completes a reset.
    return send(response, 200, state.deployment);
  }
  if (path === "/runtime-observations" && request.method === "GET") {
    // Only E2B reports a sandbox's disk.
    const e2b = state.deployment?.provider === "e2b";
    const data = state.admin.runtimeObservations().map((entry) => ({ ...entry, observation: { ...entry.observation, disk: e2b && entry.observation.status === "observed" ? { usage_bytes: 3 * 2 ** 30, limit_bytes: 10 * 2 ** 30 } : null } }));
    return send(response, 200, { object: "list", data, has_more: false, first_id: data[0]?.observation.id ?? null, last_id: data.at(-1)?.observation.id ?? null });
  }
  if (path === "/deployment" && (request.method === "POST" || request.method === "PUT")) {
    const input = await body(request);
    const initialize = request.method === "POST";
    // As Core, before any state check: the address is config.json's public_url and read-only.
    if ("core_url" in input) return error(response, 400, "core_url is derived from the installation public URL (public_url in config.json, OAC_PUBLIC_URL for Core) and cannot be set here. Remove it.", "invalid_request_error", "core_url");
    if (!Number.isInteger(input.expected_generation) || input.expected_generation < 0) return error(response, 400, "expected_generation is required.", "invalid_request_error");
    if (input.expected_generation !== state.deployment.generation) return error(response, 409, "The sandbox configuration changed. Refresh before submitting again.", "sandbox_generation_stale");
    if (state.deployment.reset) return error(response, 409, "A sandbox reset is in progress.", "sandbox_reset_in_progress");
    if (!initialize && input.provider !== state.deployment.provider) return error(response, 409, "Reset before changing the sandbox backend.", "sandbox_reset_required");
    if (initialize && state.deployment.provider) return error(response, 409, "The sandbox deployment is already configured.", "sandbox_deployment_conflict");
    if (!initialize && !state.deployment.provider) return error(response, 409, "The sandbox deployment is not configured.", "sandbox_deployment_conflict");
    const e2b = input.provider === "e2b";
    if (!e2b && (!input.resources || !input.runtime)) return error(response, 400, "resources and runtime are required.", "invalid_sandbox_configuration");
    // As Core (ErrSandboxPublicURLUnreachable): E2B sandboxes reach Core over the internet, which a loopback public_url cannot serve.
    if (e2b && state.installation === "local") return error(response, 409, "E2B sandboxes reach Core over the internet. Set an HTTPS public URL that is not loopback (public_url in config.json, OAC_PUBLIC_URL for Core).", "sandbox_configuration_error");
    // Synthetic classifier outcomes only; never persist or echo submitted keys.
    if (e2b) {
      if (!input.e2b?.template || (initialize && !input.e2b.api_key) || (Object.hasOwn(input.e2b ?? {}, "api_key") && !input.e2b.api_key)) return error(response, 400, "The E2B API key was rejected.", "e2b_api_key_invalid");
      if (input.e2b.api_key === "fixture-other-team-key") return error(response, 409, "This E2B key cannot manage the retained deployment. Reset before changing teams.", "e2b_team_mismatch");
      if (input.e2b.api_key === "fixture-invalid-key") return error(response, 400, "The E2B API key was rejected.", "e2b_api_key_invalid");
    }
    // As Core: E2B may omit resources and adopt its template build's CPU and memory; only microsandbox suspends.
    const resources = input.resources ?? { cpus: templateBuild.resources.cpus, memory_mib: templateBuild.resources.memory_mib };
    const previous = state.deployment;
    const specification = { resources, ...(input.runtime ? { runtime: input.runtime } : {}) };
    const explicitKey = e2b && Object.hasOwn(input.e2b, "api_key");
    const sameSelection = !initialize && JSON.stringify(specification) === JSON.stringify(previous.specification) && (!e2b || input.e2b.template === previous.e2b?.template);
    // Omission can be a no-op; every explicit key, including identical bytes,
    // takes the verified replacement path and advances the target generation.
    if (sameSelection && !explicitKey) return send(response, 200, previous);
    if (!initialize && !e2b) {
      for (const node of state.nodes) node.rollout = { state: node.online ? "preparing" : "unknown", ready_generation: node.rollout.ready_generation };
    }
    const held = initialize ? { allocations: 0, pending: 0 } : previous.resources;
    state.deployment = {
      ...configuredDeployment(), provider: input.provider, mode: e2b ? "direct" : "nodes",
      generation: previous.generation + 1, owner_epoch: previous.owner_epoch,
      resources: held,
      rollout: e2b ? { ...noNodeRollout(), previous_generation_sandboxes: held.allocations + held.pending } : nodeRollout(held.allocations + held.pending),
      specification,
      ...(e2b ? { e2b: { template: input.e2b?.template ?? "", api_url: input.e2b?.api_url ?? state.deployment.e2b?.api_url ?? "https://api.e2b.app", domain: input.e2b?.domain ?? state.deployment.e2b?.domain ?? "e2b.app", credential_configured: true, template_build: templateBuild } } : {}),
      suspension: input.provider === "microsandbox" ? { idle_seconds: 300, retention_seconds: 86400 } : null,
    };
    return send(response, 200, state.deployment);
  }
  if (path === "/deployment") {
    return send(response, 200, state.deployment);
  }
  if (path === "/nodes") return send(response, 200, { data: state.nodes });
  if (path === "/enrollment-tokens" && request.method === "POST") {
    if (state.deployment.reset) return error(response, 409, "A sandbox reset is in progress.", "sandbox_reset_in_progress");
    // As Core: a one-time token valid for ten minutes, whose limits and enrollment ID the node it enrolls takes;
    // only microsandbox keeps a retained limit above the active one.
    const input = await body(request);
    const maxActive = input.max_active ?? 1;
    const serial = state.nextId++;
    const enrollmentId = `00000000-0000-4000-8000-${String(serial).padStart(12, "0")}`;
    state.enrollment = { max_active: maxActive, max_retained: state.deployment?.provider === "microsandbox" ? input.max_retained ?? maxActive : maxActive, enrollment_id: enrollmentId };
    return send(response, 201, { token: `enroll_fixture_${serial}`, expires_at: new Date(Date.now() + 10 * 60_000).toISOString(), enrollment_id: enrollmentId });
  }
  let m;
  if ((m = path.match(/^\/nodes\/([^/]+)\/allocations$/))) return send(response, 200, { data: state.allocations.filter((entry) => entry.node_id === m[1]) });
  if ((m = path.match(/^\/nodes\/([^/]+)$/)) && request.method === "GET") {
    const node = state.nodes.find((entry) => entry.id === m[1]);
    return node ? send(response, 200, nodeDetail(node)) : error(response, 404, "No such node.");
  }
  if ((m = path.match(/^\/nodes\/([^/]+)$/)) && request.method === "PATCH") {
    // As Core: name and both limits together, with a retained limit of at least the active one.
    const input = await body(request);
    const node = state.nodes.find((entry) => entry.id === m[1]);
    if (!node) return error(response, 404, "No such node.");
    if (!input.name?.trim() || !(input.max_active >= 1) || !(input.max_retained >= input.max_active)) return error(response, 400, "Invalid node update.", "invalid_request");
    Object.assign(node, { name: input.name, max_active: input.max_active, max_retained: input.max_retained });
    return send(response, 200, { id: node.id, updated: true });
  }
  if ((m = path.match(/^\/nodes\/([^/]+)$/)) && request.method === "DELETE") {
    const index = state.nodes.findIndex((node) => node.id === m[1]);
    if (index < 0) return error(response, 404, "No such node.");
    state.nodes.splice(index, 1);
    if (state.deployment.mode === "nodes") state.deployment.rollout = nodeRollout(state.deployment.rollout.previous_generation_sandboxes);
    return send(response, 200, { id: m[1], deleted: true });
  }
  return error(response, 404, "Not found.");
}

/** A key ID as Core accepts it: a canonical lowercase UUID other than the nil UUID. */
const keyIdValid = (value) => typeof value === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value) && value !== "00000000-0000-0000-0000-000000000000";
const EXECUTOR_CREDENTIALS = /^\/projects\/([^/]+)\/environments\/([^/]+)\/executor-credentials(?:\/([^/]+))?$/;

/**
 * Executor credentials as Core issues them: only for the self_hosted
 * environment of a Session in the project; the token is returned once; an
 * existing key_id is reissued only with rotate:true, which also restores a
 * revoked one; rotating an unknown key_id is not found; revoking again is safe.
 * An archived project keeps listing and revoking but neither issues nor rotates.
 */
async function executorCredentialRoute(request, response, projectId, environmentId, keyId) {
  const project = state.admin.projects.find((entry) => entry.id === projectId);
  const session = project && state.admin.collections(project.id).sessions.find((entry) => entry.environment.type === "self_hosted" && entry.environment.id === environmentId);
  if (!session) return error(response, 404, "No such self-hosted environment.", "not_found_error");
  if (!state.executorCredentials.has(environmentId)) state.executorCredentials.set(environmentId, []);
  const credentials = state.executorCredentials.get(environmentId);
  if (!keyId && request.method === "GET") return send(response, 200, {
    data: credentials.map((entry) => ({ ...entry })),
    connection: { status: "never_enrolled", bound_key_id: null, enrolled_at: null, last_seen_at: null },
  });
  if (!keyId && request.method === "POST") {
    // As Core, a body that is not JSON is invalid input like any other: 400 with one message.
    const input = await body(request).catch(() => null);
    if (!keyIdValid(input?.key_id) || (input.rotate !== undefined && typeof input.rotate !== "boolean")) return error(response, 400, "Invalid resource identifier or request limits.", "invalid_request");
    if (project.archived_at) return error(response, 409, "The target Project is archived.", "project_archived");
    const existing = credentials.find((entry) => entry.key_id === input.key_id);
    if (existing && input.rotate !== true) return error(response, 409, "The executor credential exists; rotate it instead.", "executor_credential_exists");
    if (!existing && input.rotate === true) return error(response, 404, "No such executor credential.", "not_found_error");
    // As Core: rotating restores a revoked key.
    if (existing) existing.revoked_at = null;
    if (!existing) credentials.push({ key_id: input.key_id, created_at: new Date().toISOString(), revoked_at: null });
    return send(response, 201, { key_id: input.key_id, environment_id: environmentId, executor_token: `exec_fixture_${state.nextId++}` });
  }
  if (keyId && request.method === "DELETE") {
    const existing = credentials.find((entry) => entry.key_id === keyId);
    if (!existing) return error(response, 404, "No such executor credential.", "not_found_error");
    existing.revoked_at ??= new Date().toISOString();
    response.writeHead(204, { "cache-control": "no-store" });
    return response.end();
  }
  return error(response, 404, "Not found.");
}

/**
 * A node as it first registers with the last enrollment command: not yet connected,
 * provider not ready, with the token's limits and enrollment ID. Like Core, it has no
 * diagnostic field until one is reported.
 */
function registeredNode(nodeId) {
  const { enrollment_id = null, ...limits } = state.enrollment ?? { max_active: 1, max_retained: 1 };
  return { rollout: { state: "unknown", ready_generation: null }, id: nodeId, name: nodeId, provider: state.deployment?.provider ?? "docker", core_url: publicUrl(), enrollment_id, online: false, provider_ready: false, cpu_count: null, available_memory_bytes: null, available_disk_bytes: null, running: 0, snapshots: 0, last_seen_at: null, ...limits, active: 0, reserved: 0, retained: 0, cleanup_pending: 0, created_at: new Date().toISOString() };
}

const HARNESS_PROVIDER = /^\/harnesses\/([^/]+)\/model-configuration$/;
const PROVIDER_FIELDS = new Set(["protocol", "base_url", "api_key", "context_window", "max_output_tokens"]);
/** Core's one message for a body that is not a complete provider; it never echoes a value. */
const PROVIDER_SHAPE = "The body must be a complete model provider: protocol, base_url, api_key and optional nonnegative context_window and max_output_tokens.";
const httpsBase = (value) => { try { const url = new URL(value); return url.protocol === "https:" && Boolean(url.hostname) && !url.username && !url.password && !url.search && !url.hash; } catch { return false; } };
/** An omitted limit, or a nonnegative integer that fits Core's int32. */
const tokenLimit = (value) => value === undefined || (Number.isInteger(value) && value >= 0 && value <= 2 ** 31 - 1);
const jsonKind = (value) => Array.isArray(value) ? "an array" : typeof value === "string" ? "a string" : typeof value === "boolean" ? "a boolean" : Number.isInteger(value) ? "an integer" : "a number";

/** The first rule a provider body breaks, in Core's order and words; null when valid. */
function providerProblem(harness, input) {
  if (Object.keys(input).some((key) => !PROVIDER_FIELDS.has(key)) || ["protocol", "base_url", "api_key"].some((key) => typeof input[key] !== "string") ||
    !tokenLimit(input.context_window) || !tokenLimit(input.max_output_tokens)) return PROVIDER_SHAPE;
  if (!httpsBase(input.base_url)) return "model provider requires an HTTPS base_url without credentials, query or fragment";
  if (!["anthropic", "responses", "chat_completions"].includes(input.protocol)) return "unsupported model provider protocol";
  if (!input.api_key.trim() || Buffer.byteLength(input.api_key) > 16384 || /[\0\r\n]/.test(input.api_key)) return "invalid model provider API key";
  if ((input.max_output_tokens ?? 0) > (input.context_window ?? 0)) return "invalid model token limits";
  if (harness === "mcode" && !(input.context_window > 0 && input.max_output_tokens > 0)) return "selected harness requires positive model context_window and max_output_tokens";
  return null;
}

/**
 * Harnesses and their deployment default model providers, as Core serves them
 * (services/agents-api/internal/api/harness_model_providers.go): the list
 * reflects every write; an unknown harness or an unset provider is 404; PUT
 * takes a JSON object of at most 32 KiB, is a full replacement that needs the
 * key every time (mcode also both limits) and answers with the safe view;
 * DELETE is 204 and safe to repeat. A disabled harness may still be configured.
 */
async function harnessRoute(request, response, path) {
  const support = (id) => ({ protocols: id === "codex" ? ["responses"] : id === "claude_sdk" ? ["anthropic"] : ["anthropic", "responses", "chat_completions"], accepts_harness_config: id !== "mcode", token_limits_required: id === "mcode" });
  const view = (id) => ({ model_configuration_support: support(id), object: "core.harness", id, enabled: state.harnesses[id].enabled, default: state.harnesses[id].default, model_configuration: state.harnesses[id].provider });
  if (path === "/harnesses" && request.method === "GET") return send(response, 200, { object: "list", data: Object.keys(state.harnesses).map(view) });
  const match = path.match(HARNESS_PROVIDER);
  const harness = match && Object.hasOwn(state.harnesses, match[1]) ? match[1] : null;
  if (!harness) return error(response, 404, "This harness does not exist.", "not_found");
  const entry = state.harnesses[harness];
  if (request.method === "GET") return entry.provider ? send(response, 200, entry.provider) : error(response, 404, "This harness has no deployment default model provider.", "not_found");
  if (request.method === "DELETE") {
    entry.provider = null;
    response.writeHead(204, { "cache-control": "no-store" });
    return response.end();
  }
  if (request.method !== "PUT") return error(response, 405, "Method not allowed.");
  // Core's shared JSON body gate, with this route's 32 KiB limit.
  if (!/^application\/json\s*(;|$)/i.test(request.headers["content-type"] ?? "")) return error(response, 400, "expected request with Content-Type: application/json", "invalid_request_error");
  const chunks = [];
  for await (const chunk of request) chunks.push(chunk);
  const raw = Buffer.concat(chunks);
  if (raw.length > 32 * 1024) return error(response, 413, "Request exceeds 32 KiB.", "request_too_large");
  let input;
  try { input = raw.length ? JSON.parse(raw.toString("utf8")) : null; } catch {
    return error(response, 400, "Invalid body: failed to parse JSON value. Please check the value to ensure it is valid JSON. (Common errors include trailing commas, missing closing brackets, missing quotation marks, etc.)", "invalid_request_error");
  }
  input ??= {};
  if (typeof input !== "object" || Array.isArray(input)) return error(response, 400, `Invalid type: expected an object, but got ${jsonKind(input)} instead.`, "invalid_request_error");
  if (typeof input.model !== "string" || !input.model.trim()) return error(response, 400, "A model ID is required.", "model_configuration_model_invalid");
  if (input.harness_config !== undefined && (!input.harness_config || typeof input.harness_config !== "object" || Array.isArray(input.harness_config))) return error(response, 400, "Harness configuration must be an object.", "harness_config_invalid");
  if (!support(harness).protocols.includes(input.model_provider?.protocol)) return error(response, 400, "This harness does not support this protocol.", "model_provider_protocol_unsupported");
  const problem = providerProblem(harness, input.model_provider ?? {});
  if (problem) return error(response, 400, problem, "invalid_request_error");
  // As Core's error mapping: sealing the key needs the credential encryption key.
  if (!state.credentialKey) return error(response, 503, "Credential encryption is not configured on this service.", "credential_storage_unavailable");
  entry.provider = {
    object: "core.model_configuration", harness, model: input.model, harness_config: input.harness_config ?? {},
    model_provider: { protocol: input.model_provider.protocol, base_url: input.model_provider.base_url, api_key_configured: true,
      ...(input.model_provider.context_window ? { context_window: input.model_provider.context_window } : {}),
      ...(input.model_provider.max_output_tokens ? { max_output_tokens: input.model_provider.max_output_tokens } : {}),
    },
    last_used_at: null, last_error_code: null, last_error_at: null,
    updated_at: new Date().toISOString().replace(/\.\d{3}Z$/, "Z"),
  };
  return send(response, 200, entry.provider);
}

/** Test controls: reset state, inject one failure, register or change a node, and read what the browser sent. */
async function fixtureRoute(request, response, url) {
  if (url.pathname === "/__fixture/domain" && request.method === "POST") {
    Object.assign(state.domain, await body(request));
    return send(response, 200, state.domain);
  }
  if (url.pathname === "/__fixture/health") return send(response, 200, { ok: true });
  if (url.pathname === "/__fixture/reset" && request.method === "POST") {
    reset(url.searchParams.get("auth") ?? "login", url.searchParams.get("projects") === "none", url.searchParams.get("sandbox") ?? "configured", url.searchParams.get("nodes") ?? "demo", url.searchParams.get("installation") ?? "public", url.searchParams.get("credentials") ?? "configured", url.searchParams.get("installers") !== "none", url.searchParams.get("artifacts") ?? undefined);
    return send(response, 200, { ok: true });
  }
  if (url.pathname === "/__fixture/deployment" && request.method === "POST") {
    // Explicit backend observations, never a simulation driven by browser time.
    const input = await body(request);
    if (input.complete_reset) {
      state.deployment = unconfiguredDeployment(state.deployment.generation + 1, state.deployment.owner_epoch + 1);
      state.nodes = [];
      state.allocations = [];
      state.enrollment = null;
    } else Object.assign(state.deployment, input);
    return send(response, 200, state.deployment);
  }
  if (url.pathname === "/__fixture/fail-next" && request.method === "POST") {
    state.failNext = await body(request); // { method, path, status, code?, message? }
    return send(response, 200, { ok: true });
  }
  if (url.pathname === "/__fixture/node" && request.method === "POST") {
    // { id, ...fields }: registers the node on first use, then applies the fields (online, provider_ready, diagnostic,
    // or another command's enrollment_id); an empty diagnostic removes the field, as Core omits it.
    const { id: nodeId, ...fields } = await body(request);
    let node = state.nodes.find((entry) => entry.id === nodeId);
    if (!node) state.nodes.push(node = registeredNode(nodeId));
    Object.assign(node, fields);
    // A control may supply an explicit target observation. Existing enrollment
    // cases only supply connection facts, so give that newly joined target a pin.
    if (!fields.rollout && ("online" in fields || "provider_ready" in fields || "diagnostic" in fields)) {
      node.rollout = !node.online ? { state: "unknown", ready_generation: node.rollout.ready_generation }
        : node.provider_ready ? { state: "ready", ready_generation: state.deployment.generation }
        : { state: "failed", ready_generation: node.rollout.ready_generation, diagnostic: node.diagnostic || "provider_unavailable" };
    }
    if (state.deployment.mode === "nodes") state.deployment.rollout = nodeRollout(state.deployment.rollout.previous_generation_sandboxes);
    if (!node.diagnostic) delete node.diagnostic;
    return send(response, 200, node);
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
    if (url.pathname.startsWith("/core/v1/sandbox/")) return await sandboxRoute(request, response, url.pathname.slice("/core/v1/sandbox".length), url);
    if (url.pathname.startsWith("/core/v1/")) {
      const path = url.pathname.slice("/core/v1".length);
      if (path === "/harnesses" || path.startsWith("/harnesses/")) return await harnessRoute(request, response, path);
      const installation = path.match(/^\/projects\/([^/]+)\/environments\/([^/]+)\/installation$/);
      if (installation && request.method === "GET") return send(response, 200, { status: "available", version: "fixture", expires_at: Math.floor(Date.now()/1000)+1800, commands: { posix: "bash fixture-bootstrap --authorization fixture-short-lived", powershell: "& fixture-bootstrap.ps1 -Authorization fixture-short-lived" } });
      const credentials = path.match(EXECUTOR_CREDENTIALS);
      if (credentials) return await executorCredentialRoute(request, response, credentials[1], credentials[2], credentials[3]);
      return write ? await adminWrite(request, response, path) : adminRead(response, path, url);
    }
    return error(response, 404, "Not found.");
  } catch (caught) {
    error(response, 500, String(caught));
  }
}).listen(port, "127.0.0.1", () => console.log(`Console acceptance fixture on http://127.0.0.1:${port}`));
