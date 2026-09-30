import { AgentCoreError } from "./client";
import { CoreRequester, type CoreClientOptions } from "./core-request";
import { hasOwn, isNonnegativeInteger, isRecord, onlyFields, sameResourceId } from "./response-projection";
import type { ReadOptions } from "./types";

export type SandboxDiagnostic = "" | "node_unavailable" | "resource_missing" | "compute_unconfirmed" | "ownership_mismatch" | "provider_unavailable";
/** Checked against Core's shared node-diagnostics.json fixture. */
export const sandboxNodeDiagnostics = [
  "provider_unavailable",
  "docker_unavailable",
  "docker_limits_unsupported",
  "runtime_download_failed",
  "runtime_image_unavailable",
  "kvm_unavailable",
  "microsandbox_artifacts_unavailable",
  "capacity_insufficient",
] as const;
/** Fixed reason a node's provider is not ready. Core omits the field while the provider is ready, so read it as falsy (undefined) then. The client reads an unknown future value as provider_unavailable. */
export type SandboxNodeDiagnostic = "" | typeof sandboxNodeDiagnostics[number];
const nodeDiagnostics: ReadonlySet<string> = new Set(sandboxNodeDiagnostics);

/** Keep a known readiness cause; never expose unclassified node-supplied text. */
export function normalizeSandboxNodeDiagnostic(value: string): Exclude<SandboxNodeDiagnostic, ""> {
  return nodeDiagnostics.has(value) ? value as Exclude<SandboxNodeDiagnostic, ""> : "provider_unavailable";
}

export type SandboxProvider = "docker" | "microsandbox" | "e2b";
/** CPU and MiB limits for each sandbox, not node concurrency. */
export interface SandboxResources { cpus: number; memory_mib: number; root_disk_mib?: number; environment_disk_mib?: number }
export interface SandboxRuntimeRelease { source_commit: string; image_id: string; image_manifest_digest: string; microsandbox_ref: string; runtime_sha256: string; firmware_sha256: string }
export interface SandboxSpecification { resources: SandboxResources; runtime?: SandboxRuntimeRelease }
/** Core derives the deployment's address from the installation public URL; a `core_url` member is rejected. */
export interface InitializeSandboxDeployment {
  provider: SandboxProvider;
  /** Required, including zero for first setup; read it from GET before submitting once. */
  expected_generation: number;
  /** Required for Docker/microsandbox. E2B may omit it to adopt its validated template build's CPU and memory. */
  resources?: SandboxResources;
  /** Required for Docker/microsandbox; E2B uses its fixed template build. */
  runtime?: SandboxRuntimeRelease;
  configuration?: { template?: string; api_url?: string; domain?: string };
  /** Write-only. Omission on update preserves the current credential. */
  credential?: { api_key: string };
}
export interface UpdateSandboxDeployment extends InitializeSandboxDeployment {}
export interface SandboxRollout {
  /** Poll at high frequency only while preparing, independently of old Session retention. */
  state: "settled" | "preparing";
  previous_generation_sandboxes: number;
  nodes: { ready: number; preparing: number; failed: number; update_required: number; unknown: number } | null;
}
export interface SandboxNodeRollout {
  /** Target preparation; unknown/failed/preparing does not invalidate a qualified old serving pin. */
  state: "ready" | "preparing" | "failed" | "update_required" | "unknown";
  /** Durable serving pin; this alone does not imply current connection readiness. */
  ready_generation: number | null;
  diagnostic?: SandboxNodeDiagnostic;
}
export interface StartSandboxReset { expected_generation: number; clear: "auto" | "force"; deadline_seconds?: number }
export interface SandboxReset {
  clear: "auto" | "force";
  requested_at: string;
  deadline_at: string | null;
  forced_at: string | null;
  remaining: {
    busy: number; idle: number; cleanup: number; on_offline_nodes: number;
    offline_nodes: Array<{ node_id: string; name: string; resources: number }>;
  };
}

