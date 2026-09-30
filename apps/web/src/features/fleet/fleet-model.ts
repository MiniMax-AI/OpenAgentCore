import type { SandboxNode } from "@oac/agents-client";

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

/** Live provider evidence is independent of target preparation and durable serving pins. */
export function nodeServingReady(node: SandboxNode): boolean {
  return node.online && node.provider_ready;
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
  /** Sandboxes held suspended (microsandbox only): placed, but not counted as active. */
  suspended: number;
}

/**
 * Sandboxes a node holds suspended: Core counts every unreleased placement as
 * retained and only the ones not suspended as active, so the difference is
 * what sleeps as a snapshot. Only microsandbox suspends; elsewhere it is 0.
 */
export function suspendedSandboxes(node: SandboxNode): number {
  return Math.max(0, node.retained - node.active);
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
    suspended: nodes.reduce((sum, node) => sum + suspendedSandboxes(node), 0),
  };
}

/**
 * Core reachability is independent of a sandbox reset. Reset progress comes
 * from the deployment read and does not imply a Core health failure.
 */
export type CoreStatus = "checking" | "running" | "unreachable";

export function coreStatus(input: { webApiReachable: boolean | null }): CoreStatus {
  if (input.webApiReachable === false) return "unreachable";
  if (input.webApiReachable === null) return "checking";
  return "running";
}
