import { AgentCoreError } from "./client";
import { CoreRequester, type CoreClientOptions } from "./core-request";
import type { ReadOptions } from "./types";

export type SandboxDiagnostic = "" | "node_unavailable" | "resource_missing" | "compute_unconfirmed" | "ownership_mismatch" | "provider_unavailable";
/** Fixed reason a node's provider is not ready. Core omits the field while the provider is ready, so read it as falsy (undefined) then. Treat an unknown future value as provider_unavailable. */
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

/** Deployment administration under `/core/v1/sandbox`, through an authenticated console or an explicit Core key. */
export class SandboxAdminClient {
  readonly #core: CoreRequester;

  constructor(options: CoreClientOptions = {}) {
    this.#core = new CoreRequester(options.baseUrl ?? "/core/v1/sandbox", options.token, options.fetch, invalidSandboxResponse);
  }

  #json<T>(path: string, options?: ReadOptions, method?: string, body?: unknown): Promise<T> {
    return this.#core.json(path, options, method, body) as Promise<T>;
  }
  retrieveDeployment(options?: ReadOptions): Promise<SandboxDeployment> {
    return this.#json("/deployment", options);
  }
  initializeDeployment(input: InitializeSandboxDeployment, options?: ReadOptions): Promise<SandboxDeployment> {
    return this.#writeDeployment("POST", input, options);
  }
  updateDeployment(input: UpdateSandboxDeployment, options?: ReadOptions): Promise<SandboxDeployment> {
    return this.#writeDeployment("PUT", input, options);
  }
  setMaintenance(input: SetSandboxMaintenance, options?: ReadOptions): Promise<SandboxDeployment> {
    return this.#json("/deployment/maintenance", options, "PATCH", input);
  }
  async #writeDeployment(method: "POST" | "PUT", input: InitializeSandboxDeployment | UpdateSandboxDeployment, options?: ReadOptions): Promise<SandboxDeployment> {
    try {
      return await this.#json("/deployment", options, method, input);
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
  listNodes(options?: ReadOptions): Promise<{ data: SandboxNode[] }> {
    return this.#json("/nodes", options);
  }
  retrieveNode(nodeId: string, range: SandboxNodeHistoryRange, options?: ReadOptions): Promise<SandboxNodeDetail> {
    return this.#json(`/nodes/${encodeURIComponent(nodeId)}?range=${range}`, options);
  }
  listAllocations(nodeId: string, options?: ReadOptions): Promise<{ data: SandboxAllocation[] }> {
    return this.#json(`/nodes/${encodeURIComponent(nodeId)}/allocations`, options);
  }
  createEnrollment(options?: ReadOptions, capacity: { max_active?: number; max_retained?: number } = {}): Promise<{ token: string; expires_at: string }> {
    return this.#json("/enrollment-tokens", options, "POST", capacity);
  }
  updateNode(nodeId: string, input: SandboxNodeUpdate, options?: ReadOptions): Promise<{ id: string; updated: boolean }> {
    return this.#json(`/nodes/${encodeURIComponent(nodeId)}`, options, "PATCH", input);
  }
  removeNode(nodeId: string, options?: ReadOptions): Promise<{ id: string; deleted: boolean }> {
    return this.#json(`/nodes/${encodeURIComponent(nodeId)}`, options, "DELETE");
  }
}