export interface SandboxDeployment {
  rollout: SandboxRollout;
  specification?: SandboxSpecification;
  specification_digest?: string;
  installation_id: string;
  provider: SandboxProvider | "";
  /** Read-only: the installation public URL, which nodes and sandboxes use to reach Core. Present before configuration. */
  readonly core_url: string;
  reset: SandboxReset | null;
  owner_epoch: number;
  generation: number;
  mode: "nodes" | "direct" | "";
  resources: { allocations: number; pending: number };
  configuration?: { template?: string; api_url?: string; domain?: string };
  metadata?: { template_build?: SandboxE2BTemplateBuild };
  credential_configured: boolean;
  /** Idle suspension policy; microsandbox only, otherwise null. */
  suspension: { idle_seconds: number; retention_seconds: number } | null;
}
/** The fixed E2B build as Core read it when the selection was saved; unknown values are null. */
export interface SandboxE2BTemplateBuild {
  status: string | null;
  resources: { cpus: number | null; memory_mib: number | null; root_disk_mib: number | null };
}
export interface SandboxE2BDiscoveryInput { api_key: string; api_url?: string; domain?: string }
export interface SandboxE2BTemplate { id: string; names: string[] }
export interface SandboxE2BReadyBuild { id: string; cpus: number; memory_mib: number }
export interface SandboxNode {
  rollout: SandboxNodeRollout;
  id: string;
  name: string;
  provider: string;
  online: boolean;
  /** Last provider report; combine with online. Target rollout state is independent of serving readiness. */
  provider_ready: boolean;
  /** Absent while the provider is ready. */
  diagnostic?: SandboxNodeDiagnostic;
  cpu_count: number | null;
  available_memory_bytes: number | null;
  available_disk_bytes: number | null;
  running: number;
  snapshots: number;
  last_seen_at: string | null;
  max_active: number;
  max_retained: number;
  active: number;
  reserved: number;
  retained: number;
  cleanup_pending: number;
  created_at: string;
  /** Read-only: the Core address this node enrolled with. When it differs from the installation public URL, the node receives no new sandboxes and must be re-added. */
  readonly core_url: string;
  /** Read-only: the `enrollment_id` of the command that registered this node; null for nodes enrolled before Core recorded it. Core always sends the member. */
  readonly enrollment_id: string | null;
}
/** A one-time node enrollment command issued by Core. */
export interface SandboxEnrollment {
  /** The one-use secret the node registers with. */
  token: string;
  expires_at: string;
  /** Public, non-secret handle of this command; never a credential. The node it registers reports the same `enrollment_id`. */
  enrollment_id: string;
}
/** The node machine's last heartbeat observation; unavailable measurements are null. */
export interface SandboxNodeHost {
  effective_cpu_cores: number | null;
  /** Busy share of the whole host's CPU between heartbeats, 0–1. */
  cpu_utilization: number | null;
  total_memory_bytes: number | null;
  available_memory_bytes: number | null;
  available_disk_bytes: number | null;
  observed_at: string | null;
}
export interface SandboxNodeHostPoint {
  start: string;
  cpu_utilization_max: number | null;
  memory_used_bytes_max: number | null;
  available_disk_bytes_min: number | null;
}
export type SandboxNodeHistoryRange = "1h" | "6h" | "24h";
/** One node with its host observation and complete UTC buckets of host history. */
export interface SandboxNodeDetail extends SandboxNode {
  host: SandboxNodeHost;
  history: { resolution_seconds: number; points: SandboxNodeHostPoint[] };
}
/** A node's name and sandbox limits; Core takes all three, with a retained limit of at least the active one. Under Docker, Core sets the retained limit to the active one. */
export interface SandboxNodeUpdate {
  name: string;
  max_active: number;
  max_retained: number;
}
export interface SandboxAllocation {
  deployment_generation: number;
  id: string;
  node_id: string;
  tenant_id: string;
  session_id: string;
  environment_id: string;
  state: string;
  compute_phase: string;
  /** When the allocation entered its current compute_phase, or null when unknown; an allocation that existed before Core recorded it reports null until its next phase change. */
  compute_phase_changed_at: string | null;
  diagnostic: SandboxDiagnostic;
  initialization: string;
  created_at: string;
}
function invalidSandboxResponse(): never {
  throw new AgentCoreError("Core returned an invalid sandbox administration response.", 502, "invalid_admin_response");
}

