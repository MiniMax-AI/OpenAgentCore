import type { SandboxNode } from "@agents-core-web/agents-client";

/** Pure projections of the deployment fleet. Missing inputs stay null, never zero. */

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
  cpuCount: number | null;
  availableMemoryBytes: number | null;
  availableDiskBytes: number | null;
}

function sumKnown(values: ReadonlyArray<number | null>): number | null {
  const known = values.filter((value): value is number => typeof value === "number");
  return known.length ? known.reduce((sum, value) => sum + value, 0) : null;
}

export function capacitySummary(nodes: readonly SandboxNode[]): CapacitySummary {
  const online = nodes.filter((node) => node.online);
  return {
    nodes: nodes.length,
    online: online.length,
    available: nodes.filter((node) => nodeHealth(node) === "available").length,
    active: nodes.reduce((sum, node) => sum + node.active, 0),
    maxActive: online.reduce((sum, node) => sum + node.max_active, 0),
    retained: nodes.reduce((sum, node) => sum + node.retained, 0),
    maxRetained: online.reduce((sum, node) => sum + node.max_retained, 0),
    reserved: nodes.reduce((sum, node) => sum + node.reserved, 0),
    cleanupPending: nodes.reduce((sum, node) => sum + node.cleanup_pending, 0),
    cpuCount: sumKnown(online.map((node) => node.cpu_count)),
    availableMemoryBytes: sumKnown(online.map((node) => node.available_memory_bytes)),
    availableDiskBytes: sumKnown(online.map((node) => node.available_disk_bytes)),
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
