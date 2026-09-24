import { AgentCoreError, OpenAIAgentsClient } from "./client";
import type { ReadOptions } from "./types";

export type SandboxDiagnostic = "" | "node_unavailable" | "resource_missing" | "compute_unconfirmed" | "ownership_mismatch" | "provider_unavailable";

export type SandboxProvider = "docker" | "microsandbox" | "e2b";
/** CPU and MiB limits for each sandbox, not node concurrency. */
export interface SandboxResources { cpus: number; memory_mib: number; root_disk_mib?: number; environment_disk_mib?: number }
export interface SandboxRuntimeRelease { source_commit: string; image_id: string; image_manifest_digest: string; microsandbox_ref: string; runtime_sha256: string; firmware_sha256: string }
export interface SandboxSpecification { resources: SandboxResources; runtime?: SandboxRuntimeRelease }
export interface InitializeSandboxDeployment {
  provider: SandboxProvider;
  core_url: string;
  /** Required by Core for new and replacement configurations. */
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
  core_url: string;
  maintenance: boolean;
  owner_epoch: number;
  generation: number;
  mode: "nodes" | "direct" | "";
  resources: { allocations: number; pending: number };
  e2b?: { template: string; credential_configured: boolean };
}
export interface SandboxNode {
  id: string;
  name: string;
  provider: string;
  online: boolean;
  provider_ready: boolean;
  diagnostic: "" | "provider_unavailable";
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
}
export interface SandboxAllocation {
  id: string;
  node_id: string;
  tenant_id: string;
  session_id: string;
  environment_id: string;
  state: string;
  compute_phase: string;
  diagnostic: SandboxDiagnostic;
  initialization: string;
  created_at: string;
}
export interface SandboxDirectoryNode { id: string; name: string; available: boolean }
export interface SandboxPlacement {
  node_id: string;
  node_name: string;
  available: boolean;
  state: string;
  compute_phase: string;
  diagnostic: SandboxDiagnostic;
}
/** Project-scoped extensions, using the ordinary /v1 project credential. */
export class SandboxProjectClient extends OpenAIAgentsClient {
  listSandboxNodes(options?: ReadOptions): Promise<{ data: SandboxDirectoryNode[] }> {
    return this.request("/sandbox/nodes", { signal: options?.signal });
  }
  retrieveSandboxPlacement(sessionId: string, options?: ReadOptions): Promise<SandboxPlacement> {
    return this.request(`/agents/sessions/${encodeURIComponent(sessionId)}/sandbox-placement`, { signal: options?.signal });
  }
}
/** Deployment administration uses /core/v1/sandbox, through an authenticated console or an explicit server credential. */
export class SandboxAdminClient extends OpenAIAgentsClient {
  retrieveDeployment(options?: ReadOptions): Promise<SandboxDeployment> {
    return this.request("/deployment", { signal: options?.signal }, undefined, false);
  }
  initializeDeployment(input: InitializeSandboxDeployment, options?: ReadOptions): Promise<SandboxDeployment> {
    return this.writeDeployment("POST", input, options);
  }
  updateDeployment(input: UpdateSandboxDeployment, options?: ReadOptions): Promise<SandboxDeployment> {
    return this.writeDeployment("PUT", input, options);
  }
  setMaintenance(input: SetSandboxMaintenance, options?: ReadOptions): Promise<SandboxDeployment> {
    return this.request("/deployment/maintenance", { method: "PATCH", body: JSON.stringify(input), signal: options?.signal }, undefined, false);
  }
  private async writeDeployment(method: "POST" | "PUT", input: InitializeSandboxDeployment | UpdateSandboxDeployment, options?: ReadOptions): Promise<SandboxDeployment> {
    try {
      return await this.request("/deployment", { method, body: JSON.stringify(input), signal: options?.signal }, undefined, false);
    } catch (error) {
      if (!input.e2b) throw error;
      // A credential-bearing rejection may reflect the key in any error field.
      throw new AgentCoreError("Sandbox configuration could not be confirmed. Refresh before submitting again.", error instanceof AgentCoreError ? error.status : 0, "sandbox_configuration_unconfirmed");
    }
  }
  listNodes(options?: ReadOptions): Promise<{ data: SandboxNode[] }> {
    return this.request("/nodes", { signal: options?.signal }, undefined, false);
  }
  listAllocations(nodeId: string, options?: ReadOptions): Promise<{ data: SandboxAllocation[] }> {
    return this.request(`/nodes/${encodeURIComponent(nodeId)}/allocations`, { signal: options?.signal }, undefined, false);
  }
  createEnrollment(options?: ReadOptions): Promise<{ token: string; expires_at: string }> {
    return this.request("/enrollment-tokens", { method: "POST", body: "{}", signal: options?.signal }, undefined, false);
  }
  removeNode(nodeId: string, options?: ReadOptions): Promise<{ id: string; deleted: boolean }> {
    return this.request(`/nodes/${encodeURIComponent(nodeId)}`, { method: "DELETE", signal: options?.signal }, undefined, false);
  }
}