/**
 * The object has every required member, optional ones only where listed, and nothing else. The projections below
 * follow Core's store.RuntimeDeploymentView, RuntimeNode, RuntimeNodeDetail and RuntimeNodeAllocation serialization.
 */
function members(value: unknown, required: readonly string[], optional: readonly string[] = []): Record<string, unknown> {
  if (!isRecord(value) || !required.every((field) => hasOwn(value, field)) || !onlyFields(value, new Set([...required, ...optional]))) return invalidSandboxResponse();
  return value;
}
function valid(condition: boolean): void {
  if (!condition) invalidSandboxResponse();
}
const timestamp = (value: unknown) => typeof value === "string" && Number.isFinite(Date.parse(value));
const measure = (value: unknown) => typeof value === "number" && Number.isFinite(value) && value >= 0;
const nullable = (test: (value: unknown) => boolean) => (value: unknown) => value === null || test(value);
const strings = (value: Record<string, unknown>, fields: readonly string[]) => fields.every((field) => typeof value[field] === "string");

const providers = new Set(["", "docker", "microsandbox", "e2b"]);
const modes = new Set(["", "nodes", "direct"]);
const releaseFields = ["source_commit", "image_id", "image_manifest_digest", "microsandbox_ref", "runtime_sha256", "firmware_sha256"];
function projectSpecification(value: unknown): SandboxSpecification {
  const specification = members(value, ["resources"], ["runtime"]);
  const resources = members(specification.resources, ["cpus", "memory_mib"], ["root_disk_mib", "environment_disk_mib"]);
  valid(Object.values(resources).every(isNonnegativeInteger));
  if (!hasOwn(specification, "runtime")) return { resources: { ...resources } as unknown as SandboxResources };
  const runtime = members(specification.runtime, releaseFields);
  valid(strings(runtime, releaseFields));
  return { resources: { ...resources } as unknown as SandboxResources, runtime: { ...runtime } as unknown as SandboxRuntimeRelease };
}
/** The adapter's public projection has no credential member. */
function projectE2B(configuration: unknown, metadata: unknown): Pick<SandboxDeployment, "configuration" | "metadata"> {
  const config = members(configuration, ["template", "api_url", "domain"]);
  valid(strings(config, ["template", "api_url", "domain"]));
  const facts = members(metadata, [], ["template_build"]);
  if (!hasOwn(facts, "template_build")) return { configuration: { ...config }, metadata: {} };
  const build = members(facts.template_build, ["status", "resources"]);
  const resources = members(build.resources, ["cpus", "memory_mib", "root_disk_mib"]);
  valid((build.status === null || typeof build.status === "string") && Object.values(resources).every(nullable(isNonnegativeInteger)));
  return { configuration: { ...config }, metadata: { template_build: { status: build.status as string | null, resources: { ...resources } as SandboxE2BTemplateBuild["resources"] } } };
}
/** Counts and blocker identities are one Core snapshot, never reconstructed from node lists. */
function projectReset(value: unknown, held: number): SandboxReset | null {
  if (value === null) return null;
  const reset = members(value, ["clear", "requested_at", "deadline_at", "forced_at", "remaining"]);
  const remaining = members(reset.remaining, ["busy", "idle", "cleanup", "on_offline_nodes", "offline_nodes"]);
  valid((reset.clear === "auto" || reset.clear === "force") && timestamp(reset.requested_at) &&
    nullable(timestamp)(reset.deadline_at) && nullable(timestamp)(reset.forced_at) &&
    (reset.clear === "auto" ? reset.deadline_at !== null && reset.forced_at === null : reset.forced_at !== null) &&
    [remaining.busy, remaining.idle, remaining.cleanup, remaining.on_offline_nodes].every(isNonnegativeInteger) && Array.isArray(remaining.offline_nodes));
  const nodes = (remaining.offline_nodes as unknown[]).map(value => {
    const node = members(value, ["node_id", "name", "resources"]);
    valid(strings(node, ["node_id", "name"]) && node.node_id !== "" && isNonnegativeInteger(node.resources) && Number(node.resources) > 0);
    return { ...node } as SandboxReset["remaining"]["offline_nodes"][number];
  });
  valid(Number(remaining.busy) + Number(remaining.idle) + Number(remaining.cleanup) === held &&
    Number(remaining.on_offline_nodes) <= held && nodes.reduce((sum, node) => sum + node.resources, 0) === remaining.on_offline_nodes &&
    new Set(nodes.map(node => node.node_id)).size === nodes.length);
  return { ...reset, remaining: { ...remaining, offline_nodes: nodes } } as unknown as SandboxReset;
}
function projectRollout(value: unknown, mode: unknown, held: number): SandboxRollout {
  const rollout = members(value, ["state", "previous_generation_sandboxes", "nodes"]);
  valid((rollout.state === "settled" || rollout.state === "preparing") && isNonnegativeInteger(rollout.previous_generation_sandboxes) && Number(rollout.previous_generation_sandboxes) <= held);
  const nodes = rollout.nodes === null ? null : members(rollout.nodes, ["ready", "preparing", "failed", "update_required", "unknown"]);
  valid((nodes !== null) === (mode === "nodes") && (nodes === null || Object.values(nodes).every(isNonnegativeInteger)) && (rollout.state === "preparing") === (nodes !== null && Number(nodes.preparing) > 0));
  return { ...rollout, nodes: nodes && { ...nodes } } as unknown as SandboxRollout;
}
function projectNodeRollout(value: unknown, online: unknown): SandboxNodeRollout {
  const rollout = members(value, ["state", "ready_generation"], ["diagnostic"]);
  valid(["ready", "preparing", "failed", "update_required", "unknown"].includes(rollout.state as string) && nullable(isNonnegativeInteger)(rollout.ready_generation) &&
    (online !== false || rollout.state === "unknown") && (rollout.state !== "ready" || rollout.ready_generation !== null) &&
    (rollout.diagnostic === undefined || (typeof rollout.diagnostic === "string" && rollout.diagnostic !== "" && rollout.state === "failed")));
  return { ...rollout, ...(rollout.diagnostic !== undefined ? { diagnostic: normalizeSandboxNodeDiagnostic(rollout.diagnostic as string) } : {}) } as unknown as SandboxNodeRollout;
}
/** Configured deployments carry a validated public configuration and observation object. */
function projectDeployment(value: unknown): SandboxDeployment {
  const e2b = isRecord(value) && value.provider === "e2b";
  const fields = ["installation_id", "provider", "core_url", "reset", "rollout", "owner_epoch", "generation", "mode", "resources", "suspension", "credential_configured"];
  const deployment = members(value, isRecord(value) && value.provider !== "" ? [...fields, "configuration", "metadata"] : fields, ["specification", "specification_digest"]);
  valid(typeof deployment.credential_configured === "boolean");
  if (!e2b && deployment.provider !== "") { members(deployment.configuration, []); members(deployment.metadata, []); valid(deployment.credential_configured === false); }
  const resources = members(deployment.resources, ["allocations", "pending"]);
  const suspension = deployment.suspension === null ? null : members(deployment.suspension, ["idle_seconds", "retention_seconds"]);
  const configured = hasOwn(deployment, "specification");
  valid(strings(deployment, ["installation_id", "core_url"]) && providers.has(deployment.provider as string) && modes.has(deployment.mode as string) &&
    [deployment.owner_epoch, deployment.generation, resources.allocations, resources.pending].every(isNonnegativeInteger) &&
    (suspension === null || [suspension.idle_seconds, suspension.retention_seconds].every(isNonnegativeInteger)) &&
    configured === hasOwn(deployment, "specification_digest") && (!configured || (typeof deployment.specification_digest === "string" && deployment.specification_digest !== "")));
  return {
    ...deployment, rollout: projectRollout(deployment.rollout, deployment.mode, Number(resources.allocations) + Number(resources.pending)), reset: projectReset(deployment.reset, Number(resources.allocations) + Number(resources.pending)), resources: { ...resources } as SandboxDeployment["resources"], suspension: suspension && { ...suspension } as SandboxDeployment["suspension"],
    ...(configured ? { specification: projectSpecification(deployment.specification) } : {}),
    ...(e2b ? projectE2B(deployment.configuration, deployment.metadata) : {}),
  } as unknown as SandboxDeployment;
}

