import { OpenAIAgentsClient } from "./client";
import type { ReadOptions } from "./types";

export type SandboxDiagnostic = "" | "node_unavailable" | "resource_missing" | "compute_unconfirmed" | "ownership_mismatch" | "provider_unavailable";

export interface SandboxDeployment {
  installation_id: string;
  provider: "docker" | "microsandbox";
  maintenance: boolean;
  owner_epoch: number;
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
/** Deployment administration requires its own credential and /core/v1/sandbox base. */
export class SandboxAdminClient extends OpenAIAgentsClient {
  retrieveDeployment(options?: ReadOptions): Promise<SandboxDeployment> {
    return this.request("/deployment", { signal: options?.signal }, undefined, false);
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
