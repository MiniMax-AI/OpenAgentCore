import type { SandboxAllocation, SandboxNode } from "@agents-core-web/agents-client";

import type { OwnedRuntimeObservation } from "../../lib/admin-view";
import { nodeHealth } from "../fleet/fleet-model";

/** One thing on the sandbox fleet an administrator should look at, most severe first. */
export type SandboxAttention =
  | { kind: "offline"; tone: "danger"; node: SandboxNode; lastSeen: number | null }
  | { kind: "degraded"; tone: "warning"; node: SandboxNode }
  | { kind: "cleanup"; tone: "warning"; node: SandboxNode; count: number }
  | { kind: "allocation"; tone: "warning"; node: SandboxNode; count: number }
  | { kind: "unobservable"; tone: "warning"; count: number };

function seconds(value: string | null): number | null {
  if (!value) return null;
  const parsed = Date.parse(value);
  return Number.isNaN(parsed) ? null : Math.floor(parsed / 1000);
}

/**
 * What needs attention across nodes and hosted Runtimes: offline nodes, nodes
 * whose provider is not ready, pending cleanup, allocations with a diagnostic,
 * and hosted Runtimes Core could not sample.
 */
export function sandboxAttention(
  nodes: readonly SandboxNode[],
  allocations: readonly SandboxAllocation[],
  observations: readonly OwnedRuntimeObservation[],
): SandboxAttention[] {
  const items: SandboxAttention[] = [];
  for (const node of nodes) {
    const health = nodeHealth(node);
    if (health === "offline") items.push({ kind: "offline", tone: "danger", node, lastSeen: seconds(node.last_seen_at) });
    else if (health === "degraded") items.push({ kind: "degraded", tone: "warning", node });
  }
  for (const node of nodes) {
    if (node.cleanup_pending > 0) items.push({ kind: "cleanup", tone: "warning", node, count: node.cleanup_pending });
    const diagnosed = allocations.filter((allocation) => allocation.node_id === node.id && allocation.diagnostic).length;
    if (diagnosed) items.push({ kind: "allocation", tone: "warning", node, count: diagnosed });
  }
  // Waiting for an allocation or not running are states, not faults; a failed sample is a fault.
  const unobservable = observations.filter((observation) => observation.mode === "openai_hosted"
    && observation.status === "unavailable"
    && observation.reason !== "allocation_pending"
    && observation.reason !== "runtime_not_running").length;
  if (unobservable) items.push({ kind: "unobservable", tone: "warning", count: unobservable });
  return items;
}