const nodeFields = ["rollout", "id", "name", "provider", "online", "provider_ready", "cpu_count", "available_memory_bytes", "available_disk_bytes", "running", "snapshots",
  "last_seen_at", "max_active", "max_retained", "active", "reserved", "retained", "cleanup_pending", "created_at", "core_url", "enrollment_id"];
/** Core omits an empty `diagnostic`, so a present one is a code; an unknown code reads as provider_unavailable. */
function projectNode(node: Record<string, unknown>): SandboxNode {
  const { diagnostic, rollout, ...rest } = node;
  const fields = { ...rest, rollout: projectNodeRollout(rollout, node.online) };
  valid(strings(node, ["id", "name", "provider", "core_url"]) && typeof node.online === "boolean" && typeof node.provider_ready === "boolean" &&
    [node.cpu_count, node.available_memory_bytes, node.available_disk_bytes].every(nullable(isNonnegativeInteger)) &&
    [node.running, node.snapshots, node.max_active, node.max_retained, node.active, node.reserved, node.retained, node.cleanup_pending].every(isNonnegativeInteger) &&
    nullable(timestamp)(node.last_seen_at) && timestamp(node.created_at) && (node.enrollment_id === null || typeof node.enrollment_id === "string") &&
    (diagnostic === undefined || (typeof diagnostic === "string" && diagnostic !== "")));
  if (diagnostic === undefined) return { ...fields } as unknown as SandboxNode;
  return { ...fields, diagnostic: normalizeSandboxNodeDiagnostic(diagnostic as string) } as unknown as SandboxNode;
}
function projectNodeList(value: unknown): { data: SandboxNode[] } {
  const list = members(value, ["data"]);
  valid(Array.isArray(list.data));
  return { data: (list.data as unknown[]).map((entry) => projectNode(members(entry, nodeFields, ["diagnostic"]))) };
}
/** A never-observed host is all null; the history lists every bucket of the range. */
function projectNodeDetail(value: unknown, nodeId: string): SandboxNodeDetail {
  const { host, history, ...fields } = members(value, [...nodeFields, "host", "history"], ["diagnostic"]);
  const node = projectNode(fields);
  const observed = members(host, ["effective_cpu_cores", "cpu_utilization", "total_memory_bytes", "available_memory_bytes", "available_disk_bytes", "observed_at"]);
  const buckets = members(history, ["resolution_seconds", "points"]);
  valid(sameResourceId(node.id, nodeId) && [observed.effective_cpu_cores, observed.cpu_utilization].every(nullable(measure)) &&
    [observed.total_memory_bytes, observed.available_memory_bytes, observed.available_disk_bytes].every(nullable(isNonnegativeInteger)) &&
    nullable(timestamp)(observed.observed_at) && isNonnegativeInteger(buckets.resolution_seconds) && buckets.resolution_seconds > 0 && Array.isArray(buckets.points));
  const points = (buckets.points as unknown[]).map((entry) => {
    const point = members(entry, ["start", "cpu_utilization_max", "memory_used_bytes_max", "available_disk_bytes_min"]);
    valid(timestamp(point.start) && nullable(measure)(point.cpu_utilization_max) && [point.memory_used_bytes_max, point.available_disk_bytes_min].every(nullable(isNonnegativeInteger)));
    return { ...point } as unknown as SandboxNodeHostPoint;
  });
  return { ...node, host: { ...observed } as unknown as SandboxNodeHost, history: { resolution_seconds: buckets.resolution_seconds as number, points } };
}

