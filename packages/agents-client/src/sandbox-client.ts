import { AgentCoreError } from "./client";
import { CoreRequester, type CoreClientOptions } from "./core-request";
import { hasOwn, isNonnegativeInteger, isRecord, onlyFields, sameResourceId } from "./response-projection";
import type { ReadOptions } from "./types";

export type SandboxDiagnostic = "" | "node_unavailable" | "resource_missing" | "compute_unconfirmed" | "ownership_mismatch" | "provider_unavailable";
/** Fixed reason a node's provider is not ready. Core omits the field while the provider is ready, so read it as falsy (undefined) then. The client reads an unknown future value as provider_unavailable. */
export type SandboxNodeDiagnostic =
  | ""
  | "provider_unavailable"
  | "docker_unavailable"
  | "docker_limits_unsupported"
  | "runtime_image_unavailable"
  | "kvm_unavailable"
  | "microsandbox_artifacts_unavailable"
  | "capacity_insufficient";

export type SandboxProvider = "docker" | "microsandbox" | "e2b";
/** CPU and MiB limits for each sandbox, not node concurrency. */
export interface SandboxResources { cpus: number; memory_mib: number; root_disk_mib?: number; environment_disk_mib?: number }
export interface SandboxRuntimeRelease { source_commit: string; image_id: string; image_manifest_digest: string; microsandbox_ref: string; runtime_sha256: string; firmware_sha256: string }
export interface SandboxSpecification { resources: SandboxResources; runtime?: SandboxRuntimeRelease }
/** Core derives the deployment's address from the installation public URL; a `core_url` member is rejected. */
export interface InitializeSandboxDeployment {
  provider: SandboxProvider;
  /** Required for Docker/microsandbox. E2B may omit it to adopt its validated template build's CPU and memory. */
  resources?: SandboxResources;
  /** Required for Docker/microsandbox; E2B uses its fixed template build. */
  runtime?: SandboxRuntimeRelease;
  e2b?: { api_key: string; template: string };
}
export interface UpdateSandboxDeployment extends InitializeSandboxDeployment { expected_generation: number }
export interface SetSandboxMaintenance { maintenance: boolean; expected_generation: number }

