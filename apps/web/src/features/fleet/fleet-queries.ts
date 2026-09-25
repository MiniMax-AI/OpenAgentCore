import { queryOptions } from "@tanstack/react-query";
import { SandboxAdminClient, type SandboxAllocation, type SandboxDeployment, type SandboxNode, type SandboxNodeHistoryRange } from "@agents-core-web/agents-client";

import { sandboxConsoleConfig } from "../sandbox/console-config";

export interface FleetSnapshot {
  deployment: SandboxDeployment;
  nodes: SandboxNode[];
  allocations: SandboxAllocation[];
  /** Epoch milliseconds of the last complete read. */
  loadedAt: number;
}

let sandboxClient: SandboxAdminClient | null = null;
function client(): SandboxAdminClient {
  sandboxClient ??= new SandboxAdminClient({ baseUrl: "/core/v1/sandbox" });
  return sandboxClient;
}

/** The console's own configuration: whether it holds a sandbox administration credential. */
export const consoleConfigQuery = queryOptions({
  queryKey: ["console-config"],
  queryFn: ({ signal }) => sandboxConsoleConfig(signal),
});

async function loadFleet(readAllocations: boolean, signal: AbortSignal): Promise<FleetSnapshot> {
  const sandbox = client();
  const [deployment, nodes] = await Promise.all([
    sandbox.retrieveDeployment({ signal }),
    sandbox.listNodes({ signal }),
  ]);
  const allocations = readAllocations
    ? await Promise.all(nodes.data.map((node) => sandbox.listAllocations(node.id, { signal })))
    : [];
  return {
    deployment,
    nodes: nodes.data,
    allocations: allocations.flatMap((page) => page.data),
    loadedAt: Date.now(),
  };
}

/** The deployment, its nodes and, when asked for, every node's allocations. */
export function fleetQuery(allocations: boolean) {
  return queryOptions({
    queryKey: ["sandbox-fleet", allocations ? "with-allocations" : "nodes"],
    queryFn: ({ signal }) => loadFleet(allocations, signal),
  });
}

/** One node's host observation and host history over a range; polled while shown. */
export function nodeDetailQuery(nodeId: string, range: SandboxNodeHistoryRange) {
  return queryOptions({
    queryKey: ["sandbox-node", nodeId, range],
    queryFn: ({ signal }) => client().retrieveNode(nodeId, range, { signal }),
  });
}