const allocationFields = ["id", "node_id", "tenant_id", "session_id", "environment_id", "state", "compute_phase", "initialization", "diagnostic"];
const allocationDiagnostics = new Set(["", "node_unavailable", "resource_missing", "compute_unconfirmed", "ownership_mismatch", "provider_unavailable"]);
function projectAllocations(value: unknown, nodeId: string): { data: SandboxAllocation[] } {
  const list = members(value, ["data"]);
  valid(Array.isArray(list.data));
  return { data: (list.data as unknown[]).map((entry) => {
    const allocation = members(entry, [...allocationFields, "deployment_generation", "compute_phase_changed_at", "created_at"]);
    valid(isNonnegativeInteger(allocation.deployment_generation) && strings(allocation, allocationFields) && sameResourceId(allocation.node_id as string, nodeId) && allocationDiagnostics.has(allocation.diagnostic as string) &&
      nullable(timestamp)(allocation.compute_phase_changed_at) && timestamp(allocation.created_at));
    return { ...allocation } as unknown as SandboxAllocation;
  }) };
}

/** Deployment administration under `/core/v1/sandbox`, through an authenticated console or an explicit Core key. */
export class SandboxAdminClient {
  readonly #core: CoreRequester;

  constructor(options: CoreClientOptions = {}) {
    this.#core = new CoreRequester(options.baseUrl ?? "/core/v1/sandbox", options.token, options.fetch, invalidSandboxResponse);
  }

