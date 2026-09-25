// Browser acceptance fixture: the console service's routes (/console/**) and the
// Core management tree it forwards (/core/v1/**: installation, projects, summary,
// audit log, metrics and /core/v1/sandbox/**), with synthetic, deterministic data and
// in-memory writes. It never serves /v1; any /v1 request, and any
// browser-supplied Authorization header, is recorded so a test can assert that
// the console stays on its management boundary.
import http from "node:http";

import { buildAdmin } from "./data/admin.mjs";
import { coreMetrics } from "./data/core-metrics.mjs";
import { buildResources } from "./data/resources.mjs";
import { buildDemo } from "./data/routes.mjs";

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
const release = { source_commit: "c0ffee".padEnd(40, "0"), image_id: `sha256:${hex("1")}`, image_manifest_digest: `sha256:${hex("2")}`, microsandbox_ref: `parsar-core-runtime@sha256:${hex("3")}`, runtime_sha256: hex("4"), firmware_sha256: hex("5") };
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
      path: "/opt/parsar/config.json", apply_command: "sudo parsar apply", applied_at: "2026-09-24T09:30:00Z",
      settings: [
        setting("public_url", publicUrl(), LOCAL_URL, ["core", "web"]),
        setting("listen_address", "127.0.0.1:8091", "127.0.0.1:8091", ["core"]),
        setting("web_listen_address", "127.0.0.1:4173", "127.0.0.1:4173", ["web"]),
        setting("data_dir", "/var/lib/parsar", "/var/lib/parsar", [], { changeable: false }),
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

function configuredDeployment() {
  return { installation_id: INSTALLATION_ID, provider: "docker", core_url: publicUrl(), maintenance: false, owner_epoch: 3, generation: 1, mode: "nodes", resources: { allocations: 0, pending: 0 }, specification: { resources: { cpus: 2, memory_mib: 4096 }, runtime: release }, specification_digest: "fixture", suspension: null };
}
/** The E2B template build as Core read it when the selection was saved. */
const templateBuild = { status: "ready", resources: { cpus: 2, memory_mib: 2048, root_disk_mib: 10240 } };

// E2B runs sandboxes in its cloud: no nodes, only what Core holds there.
function e2bDeployment() {
  return { ...configuredDeployment(), provider: "e2b", mode: "direct", resources: { allocations: 3, pending: 1 }, specification: { resources: { cpus: 2, memory_mib: 2048 } }, e2b: { template: "parsar-runtime:0f1e2d3c-4b5a-6978-8a9b-0c1d2e3f4a5b", credential_configured: true, template_build: templateBuild } };
}

function reset(mode = "login", fresh = false, sandbox = "configured", nodes = "demo", address = "public") {
  const base = buildDemo();
  const now = Math.floor(Date.now() / 1000);
  const resources = buildResources(now, base.agents, base.sessions);
  const admin = buildAdmin(now, base, resources);
  // A fresh install: no project, Session or Runtime yet; Getting started leads.
  if (fresh) for (const list of [admin.projects, base.sessions, base.observations, base.allocations]) list.splice(0);
  // "none": no node has enrolled yet.
  if (nodes === "none") base.nodes.splice(0);
  state = {
    ...base, resources, admin,
    // "authenticated": the console holds a session for the fixture cookie; "login": it holds none.
    auth: { mode: mode === "authenticated" ? "authenticated" : "login", failures: 0, lockedUntil: 0 },
    violations: [], writes: [], failNext: null, nextId: 1,
    // Executor credential metadata by environment ID; tokens are never kept.
    executorCredentials: new Map(),
    // How config.json's public_url is set: "public", "local" or "stale".
    installation: address,
    // "none": the deployment is not configured yet, so the Nodes page offers setup.
    deployment: null,
  };
  state.deployment = sandbox === "none" ? null : sandbox === "e2b" ? e2bDeployment() : configuredDeployment();
  // Each node reports the address it enrolled with; in "stale" mode the first one enrolled before public_url changed.
  state.nodes.forEach((node, index) => { node.core_url = address === "stale" && index === 0 ? OLD_URL : publicUrl(); });
  if (sandbox === "e2b") Object.assign(state, { nodes: [], allocations: [] });
}
reset();

function send(response, status, body, headers = {}) {
  response.writeHead(status, { "content-type": "application/json", "cache-control": "no-store", ...headers });
  response.end(JSON.stringify(body));
}
function error(response, status, message, code = null, param = null) {
  // Derive `type` as Core's writeError does (services/agents-api/internal/api/errors.go).
  const type = status >= 500 ? "server_error" : status === 409 ? "conflict_error"
    : code === "not_found_error" || code === "invalid_beta" ? code : "invalid_request_error";
  send(response, status, { error: { message, type, code, param } });
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
  if (url.pathname === "/console/config") {
    // Signing in grants administration, so the console reports only its node installer.
    return send(response, 200, { node_installer: true, node_installer_sha256: "a".repeat(64) });
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

async function sandboxRoute(request, response, path) {
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
    if ("core_url" in input) return error(response, 400, "core_url is derived from the installation public URL (public_url in config.json, AGENTS_API_PUBLIC_URL for Core) and cannot be set here. Remove it.", "invalid_request_error", "core_url");
    if (initialize && state.deployment) return error(response, 409, "The sandbox deployment is already configured.", "sandbox_deployment_conflict");
    if (!initialize && !state.deployment) return error(response, 409, "The sandbox deployment is not configured.", "sandbox_deployment_conflict");
    const e2b = input.provider === "e2b";
    if (!e2b && (!input.resources || !input.runtime)) return error(response, 400, "resources and runtime are required.", "invalid_sandbox_configuration");
    // As Core (ErrSandboxPublicURLUnreachable): E2B sandboxes reach Core over the internet, which a loopback public_url cannot serve.
    if (e2b && state.installation === "local") return error(response, 409, "E2B sandboxes reach Core over the internet. Set an HTTPS public URL that is not loopback (public_url in config.json, AGENTS_API_PUBLIC_URL for Core).", "sandbox_configuration_error");
    // As Core: E2B may omit resources and adopt its template build's CPU and memory; only microsandbox suspends.
    const resources = input.resources ?? { cpus: templateBuild.resources.cpus, memory_mib: templateBuild.resources.memory_mib };
    state.deployment = {
      ...configuredDeployment(), provider: input.provider, mode: e2b ? "direct" : "nodes",
      ...(initialize ? {} : { maintenance: true, generation: state.deployment.generation + 1 }),
      specification: { resources, ...(input.runtime ? { runtime: input.runtime } : {}) },
      ...(e2b ? { e2b: { template: input.e2b?.template ?? "", credential_configured: true, template_build: templateBuild } } : {}),
      suspension: input.provider === "microsandbox" ? { idle_seconds: 300, retention_seconds: 86400 } : null,
    };
    return send(response, 200, state.deployment);
  }
  if (path === "/deployment") {
    return send(response, 200, state.deployment ?? { installation_id: INSTALLATION_ID, provider: "", core_url: publicUrl(), maintenance: false, owner_epoch: 3, generation: 0, mode: "", resources: { allocations: 0, pending: 0 }, suspension: null });
  }
  if (path === "/nodes") return send(response, 200, { data: state.nodes });
  if (path === "/enrollment-tokens" && request.method === "POST") {
    // As Core: a one-time token valid for ten minutes, whose limits the node it enrolls takes;
    // only microsandbox keeps a retained limit above the active one.
    const input = await body(request);
    const maxActive = input.max_active ?? 1;
    state.enrollmentLimits = { max_active: maxActive, max_retained: state.deployment?.provider === "microsandbox" ? input.max_retained ?? maxActive : maxActive };
    return send(response, 201, { token: `enroll_fixture_${state.nextId++}`, expires_at: new Date(Date.now() + 10 * 60_000).toISOString() });
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
  if (!keyId && request.method === "GET") return send(response, 200, { data: credentials.map((entry) => ({ ...entry })) });
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
 * provider not ready, with the token's limits. Like Core, it has no diagnostic
 * field until one is reported.
 */
function registeredNode(nodeId) {
  const limits = state.enrollmentLimits ?? { max_active: 1, max_retained: 1 };
  return { id: nodeId, name: nodeId, provider: state.deployment?.provider ?? "docker", core_url: publicUrl(), online: false, provider_ready: false, cpu_count: null, available_memory_bytes: null, available_disk_bytes: null, running: 0, snapshots: 0, last_seen_at: null, ...limits, active: 0, reserved: 0, retained: 0, cleanup_pending: 0, created_at: new Date().toISOString() };
}

/** Test controls: reset state, inject one failure, register or change a node, and read what the browser sent. */
async function fixtureRoute(request, response, url) {
  if (url.pathname === "/__fixture/health") return send(response, 200, { ok: true });
  if (url.pathname === "/__fixture/reset" && request.method === "POST") {
    reset(url.searchParams.get("auth") ?? "login", url.searchParams.get("projects") === "none", url.searchParams.get("sandbox") ?? "configured", url.searchParams.get("nodes") ?? "demo", url.searchParams.get("installation") ?? "public");
    return send(response, 200, { ok: true });
  }
  if (url.pathname === "/__fixture/fail-next" && request.method === "POST") {
    state.failNext = await body(request); // { method, path, status, code?, message? }
    return send(response, 200, { ok: true });
  }
  if (url.pathname === "/__fixture/node" && request.method === "POST") {
    // { id, ...fields }: registers the node on first use, then applies the fields (online, provider_ready, diagnostic);
    // an empty diagnostic removes the field, as Core omits it.
    const { id: nodeId, ...fields } = await body(request);
    let node = state.nodes.find((entry) => entry.id === nodeId);
    if (!node) state.nodes.push(node = registeredNode(nodeId));
    Object.assign(node, fields);
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
    if (url.pathname.startsWith("/core/v1/sandbox/")) return await sandboxRoute(request, response, url.pathname.slice("/core/v1/sandbox".length));
    if (url.pathname.startsWith("/core/v1/")) {
      const path = url.pathname.slice("/core/v1".length);
      const credentials = path.match(EXECUTOR_CREDENTIALS);
      if (credentials) return await executorCredentialRoute(request, response, credentials[1], credentials[2], credentials[3]);
      return write ? await adminWrite(request, response, path) : adminRead(response, path, url);
    }
    return error(response, 404, "Not found.");
  } catch (caught) {
    error(response, 500, String(caught));
  }
}).listen(port, "127.0.0.1", () => console.log(`Console acceptance fixture on http://127.0.0.1:${port}`));

