import { queryOptions, type QueryClient } from "@tanstack/react-query";
import { SandboxAdminClient, type SandboxAllocation, type SandboxDeployment, type SandboxNode, type SandboxNodeHistoryRange } from "@oac/agents-client";

import { sandboxDeploymentQuery } from "../sandbox/sandbox-queries";
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

async function loadFleet(readAllocations: boolean, signal: AbortSignal, cache: QueryClient): Promise<FleetSnapshot> {
  const sandbox = client();
  const [deployment, nodes] = await Promise.all([
    // Keep reset truth in the shared deployment cache, even if a node read fails.
    cache.fetchQuery({ ...sandboxDeploymentQuery, staleTime: 0 }),
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
    queryFn: ({ signal, client }) => loadFleet(allocations, signal, client),
  });
}

/** One node's host observation and host history over a range; polled while shown. */
export function nodeDetailQuery(nodeId: string, range: SandboxNodeHistoryRange) {
  return queryOptions({
    queryKey: ["sandbox-node", nodeId, range],
    queryFn: ({ signal }) => client().retrieveNode(nodeId, range, { signal }),
  });
}