  async listE2BTemplates(input: SandboxE2BDiscoveryInput, options?: ReadOptions): Promise<SandboxE2BTemplate[]> {
    const value = await this.#core.json("/providers/e2b/discovery", options, "POST", { configuration: { api_url: input.api_url, domain: input.domain }, credential: { api_key: input.api_key }, query: {} });
    if (!isRecord(value) || !onlyFields(value, new Set(["templates"])) || !Array.isArray(value.templates) || value.templates.length > 200) invalidSandboxResponse();
    return value.templates.map((item) => {
      if (!isRecord(item) || !onlyFields(item, new Set(["id", "names"])) || typeof item.id !== "string" || !Array.isArray(item.names) || !item.names.every((name) => typeof name === "string")) invalidSandboxResponse();
      return { id: item.id, names: item.names };
    });
  }
  async listE2BReadyBuilds(templateId: string, input: SandboxE2BDiscoveryInput, options?: ReadOptions): Promise<SandboxE2BReadyBuild[]> {
    const value = await this.#core.json("/providers/e2b/discovery", options, "POST", { configuration: { api_url: input.api_url, domain: input.domain }, credential: { api_key: input.api_key }, query: { template: templateId } });
    if (!isRecord(value) || !onlyFields(value, new Set(["builds"])) || !Array.isArray(value.builds) || value.builds.length > 200) invalidSandboxResponse();
    return value.builds.map((item) => {
      if (!isRecord(item) || !onlyFields(item, new Set(["id", "cpus", "memory_mib"])) || typeof item.id !== "string" || !isNonnegativeInteger(item.cpus) || !isNonnegativeInteger(item.memory_mib)) invalidSandboxResponse();
      return { id: item.id, cpus: item.cpus, memory_mib: item.memory_mib };
    });
  }