export interface SandboxDeployment {
  specification?: SandboxSpecification;
  specification_digest?: string;
  installation_id: string;
  provider: SandboxProvider | "";
  /** Read-only: the installation public URL, which nodes and sandboxes use to reach Core. Present before configuration. */
  readonly core_url: string;
  maintenance: boolean;
  owner_epoch: number;
  generation: number;
  mode: "nodes" | "direct" | "";
  resources: { allocations: number; pending: number };
  e2b?: { template: string; credential_configured: boolean; template_build: SandboxE2BTemplateBuild };
  /** Idle suspension policy; microsandbox only, otherwise null. */
  suspension: { idle_seconds: number; retention_seconds: number } | null;
}
/** The fixed E2B build as Core read it when the selection was saved; unknown values are null. */
export interface SandboxE2BTemplateBuild {
  status: string | null;
  resources: { cpus: number | null; memory_mib: number | null; root_disk_mib: number | null };
}
export interface SandboxNode {
  id: string;
  name: string;
  provider: string;
  online: boolean;
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
/** The safe E2B view; it has no key member. */
function projectE2B(value: unknown): NonNullable<SandboxDeployment["e2b"]> {
  const e2b = members(value, ["template", "credential_configured", "template_build"]);
  const build = members(e2b.template_build, ["status", "resources"]);
  const resources = members(build.resources, ["cpus", "memory_mib", "root_disk_mib"]);
  valid(typeof e2b.template === "string" && typeof e2b.credential_configured === "boolean" && (build.status === null || typeof build.status === "string") &&
    Object.values(resources).every(nullable(isNonnegativeInteger)));
  return {
    template: e2b.template as string, credential_configured: e2b.credential_configured as boolean,
    template_build: { status: build.status as string | null, resources: { ...resources } as SandboxE2BTemplateBuild["resources"] },
  };
}
/** `specification` and `specification_digest` appear together once configured; `e2b` appears exactly for E2B. */
function projectDeployment(value: unknown): SandboxDeployment {
  const e2b = isRecord(value) && value.provider === "e2b";
  const fields = ["installation_id", "provider", "core_url", "maintenance", "owner_epoch", "generation", "mode", "resources", "suspension"];
  const deployment = members(value, e2b ? [...fields, "e2b"] : fields, ["specification", "specification_digest"]);
  const resources = members(deployment.resources, ["allocations", "pending"]);
  const suspension = deployment.suspension === null ? null : members(deployment.suspension, ["idle_seconds", "retention_seconds"]);
  const configured = hasOwn(deployment, "specification");
  valid(strings(deployment, ["installation_id", "core_url"]) && providers.has(deployment.provider as string) && modes.has(deployment.mode as string) &&
    typeof deployment.maintenance === "boolean" && [deployment.owner_epoch, deployment.generation, resources.allocations, resources.pending].every(isNonnegativeInteger) &&
    (suspension === null || [suspension.idle_seconds, suspension.retention_seconds].every(isNonnegativeInteger)) &&
    configured === hasOwn(deployment, "specification_digest") && (!configured || (typeof deployment.specification_digest === "string" && deployment.specification_digest !== "")));
  return {
    ...deployment, resources: { ...resources } as SandboxDeployment["resources"], suspension: suspension && { ...suspension } as SandboxDeployment["suspension"],
    ...(configured ? { specification: projectSpecification(deployment.specification) } : {}),
    ...(e2b ? { e2b: projectE2B(deployment.e2b) } : {}),
  } as unknown as SandboxDeployment;
}

const nodeFields = ["id", "name", "provider", "online", "provider_ready", "cpu_count", "available_memory_bytes", "available_disk_bytes", "running", "snapshots",
  "last_seen_at", "max_active", "max_retained", "active", "reserved", "retained", "cleanup_pending", "created_at", "core_url", "enrollment_id"];
const nodeDiagnostics = new Set(["provider_unavailable", "docker_unavailable", "docker_limits_unsupported", "runtime_image_unavailable", "kvm_unavailable", "microsandbox_artifacts_unavailable", "capacity_insufficient"]);
/** Core omits an empty `diagnostic`, so a present one is a code; an unknown code reads as provider_unavailable. */
function projectNode(node: Record<string, unknown>): SandboxNode {
  const { diagnostic, ...fields } = node;
  valid(strings(node, ["id", "name", "provider", "core_url"]) && typeof node.online === "boolean" && typeof node.provider_ready === "boolean" &&
    [node.cpu_count, node.available_memory_bytes, node.available_disk_bytes].every(nullable(isNonnegativeInteger)) &&
    [node.running, node.snapshots, node.max_active, node.max_retained, node.active, node.reserved, node.retained, node.cleanup_pending].every(isNonnegativeInteger) &&
    nullable(timestamp)(node.last_seen_at) && timestamp(node.created_at) && (node.enrollment_id === null || typeof node.enrollment_id === "string") &&
    (diagnostic === undefined || (typeof diagnostic === "string" && diagnostic !== "")));
  if (diagnostic === undefined) return { ...fields } as unknown as SandboxNode;
  return { ...fields, diagnostic: nodeDiagnostics.has(diagnostic as string) ? diagnostic : "provider_unavailable" } as unknown as SandboxNode;
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
    const allocation = members(entry, [...allocationFields, "compute_phase_changed_at", "created_at"]);
    valid(strings(allocation, allocationFields) && sameResourceId(allocation.node_id as string, nodeId) && allocationDiagnostics.has(allocation.diagnostic as string) &&
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
  async setMaintenance(input: SetSandboxMaintenance, options?: ReadOptions): Promise<SandboxDeployment> {
    return projectDeployment(await this.#core.json("/deployment/maintenance", options, "PATCH", input));
  }
  async #writeDeployment(method: "POST" | "PUT", input: InitializeSandboxDeployment | UpdateSandboxDeployment, options?: ReadOptions): Promise<SandboxDeployment> {
    try {
      // As with an unparsable body, an E2B write with an invalid response is unconfirmed.
      return projectDeployment(await this.#core.json("/deployment", options, method, input));
    } catch (error) {
      if (!input.e2b) throw error;
      // The public-URL rejection is safe to show unless it somehow reflects the key.
      const key = input.e2b.api_key;
      if (error instanceof AgentCoreError && error.status === 409 && error.code === "sandbox_configuration_error"
        && !error.message.includes(key) && !(error.param ?? "").includes(key)) {
        // Only Core's message and param pass through; nothing else from the response does.
        throw new AgentCoreError(error.message, 409, "sandbox_configuration_error", error.param ?? null);
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
