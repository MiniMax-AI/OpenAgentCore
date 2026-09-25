import type { SandboxNode } from "@agents-core-web/agents-client";

/**
 * Pure projections of the deployment fleet. Missing inputs stay null, never zero.
 * Host resources (CPU, free memory, free disk) are per node and not summed: a
 * sandbox runs on one node, and nodes report no totals to compare a sum with.
 */

export type NodeHealth = "available" | "degraded" | "offline";

export function nodeHealth(node: SandboxNode): NodeHealth {
  if (!node.online) return "offline";
  return node.provider_ready && !node.diagnostic ? "available" : "degraded";
}

export interface CapacitySummary {
  nodes: number;
  online: number;
  available: number;
  active: number;
  /** Active-sandbox limit across online nodes. */
  maxActive: number;
  retained: number;
  maxRetained: number;
  reserved: number;
  cleanupPending: number;
}

/** Slots in use and their limits count online nodes only, so a used share never mixes in an offline node's stale figures. */
export function capacitySummary(nodes: readonly SandboxNode[]): CapacitySummary {
  const online = nodes.filter((node) => node.online);
  return {
    nodes: nodes.length,
    online: online.length,
    available: nodes.filter((node) => nodeHealth(node) === "available").length,
    active: online.reduce((sum, node) => sum + node.active, 0),
    maxActive: online.reduce((sum, node) => sum + node.max_active, 0),
    retained: online.reduce((sum, node) => sum + node.retained, 0),
    maxRetained: online.reduce((sum, node) => sum + node.max_retained, 0),
    reserved: nodes.reduce((sum, node) => sum + node.reserved, 0),
    cleanupPending: nodes.reduce((sum, node) => sum + node.cleanup_pending, 0),
  };
}

/**
 * Core itself, as far as the console can tell: whether the Web API answers
 * and whether the sandbox deployment is in maintenance. Core reports no CPU or
 * memory figures of its own yet.
 */
export type CoreStatus = "checking" | "running" | "maintenance" | "unreachable";

export function coreStatus(input: { webApiReachable: boolean | null; maintenance: boolean | null }): CoreStatus {
  if (input.webApiReachable === false) return "unreachable";
  if (input.webApiReachable === null) return "checking";
  return input.maintenance ? "maintenance" : "running";
}