  #json<T>(path: string, options?: ReadOptions, method?: string, body?: unknown): Promise<T> {
    return this.#core.json(path, options, method, body) as Promise<T>;
  }
  async retrieveDeployment(options?: ReadOptions): Promise<SandboxDeployment> {
    return projectDeployment(await this.#core.json("/deployment", options));
  }
  initializeDeployment(input: InitializeSandboxDeployment, options?: ReadOptions): Promise<SandboxDeployment> {
    return this.#writeDeployment("POST", input, options);
  }
  updateDeployment(input: UpdateSandboxDeployment, options?: ReadOptions): Promise<SandboxDeployment> {
    return this.#writeDeployment("PUT", input, options);
  }
  async startReset(input: StartSandboxReset, options?: ReadOptions): Promise<SandboxDeployment> {
    return projectDeployment(await this.#core.json("/deployment/reset", options, "POST", input));
  }
  async cancelReset(expectedGeneration: number, options?: ReadOptions): Promise<SandboxDeployment> {
    return projectDeployment(await this.#core.json(`/deployment/reset?expected_generation=${encodeURIComponent(expectedGeneration)}`, options, "DELETE"));
  }
  async #writeDeployment(method: "POST" | "PUT", input: InitializeSandboxDeployment | UpdateSandboxDeployment, options?: ReadOptions): Promise<SandboxDeployment> {
    try {
      // As with an unparsable body, a configuration write with an invalid response is unconfirmed.
      return projectDeployment(await this.#core.json("/deployment", options, method, input));
    } catch (error) {
      if (error instanceof AgentCoreError && [400, 409, 503].includes(error.status)) {
        const messages: Record<string, string> = {
          invalid_sandbox_configuration: "Invalid sandbox provider configuration.",
          sandbox_specification_mismatch: "The node specification differs from the deployment.",
          sandbox_deployment_conflict: "The sandbox deployment cannot change in its current state.",
          sandbox_generation_stale: "The sandbox configuration changed. Refresh before submitting again.",
          sandbox_reset_required: "Reset the sandbox deployment before changing this configuration.",
          sandbox_reset_in_progress: "A sandbox reset is in progress.",
          sandbox_not_configured: "The sandbox deployment is not configured.",
          sandbox_in_use: "Hosted sandbox resources still belong to this deployment.",
          sandbox_credential_ownership: "This E2B key cannot manage the retained deployment. Reset before changing teams.",
          sandbox_credential_invalid: "The E2B API key was rejected.",
          sandbox_configuration_invalid: "Select a ready immutable E2B template build with matching resources.",
          sandbox_verification_unconfirmed: "E2B verification could not be confirmed. Refresh before submitting again.",
        };
        if (error.code && Object.hasOwn(messages, error.code)) {
          // Credential-bearing errors expose fixed local copy and allowlisted
          // numeric facts plus exact status/code/field matches only.
          const fields = error.code === "sandbox_generation_stale" ? ["current_generation"] : error.code === "sandbox_in_use" ? ["allocations", "pending"] : error.code === "invalid_sandbox_configuration" ? ["min", "max"] : [];
          const details = Object.fromEntries(fields.filter(field => isNonnegativeInteger(error.details?.[field])).map(field => [field, Number(error.details![field])]));
          const safeParam = error.status === 400
            ? error.code === "sandbox_credential_invalid" ? "credential" : error.code === "sandbox_configuration_invalid" ? "configuration" : error.code === "invalid_sandbox_configuration" && ["runtime", "resources.cpus", "resources.memory_mib", "resources.root_disk_mib", "resources.environment_disk_mib"].includes(error.param ?? "") ? error.param : null
            : error.status === 409 && error.code === "sandbox_credential_ownership" ? "credential" : null;
          const param = error.param === safeParam ? safeParam : null;
          throw new AgentCoreError(messages[error.code]!, error.status, error.code, param, undefined, Object.keys(details).length ? details : undefined);
        }
      }
      // This code has one fixed Core meaning. Never forward its raw message or
      // param: an omitted key cannot be used to detect a reflected stored key.
      if (error instanceof AgentCoreError && error.status === 409 && error.code === "sandbox_configuration_error") {
        throw new AgentCoreError("E2B sandboxes reach Core over the internet. Set an HTTPS public URL that is not loopback.", 409, "sandbox_configuration_error", null);
      }
      // Any other credential-bearing rejection may reflect the key in any error field.
      throw new AgentCoreError("Sandbox configuration could not be confirmed. Refresh before submitting again.", error instanceof AgentCoreError ? error.status : 0, "sandbox_configuration_unconfirmed");
    }
  }
  async listNodes(options?: ReadOptions): Promise<{ data: SandboxNode[] }> {
    return projectNodeList(await this.#core.json("/nodes", options));
  }
  async retrieveNode(nodeId: string, range: SandboxNodeHistoryRange, options?: ReadOptions): Promise<SandboxNodeDetail> {
    return projectNodeDetail(await this.#core.json(`/nodes/${encodeURIComponent(nodeId)}?range=${range}`, options), nodeId);
  }
  async listAllocations(nodeId: string, options?: ReadOptions): Promise<{ data: SandboxAllocation[] }> {
    return projectAllocations(await this.#core.json(`/nodes/${encodeURIComponent(nodeId)}/allocations`, options), nodeId);
  }
  createEnrollment(options?: ReadOptions, capacity: { max_active?: number; max_retained?: number } = {}): Promise<SandboxEnrollment> {
    return this.#json("/enrollment-tokens", options, "POST", capacity);
  }
  updateNode(nodeId: string, input: SandboxNodeUpdate, options?: ReadOptions): Promise<{ id: string; updated: boolean }> {
    return this.#json(`/nodes/${encodeURIComponent(nodeId)}`, options, "PATCH", input);
  }
  removeNode(nodeId: string, options?: ReadOptions): Promise<{ id: string; deleted: boolean }> {
    return this.#json(`/nodes/${encodeURIComponent(nodeId)}`, options, "DELETE");
  }
}
