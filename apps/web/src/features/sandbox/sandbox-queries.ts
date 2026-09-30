import { SandboxAdminClient, type SandboxAllocation, type SandboxDeployment, type SandboxNode, type SandboxProvider } from "@oac/agents-client";
import { queryOptions, useQuery } from "@tanstack/react-query";

import { confirmSandboxRead, startSandboxRead } from "./sandbox-write-ownership";
import { sandboxConsoleConfig } from "./console-config";

/**
 * Cache entries for the sandbox deployment (`/core/v1/sandbox`). Every read
 * lives under `["sandbox"]`; deployment writes are never retried and never
 * written into the cache except as Core's confirmed response.
 */
export const sandboxAdmin = new SandboxAdminClient({ baseUrl: "/core/v1/sandbox" });

export const sandboxScope = ["sandbox"] as const;

/** Whether this console may administer sandboxes, and its node installer. */
export const sandboxConsoleConfigQuery = queryOptions({
  queryKey: ["console-config"],
  queryFn: async ({ signal }) => {
    const config = await sandboxConsoleConfig(signal);
    // Never cache a result read after cancellation.
    signal.throwIfAborted();
    return config;
  },
});

/** The deployment alone, for pages that only describe it. */
export const sandboxDeploymentQuery = queryOptions<SandboxDeployment>({
  queryKey: [...sandboxScope, "deployment"],
  queryFn: async ({ signal, client }) => {
    const readStartedAt = startSandboxRead();
    const deployment = await sandboxAdmin.retrieveDeployment({ signal });
    signal.throwIfAborted();
    confirmSandboxRead(client, readStartedAt);
    return deployment;
  },
  refetchInterval: (query) => sandboxResetPollInterval(query.state.data),
  refetchIntervalInBackground: false,
});

/** Only Core reports active work; retained older sandboxes do not keep polling alive. */
export function sandboxResetPollInterval(deployment: SandboxDeployment | undefined): 5000 | false {
  return deployment?.reset || deployment?.rollout.state === "preparing" ? 5000 : false;
}

/**
 * The deployment's sandbox provider from the cached deployment read: "" before
 * setup, null while unknown or when this console has no sandbox administration.
 * Pages that differ for E2B (no machines) and own nodes read it.
 */
export function useSandboxProvider(): SandboxProvider | "" | null {
  const config = useQuery(sandboxConsoleConfigQuery);
  const deployment = useQuery({ ...sandboxDeploymentQuery, enabled: config.data?.sandbox_admin === true });
  return deployment.data?.provider ?? null;
}

export interface SandboxSnapshot {
  deployment: SandboxDeployment;
  nodes: SandboxNode[];
  allocations: SandboxAllocation[];
  /** A failed node/receipt read never hides a successful deployment/reset read. */
  nodesError: unknown | null;
  /** `performance.now()` when this supplemental inventory read began. */
  readAt: number;
}

/** The Nodes page keeps authoritative deployment progress even if node reads fail. */
export const sandboxSnapshotQuery = queryOptions<SandboxSnapshot>({
  queryKey: [...sandboxScope, "snapshot"],
  refetchInterval: (query) => sandboxResetPollInterval(query.state.data?.deployment),
  refetchIntervalInBackground: false,
  queryFn: async ({ signal, client }): Promise<SandboxSnapshot> => {
    const readAt = performance.now();
    const deployment = await client.fetchQuery({ ...sandboxDeploymentQuery, staleTime: 0 });
    signal.throwIfAborted();
    let nodes: SandboxNode[] = [];
    try {
      if (deployment.provider && deployment.provider !== "e2b") nodes = (await sandboxAdmin.listNodes({ signal })).data;
      const allocations = await Promise.all(nodes.map((node) => sandboxAdmin.listAllocations(node.id, { signal })));
      signal.throwIfAborted();
      return { deployment, nodes, allocations: allocations.flatMap((page) => page.data), nodesError: null, readAt };
    } catch (nodesError) {
      signal.throwIfAborted();
      const previous = client.getQueryData(sandboxSnapshotQuery.queryKey);
      if (previous && sandboxSnapshotMatchesDeployment(previous, deployment)) {
        // Online updates retain older ownership. A failed supplemental refresh
        // keeps that evidence visibly stale; it cannot erase existing resources.
        return { ...previous, nodesError };
      }
      return { deployment, nodes, allocations: [], nodesError, readAt };
    }
  },
});

/** Node facts belong to this lifecycle, never a previous reset or installation. */
export function sandboxSnapshotMatchesDeployment(snapshot: SandboxSnapshot, deployment: SandboxDeployment): boolean {
  const previous = snapshot.deployment;
  return previous.installation_id === deployment.installation_id && previous.owner_epoch === deployment.owner_epoch &&
    previous.provider === deployment.provider && previous.mode === deployment.mode &&
    previous.reset?.requested_at === deployment.reset?.requested_at && previous.reset?.clear === deployment.reset?.clear &&
    previous.reset?.forced_at === deployment.reset?.forced_at;
}
